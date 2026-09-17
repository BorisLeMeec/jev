package run

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/borislemeec/jev/internal/scan"
	"github.com/borislemeec/jev/internal/usage"
)

// Scan is the dry run: it shows which files would be sent, what each one is
// reduced to, and what the request would cost — without calling the API or
// needing a key.
func Scan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, scanUsage) }
	var (
		show  = fs.String("show", "", "print the full skeleton of files whose path contains this")
		list  = fs.Bool("list", false, "print one path per line and nothing else")
		batch = fs.Int("batch", 25, "files per request, for the cost estimate")
		all   = fs.Bool("all", false, "include tests, generated and vendored files")
	)
	if err := fs.Parse(permute(args)); err != nil {
		return err
	}
	paths := fs.Args()
	if len(paths) == 0 {
		paths = []string{"."}
	}

	files, err := scan.Walk(paths, scan.Options{IncludeAll: *all})
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no readable source files under %s", strings.Join(paths, " "))
	}

	var srcBytes, skelBytes int
	for _, f := range files {
		srcBytes += f.Bytes
		skelBytes += len(f.Skeleton)
	}

	// Machine-readable corpus listing. Benchmarks and scripts need the exact
	// file set jev would consider, without the table's truncated paths.
	if *list {
		for _, f := range files {
			fmt.Println(f.Path)
		}
		return nil
	}

	if *show != "" {
		for _, f := range files {
			if !strings.Contains(f.Path, *show) {
				continue
			}
			fmt.Printf("── %s  (%d lines → %d skeleton lines)\n", f.Path, f.Lines, strings.Count(f.Skeleton, "\n")+1)
			fmt.Println(f.Skeleton)
			fmt.Println()
		}
		return nil
	}

	fmt.Printf("%-58s %7s %8s\n", "file", "lines", "skel")
	for _, f := range files {
		fmt.Printf("%-58s %7d %8d\n", trimPath(f.Path, 58), f.Lines, strings.Count(f.Skeleton, "\n")+1)
	}

	skelTok := skelBytes / 4
	requests := (len(files) + *batch - 1) / *batch
	fmt.Printf("\n%d files, %s of source reduced to %s of skeleton (%.0f%%).\n",
		len(files), human(srcBytes), human(skelBytes), 100*float64(skelBytes)/float64(maxInt(srcBytes, 1)))
	fmt.Printf("One `jev find` over this tree: %d requests, ~%d tokens, ~$%.4f.\n",
		requests, skelTok, float64(skelTok)*usage.PricePerMTok/1e6)
	fmt.Printf("Use --show <substring> to read the skeleton a file is reduced to.\n")
	return nil
}

func trimPath(p string, n int) string {
	if len(p) <= n {
		return p
	}
	return "…" + p[len(p)-n+1:]
}

func human(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
}

const scanUsage = `usage: jev scan [flags] [path...]

Dry run. Lists the files jev find would send, what each is reduced to, and the
cost of the request. Makes no API call and needs no key.

  jev scan
  jev scan internal/ --show api.go

flags:
`
