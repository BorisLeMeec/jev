package run

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/borislemeec/jev/internal/typesafe"
)

func absPath(p string) (string, error) { return filepath.Abs(p) }

// Probe sends one small request exercising all three primitives and prints the
// raw response.
//
// The answer shapes this tool decodes were written from the published docs, not
// from a live response. Run this first: if a field name differs, it shows up
// here as a zero where a number should be, next to the JSON that proves it.
func Probe(args []string) error {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, probeUsage) }
	if err := fs.Parse(args); err != nil {
		return err
	}

	client, err := typesafe.New()
	if err != nil {
		return err
	}
	client.Debug = os.Stdout

	state := map[string]any{
		"file": map[string]any{
			"path":    "internal/app/photo.go",
			"content": "func rotate(img image.Image, exifOrientation int) image.Image {\n\t// turn the image upright before re-encoding\n}",
		},
	}

	fmt.Println("== raw response ==")
	resp, err := client.Ask(context.Background(), state, map[string]typesafe.Question{
		"noul":   typesafe.Noul("Does the code in `file.content` change how an image is oriented?"),
		"choice": typesafe.Choice("What does the code in `file.content` mainly do?", map[string]string{"images": "Processes or transforms images", "network": "Sends or receives data over a network", "storage": "Reads or writes a database"}),
		"score":  typesafe.Score("How much of the work is visible in `file.content`?", []string{"Only a signature, no body", "A partial implementation", "The complete implementation"}),
	})
	if err != nil {
		return err
	}

	fmt.Println("\n== decoded ==")
	fmt.Printf("model: %s\n", resp.Model)
	fmt.Printf("usage: %d input tokens (%.6f USD)\n", resp.Usage.InputTokens, float64(resp.Usage.InputTokens)*0.042/1e6)
	for _, id := range []string{"noul", "choice", "score"} {
		a, ok := resp.Answers[id]
		if !ok {
			fmt.Printf("%-7s MISSING from answers\n", id)
			continue
		}
		fmt.Printf("%-7s type=%q noul=%.3f choice=%q score=%.3f confidence=%.3f probs=%d\n",
			id, a.Type, a.Noul, a.Choice, a.Score, a.Confidence, len(a.Probabilities))
	}

	fmt.Println("\nCheck each decoded row against the raw JSON above. A field that")
	fmt.Println("stayed zero where the raw body has a value means the struct tag in")
	fmt.Println("internal/typesafe/client.go needs renaming to match.")
	return nil
}

const probeUsage = `usage: jev probe

Sends one small request using all three primitives and prints the raw JSON
alongside what this tool decoded from it. Use it to verify the API contract
after installing, and whenever answers look wrong.
`
