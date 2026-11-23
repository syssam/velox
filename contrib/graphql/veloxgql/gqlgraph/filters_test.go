package gqlgraph_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
)

// sIn is vIn of a type with Veloxe and without VeloxeFilters.
type sIn struct {
	Not *sIn
	And []*sIn
	Or  []*sIn

	ID      *int64
	IDIn    []int64
	IDNotIn []int64
}

var sType = func() *gqlgraph.Type {
	c := gqlgraph.TableType[person]("Person")
	c.Veloxe, c.WhereFields = true, vFields
	return c
}()

func (w *sIn) Predicate(ctx context.Context) (velox.Predicate, error) {
	return gqlgraph.Predicate(ctx, sType, w)
}

// TestVeloxeStrictFilters: a type with Veloxe and without VeloxeFilters,
// as compat=veloxe,filters=strict generates it, keeps velox's fail-closed
// where semantics: an empty idIn or or matches no node, a nested empty
// input is no error, and the width cap applies.
func TestVeloxeStrictFilters(t *testing.T) {
	db, _ := peopleDB(t)
	ids := func(w *sIn) ([]int64, error) {
		p, err := w.Predicate(context.Background())
		if err != nil {
			return nil, err
		}
		rows, err := db.Query[person]().Where(velox.Pred[person](p)).OrderBy(people.ID.Asc()).AllowUnbounded().All(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		out := []int64{}
		for _, r := range rows {
			out = append(out, r.ID)
		}
		return out, nil
	}
	for _, c := range []struct {
		name string
		w    sIn
		want []int64
	}{
		{"nothing", sIn{}, all},
		{"idIn []", sIn{IDIn: []int64{}}, []int64{}},
		{"or []", sIn{Or: []*sIn{}}, []int64{}},
		{"not {}", sIn{Not: &sIn{}}, []int64{}},
		{"and [{idIn: []}, id 4]", sIn{And: []*sIn{{IDIn: []int64{}}, {ID: new(int64(4))}}}, []int64{}},
	} {
		if got, err := ids(&c.w); err != nil || !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, %v; want %v", c.name, got, err, c.want)
		}
	}
	if _, err := ids(&sIn{IDIn: span(1, 150)}); !errors.Is(err, veloxgql.ErrFilterTooWide) {
		t.Errorf("idIn of 150: %v, want ErrFilterTooWide", err)
	}
}
