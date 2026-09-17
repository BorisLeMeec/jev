package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write builds a throwaway tree and returns its root.
func write(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func walk(t *testing.T, root string) map[string]File {
	t.Helper()
	files, err := Walk([]string{root}, Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]File{}
	for _, f := range files {
		out[f.Path] = f
	}
	return out
}

func TestSkeletonKeepsDeclarations(t *testing.T) {
	root := write(t, map[string]string{
		"api.go": `// Package app serves the API.
package app

import "net/http"

func (a *App) apiSignup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("first_name")
	if name == "" {
		http.Error(w, "bad", 400)
		return
	}
	_ = name
}

type userJSON struct {
	Email string
}
`,
	})
	got := walk(t, root)["api.go"]

	for _, want := range []string{"package app", "func (a *App) apiSignup", "type userJSON struct"} {
		if !strings.Contains(got.Skeleton, want) {
			t.Errorf("skeleton missing %q\n--- got ---\n%s", want, got.Skeleton)
		}
	}
	// Bodies are the bulk of a file and say little about its role.
	if strings.Contains(got.Skeleton, `http.Error(w, "bad", 400)`) {
		t.Errorf("skeleton kept a statement from inside a function body:\n%s", got.Skeleton)
	}
	if got.Lines != 17 {
		t.Errorf("Lines = %d, want 17 (the real file length, not the skeleton's)", got.Lines)
	}
}

// A template's meaning is in its form fields. Before markupRe existed this file
// reduced to its first line, which made it unfindable.
func TestSkeletonKeepsFormFields(t *testing.T) {
	root := write(t, map[string]string{
		"join.html": `{{template "head" .}}
<div class="wrap">
<h1>Salut 👋</h1>
<form method="post" action="/p/{{.Token}}/signup">
<label>Prénom <input name="first_name" required></label>
<label>Email <input name="email" type="email" required></label>
<button class="primary">C'est parti</button>
</form>
</div>
{{template "foot" .}}
`,
	})
	skel := walk(t, root)["join.html"].Skeleton

	for _, want := range []string{"signup", "first_name", "email", "<form"} {
		if !strings.Contains(skel, want) {
			t.Errorf("skeleton missing %q\n--- got ---\n%s", want, skel)
		}
	}
}

// Any file type must end up with enough text to describe itself, or it can
// never be found. The fallback covers the formats the heuristics do not know.
func TestUnknownFormatFallsBackToOpeningLines(t *testing.T) {
	root := write(t, map[string]string{
		"config.toml": `title = "passeD"
[server]
addr = ":8080"
[database]
path = "data/passed.db"
[uploads]
dir = "data/uploads"
max_mb = 12
`,
	})
	skel := walk(t, root)["config.toml"].Skeleton
	if n := len(strings.Split(skel, "\n")); n < 6 {
		t.Errorf("fallback kept only %d lines, too few to identify the file:\n%s", n, skel)
	}
	if !strings.Contains(skel, "passed.db") {
		t.Errorf("fallback lost the distinguishing content:\n%s", skel)
	}
}

func TestSkipsBinaryAndVendored(t *testing.T) {
	root := write(t, map[string]string{
		"main.go":                   "package main\n",
		"logo.png":                  "\x89PNG\r\n\x1a\n\x00\x00binary",
		"blob.dat":                  "text then \x00 a NUL byte",
		"node_modules/dep/index.js": "export const x = 1\n",
		"vendor/lib/lib.go":         "package lib\n",
		".git/config":               "[core]\n",
	})
	got := walk(t, root)

	if _, ok := got["main.go"]; !ok {
		t.Error("main.go was skipped")
	}
	for _, skipped := range []string{"logo.png", "blob.dat", "node_modules/dep/index.js", "vendor/lib/lib.go", ".git/config"} {
		if _, ok := got[skipped]; ok {
			t.Errorf("%s should have been skipped", skipped)
		}
	}
}

func TestGitignoreIsApplied(t *testing.T) {
	root := write(t, map[string]string{
		".gitignore":  "data/\n*.log\nsecret.txt\n",
		"keep.go":     "package keep\n",
		"secret.txt":  "token\n",
		"app.log":     "line\n",
		"data/db.sql": "CREATE TABLE users (id INT);\n",
	})
	got := walk(t, root)

	if _, ok := got["keep.go"]; !ok {
		t.Error("keep.go was ignored but should not be")
	}
	for _, skipped := range []string{"secret.txt", "app.log", "data/db.sql"} {
		if _, ok := got[skipped]; ok {
			t.Errorf("%s matches .gitignore and should have been skipped", skipped)
		}
	}
}

// A skeleton that grew without bound would defeat the point of batching.
func TestSkeletonIsBounded(t *testing.T) {
	var b strings.Builder
	for i := range 500 {
		b.WriteString("func f")
		b.WriteString(string(rune('A' + i%26)))
		b.WriteString("() {}\n")
	}
	root := write(t, map[string]string{"big.go": "package big\n" + b.String()})

	files, err := Walk([]string{root}, Options{Root: root, MaxSkelLine: 40})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(strings.Split(files[0].Skeleton, "\n")); n > 40 {
		t.Errorf("skeleton kept %d lines, want at most 40", n)
	}
	if files[0].Lines < 500 {
		t.Errorf("Lines = %d, want the true file length", files[0].Lines)
	}
}
