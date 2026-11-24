package gqlgraph_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
)

// exprOrder is a query's ordering by an expression and the order of the
// people it gives, NULL placed where the ordering places it, ties by the
// key ASC.
type exprOrder struct {
	name string
	o    velox.Order[person]
	want []int64
}

// exprOrders are orderings by a computed number, a computed fraction and a
// computed text, each with ties, NULLs and bound values, in each direction
// and NULL placement.
func exprOrders() []exprOrder {
	mod := velox.Calc[person]("NULLIF(? % ?, ?)", people.ID, int64(3), int64(0))
	half := velox.Calc[person]("NULLIF(? * ?, ?)", people.Age, 0.5, 46.0)
	name := velox.Calc[person]("NULLIF(?, ?)", people.Name, "d")
	type value struct {
		null bool
		c    func(a, b int64) int
	}
	modOf := func(id int64) value { return value{id%3 == 0, func(a, b int64) int { return cmp.Compare(a%3, b%3) }} }
	halfOf := func(id int64) value { return value{100-id == 92, func(a, b int64) int { return cmp.Compare(b, a) }} }
	nameOf := func(id int64) value {
		return value{names[id] == "d", func(a, b int64) int { return cmp.Compare(names[a], names[b]) }}
	}
	var out []exprOrder
	for _, e := range []struct {
		name string
		t    velox.Term[person]
		of   func(int64) value
	}{{"id % 3", mod, modOf}, {"age * 0.5", half, halfOf}, {"name", name, nameOf}} {
		for _, desc := range []bool{false, true} {
			for nulls := range 3 {
				o, first := e.t.Asc(), desc
				if desc {
					o = e.t.Desc()
				}
				switch nulls {
				case 1:
					o, first = o.NullsFirst(), true
				case 2:
					o, first = o.NullsLast(), false
				}
				want := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
				slices.SortStableFunc(want, func(a, b int64) int {
					va, vb := e.of(a), e.of(b)
					switch {
					case va.null && vb.null:
						return 0
					case va.null:
						return map[bool]int{true: -1, false: 1}[first]
					case vb.null:
						return map[bool]int{true: 1, false: -1}[first]
					case desc:
						return -va.c(a, b)
					}
					return va.c(a, b)
				})
				out = append(out, exprOrder{fmt.Sprintf("%s desc %v nulls %d", e.name, desc, nulls), o, want})
			}
		}
	}
	return out
}

// TestConnectionOrderedByExpression: a query ordered by an expression pages
// forward and backward at each page size, through cursors that went over
// the wire, with no row repeated or skipped and the rows in the order the
// expression gives, NULL where its ordering puts it, ties by the key.
func TestConnectionOrderedByExpression(t *testing.T) {
	ctx := context.Background()
	db, _ := peopleDB(t)
	for _, c := range exprOrders() {
		t.Run(c.name, func(t *testing.T) {
			q := db.Query[person]().OrderBy(c.o)
			page := func(args gqlgraph.Args) *gqlgraph.PageOf[person] {
				t.Helper()
				p, err := gqlgraph.PageWith(ctx, q, "Person", args, nil, personKey())
				if err != nil {
					t.Fatalf("%+v: %v", args, err)
				}
				for _, cur := range p.Cursors {
					if len(cur.Terms) != 1 || !strings.HasPrefix(cur.Terms[0].Field, "expr:") {
						t.Fatalf("cursor %+v: want one expr: term", cur)
					}
				}
				return p
			}
			for n := 1; n <= 4; n++ {
				var got []int64
				p := page(gqlgraph.Args{First: new(n)})
				for len(got) <= 10 {
					got = append(got, ids(p.Rows)...)
					if !p.Info.HasNextPage {
						break
					}
					p = page(gqlgraph.Args{First: new(n), After: wire(t, *p.Info.EndCursor)})
				}
				if !slices.Equal(got, c.want) {
					t.Errorf("forward by %d: %v, want %v", n, got, c.want)
				}
				got = nil
				p = page(gqlgraph.Args{Last: new(n)})
				for len(got) <= 10 {
					got = append(ids(p.Rows), got...)
					if !p.Info.HasPreviousPage {
						break
					}
					p = page(gqlgraph.Args{Last: new(n), Before: wire(t, *p.Info.StartCursor)})
				}
				if !slices.Equal(got, c.want) {
					t.Errorf("backward by %d: %v, want %v", n, got, c.want)
				}
			}
		})
	}
}

// TestConnectionExpressionThenOrderBy: the query's expression comes before
// the orderBy argument, which breaks its ties before the key.
func TestConnectionExpressionThenOrderBy(t *testing.T) {
	ctx := context.Background()
	db, _ := peopleDB(t)
	q := db.Query[person]().OrderBy(velox.Calc[person]("? % ?", people.ID, int64(2)).Desc())
	var got []int64
	args := gqlgraph.Args{First: new(3)}
	for range 5 {
		p, err := gqlgraph.PageWith(ctx, q, "Person", args, []gqlgraph.OrderTerm[person]{nameTerm(veloxgql.OrderDirectionAsc)}, personKey())
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ids(p.Rows)...)
		if !p.Info.HasNextPage {
			break
		}
		args.After = wire(t, *p.Info.EndCursor)
	}
	// Odd ids first, each half by name, then the key.
	want := []int64{3, 1, 5, 9, 7, 2, 4, 6, 8, 10}
	if !slices.Equal(got, want) {
		t.Errorf("%v, want %v", got, want)
	}
}

