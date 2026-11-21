package gqlgenx

import (
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
)

// TestOccurrence: occurrence gives one pointer for a field with equal
// selections, compared by their elements as gqlgen merges them anew for
// each object, and different pointers for one *ast.Field with different
// selections or under another operation.
func TestOccurrence(t *testing.T) {
	s := &source{}
	f := &ast.Field{Name: "tags"}
	id, name := &ast.Field{Name: "id"}, &ast.Field{Name: "name"}
	oc := &graphql.OperationContext{}
	a := s.occurrence(f, ast.SelectionSet{id, name}, oc)
	if b := s.occurrence(f, ast.SelectionSet{id, name}, oc); b != a {
		t.Error("equal selections, merged anew: two occurrences, want one")
	}
	other := s.occurrence(f, ast.SelectionSet{id}, oc)
	if other == a {
		t.Error("different selections of one field: one occurrence, want two")
	}
	if b := s.occurrence(f, ast.SelectionSet{id}, oc); b != other {
		t.Error("the second selection again: a new occurrence, want the one made")
	}
	if b := s.occurrence(f, ast.SelectionSet{name, id}, oc); b == a {
		t.Error("the same fields in another order: one occurrence, want two")
	}
	if b := s.occurrence(f, ast.SelectionSet{id, name}, &graphql.OperationContext{}); b == a {
		t.Error("another operation: one occurrence, want two")
	}
	if b := s.occurrence(&ast.Field{Name: "tags"}, ast.SelectionSet{id, name}, oc); b == a {
		t.Error("another *ast.Field with equal selections: one occurrence, want two")
	}
}
