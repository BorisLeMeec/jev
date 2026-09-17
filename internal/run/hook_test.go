package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runHook feeds a payload through the hook's decision logic by invoking the
// exported entry point with stdin and stdout redirected.
func runHook(t *testing.T, payload any) hookOutput {
	t.Helper()
	in, err := os.CreateTemp(t.TempDir(), "in")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(in).Encode(payload); err != nil {
		t.Fatal(err)
	}
	in.Seek(0, 0)
	out, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = in, out
	defer func() { os.Stdin, os.Stdout = oldIn, oldOut }()

	if err := Hook([]string{"read"}); err != nil {
		t.Fatalf("Hook: %v", err)
	}
	os.Stdout = oldOut
	out.Seek(0, 0)
	var got hookOutput
	if err := json.NewDecoder(out).Decode(&got); err != nil {
		t.Fatalf("decoding hook output: %v", err)
	}
	return got
}

func bigFile(t *testing.T, lines int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "big.go")
	if err := os.WriteFile(p, []byte(strings.Repeat("// a line of source\n", lines)), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Every one of these must leave the read exactly as the agent wrote it. A
// regression here means the hook silently hides code, which is the failure this
// project can least afford.
func TestHookPassesThrough(t *testing.T) {
	big := bigFile(t, 4000)

	for _, tc := range []struct {
		name    string
		disable string
		payload map[string]any
	}{
		{"explicitly disabled", "1", map[string]any{
			"tool_name": "Read", "tool_input": map[string]any{"file_path": big}}},
		{"not a Read", "", map[string]any{
			"tool_name": "Grep", "tool_input": map[string]any{"file_path": big}}},
		{"no path", "", map[string]any{
			"tool_name": "Read", "tool_input": map[string]any{}}},
		{"agent set an offset", "", map[string]any{
			"tool_name": "Read", "tool_input": map[string]any{"file_path": big, "offset": 100}}},
		{"agent set a limit", "", map[string]any{
			"tool_name": "Read", "tool_input": map[string]any{"file_path": big, "limit": 50}}},
		{"file does not exist", "", map[string]any{
			"tool_name": "Read", "tool_input": map[string]any{"file_path": big + ".missing"}}},
		{"file is small", "", map[string]any{
			"tool_name": "Read", "tool_input": map[string]any{"file_path": bigFile(t, 40)}}},
		{"file just under the floor", "", map[string]any{
			"tool_name": "Read", "tool_input": map[string]any{"file_path": bigFile(t, 399)}}},
		{"no transcript, so no goal", "", map[string]any{
			"tool_name": "Read", "tool_input": map[string]any{"file_path": big}, "transcript_path": ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("JEV_HOOK_DISABLE", tc.disable)
			got := runHook(t, tc.payload)
			if got.HookSpecificOutput != nil && got.HookSpecificOutput.UpdatedInput != nil {
				t.Errorf("hook rewrote the read but should have passed it through: %+v",
					got.HookSpecificOutput.UpdatedInput)
			}
		})
	}
}

func TestLastUserMessage(t *testing.T) {
	dir := t.TempDir()
	write := func(lines ...string) string {
		p := filepath.Join(dir, "t.jsonl")
		os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
		return p
	}

	t.Run("plain string content", func(t *testing.T) {
		p := write(
			`{"type":"user","message":{"role":"user","content":"where is the retry logic"}}`,
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"looking"}]}}`)
		if got := lastUserMessage(p); got != "where is the retry logic" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("later message wins", func(t *testing.T) {
		p := write(
			`{"type":"user","message":{"role":"user","content":"first question"}}`,
			`{"type":"user","message":{"role":"user","content":"second question"}}`)
		if got := lastUserMessage(p); got != "second question" {
			t.Errorf("got %q", got)
		}
	})

	// A tool result arrives as a user-role message. Treating it as the goal
	// would aim the search at the agent's own last output.
	t.Run("tool results are not the goal", func(t *testing.T) {
		p := write(
			`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"find the parser"}]}}`,
			`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"some output"}]}}`)
		if got := lastUserMessage(p); got != "find the parser" {
			t.Errorf("got %q, want the typed question", got)
		}
	})

	t.Run("system reminders are trimmed", func(t *testing.T) {
		p := write(`{"type":"user","message":{"role":"user","content":"explain the auth flow please<system-reminder>noise</system-reminder>"}}`)
		if got := lastUserMessage(p); got != "explain the auth flow please" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("missing transcript is not fatal", func(t *testing.T) {
		if got := lastUserMessage(filepath.Join(dir, "nope.jsonl")); got != "" {
			t.Errorf("got %q", got)
		}
	})
}