// TestConnectionExpressionCursorRefused: a cursor taken under another
// expression, the same one with another bound value or another NULL
// placement, or none, is taken under another order; a value no page wrote
// is a bad cursor; and a column ordering of q is still refused.
func TestConnectionExpressionCursorRefused(t *testing.T) {
	ctx := context.Background()
	db, _ := peopleDB(t)
	by := func(v int64) velox.Term[person] { return velox.Calc[person]("? % ?", people.ID, v) }
	p, err := gqlgraph.PageWith(ctx, db.Query[person]().OrderBy(by(3).Asc()), "Person", gqlgraph.Args{First: new(2)}, nil, personKey())
	if err != nil {
		t.Fatal(err)
	}
	cur := *p.Info.EndCursor
	for name, q := range map[string]velox.Query[person]{
		"another value": db.Query[person]().OrderBy(by(4).Asc()),
		"another nulls": db.Query[person]().OrderBy(by(3).Asc().NullsFirst()),
		"another text":  db.Query[person]().OrderBy(velox.Calc[person]("? + ?", people.ID, int64(3)).Asc()),
		"no expression": db.Query[person](),
	} {
		_, err := gqlgraph.PageWith(ctx, q, "Person", gqlgraph.Args{First: new(2), After: wire(t, cur)}, nil, personKey())
		if !errors.Is(err, veloxgql.ErrBadCursor) || !strings.Contains(err.Error(), "another order") {
			t.Errorf("%s: %v, want a cursor taken under another order", name, err)
		}
	}
	q := db.Query[person]().OrderBy(by(3).Asc())
	for _, v := range []any{"x", "s", "t:yesterday", "f:1.5", 1.5} {
		bad := cur
		bad.Terms = []veloxgql.CursorTerm{{Field: cur.Terms[0].Field, Direction: cur.Terms[0].Direction, Value: v}}
		if _, err := gqlgraph.PageWith(ctx, q, "Person", gqlgraph.Args{First: new(2), After: &bad}, nil, personKey()); !errors.Is(err, veloxgql.ErrBadCursor) {
			t.Errorf("value %#v: %v, want ErrBadCursor", v, err)
		}
	}
	if _, err := gqlgraph.PageWith(ctx, db.Query[person]().OrderBy(by(3).Asc(), people.Age.Asc()), "Person", gqlgraph.Args{First: new(2)}, nil, personKey()); err == nil || !strings.Contains(err.Error(), "q may be ordered only by expressions") {
		t.Errorf("a column ordering after an expression: %v", err)
	}
}

// TestConnectionExpressionValues: each kind of value a page reads goes over
// the wire and back as the same Go value, so the next page binds what the
// row held: an integer, a fraction that is whole, text that looks like a
// number, a time, a bool and NULL.
func TestConnectionExpressionValues(t *testing.T) {
	ctx := context.Background()
	db, _ := peopleDB(t)
	for _, c := range []struct {
		text string
		args []any
	}{
		{"? * ?", []any{people.Age, int64(2)}},
		{"? * ?", []any{people.Age, 1.0}},
		{"CAST(? AS TEXT)", []any{people.Age}},
		{"? > ?", []any{people.Age, int64(94)}},
		{"CASE WHEN ? > ? THEN NULL ELSE ? END", []any{people.ID, int64(5), people.Name}},
	} {
		q := db.Query[person]().OrderBy(velox.Calc[person](c.text, c.args...).Asc())
		var got []int64
		args := gqlgraph.Args{First: new(3)}
		for range 5 {
			p, err := gqlgraph.PageWith(ctx, q, "Person", args, nil, personKey())
			if err != nil {
				t.Fatalf("%s: %v", c.text, err)
			}
			got = append(got, ids(p.Rows)...)
			if !p.Info.HasNextPage {
				break
			}
			args.After = wire(t, *p.Info.EndCursor)
		}
		all, err := q.OrderBy(people.ID.Asc()).Limit(20).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := make([]int64, len(all))
		for i := range all {
			want[i] = all[i].ID
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", c.text, got, want)
		}
	}
}

// TestOldCursorBytes: a cursor of a connection with no ordering by an
// expression is byte for byte what it was before decision 0166, and the
// cursor taken then pages as it did.
func TestOldCursorBytes(t *testing.T) {
	ctx := context.Background()
	db, _ := peopleDB(t)
	order := []gqlgraph.OrderTerm[person]{nameTerm(veloxgql.OrderDirectionAsc)}
	p, err := gqlgraph.PageWith(ctx, db.Query[person](), "Person", gqlgraph.Args{First: new(3)}, order, personKey())
	if err != nil {
		t.Fatal(err)
	}
	// {"v":1,"t":"Person","o":[{"f":"NAME","d":"ASC","v":"b"}],"k":3}
	const old = "eyJ2IjoxLCJ0IjoiUGVyc29uIiwibyI6W3siZiI6Ik5BTUUiLCJkIjoiQVNDIiwidiI6ImIifV0sImsiOjN9"
	if got := encode(t, *p.Info.EndCursor); got != old {
		t.Errorf("end cursor %s, want %s", got, old)
	}
	var cur veloxgql.Cursor
	if err := cur.UnmarshalGQL(old); err != nil {
		t.Fatal(err)
	}
	next, err := gqlgraph.PageWith(ctx, db.Query[person](), "Person", gqlgraph.Args{First: new(3), After: &cur}, order, personKey())
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(next.Rows); !slices.Equal(got, []int64{6, 8, 1}) {
		t.Errorf("after the old cursor: %v, want [6 8 1]", got)
	}
}
