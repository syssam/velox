package gqlcore

import (
	"context"
	"slices"
	"testing"

	"github.com/syssam/velox"
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

// fieldSource gives every context the one field f.
type fieldSource struct{ f Field }

func (s fieldSource) Field(context.Context) (Field, bool) { return s.f, true }
func (fieldSource) Wait(context.Context) func()           { return func() {} }

// TestState: Install puts the DB, the limits and the source in a context,
// WithDB replaces the DB alone, a context without a state has none of them,
// and Install refuses a nil source.
func TestState(t *testing.T) {
	db, tx, limits, src := &velox.DB{}, &velox.DB{}, new(int), &fakeSource{}
	ctx := Install(context.Background(), db, limits, src)
	if DB(ctx) != db || LimitsOf(ctx) != limits || Source(ctx) != src {
		t.Error("Install's state")
	}
	txCtx := WithDB(ctx, tx)
	if DB(txCtx) != tx || LimitsOf(txCtx) != limits || Source(txCtx) != src || DB(ctx) != db {
		t.Error("WithDB's state")
	}
	bg := context.Background()
	if DB(bg) != nil || LimitsOf(bg) != nil || Source(bg) != nil {
		t.Error("a context without a state")
	}
	if _, ok := FieldOf(bg); ok {
		t.Error("FieldOf without a source")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("Install with a nil source did not panic")
			}
		}()
		Install(bg, db, limits, nil)
	}()
}

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
		ctx := WithTable(Install(context.Background(), nil, nil, fieldSource{c.f}), nil)
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
