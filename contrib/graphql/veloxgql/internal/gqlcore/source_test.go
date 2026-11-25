package gqlcore

import (
	"context"
	"slices"
	"testing"
)

// sel is a Selection of fixed fields per type; a type it lacks it cannot
// tell.
type sel map[string][]SelectedField

func (s sel) On(typ string) ([]SelectedField, bool) {
	fs, ok := s[typ]
	return fs, ok
}

// leaf is a selected field with no sub-selection.
func leaf(name string) SelectedField { return SelectedField{Name: name} }

// TestSelected walks a connection's edges and nodes, and a field of the type
// itself, and is false where it cannot see what the objects read.
func TestSelectedFields(t *testing.T) {
	node := sel{"Post": {leaf("id"), leaf("title"), leaf("__typename"), leaf("id")}}
	edges := sel{"PostEdge": {leaf("cursor"), leaf("__typename"), {Name: "node", Sel: node}}}
	conn := sel{"PostConnection": {leaf("totalCount"), leaf("pageInfo"), leaf("__typename"), {Name: "edges", Sel: edges}}}
	cases := []struct {
		name string
		f    Field
		want []string
		ok   bool
	}{
		{"connection", Field{Type: "PostConnection", Key: 1, Sel: conn}, []string{"id", "title"}, true},
		{"object", Field{Type: "Post", Key: 2, Sel: node}, []string{"id", "title"}, true},
		{"only totalCount", Field{Type: "PostConnection", Key: 3, Sel: sel{"PostConnection": {leaf("totalCount")}}}, []string{}, true},
		{"edges with no selection", Field{Type: "PostConnection", Key: 4, Sel: sel{"PostConnection": {leaf("edges")}}}, []string{}, true},
		{"another type", Field{Type: "User", Key: 5, Sel: node}, nil, false},
		{"an app's connection field", Field{Type: "PostConnection", Key: 6, Sel: sel{"PostConnection": {leaf("mine")}}}, nil, false},
		{"an app's edge field", Field{Type: "PostConnection", Key: 7, Sel: sel{"PostConnection": {{Name: "edges", Sel: sel{"PostEdge": {leaf("mine")}}}}}}, nil, false},
		{"untold connection", Field{Type: "PostConnection", Key: 8, Sel: sel{}}, nil, false},
		{"untold edge", Field{Type: "PostConnection", Key: 9, Sel: sel{"PostConnection": {{Name: "edges", Sel: sel{}}}}}, nil, false},
		{"untold node", Field{Type: "PostConnection", Key: 10, Sel: sel{"PostConnection": {{Name: "edges", Sel: sel{"PostEdge": {{Name: "node", Sel: sel{}}}}}}}}, nil, false},
		{"no key", Field{Type: "Post", Sel: node}, nil, false},
		{"no selection", Field{Type: "Post", Key: 11}, nil, false},
	}
	for _, c := range cases {
		c.f.Table = NewTable()
		ctx, _ := inField(context.Background(), c.f)
		for range 2 { // the second from the table
			got, ok := Selected(ctx, "Post")
			if ok != c.ok || !slices.Equal(got, c.want) {
				t.Errorf("%s: %v %v, want %v %v", c.name, got, ok, c.want, c.ok)
			}
		}
	}
	if _, ok := Selected(context.Background(), "Post"); ok {
		t.Error("Selected without a source")
	}
}
