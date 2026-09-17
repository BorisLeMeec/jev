// Package usage records what each jev run cost and what it kept out of the
// agent's context.
//
// The second number is the point of the tool, so it is worth being careful
// about what it claims. "Avoided" counts the tokens of file content that jev
// read on the agent's behalf. It is an upper bound on what an agent would
// otherwise have pulled into its context, not a measured saving: an agent
// looking for one function might have read three files, or thirty.
package usage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// USD per million input tokens. Output tokens are not billed.
const PricePerMTok = 0.042

type Record struct {
	When      time.Time `json:"when"`
	Command   string    `json:"command"`
	Requests  int       `json:"requests"`
	InTokens  int       `json:"in_tokens"`
	Files     int       `json:"files"`
	ScanBytes int       `json:"scan_bytes"`
	OutTokens int       `json:"ret_tokens"` // what jev printed back into context
}

func dir() string {
	if d := strings.TrimSpace(os.Getenv("JEV_DATA")); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".jev"
	}
	return filepath.Join(home, ".jev")
}

func path() string { return filepath.Join(dir(), "usage.jsonl") }

// Log appends one record. Failure is silent: accounting must never break a
// search that otherwise worked.
func Log(r Record) {
	if os.Getenv("JEV_NO_STATS") != "" {
		return
	}
	r.When = time.Now()
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if b, err := json.Marshal(r); err == nil {
		fmt.Fprintf(f, "%s\n", b)
	}
}

// Report loads the log and hands it to Render.
func Report(w *os.File, history bool) error {
	data, err := os.ReadFile(path())
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(w, "No runs recorded yet.")
			return nil
		}
		return err
	}
	var recs []Record
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r Record
		if json.Unmarshal([]byte(line), &r) == nil {
			recs = append(recs, r)
		}
	}
	if len(recs) == 0 {
		fmt.Fprintln(w, "No runs recorded yet.")
		return nil
	}
	Render(w, recs, history)
	return nil
}

func cost(tokens int) float64 { return float64(tokens) * PricePerMTok / 1e6 }
