package gqlgraph_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
)

// TestConnectionExpressionValueKind: on a connection ordered by an
// expression whose kind velox knows, a number (a Calc marked Decimal), a
// cursor whose value a client changed to text is a bad cursor, not a page
// from the wrong place; a number of another Go type than the page wrote
// still pages.
func TestConnectionExpressionValueKind(t *testing.T) {
	ctx := context.Background()
	db, _ := peopleDB(t)
	q := db.Query[person]().OrderBy(velox.Calc[person]("? * ?", people.Age, int64(2)).Decimal().Asc())
	p, err := gqlgraph.PageWith(ctx, q, "Person", gqlgraph.Args{First: new(3)}, nil, personKey())
	if err != nil {
		t.Fatal(err)
	}
	cur := *p.Info.EndCursor
	for _, c := range []struct {
		v  any
		ok bool
	}{
		{"s:abc", false},
		{"s:186", false},
		{"t:2026-03-01T00:00:00Z", false},
		{true, false},
		{json.Number("186.0"), true},
		{int64(186), true},
	} {
		edited := cur
		edited.Terms = slices.Clone(cur.Terms)
		edited.Terms[0].Value = c.v
		next, err := gqlgraph.PageWith(ctx, q, "Person", gqlgraph.Args{First: new(3), After: wire(t, edited)}, nil, personKey())
		switch {
		case c.ok && err != nil:
			t.Errorf("value %#v: %v", c.v, err)
		case c.ok && !slices.Equal(ids(next.Rows), []int64{6, 5, 4}):
			t.Errorf("value %#v: %v, want [6 5 4]", c.v, ids(next.Rows))
		case !c.ok && !errors.Is(err, veloxgql.ErrBadCursor):
			t.Errorf("value %#v: %v, want ErrBadCursor", c.v, err)
		}
	}
}
