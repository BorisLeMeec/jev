package run

import (
	"reflect"
	"testing"
)

// Go's flag package stops parsing at the first non-flag argument, so flags
// written after the query were silently swallowed as file paths — `jev find
// "x" --min 0.9` ran with the default threshold and no warning.
func TestPermuteMovesFlagsAhead(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []string
		want []string
	}{
		{"trailing value flag", []string{"the query", "--min", "0.9"}, []string{"--min", "0.9", "the query"}},
		{"trailing bool flag", []string{"the query", "--json"}, []string{"--json", "the query"}},
		{"leading flags untouched", []string{"-n", "3", "the query"}, []string{"-n", "3", "the query"}},
		{"equals form", []string{"the query", "--min=0.9", "internal/"}, []string{"--min=0.9", "the query", "internal/"}},
		{"bool then path", []string{"the query", "--all", "internal/"}, []string{"--all", "the query", "internal/"}},
		{"mixed", []string{"the query", "internal/", "-n", "3", "--debug"}, []string{"-n", "3", "--debug", "the query", "internal/"}},
		{"double dash stops", []string{"--json", "the query", "--", "-weird-path"}, []string{"--json", "the query", "-weird-path"}},
		{"no flags", []string{"the query", "internal/"}, []string{"the query", "internal/"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := permute(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("permute(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

// A query that happens to start with a dash must not be eaten as a flag value.
func TestPermuteKeepsQueryAfterBoolFlag(t *testing.T) {
	got := permute([]string{"--all", "where is signup", "web/"})
	want := []string{"--all", "where is signup", "web/"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q want %q", got, want)
	}
}
