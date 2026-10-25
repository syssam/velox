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
		"posts": "post", "children": "child", "buses": "bus", "wishes": "wish", "quizzes": "quiz", "ies": "ie", "s": "",
	} {
		if got := plugin.Singular(in); got != want {
			t.Errorf("Singular(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGoPlural(t *testing.T) {
	for in, want := range map[string]string{
		"User": "Users", "Status": "StatusSlice", "Settings": "SettingsSlice", "Media": "MediaSlice",
		"Person": "Persons", "Category": "Categories", "Index": "Indexes", "index": "indices",
	} {
		if got := plugin.GoPlural(in); got != want {
			t.Errorf("GoPlural(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestNamesGolden pins the five frozen helpers to testdata/names.golden. It has
// no update flag: changing the file by hand is the deliberate act.
func TestNamesGolden(t *testing.T) {
	data, err := os.ReadFile("testdata/names.golden")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	// The count is pinned, so that deleting a row fails. Adding a row means
	// raising it, by hand.
	const rows = 268
	if len(lines) != rows {
		t.Fatalf("golden has %d rows, want %d", len(lines), rows)
	}
	seen := map[string]bool{}
	for i, line := range lines {
		c := strings.Split(line, "\t")
		if len(c) != 6 {
			t.Fatalf("line %d: %d columns, want 6", i+1, len(c))
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
			{"GoPlural", plugin.GoPlural(in), c[5]},
		} {
			if x.got != x.want {
				t.Errorf("line %d: %s(%q) = %q, want %q", i+1, x.name, in, x.got, x.want)
			}
		}
	}
}

// TestEntNames checks the helpers against the names ent's own integration
// code generates, in testdata/ent.golden: each table row's table and slice
// type, and each edge row's singular and its Go name.
func TestEntNames(t *testing.T) {
	data, err := os.ReadFile("testdata/ent.golden")
	if err != nil {
		t.Fatal(err)
	}
	rows := 0
	for i, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		rows++
		c := strings.Split(line, "\t")
		if len(c) != 4 {
			t.Fatalf("line %d: %d columns, want 4", i+1, len(c))
		}
		switch name := c[1]; c[0] {
		case "table":
			if got := plugin.Snake(plugin.Plural(name)); got != c[2] {
				t.Errorf("line %d: Snake(Plural(%q)) = %q, want %q", i+1, name, got, c[2])
			}
			if got := plugin.GoPlural(name); got != c[3] {
				t.Errorf("line %d: GoPlural(%q) = %q, want %q", i+1, name, got, c[3])
			}
		case "edge":
			if got := plugin.Singular(name); got != c[2] {
				t.Errorf("line %d: Singular(%q) = %q, want %q", i+1, name, got, c[2])
			}
			if got := plugin.GoName(plugin.Singular(name)); got != c[3] {
				t.Errorf("line %d: GoName(Singular(%q)) = %q, want %q", i+1, name, got, c[3])
			}
		default:
			t.Fatalf("line %d: kind %q, want table or edge", i+1, c[0])
		}
	}
	if rows != 105 {
		t.Fatalf("golden has %d rows, want 105", rows)
	}
}
