package codegen

import (
	"strings"
	"testing"
)

// TestEntityDocNamesTheType: an entity's doc starts with its name, as Go's
// doc comments do and revive checks. A schema comment that does not is the
// second paragraph, after the default sentence; one that does is the doc.
func TestEntityDocNamesTheType(t *testing.T) {
	for comment, want := range map[string]string{
		"A post, by its author.":      "// Post is the entity of schema Post.\n//\n// A post, by its author.\ntype Post struct {",
		"Post is a post.":             "// Post is a post.\ntype Post struct {",
		"A Post is written by users.": "// A Post is written by users.\ntype Post struct {",
		"Posting rules.":              "// Post is the entity of schema Post.\n//\n// Posting rules.\ntype Post struct {",
		"":                            "// Post is the entity of schema Post.\ntype Post struct {",
	} {
		var w writer
		entityStruct(&w, &OutEntity{Name: "Post", Comment: comment})
		if got := w.String(); !strings.HasPrefix(got, want) {
			t.Errorf("comment %q:\n%s\nwant it to start\n%s", comment, got, want)
		}
	}
}
