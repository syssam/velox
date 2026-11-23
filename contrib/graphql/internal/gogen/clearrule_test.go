package gogen

import (
	"testing"

	"github.com/syssam/velox/contrib/graphql/internal/sdl"
)

// An update input's Setters doc says how a clear field meets another only
// when it has one that does.
func TestClearRule(t *testing.T) {
	value := sdl.InputField{Kind: sdl.InputValue, Field: 1, Edge: -1}
	clr := sdl.InputField{Kind: sdl.InputClear, Field: 1, Edge: -1}
	clearEdge := sdl.InputField{Kind: sdl.InputClearEdge, Field: -1, Edge: 0}
	id := sdl.InputField{Kind: sdl.InputEdgeID, Field: -1, Edge: 0}
	add := sdl.InputField{Kind: sdl.InputAddIDs, Field: -1, Edge: 0}
	other := sdl.InputField{Kind: sdl.InputAddIDs, Field: -1, Edge: 1}
	const wins, before = ", and a clear field wins over its field", ", and a clear field of an edge with a list of IDs clears it before they are added"
	for _, c := range []struct {
		name string
		fs   []sdl.InputField
		want string
	}{
		{"no clear field", []sdl.InputField{value, add}, ""},
		{"a field's", []sdl.InputField{value, clr}, wins},
		{"an edge's, with its ID", []sdl.InputField{id, clearEdge}, wins},
		{"an edge's, with its IDs", []sdl.InputField{clearEdge, add}, before},
		{"an edge's, alone", []sdl.InputField{clearEdge, other}, ""},
		{"both", []sdl.InputField{clr, clearEdge, add}, wins + before},
	} {
		if got := clearRule(c.fs); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
