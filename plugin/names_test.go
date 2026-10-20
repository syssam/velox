package plugin_test

import (
	"os"
	"strings"
	"testing"

	"github.com/syssam/velox2/plugin"
)

func TestSingular(t *testing.T) {
	for in, want := range map[string]string{
		"friends": "friend", "categories": "category", "boxes": "box", "matches": "match", "following": "following",
		"posts": "post", "children": "children", "buses": "bus", "wishes": "wish", "quizzes": "quizz", "ies": "ie", "s": "s",
	} {
		if got := plugin.Singular(in); got != want {
			t.Errorf("Singular(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestNamesGolden pins the four frozen helpers to testdata/names.golden. It has
// no update flag: changing the file by hand is the deliberate act.
func TestNamesGolden(t *testing.T) {
	data, err := os.ReadFile("testdata/names.golden")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	// The count is pinned, so that deleting a row fails. Adding a row means
	// raising it, by hand.
	const rows = 243
	if len(lines) != rows {
		t.Fatalf("golden has %d rows, want %d", len(lines), rows)
	}
	seen := map[string]bool{}
	for i, line := range lines {
		c := strings.Split(line, "\t")
		if len(c) != 5 {
			t.Fatalf("line %d: %d columns, want 5", i+1, len(c))
		}
		in := c[0]
		if seen[in] {
			t.Fatalf("line %d: input %q is listed twice", i+1, in)
		}
		seen[in] = true
		for _, x := range []struct{ name, got, want string }{
			{"Snake", plugin.Snake(in), c[1]},
			{"Plural", plugin.Plural(in), c[2]},
			{"Singular", plugin.Singular(in), c[3]},
			{"GoName", plugin.GoName(in), c[4]},
		} {
			if x.got != x.want {
				t.Errorf("line %d: %s(%q) = %q, want %q", i+1, x.name, in, x.got, x.want)
			}
		}
	}
}
