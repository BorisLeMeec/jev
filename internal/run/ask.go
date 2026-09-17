package run

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/borislemeec/jev/internal/scan"
	"github.com/borislemeec/jev/internal/typesafe"
	"github.com/borislemeec/jev/internal/usage"
)

// askInstructions keeps the judgment on the file rather than on the world. A
// question like "does this handle retries?" should be answered from the code in
// front of it, not from what the model knows about retry libraries.
//
// Answers come back strongly bimodal: over nine labelled questions the true
// files scored 0.92-0.99 and the false ones 0.02-0.13, with rare exceptions on
// either side. The default --yes of 0.5 sits in that empty middle. It was 0.7,
// chosen from nothing; 0.5 was measured to be weakly better (one fewer missed
// file, still no false alarms) and the curve between them is flat, so this is a
// small correction rather than a discovery. See bench/RESULTS_ASK.md.
const askInstructions = "Answer `question` about the source file at `file.path`, using only the code in " +
	"`file.content`. Answer yes only if the code itself shows it; do not assume behaviour that is not visible here."

type askResult struct {
	Path   string  `json:"path"`
	Score  float64 `json:"score"`
	Line   int     `json:"line,omitempty"`
	Answer string  `json:"answer"`
	err    error
}

// Ask puts a yes/no question to one or more files without the agent reading them.
func Ask(args []string) error {
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, askUsage) }
	var (
		yesAt    = fs.Float64("yes", 0.5, "probability at or above which the answer reads as yes")
		noAt     = fs.Float64("no", 0.3, "probability at or below which the answer reads as no")
		cite     = fs.Bool("cite", true, "locate the relevant line in files that answer yes")
		parallel = fs.Int("p", 6, "concurrent requests")
		quiet    = fs.Bool("q", false, "print only files that answer yes")
		asJSON   = fs.Bool("json", false, "emit JSON")
		debug    = fs.Bool("debug", false, "dump raw API traffic to stderr")
	)
	if err := fs.Parse(permute(args)); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 2 {
		fs.Usage()
		return fmt.Errorf("need a question and at least one file")
	}
	question, targets := rest[0], rest[1:]

	client, err := typesafe.New()
	if err != nil {
		return err
	}
	if *debug {
		client.Debug = os.Stderr
	}

	files, err := expand(targets)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no readable files in %s", strings.Join(targets, " "))
	}

	ctx := context.Background()
	var (
		mu      sync.Mutex
		results []askResult
		stats   usage.Record
		wg      sync.WaitGroup
		sem     = make(chan struct{}, maxInt(*parallel, 1))
	)

	for _, f := range files {
		wg.Add(1)
		go func(f scan.File) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			data, err := os.ReadFile(f.Abs)
			if err != nil {
				mu.Lock()
				results = append(results, askResult{Path: f.Path, err: err})
				mu.Unlock()
				return
			}

			score, tokens, err := askFile(ctx, client, question, f.Path, string(data))
			if err != nil {
				mu.Lock()
				results = append(results, askResult{Path: f.Path, err: err})
				mu.Unlock()
				return
			}

			r := askResult{Path: f.Path, Score: score, Answer: verdict(score, *yesAt, *noAt)}
			reqs := 1
			if *cite && score >= *yesAt {
				if got, lerr := locateLine(ctx, client, question, f.Path, data); lerr == nil && got.Conf >= 0.5 {
					r.Line = got.Line
					tokens += got.Tokens
					reqs++
				}
			}

			mu.Lock()
			stats.Requests += reqs
			stats.InTokens += tokens
			stats.ScanBytes += len(data)
			results = append(results, r)
			mu.Unlock()
		}(f)
	}
	wg.Wait()

	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })

	out := renderAsk(results, *quiet, *yesAt, *asJSON)
	fmt.Print(out)

	stats.Command = "ask"
	stats.Files = len(files)
	stats.OutTokens = len(out) / 4
	usage.Log(stats)
	return nil
}

// maxContent bounds one request. The model accepts about 32k input tokens —
// measured, 113 KB of payload reported 31,014 and passed, 134 KB was refused
// with max_tokens_exceeded — so a file beyond this is split and the highest
// answer wins: if any part of the file says yes, the file says yes. This was
// 200_000, which silently turned every large file into a 400.
const maxContent = 90_000

func askFile(ctx context.Context, c *typesafe.Client, question, path, content string) (float64, int, error) {
	parts := []string{content}
	if len(content) > maxContent {
		parts = nil
		for i := 0; i < len(content); i += maxContent {
			parts = append(parts, content[i:minInt(i+maxContent, len(content))])
		}
	}

	best, tokens := 0.0, 0
	for _, part := range parts {
		resp, err := c.Ask(ctx, map[string]any{
			"question": question,
			"file":     map[string]any{"path": path, "content": part},
		}, map[string]typesafe.Question{
			"answer": typesafe.Noul(askInstructions),
		})
		if err != nil {
			return 0, tokens, err
		}
		tokens += resp.Usage.InputTokens
		if v := resp.Answers["answer"].Noul; v > best {
			best = v
		}
	}
	return best, tokens, nil
}

// expand turns file and directory arguments into a file list. A directory is
// walked with the same filters as find, so `jev ask "..." internal/` behaves.
func expand(targets []string) ([]scan.File, error) {
	var direct []scan.File
	var dirs []string
	for _, t := range targets {
		info, err := os.Stat(t)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t, err)
		}
		if info.IsDir() {
			dirs = append(dirs, t)
			continue
		}
		abs, err := absPath(t)
		if err != nil {
			return nil, err
		}
		direct = append(direct, scan.File{Path: t, Abs: abs, Bytes: int(info.Size())})
	}
	if len(dirs) > 0 {
		walked, err := scan.Walk(dirs, scan.Options{})
		if err != nil {
			return nil, err
		}
		direct = append(direct, walked...)
	}
	return direct, nil
}

func verdict(score, yesAt, noAt float64) string {
	switch {
	case score >= yesAt:
		return "yes"
	case score <= noAt:
		return "no"
	default:
		return "unclear"
	}
}

func renderAsk(results []askResult, quiet bool, yesAt float64, asJSON bool) string {
	if asJSON {
		b, _ := json.MarshalIndent(results, "", "  ")
		return string(b) + "\n"
	}
	var b strings.Builder
	shown := 0
	for _, r := range results {
		if r.err != nil {
			fmt.Fprintf(&b, "  ----  error  %s (%v)\n", r.Path, r.err)
			continue
		}
		if quiet && r.Score < yesAt {
			continue
		}
		loc := r.Path
		if r.Line > 0 {
			loc = fmt.Sprintf("%s:%d", r.Path, r.Line)
		}
		fmt.Fprintf(&b, "  %.2f  %-7s %s\n", r.Score, r.Answer, loc)
		shown++
	}
	if shown == 0 && b.Len() == 0 {
		return "no file answers yes\n"
	}
	return b.String()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

const askUsage = `usage: jev ask [flags] "<yes/no question>" <file|dir...>

Puts one question to each file and reports the probability that the answer is
yes, with the relevant line for the files that say yes. jev reads the files;
the agent reads only these lines.

  jev ask "does this validate the email before saving?" internal/app/api.go
  jev ask "does this call the network on the main thread?" mobile/lib/
  jev ask "is there a SQL query built by string concatenation?" internal/ -q

flags:
`
