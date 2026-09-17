// Package scan walks a source tree and reduces each file to a skeleton: the
// declarations and leading comments that say what the file is for.
//
// Skeletons exist because ranking wants breadth, not depth. A file's imports
// and function names identify its role better than its body does, and they
// cost a fraction of the tokens — which matters when the whole tree goes into
// one request.
package scan

import (
	"bufio"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// File is one candidate for ranking.
type File struct {
	Path     string // relative to the scan root, forward slashes
	Abs      string
	Bytes    int
	Lines    int
	Skeleton string
}

type Options struct {
	Root        string
	MaxFileSize int  // skip files larger than this (bytes); 0 = 2 MiB
	MaxSkelLine int  // lines kept per skeleton; 0 = 40
	IncludeAll  bool // keep tests, vendored and generated code
}

// Directories that never contain the answer and always cost tokens.
var skipDirs = map[string]bool{
	".git": true, ".svn": true, ".hg": true,
	"node_modules": true, "vendor": true, "bower_components": true,
	"build": true, "dist": true, "out": true, "target": true,
	".dart_tool": true, ".idea": true, ".vscode": true, ".ig": true,
	"__pycache__": true, ".venv": true, "venv": true, ".tox": true,
	".next": true, ".nuxt": true, ".cache": true, "coverage": true,
	"Pods": true, ".gradle": true, ".terraform": true,
}

var skipExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".ico": true, ".svg": true, ".pdf": true, ".zip": true, ".gz": true,
	".tar": true, ".bz2": true, ".xz": true, ".7z": true, ".jar": true,
	".mp4": true, ".mov": true, ".mp3": true, ".wav": true, ".ttf": true,
	".otf": true, ".woff": true, ".woff2": true, ".eot": true,
	".so": true, ".dylib": true, ".dll": true, ".exe": true, ".bin": true,
	".class": true, ".pyc": true, ".o": true, ".a": true,
	".lock": true, ".sum": true,
}

// Declarations worth keeping, across the languages this is likely to meet.
// One broad pattern beats a per-language table: it degrades gracefully on a
// language nobody anticipated.
var declRe = regexp.MustCompile(`^\s*(` +
	`package\b|import\b|from\s+\S+\s+import\b|` +
	`(?:public|private|protected|internal|static|final|abstract|export|default|async|extern|pub)\s+|` +
	`func\b|function\b|def\b|fn\b|sub\b|` +
	`type\b|struct\b|interface\b|enum\b|trait\b|impl\b|protocol\b|extension\b|` +
	`class\b|abstract\s+class\b|mixin\b|object\b|module\b|namespace\b|` +
	`const\b|var\b|let\b|val\b|static\b|` +
	`@\w+|` + // annotations / decorators
	`(?:CREATE|ALTER)\s+(?:TABLE|INDEX|VIEW)\b|` +
	`\w[\w<>\[\]\*\s,\.]*\s+\w+\s*\([^)]*\)\s*(?:\{|=>|;|$)` + // C-family method
	`)`)

// Route patterns: a line that registers a URL is usually the best single clue
// about what a web file does, and rarely looks like a declaration.
var routeRe = regexp.MustCompile(`(?i)(HandleFunc|\.(get|post|put|patch|delete)\s*\(|@(Get|Post|Put|Patch|Delete|Request)Mapping|route\s*\(|path:\s*['"]|r\.(Get|Post|Handle))`)

// Markup carries its meaning in forms, fields and headings rather than in
// declarations. Without this, a template collapses to its doctype — and a
// template is often exactly the file being looked for.
var markupRe = regexp.MustCompile(`(?i)(<form|<input|<select|<textarea|<button|<label|` +
	`\baction=|\bname=|\btype="(text|email|password|submit|tel|number)"|` +
	`<h[1-4]\b|<title|\{\{\s*(define|template|block|if|range)\b|` +
	`\{%\s*(block|for|if)\b|v-model=|ng-model=|formControlName=)`)

func (o *Options) fill() {
	if o.MaxFileSize == 0 {
		o.MaxFileSize = 2 << 20
	}
	if o.MaxSkelLine == 0 {
		o.MaxSkelLine = 40
	}
}

