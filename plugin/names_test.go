package plugin_test

import (
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
