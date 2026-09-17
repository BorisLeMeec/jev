package run

import (
	"context"
	"fmt"
	"strings"

	"github.com/borislemeec/jev/internal/typesafe"
)

// maxChunks keeps a file inside one Choice question, which accepts at most 255
// options. Within a section the chunk grows rather than the count.
const maxChunks = 200

// maxSectionBytes bounds one request. The model accepts about 32k input tokens:
// measured, a 113 KB payload reported 31,014 input tokens and went through,
// while 134 KB was refused with max_tokens_exceeded. A chunked request carries
// the source roughly twice (text plus the criteria labels), so the source per
// request is held well under that.
const maxSectionBytes = 80_000

type located struct {
	Line   int
	Exists float64 // the "is it really here" noul; measured unreliable, see below
	Conf   float64 // how peaked the chunk distribution is
	Tokens int
}

// locateLine asks which run of lines in one file most directly implements the
// goal. It returns Line 0 when no chunk stands out, which is a real answer: the
// file is relevant without having a single place that is the answer.
func locateLine(ctx context.Context, c *typesafe.Client, goal, path string, data []byte) (located, error) {
	// A file too large for one request is cut into sections and each is located
	// independently; the section whose chunk distribution is most peaked wins.
	// That works because confidence is exactly the signal the hook already
	// trusts: a section that does not contain the answer spreads its
	// probability and loses to one that concentrates it.
	if len(data) > maxSectionBytes {
		return locateAcrossSections(ctx, c, goal, path, data)
	}
	return locateWithin(ctx, c, goal, path, data, 0)
}

// locateAcrossSections splits an oversized file on line boundaries and keeps the
// best-scoring section's answer, with line numbers mapped back to the whole file.
func locateAcrossSections(ctx context.Context, c *typesafe.Client, goal, path string, data []byte) (located, error) {
	lines := strings.Split(string(data), "\n")
	sections := (len(data) + maxSectionBytes - 1) / maxSectionBytes
	per := (len(lines) + sections - 1) / sections

	var best located
	var firstErr error
	for i := 0; i < len(lines); i += per {
		end := i + per
		if end > len(lines) {
			end = len(lines)
		}
		got, err := locateWithin(ctx, c, goal, path, []byte(strings.Join(lines[i:end], "\n")), i)
		best.Tokens += got.Tokens
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if got.Conf > best.Conf {
			got.Tokens = best.Tokens
			best = got
		}
	}
	if best.Line == 0 && firstErr != nil {
		return located{}, firstErr
	}
	return best, nil
}

// locateWithin does the real work over one span. lineOffset is how many lines of
// the original file precede this span, so reported lines are absolute.
func locateWithin(ctx context.Context, c *typesafe.Client, goal, path string, data []byte, lineOffset int) (located, error) {
	lines := strings.Split(string(data), "\n")

	size := 10
	if n := (len(lines) + maxChunks - 1) / maxChunks; n > size {
		size = n
	}

	type chunk struct {
		Start int    `json:"start_line"`
		Text  string `json:"text"`
	}
	chunks := map[string]chunk{}
	criteria := map[string]string{}
	for start := 0; start < len(lines); start += size {
		end := start + size
		if end > len(lines) {
			end = len(lines)
		}
		id := fmt.Sprintf("c%d", start/size)
		chunks[id] = chunk{Start: lineOffset + start + 1, Text: strings.Join(lines[start:end], "\n")}
		criteria[id] = fmt.Sprintf("lines %d-%d", lineOffset+start+1, lineOffset+end)
	}
	// A Choice needs something to choose between; a file this short is its own
	// answer.
	if len(criteria) < 2 {
		return located{Line: lineOffset + 1, Exists: 1, Conf: 1}, nil
	}

	resp, err := c.Ask(ctx, map[string]any{
		"goal": goal,
		"file": map[string]any{"path": path, "chunks": chunks},
	}, map[string]typesafe.Question{
		"where":  typesafe.Choice(locateInstructions, criteria),
		"exists": typesafe.Noul(locateExists),
	})
	if err != nil {
		return located{}, err
	}

	// Exists is kept for reporting but is not a safety signal: over twelve
	// labelled targets it read 0.26 and 0.31 on windows that were correct.
	// Conf — how concentrated the chunk distribution is — separated the twelve
	// hits (0.71-0.98) from the one observed miss (0.42).
	out := located{
		Exists: resp.Answers["exists"].Noul,
		Conf:   resp.Answers["where"].Confidence,
		Tokens: resp.Usage.InputTokens,
	}
	// The chosen line is always reported. Whether it is trustworthy enough to
	// act on is the caller's decision, because the cost of being wrong differs:
	// a stray line number next to a correctly-ranked file is a hint, while a
	// narrowed Read hides everything outside the window.
	if ch, ok := chunks[resp.Answers["where"].Choice]; ok {
		out.Line = ch.Start
	}
	return out, nil
}
