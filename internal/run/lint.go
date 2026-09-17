package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/borislemeec/jev/internal/typesafe"
	"github.com/borislemeec/jev/internal/usage"
)

// Lint checks a just-made edit against the project's own written conventions.
//
// It is the opposite of the Read hook in the one way that matters: it only ever
// adds a line of context, never removes anything. A wrong answer here costs the
// agent a moment's attention; a wrong answer in the Read hook hides code. That
// asymmetry is why it can afford to run on every edit. It is nonetheless off
// until JEV_LINT_ENABLE is set: the rules are a project's own, the useful ones
// are not obvious to write, and a checker nobody has tuned is just noise on
// every edit. Opt in once you have rules worth enforcing.
//
// Rules live in .jev-rules.json at the repository root, so a project states its
// own conventions rather than inheriting someone else's idea of good style:
//
//	[{"id": "raw-material",
//	  "paths": ["*.dart"],
//	  "instructions": "Does this change add a Material widget where theme.dart
//	                   already offers an equivalent?",
//	  "criteria": {"true": "...", "false": "..."}}]
//
// All applicable rules travel in one request over one state — different
// questions about the same diff, which is the shape batching is actually good
// at, unlike one question about many different files.
type lintRule struct {
	ID           string            `json:"id"`
	Paths        []string          `json:"paths"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria,omitempty"`
	Threshold    float64           `json:"threshold,omitempty"`
}

const lintRulesFile = ".jev-rules.json"

// defaultLintThreshold is measured on 27 labelled changes, 11 of them written
// to be near-misses: real violations scored 0.64-0.96 and clean changes
// 0.02-0.52, and 0.60 sits in that gap. At 0.5 the hardest near-miss — an
// English code comment beside French UI copy — fired at 0.52; at 0.60 there
// were no false alarms and nothing was missed. Fitted to 27 cases, so a rule
// may override it with its own "threshold".
const defaultLintThreshold = 0.60

// maxDiffBytes keeps one request inside the model's ~32k input tokens with room
// to spare; a change bigger than this is reported unchecked rather than half
// checked.
const maxDiffBytes = 60_000

type lintInput struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
	CWD       string         `json:"cwd"`
}

func (r lintRule) applies(path string) bool {
	if len(r.Paths) == 0 {
		return true
	}
	base := filepath.Base(path)
	for _, pat := range r.Paths {
		if ok, _ := filepath.Match(pat, base); ok {
			return true
		}
		if strings.Contains(filepath.ToSlash(path), strings.TrimSuffix(pat, "*")) &&
			strings.HasSuffix(pat, "*") {
			return true
		}
	}
	return false
}

// loadRules walks up from the edited file looking for the rules file, so an edit
// anywhere in a repository finds the conventions declared at its root.
func loadRules(start string) ([]lintRule, string) {
	dir := start
	for i := 0; i < 12; i++ {
		p := filepath.Join(dir, lintRulesFile)
		if data, err := os.ReadFile(p); err == nil {
			var rules []lintRule
			if json.Unmarshal(data, &rules) == nil {
				return rules, p
			}
			return nil, "" // a malformed rules file is silence, not noise
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil, ""
}

func Lint(args []string) error {
	if len(args) == 0 || args[0] != "edit" {
		return fmt.Errorf("usage: jev lint edit   (reads a PostToolUse payload on stdin)")
	}
	// Off by default. Unlike find, ask and the Read hook, this one earns its
	// keep only against rules the project actually wrote.
	if strings.TrimSpace(os.Getenv("JEV_LINT_ENABLE")) == "" ||
		strings.TrimSpace(os.Getenv("JEV_LINT_DISABLE")) != "" {
		lintDebug("disabled (set JEV_LINT_ENABLE=1 to turn it on)")
		return emitLint("")
	}

	var in lintInput
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		lintDebug("unreadable payload: %v", err)
		return emitLint("")
	}
	lintDebug("tool=%s", in.ToolName)
	switch in.ToolName {
	case "Edit", "Write", "NotebookEdit":
	default:
		lintDebug("not an edit tool")
		return emitLint("")
	}

	path, _ := in.ToolInput["file_path"].(string)
	if path == "" {
		lintDebug("no file_path")
		return emitLint("")
	}
	before, _ := in.ToolInput["old_string"].(string)
	after, _ := in.ToolInput["new_string"].(string)
	if after == "" {
		after, _ = in.ToolInput["content"].(string) // Write
	}
	if strings.TrimSpace(after) == "" {
		lintDebug("nothing added")
		return emitLint("")
	}
	if len(before)+len(after) > maxDiffBytes {
		lintDebug("change is %d bytes, over the limit", len(before)+len(after))
		return emitLint("")
	}

	rules, rulesPath := loadRules(filepath.Dir(path))
	lintDebug("loaded %d rules from %q", len(rules), rulesPath)
	var apply []lintRule
	for _, r := range rules {
		if r.Instructions != "" && r.applies(path) {
			apply = append(apply, r)
		}
	}
	if len(apply) == 0 {
		lintDebug("no rule applies to %s", path)
		return emitLint("")
	}

	client, err := typesafe.New()
	if err != nil {
		lintDebug("no API key")
		return emitLint("")
	}

	questions := make(map[string]typesafe.Question, len(apply))
	for _, r := range apply {
		questions[r.ID] = typesafe.Question{
			Type: "noul", Instructions: r.Instructions, Criteria: r.Criteria,
		}
	}
	resp, err := client.Ask(ctxBackground(), map[string]any{
		"file":   path,
		"before": before,
		"after":  after,
	}, questions)
	if err != nil {
		lintDebug("request failed: %v", err)
		return emitLint("")
	}
	for _, r := range apply {
		lintDebug("  %-28s %.2f", r.ID, resp.Answers[r.ID].Noul)
	}
	// Account for the spend: `jev gain` should show what the linter costs, not
	// silently leave it out of the total.
	usage.Log(usage.Record{
		Command: "lint", Requests: 1, Files: 1,
		InTokens: resp.Usage.InputTokens, ScanBytes: len(before) + len(after),
	})

	type fired struct {
		id    string
		score float64
	}
	var hits []fired
	for _, r := range apply {
		t := r.Threshold
		if t <= 0 {
			t = defaultLintThreshold
		}
		if v := resp.Answers[r.ID].Noul; v >= t {
			hits = append(hits, fired{r.ID, v})
		}
	}
	if len(hits) == 0 {
		return emitLint("")
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].score > hits[j].score })

	var b strings.Builder
	fmt.Fprintf(&b, "jev lint on %s — this change may break a project convention:\n", filepath.Base(path))
	for _, h := range hits {
		fmt.Fprintf(&b, "  %.2f  %s\n", h.score, h.id)
	}
	b.WriteString("These are probabilities from a small model, not proof. Check the change; " +
		"if the rule does not apply here, say so and move on.")
	return emitLint(b.String())
}

// lintDebug reports to stderr under JEV_LINT_DEBUG. A linter that stays quiet
// is indistinguishable from one that is broken, and the quiet case is the
// common one — so the scores have to be inspectable.
func lintDebug(format string, args ...any) {
	if os.Getenv("JEV_LINT_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "jev lint: "+format+"\n", args...)
	}
}

func emitLint(context string) error {
	out := map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PostToolUse"}}
	if context != "" {
		out["additionalContext"] = context
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}

func ctxBackground() context.Context { return context.Background() }
