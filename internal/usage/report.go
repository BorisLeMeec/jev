package usage

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// claudeInputUSDPerMTok is Claude Opus 5's input price, used only for the
// comparison line. It is a reference point, not a claim: an agent would not
// have read every byte jev did.
const claudeInputUSDPerMTok = 5.0

const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	dim    = "\033[2m"
	green  = "\033[32m"
	cyan   = "\033[36m"
	yellow = "\033[33m"
)

// styler turns styling off when the output is not a terminal, or when NO_COLOR
// is set, so piping `jev gain` into a file gives plain text.
type styler struct{ on bool }

func newStyler(w io.Writer) styler {
	if os.Getenv("NO_COLOR") != "" {
		return styler{}
	}
	f, ok := w.(*os.File)
	if !ok {
		return styler{}
	}
	st, err := f.Stat()
	return styler{on: err == nil && st.Mode()&os.ModeCharDevice != 0}
}

func (s styler) c(code, text string) string {
	if !s.on {
		return text
	}
	return code + text + reset
}

// short renders a token count the way a reader scans it: 1.1M, 28.8K, 946.
func short(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

// bar draws a proportional bar, never empty when the value is non-zero — a
// command that ran deserves a mark, even a thin one.
func bar(value, max float64, width int) string {
	if max <= 0 {
		return strings.Repeat("░", width)
	}
	filled := int(value / max * float64(width))
	if filled < 1 && value > 0 {
		return "▏" + strings.Repeat("░", width-1)
	}
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

func money(v float64) string {
	if v > 0 && v < 0.01 {
		return fmt.Sprintf("$%.4f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

// Render writes the summary. The headline is leverage — tokens jev examined
// against tokens it put into the conversation — because that is the quantity
// actually measured. It is deliberately not called a saving: see the closing
// note, which is part of the output and not a footnote to be dropped.
func Render(w io.Writer, recs []Record, history bool) {
	s := newStyler(w)
	line := strings.Repeat("━", 62)

	byCmd := map[string]*Record{}
	runs := map[string]int{}
	var tot Record
	for _, r := range recs {
		agg, ok := byCmd[r.Command]
		if !ok {
			agg = &Record{Command: r.Command}
			byCmd[r.Command] = agg
		}
		runs[r.Command]++
		for _, p := range []*Record{agg, &tot} {
			p.Requests += r.Requests
			p.InTokens += r.InTokens
			p.Files += r.Files
			p.ScanBytes += r.ScanBytes
			p.OutTokens += r.OutTokens
		}
	}

	examined := tot.ScanBytes / 4
	returned := tot.OutTokens
	spent := float64(tot.InTokens) * PricePerMTok / 1e6
	elsewhere := float64(examined) * claudeInputUSDPerMTok / 1e6

	fmt.Fprintf(w, "\n%s\n%s\n", s.c(bold, "jev — token leverage"), s.c(dim, line))
	fmt.Fprintf(w, "  %-22s %s\n", "Runs:", s.c(bold, fmt.Sprintf("%d", len(recs))))
	fmt.Fprintf(w, "  %-22s %s\n", "Requests:", short(tot.Requests))
	fmt.Fprintf(w, "  %-22s %s %s\n", "Examined by jev:",
		s.c(cyan, short(examined)), s.c(dim, "tokens, out of context"))
	fmt.Fprintf(w, "  %-22s %s %s\n", "Returned to the agent:",
		s.c(yellow, short(returned)), s.c(dim, "tokens, into context"))

	if returned > 0 {
		lev := float64(examined) / float64(returned)
		fmt.Fprintf(w, "\n  %s %s  %s\n", s.c(dim, "examined "),
			s.c(cyan, bar(1, 1, 40)), s.c(cyan, short(examined)))
		fmt.Fprintf(w, "  %s %s  %s\n", s.c(dim, "returned "),
			s.c(yellow, bar(float64(returned), float64(examined), 40)), s.c(yellow, short(returned)))
		fmt.Fprintf(w, "\n  %s\n", s.c(bold+green,
			fmt.Sprintf("%.0f× leverage — %.0f tokens read for every 1 added to the conversation", lev, lev)))
	}

	if len(byCmd) > 0 {
		fmt.Fprintf(w, "\n%s\n", s.c(bold, "By command"))
		fmt.Fprintf(w, "%s\n", s.c(dim, line))
		fmt.Fprintf(w, "  %-8s %6s %9s %10s %10s %9s  %s\n",
			"command", "runs", "requests", "examined", "cost", "per run", "share")
		type row struct {
			name string
			rec  *Record
		}
		var rows []row
		for n, r := range byCmd {
			rows = append(rows, row{n, r})
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].rec.ScanBytes > rows[j].rec.ScanBytes })
		maxScan := float64(rows[0].rec.ScanBytes)
		for _, r := range rows {
			cost := float64(r.rec.InTokens) * PricePerMTok / 1e6
			n := runs[r.name]
			fmt.Fprintf(w, "  %-8s %6d %9s %10s %10s %9s  %s\n",
				r.name, n, short(r.rec.Requests), short(r.rec.ScanBytes/4),
				money(cost), money(cost/float64(max(n, 1))),
				s.c(cyan, bar(float64(r.rec.ScanBytes), maxScan, 12)))
		}
	}

	fmt.Fprintf(w, "\n%s\n", s.c(dim, line))
	fmt.Fprintf(w, "  %-36s %s\n", "Spent on jev:", s.c(bold+green, money(spent)))
	if elsewhere > 0 && spent > 0 {
		fmt.Fprintf(w, "  %-36s %s %s\n", "Same tokens at Opus 5 input rates:",
			money(elsewhere), s.c(dim, fmt.Sprintf("(%.0f× more, read once)", elsewhere/spent)))
	}

	fmt.Fprintf(w, "\n%s\n", s.c(dim,
		"  Examined counts what jev read on the agent's behalf. It is an upper\n"+
			"  bound on what the agent would otherwise have read, not a measured\n"+
			"  saving — and the comparison above assumes those bytes entered the\n"+
			"  context only once, when in practice they are re-read every turn."))

	if history {
		fmt.Fprintf(w, "\n%s\n%s\n", s.c(bold, "History"), s.c(dim, line))
		fmt.Fprintf(w, "  %-20s %-8s %8s %10s %10s\n", "when", "cmd", "reqs", "tokens", "cost")
		for _, r := range recs {
			fmt.Fprintf(w, "  %-20s %-8s %8d %10s %10s\n",
				r.When.Format("2006-01-02 15:04:05"), r.Command, r.Requests,
				short(r.InTokens), money(float64(r.InTokens)*PricePerMTok/1e6))
		}
	}
	fmt.Fprintln(w)
}
