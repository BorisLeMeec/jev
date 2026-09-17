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

// Two judgments, deliberately identical in wording except for what they are
// shown. The screen sees a skeleton, the verify pass sees the whole file, so
// the two scores stay comparable and the second can simply replace the first.
//
// Both reference `file.path` in a state holding exactly one file, and that is
// load-bearing rather than tidy. Packing several files into one request and
// asking about `files[i]` measured far worse: the answers stop binding to their
// file, and the ranking inverts — a true positive at 0.70 fell to 0.16 while a
// false positive at 0.06 rose to 0.55. One file per request.
const screenInstructions = "Is the file at `file.path` one of the places where what `goal` describes is actually " +
	"carried out? Judge only from `file.skeleton`, a reduction of the file to its declarations, by what this " +
	"file's own code does — not by whether it uses the same words as `goal`. The codebase may name the concept " +
	"differently, in another natural language, or with an internal term."

const verifyInstructions = "Is the file at `file.path` one of the places where what `goal` describes is actually " +
	"carried out? Judge from `file.content`, the file's full source, by what this file's own code does — not by " +
	"whether it uses the same words as `goal`. The codebase may name the concept differently, in another natural " +
	"language, or with an internal term."

var screenCriteria = map[string]string{
	"true": "This file's own code performs it: it defines the handler, endpoint, form, screen, query or logic " +
		"that carries it out. Removing this file would break it.",
	"false": "Everything else, including files that only support it — shared components, styling, helpers, " +
		"configuration, dependency manifests — and files that merely call it or link to it.",
}

const locateInstructions = "Which numbered chunk of `file.chunks` contains the part of this file that most " +
	"directly implements what `goal` describes? Each chunk is a run of consecutive lines from the file at `file.path`."

const locateExists = "Does any chunk in `file.chunks` actually implement what `goal` describes, rather than " +
	"merely mentioning or calling it?"

type hit struct {
	Path     string  `json:"path"`
	Score    float64 `json:"score"`
	Line     int     `json:"line,omitempty"`
	Verified bool    `json:"verified"`
	abs      string
}

// Find ranks every file in a tree against a natural-language goal.
func Find(args []string) error {
	fs := flag.NewFlagSet("find", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, findUsage) }
	var (
		min      = fs.Float64("min", 0.6, "minimum probability to report a file")
		limit    = fs.Int("n", 10, "maximum files to report")
		verify   = fs.Int("verify", 8, "re-score this many top files against their full contents (0 disables)")
		locate   = fs.Int("locate", 3, "find the line inside this many top files (0 disables)")
		parallel = fs.Int("p", 8, "concurrent requests")
		maxFiles = fs.Int("max-files", 800, "refuse to scan more files than this without an explicit raise")
		all      = fs.Bool("all", false, "include tests, generated and vendored files")
		asJSON   = fs.Bool("json", false, "emit JSON")
		debug    = fs.Bool("debug", false, "dump raw API traffic to stderr")
	)
	if err := fs.Parse(permute(args)); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return fmt.Errorf("missing the question to search for")
	}
	goal := rest[0]
	paths := rest[1:]
	if len(paths) == 0 {
		paths = []string{"."}
	}

	client, err := typesafe.New()
	if err != nil {
		return err
	}
	if *debug {
		client.Debug = os.Stderr
	}

	files, err := scan.Walk(paths, scan.Options{IncludeAll: *all})
	if err != nil {
		return fmt.Errorf("scanning: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("no readable source files under %s", strings.Join(paths, " "))
	}
	if len(files) > *maxFiles {
		return fmt.Errorf("%d files is more than the --max-files limit of %d; narrow the path or raise the limit "+
			"(one request per file, so this would be %d requests)", len(files), *maxFiles, len(files))
	}

	ctx := context.Background()
	var stats usage.Record

	hits, err := screen(ctx, client, goal, files, *parallel, &stats)
	if err != nil {
		return err
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })

	// The screen is cheap and lossy; it decides what is worth a closer look, not
	// what the answer is. Verifying more files than are reported leaves room for
	// one to climb into the results on its full contents.
	if *verify > 0 {
		verifyTop(ctx, client, goal, hits[:min2(*verify, len(hits))], *parallel, &stats)
		sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	}

	var kept []hit
	for _, h := range hits {
		if h.Score < *min || len(kept) >= *limit {
			break
		}
		kept = append(kept, h)
	}
	if len(kept) > 0 && *locate > 0 {
		locateLines(ctx, client, goal, kept[:min2(*locate, len(kept))], &stats)
	}

	out := render(goal, len(files), hits, kept, *asJSON)
	fmt.Print(out)

	stats.Command = "find"
	stats.Files = len(files)
	stats.OutTokens = len(out) / 4
	usage.Log(stats)
	return nil
}

