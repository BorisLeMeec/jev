package run

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/borislemeec/jev/internal/typesafe"
)

// Hook narrows a Read of a very large file to the window that answers what the
// agent is actually after, by rewriting the tool's offset and limit.
//
// This is the one place a hook can do something the CLI cannot: PreToolUse may
// rewrite tool input, so the saving lands without the agent having to choose it.
// It is also the most dangerous thing in this project. Hiding code from a reader
// is a silent omission, and the token-economy benchmark showed that a quiet miss
// costs far more than a noisy false positive. Every decision below is therefore
// biased toward doing nothing:
//
//   - off entirely when JEV_HOOK_DISABLE is set
//   - only files above a measured line floor
//   - only when an explicit offset or limit was not already given
//   - only when the model is confident the answer really is in one place
//   - a window far wider than the match, never a tight crop
//   - any error, timeout or doubt returns the read untouched
//   - when it does narrow, it says so and says how to undo it
type hookInput struct {
	ToolName       string         `json:"tool_name"`
	ToolInput      map[string]any `json:"tool_input"`
	CWD            string         `json:"cwd"`
	TranscriptPath string         `json:"transcript_path"`
}

type hookOutput struct {
	HookSpecificOutput *hookSpecific `json:"hookSpecificOutput,omitempty"`
	AdditionalContext  string        `json:"additionalContext,omitempty"`
}

type hookSpecific struct {
	HookEventName string         `json:"hookEventName"`
	UpdatedInput  map[string]any `json:"updatedInput,omitempty"`
}

// passThrough emits the empty decision: the read proceeds exactly as written.
// The reason goes to stderr under JEV_HOOK_DEBUG — a hook that silently does
// nothing is impossible to tell apart from a hook that is broken.
func passThrough(reason string) error {
	if os.Getenv("JEV_HOOK_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "jev hook: passing through (%s)\n", reason)
	}
	return json.NewEncoder(os.Stdout).Encode(hookOutput{})
}

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil && v > 0 {
		return v
	}
	return def
}

