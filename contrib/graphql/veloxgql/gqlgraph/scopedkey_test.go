package gqlgraph_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

type hideOwnerKey struct{}

// hideOwner is a context under which hideOwnerFilter hides owner 1.
func hideOwner() context.Context {
	return context.WithValue(context.Background(), hideOwnerKey{}, true)
}

// hideOwnerFilter is a WithFilter filter that hides owner 1 from reads under
// hideOwner's context, and restricts nothing else.
func hideOwnerFilter(ctx context.Context, entity string, op velox.Op) (velox.Predicate, error) {
	if entity != "owner" || op != velox.OpSelect || ctx.Value(hideOwnerKey{}) == nil {
		return velox.Allow(), nil
	}
	return velox.FieldNEQ("id", 1), nil
}

// TestForeignKeyRunTimeFilter is Review Focus 2: with a WithFilter filter
// hiding a child's owner, ownerIDIsNil, ownerIDNotNil, ownerID and
// ownerIDIn read the hidden owner's key as null, and agree with hasOwner.
// Without it they are the column's predicates alone, with no EXISTS. The
// predicates are built once, before any DB runs them, and each runs on one
// DB, so one render cache, under both contexts, in turn: neither reuses the
// other's SQL.
func TestForeignKeyRunTimeFilter(t *testing.T) {
	_, raw, _ := edgeDB(t)
	l := &edgeLog{sql: map[string][]string{}}
	db := velox.Open(velox.SQLite, raw, velox.WithFilter(hideOwnerFilter), velox.WithAround(l.around))
	tru, fls := new(true), new(false)
	one, two := int64(1), int64(2)
	ownerEdge, ownerCol := int(children.Owner.I), int(children.OwnerID.I)
	target := gqlgraph.TableType[owner]("Owner")
	key := func(o gqlgraph.Ops[int64]) func(*gqlgraph.Filter) {
		return func(f *gqlgraph.Filter) { gqlgraph.EdgeIDField(f, "ownerID", ownerCol, ownerEdge, target, o) }
	}
	has := func(b *bool) func(*gqlgraph.Filter) {
		return func(f *gqlgraph.Filter) { gqlgraph.Has(f, "hasOwner", b, ownerEdge, untyped.TableOf[owner]()) }
	}
	cases := []struct {
		name         string
		build        func(*gqlgraph.Filter)
		plain, hide  []int64
		existsAlways bool // the predicate is the edge's own Has
	}{
		{"hasOwner true", has(tru), []int64{1, 2, 3, 4, 5, 6}, []int64{5, 6}, true},
		{"hasOwner false", has(fls), []int64{7}, []int64{1, 2, 3, 4, 7}, true},
		{"ownerIDIsNil true", key(gqlgraph.Ops[int64]{IsNil: tru}), []int64{7}, []int64{1, 2, 3, 4, 7}, false},
		{"ownerIDIsNil false", key(gqlgraph.Ops[int64]{IsNil: fls}), []int64{1, 2, 3, 4, 5, 6}, []int64{5, 6}, false},
		{"ownerIDNotNil true", key(gqlgraph.Ops[int64]{NotNil: tru}), []int64{1, 2, 3, 4, 5, 6}, []int64{5, 6}, false},
		{"ownerIDNotNil false", key(gqlgraph.Ops[int64]{NotNil: fls}), []int64{7}, []int64{1, 2, 3, 4, 7}, false},
		{"ownerID 1", key(gqlgraph.Ops[int64]{EQ: &one}), []int64{1, 2, 3, 4}, nil, false},
		{"ownerID 2", key(gqlgraph.Ops[int64]{EQ: &two}), []int64{5, 6}, []int64{5, 6}, false},
		{"ownerIDNEQ 1", key(gqlgraph.Ops[int64]{NEQ: &one}), []int64{5, 6}, []int64{5, 6}, false},
		{"ownerIDIn 1, 2", key(gqlgraph.Ops[int64]{In: []int64{1, 2}}), []int64{1, 2, 3, 4, 5, 6}, []int64{5, 6}, false},
		{"ownerIDNotIn 2", key(gqlgraph.Ops[int64]{NotIn: []int64{2}}), []int64{1, 2, 3, 4}, nil, false},
	}
	tbl := untyped.TableOf[child]()
	run := func(ctx context.Context, p velox.Predicate) []int64 {
		t.Helper()
		rows, err := untyped.Query(db, tbl).Where(p).OrderBy(velox.OrderTerm{Column: 0}).AllowUnbounded().All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := []int64{}
		for i := range rows.Len() {
			out = append(out, *tbl.Field(rows.At(i), 0).(*int64))
		}
		return out
	}
	for _, c := range cases {
		f := gqlgraph.NewFilter(context.Background(), gqlgraph.TableType[child]("Child"))
		c.build(f)
		p, err := f.Pred()
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if c.hide == nil {
			c.hide = []int64{}
		}
		// Plain, hidden, then plain and hidden again: each run after the
		// first finds its shape cached, if the two shared one.
		for i, ctx := range []context.Context{context.Background(), hideOwner(), context.Background(), hideOwner()} {
			hidden := i%2 == 1
			want := c.plain
			if hidden {
				want = c.hide
			}
			l.take("children")
			if got := run(ctx, p); !slices.Equal(got, want) {
				t.Errorf("%s, hidden %v, run %d: %v, want %v", c.name, hidden, i, got, want)
			}
			ran := l.take("children")
			if len(ran) != 1 {
				t.Fatalf("%s: %d statements: %q", c.name, len(ran), ran)
			}
			if exists := strings.Contains(ran[0], "EXISTS"); exists != (hidden || c.existsAlways) {
				t.Errorf("%s, hidden %v, run %d: EXISTS %v in %s", c.name, hidden, i, exists, ran[0])
			}
		}
	}
}