// Walk collects skeletons for every text file under the given paths.
func Walk(paths []string, opts Options) ([]File, error) {
	opts.fill()
	root := opts.Root
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	ignored := loadGitignore(absRoot)

	seen := map[string]bool{}
	var out []File

	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable entries are skipped, not fatal
			}
			name := d.Name()
			if d.IsDir() {
				if path != abs && (skipDirs[name] || strings.HasPrefix(name, ".") && name != ".") {
					return fs.SkipDir
				}
				return nil
			}
			if seen[path] {
				return nil
			}
			if skipExts[strings.ToLower(filepath.Ext(name))] || strings.HasPrefix(name, ".") {
				return nil
			}
			rel, rerr := filepath.Rel(absRoot, path)
			if rerr != nil {
				rel = path
			}
			rel = filepath.ToSlash(rel)
			if ignored(rel) {
				return nil
			}
			if !opts.IncludeAll && isNoise(rel) {
				return nil
			}
			info, ierr := d.Info()
			if ierr != nil || info.Size() == 0 || info.Size() > int64(opts.MaxFileSize) {
				return nil
			}
			f, ok := read(path, rel, opts.MaxSkelLine)
			if !ok {
				return nil
			}
			seen[path] = true
			out = append(out, f)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func isNoise(rel string) bool {
	base := strings.ToLower(filepath.Base(rel))
	switch {
	case strings.Contains(rel, "/testdata/"), strings.HasPrefix(rel, "testdata/"):
		return true
	case strings.HasSuffix(base, ".min.js"), strings.HasSuffix(base, ".min.css"):
		return true
	case strings.HasSuffix(base, ".pb.go"), strings.HasSuffix(base, "_generated.go"):
		return true
	case strings.HasSuffix(base, ".g.dart"), strings.HasSuffix(base, ".freezed.dart"):
		return true
	case strings.HasPrefix(base, "generatedpluginregistrant."):
		// Flutter's generated plugin registrar. It genuinely "registers"
		// things, so it scores well against unrelated registration queries
		// while never being the answer to one.
		return true
	}
	return false
}

// read builds one file's skeleton. It returns false for binary files.
func read(abs, rel string, maxLines int) (File, bool) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return File{}, false
	}
	// A NUL byte in the first 8k is the standard cheap binary test.
	head := data
	if len(head) > 8192 {
		head = head[:8192]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return File{}, false
	}

	var kept []string
	total := 0
	leading := true // still inside the opening comment / import block
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for sc.Scan() {
		line := sc.Text()
		total++
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if len(kept) >= maxLines {
			continue // keep counting lines, stop keeping them
		}

		isComment := strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") ||
			strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") ||
			strings.HasPrefix(trimmed, "--") || strings.HasPrefix(trimmed, "<!--")

		switch {
		case leading && total <= 12:
			// The top of a file names it: package, imports, doc comment.
			kept = append(kept, clip(trimmed))
			if !isComment && !declRe.MatchString(line) {
				leading = false
			}
		case declRe.MatchString(line), routeRe.MatchString(line), markupRe.MatchString(line):
			kept = append(kept, clip(trimmed))
		}
	}
	if sc.Err() != nil {
		return File{}, false
	}
	// Too few recognised lines means the heuristics do not fit this file type.
	// Falling back to the opening lines is worse than a real skeleton but much
	// better than the one-line stub that would otherwise represent the file —
	// and a file that cannot describe itself can never be found.
	if len(kept) < 6 {
		kept = merge(kept, firstLines(data, maxLines/2))
	}
	return File{
		Path:     rel,
		Abs:      abs,
		Bytes:    len(data),
		Lines:    total,
		Skeleton: strings.Join(kept, "\n"),
	}, true
}

// merge appends the lines of b that a does not already contain, preserving
// order and dropping duplicates.
func merge(a, b []string) []string {
	seen := make(map[string]bool, len(a))
	for _, s := range a {
		seen[s] = true
	}
	for _, s := range b {
		if !seen[s] {
			seen[s] = true
			a = append(a, s)
		}
	}
	return a
}

func firstLines(data []byte, n int) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() && len(out) < n {
		if t := strings.TrimSpace(sc.Text()); t != "" {
			out = append(out, clip(t))
		}
	}
	return out
}

func clip(s string) string {
	const max = 160
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// loadGitignore reads the root .gitignore and returns a matcher for the simple
// patterns that cover nearly all real entries: bare names, directory suffixes
// and single-extension globs. Anything more exotic is ignored rather than
// mis-applied — over-matching would silently hide files from the search.
func loadGitignore(root string) func(rel string) bool {
	data, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		return func(string) bool { return false }
	}
	var names, dirs, exts []string
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		line = strings.TrimPrefix(line, "/")
		switch {
		case strings.HasPrefix(line, "*."):
			exts = append(exts, line[1:])
		case strings.HasSuffix(line, "/"):
			dirs = append(dirs, strings.TrimSuffix(line, "/"))
		case !strings.ContainsAny(line, "*?["):
			names = append(names, line)
		}
	}
	return func(rel string) bool {
		for _, e := range exts {
			if strings.HasSuffix(rel, e) {
				return true
			}
		}
		segs := strings.Split(rel, "/")
		for _, d := range dirs {
			for _, s := range segs[:max(len(segs)-1, 0)] {
				if s == d {
					return true
				}
			}
		}
		for _, n := range names {
			if rel == n {
				return true
			}
			for _, s := range segs {
				if s == n {
					return true
				}
			}
		}
		return false
	}
}