func Hook(args []string) error {
	if len(args) == 0 || args[0] != "read" {
		return fmt.Errorf("usage: jev hook read   (reads a PreToolUse payload on stdin)")
	}
	// On by default. JEV_HOOK_DISABLE is the escape hatch: one variable turns
	// every narrowing off without uninstalling anything, which is what you want
	// reaching for when a read looks like it lost something.
	if strings.TrimSpace(os.Getenv("JEV_HOOK_DISABLE")) != "" {
		return passThrough("JEV_HOOK_DISABLE is set")
	}

	var in hookInput
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		return passThrough("unreadable payload")
	}
	if in.ToolName != "Read" {
		return passThrough("not a Read")
	}

	path, _ := in.ToolInput["file_path"].(string)
	if path == "" {
		return passThrough("no file_path")
	}
	// An explicit window is the agent's own decision; never second-guess it.
	if _, ok := in.ToolInput["offset"]; ok {
		return passThrough("agent set an offset")
	}
	if _, ok := in.ToolInput["limit"]; ok {
		return passThrough("agent set a limit")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return passThrough("unreadable file")
	}
	lines := strings.Count(string(data), "\n") + 1
	minLines := envInt("JEV_HOOK_MIN_LINES", 400)
	if lines < minLines {
		return passThrough(fmt.Sprintf("file is %d lines, under the %d-line floor", lines, minLines))
	}

	// A file too big for one request is located section by section, and the
	// confidences of different sections are not comparable — measured, that path
	// loses the target 3 times in 11, while a file that fits in one request has
	// not lost one in 23. find and ask still use the sectioned path, where a
	// wrong line is a hint beside a correct file; here it would hide code.
	if len(data) > maxSectionBytes {
		return passThrough(fmt.Sprintf("file is %d KB, over the %d KB single-request limit",
			len(data)/1024, maxSectionBytes/1024))
	}

	goal := lastUserMessage(in.TranscriptPath)
	if len(goal) < 12 {
		return passThrough("no goal found in the transcript")
	}

	client, err := typesafe.New()
	if err != nil {
		return passThrough("no API key")
	}
	got, err := locateLine(context.Background(), client, goal, filepath.Base(path), data)
	if err != nil || got.Line == 0 {
		return passThrough(fmt.Sprintf("locate failed or found nothing (err=%v, line=%d, exists=%.2f)", err, got.Line, got.Exists))
	}
	// Narrow only when the chunk distribution is peaked. This gate is measured
	// rather than guessed: across twelve labelled targets in three large files
	// every correct window scored 0.71 or above, and the one window that lost
	// its target scored 0.42. The floor sits between them with margin on both
	// sides — but it rests on a single observed failure, so it is deliberately
	// far above the highest confidence ever seen to fail.
	minConf := 0.60
	if v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv("JEV_HOOK_MIN_CONF")), 64); err == nil && v > 0 {
		minConf = v
	}
	if got.Conf < minConf {
		return passThrough(fmt.Sprintf("chunk confidence %.2f is under the %.2f floor", got.Conf, minConf))
	}

	// A fifth of the file, never under 150 lines. Both numbers are measured.
	// Over 32 targets in 413-1191 line files the chosen line sat a median of 4
	// lines from the real one, 31 of 32 within 25 lines and all 32 within 60.
	// A 150-line window is centred on the pick, so it forgives ±75 — comfortably
	// past the worst error seen. It was 300, which on a 413-line file kept 73%
	// of the file and saved almost nothing.
	window := lines / 5
	if window < 150 {
		window = 150
	}
	if window >= lines {
		return passThrough("window would cover the whole file")
	}
	offset := got.Line - window/2
	if offset < 1 {
		offset = 1
	}
	if offset+window > lines {
		offset = lines - window
	}

	return json.NewEncoder(os.Stdout).Encode(hookOutput{
		HookSpecificOutput: &hookSpecific{
			HookEventName: "PreToolUse",
			UpdatedInput: map[string]any{
				"file_path": path,
				"offset":    offset,
				"limit":     window,
			},
		},
		AdditionalContext: fmt.Sprintf(
			"jev narrowed this Read: %s is %d lines, showing %d-%d (match at line %d, confidence %.2f). "+
				"Read it again with an explicit offset or limit to see any other part — nothing was removed from the file.",
			filepath.Base(path), lines, offset, offset+window-1, got.Line, got.Conf),
	})
}

// lastUserMessage returns the most recent thing the user actually typed, which
// is the best available statement of what the agent is looking for. Tool results
// and the agent's own turns are skipped: they describe what it has been doing,
// not what it was asked for.
func lastUserMessage(transcript string) string {
	if transcript == "" {
		return ""
	}
	f, err := os.Open(transcript)
	if err != nil {
		return ""
	}
	defer f.Close()

	var last string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var o struct {
			Type    string `json:"type"`
			Message struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &o) != nil || o.Type != "user" {
			continue
		}
		switch c := o.Message.Content.(type) {
		case string:
			if t := strings.TrimSpace(c); t != "" {
				last = t
			}
		case []any:
			var b strings.Builder
			for _, raw := range c {
				m, ok := raw.(map[string]any)
				if !ok || m["type"] != "text" {
					continue // a tool_result is not the user speaking
				}
				if t, ok := m["text"].(string); ok {
					b.WriteString(t)
					b.WriteString(" ")
				}
			}
			if t := strings.TrimSpace(b.String()); t != "" {
				last = t
			}
		}
	}
	// System reminders and command wrappers are noise around the real request.
	// A marker at position zero leaves nothing, which is the right outcome: an
	// empty goal makes the caller pass the read through untouched.
	for _, marker := range []string{"<system-reminder>", "<command-name>", "<local-command-stdout>"} {
		if i := strings.Index(last, marker); i >= 0 {
			last = last[:i]
		}
	}
	last = strings.TrimSpace(last)
	if len(last) > 600 {
		last = last[:600]
	}
	return last
}