// screen scores every file from its skeleton, one file per request.
func screen(ctx context.Context, c *typesafe.Client, goal string, files []scan.File, parallel int, stats *usage.Record) ([]hit, error) {
	var (
		mu   sync.Mutex
		hits []hit
		fail error
		wg   sync.WaitGroup
		sem  = make(chan struct{}, maxInt(parallel, 1))
	)
	for _, f := range files {
		wg.Add(1)
		go func(f scan.File) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			resp, err := c.Ask(ctx, map[string]any{
				"goal": goal,
				"file": map[string]any{"path": f.Path, "skeleton": f.Skeleton},
			}, map[string]typesafe.Question{
				"match": {Type: "noul", Instructions: screenInstructions, Criteria: screenCriteria},
			})

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if fail == nil {
					fail = err
				}
				return
			}
			stats.Requests++
			stats.InTokens += resp.Usage.InputTokens
			stats.ScanBytes += len(f.Skeleton)
			hits = append(hits, hit{Path: f.Path, Score: resp.Answers["match"].Noul, abs: f.Abs})
		}(f)
	}
	wg.Wait()

	if fail != nil && len(hits) == 0 {
		return nil, fail
	}
	return hits, nil
}

// verifyTop re-scores the leading candidates against their full contents, which
// separates them far better than skeletons do: a skeleton drops the body, and
// the body is often what makes a file the answer rather than a neighbour of it.
func verifyTop(ctx context.Context, c *typesafe.Client, goal string, hits []hit, parallel int, stats *usage.Record) {
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, maxInt(parallel, 1))
	)
	for i := range hits {
		wg.Add(1)
		go func(h *hit) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			data, err := os.ReadFile(h.abs)
			if err != nil {
				return
			}
			content := string(data)
			if len(content) > maxContent {
				content = content[:maxContent]
			}
			resp, err := c.Ask(ctx, map[string]any{
				"goal": goal,
				"file": map[string]any{"path": h.Path, "content": content},
			}, map[string]typesafe.Question{
				"match": {Type: "noul", Instructions: verifyInstructions, Criteria: screenCriteria},
			})
			if err != nil {
				return // keep the screen's score rather than dropping the file
			}
			mu.Lock()
			stats.Requests++
			stats.InTokens += resp.Usage.InputTokens
			stats.ScanBytes += len(data)
			mu.Unlock()

			h.Score = resp.Answers["match"].Noul
			h.Verified = true
		}(&hits[i])
	}
	wg.Wait()
}

// locateLines turns "this file" into "this line", which is what actually saves
// the agent a Read.
func locateLines(ctx context.Context, c *typesafe.Client, goal string, hits []hit, stats *usage.Record) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := range hits {
		wg.Add(1)
		go func(h *hit) {
			defer wg.Done()
			data, err := os.ReadFile(h.abs)
			if err != nil {
				return
			}
			got, err := locateLine(ctx, c, goal, h.Path, data)
			if err != nil {
				return
			}
			mu.Lock()
			stats.Requests++
			stats.InTokens += got.Tokens
			mu.Unlock()
			// A line is a hint beside an already-ranked file, so a loose gate:
			// better to omit the line than to point at the wrong one.
			if got.Conf >= 0.5 {
				h.Line = got.Line
			}
		}(&hits[i])
	}
	wg.Wait()
}

func render(goal string, scanned int, all, kept []hit, asJSON bool) string {
	if asJSON {
		b, _ := json.MarshalIndent(map[string]any{"goal": goal, "scanned": scanned, "matches": kept}, "", "  ")
		return string(b) + "\n"
	}
	var b strings.Builder
	if len(kept) == 0 {
		best, bestPath := 0.0, ""
		if len(all) > 0 {
			best, bestPath = all[0].Score, all[0].Path
		}
		fmt.Fprintf(&b, "no match in %d files (best %.2f: %s)\n", scanned, best, bestPath)
		return b.String()
	}
	fmt.Fprintf(&b, "%d files scanned, %d match:\n", scanned, len(kept))
	for _, h := range kept {
		loc := h.Path
		if h.Line > 0 {
			loc = fmt.Sprintf("%s:%d", h.Path, h.Line)
		}
		mark := " "
		if !h.Verified {
			mark = "~" // scored from the skeleton only
		}
		fmt.Fprintf(&b, "  %.2f%s %s\n", h.Score, mark, loc)
	}
	return b.String()
}

// permute moves flags ahead of positional arguments so that both
// `jev find -n 3 "query"` and `jev find "query" -n 3` work. Go's flag package
// stops at the first non-flag, which silently turned trailing flags into paths.
func permute(args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i+1:]...)
			break
		}
		if len(a) > 1 && a[0] == '-' {
			flags = append(flags, a)
			// A flag written as "-n 3" takes the next argument with it; one
			// written as "-n=3" or a bool flag does not.
			if !strings.Contains(a, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") &&
				!boolFlags[strings.TrimLeft(a, "-")] {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		rest = append(rest, a)
	}
	return append(flags, rest...)
}

var boolFlags = map[string]bool{"all": true, "json": true, "debug": true, "q": true, "cite": true, "list": true, "h": true, "help": true}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

const findUsage = `usage: jev find [flags] "<what you are looking for>" [path...]

Ranks every source file under path against a plain-language description and
prints the ones that match, with a line number where it can find one. Files are
read by jev, not by the agent, so only the result enters the context.

Two passes: every file is screened from its declarations, then the leading
candidates are re-scored against their full contents. A score marked "~" comes
from the screen alone and is less reliable.

  jev find "where is the register flow"
  jev find "the code that rotates uploaded photos" internal/
  jev find "rate limiting" --min 0.7 -n 5

flags:
`
