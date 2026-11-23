package gqlgraph_test

import (
	"context"
	"slices"
	"testing"

	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
	"github.com/syssam/velox/untyped"
)

// TestPredicateTellsWhereNames: Predicate on a nil input, under a context
// that asks, tells the input's GraphQL name and each struct field's, in
// the order velox-graphql writes the fields: a run's predicates in
// fieldOrder, each the field's name and the predicate's suffix. A context
// that does not ask is told nothing, and an input that is set tells
// nothing.
func TestPredicateTellsWhereNames(t *testing.T) {
	typ := gqlgraph.Type{
		Name:  "Person",
		Table: untyped.TableOf[person],
		WhereFields: []gqlgraph.WhereField{
			{Name: "not", Kind: gqlgraph.WhereNot},
			{Name: "and", Kind: gqlgraph.WhereAnd},
			{Name: "or", Kind: gqlgraph.WhereOr},
			{Name: "id", Kind: gqlgraph.WhereID, Ops: gqlgraph.OpsEquality | gqlgraph.OpsOrder},
			{Name: "name", Ops: gqlgraph.OpsText | gqlgraph.OpsNull | gqlgraph.OpEQ},
			{Name: "tags", Kind: gqlgraph.WhereArray, Ops: gqlgraph.OpHas | gqlgraph.OpHasSome | gqlgraph.OpHasEvery | gqlgraph.OpIsEmpty},
			{Name: "hasFriends", Kind: gqlgraph.WhereHas},
			{Name: "hasFriendsWith", Kind: gqlgraph.WhereHasWith},
		},
	}
	ctx, slot := gqlcore.AskWhereNames(context.Background())
	if _, err := gqlgraph.Predicate(ctx, &typ, (*struct{})(nil)); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"not", "and", "or",
		"id", "idNEQ", "idIn", "idNotIn", "idGT", "idGTE", "idLT", "idLTE",
		"name", "nameContains", "nameHasPrefix", "nameHasSuffix", "nameEqualFold", "nameContainsFold", "nameIsNil", "nameNotNil",
		"tagsHas", "tagsHasSome", "tagsHasEvery", "tagsIsEmpty",
		"hasFriends", "hasFriendsWith",
	}
	if slot.Input != "PersonWhereInput" || !slices.Equal(slot.Fields, want) {
		t.Errorf("told %q %q\nwant PersonWhereInput %q", slot.Input, slot.Fields, want)
	}

	*slot = gqlcore.WhereNames{}
	if _, err := gqlgraph.Predicate(ctx, &typ, &struct{}{}); err == nil {
		t.Error("a struct that is not the input's: no error")
	}
	if slot.Input != "" {
		t.Errorf("a set input told %q", slot.Input)
	}
	gqlcore.TellWhereNames(context.Background(), func() gqlcore.WhereNames {
		t.Error("a context that does not ask was told")
		return gqlcore.WhereNames{}
	})
}
