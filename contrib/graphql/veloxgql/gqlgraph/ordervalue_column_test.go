package gqlgraph_test

import (
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
)

// mixedUsers opens users with ties in their names' first letters, NULL
// ages and ties in the ages.
func mixedUsers(t *testing.T) (*velox.DB, []user) {
	names := []string{"bo", "Al", "cy", "ann", "Bea", "abe", "Cal", "bob", "amy", "cid", "Ben", "ada"}
	ages := []any{30, nil, 20, 30, nil, 20, 40, 30, nil, 20, 30, nil}
	db := usersDB(t, names, ages)
	us := make([]user, len(names))
	for i := range names {
		us[i] = user{ID: int64(i + 1), Name: names[i]}
		if a, ok := ages[i].(int); ok {
			age := int64(a)
			us[i].Age = &age
		}
	}
	return db, us
}

// mixedOrder is one ordering of a mixed q: the Order and how two users
// compare by it, NULL placed as it places it.
type mixedOrder struct {
	name string
	o    velox.Order[user]
	cmp  func(a, b user) int
}

// initial is the ordering by the first letter of a user's name, lowered: a
// Calc marked Text.
func initial(desc bool, nulls int) mixedOrder {
	t := velox.Text(velox.Calc[user]("substr(lower(?), 1, 1)", users.Name))
	return mixedOrder{fmt.Sprintf("initial desc %v nulls %d", desc, nulls), placedOrder(t.Asc(), t.Desc(), desc, nulls),
		func(a, b user) int {
			return dir(cmp.Compare(strings.ToLower(a.Name[:1]), strings.ToLower(b.Name[:1])), desc)
		}}
}

// age is the ordering by the nullable column age.
func age(desc bool, nulls int) mixedOrder {
	first := [3]bool{desc, true, false}[nulls]
	return mixedOrder{fmt.Sprintf("age desc %v nulls %d", desc, nulls), placedOrder(users.Age.Asc(), users.Age.Desc(), desc, nulls),
		func(a, b user) int {
			switch {
			case a.Age == nil && b.Age == nil:
				return 0
			case a.Age == nil || b.Age == nil:
				if (a.Age == nil) == first {
					return -1
				}
				return 1
			}
			return dir(cmp.Compare(*a.Age, *b.Age), desc)
		}}
}

// name is the ordering by the string column name.
func name(desc bool) mixedOrder {
	return mixedOrder{fmt.Sprintf("name desc %v", desc), placedOrder(users.Name.Asc(), users.Name.Desc(), desc, 2),
		func(a, b user) int { return dir(cmp.Compare(a.Name, b.Name), desc) }}
}

// placedOrder is asc or desc, by desc, with the NULL placement nulls: 0
// the default, 1 first, 2 last.
func placedOrder(asc, desc velox.Order[user], d bool, nulls int) velox.Order[user] {
	o := asc
	if d {
		o = desc
	}
	switch nulls {
	case 1:
		o = o.NullsFirst()
	case 2:
		o = o.NullsLast()
	}
	return o
}

// dir is c, turned when desc.
func dir(c int, desc bool) int {
	if desc {
		return -c
	}
	return c
}

// pageAll pages the connection of q, which has exprs orderings, ordered by
// order and then the key, forward by n and then backward by n, through
// cursors that went over the wire, and returns the keys each way read, in
// the connection's order. Each cursor holds an expr: term for each of q's
// orderings, and none is a msgpack cursor.
func pageAll(t *testing.T, q velox.Query[user], exprs int, order []gqlgraph.OrderTerm[user], n, most int) (fwd, back []int64) {
	t.Helper()
	ctx := context.Background()
	page := func(args gqlgraph.Args) *gqlgraph.PageOf[user] {
		t.Helper()
		p, err := gqlgraph.PageWith(ctx, q, "User", args, order, userKey())
		if err != nil {
			t.Fatalf("%+v: %v", args, err)
		}
		for _, cur := range p.Cursors {
			for i := range exprs {
				if !strings.HasPrefix(cur.Terms[i].Field, "expr:") {
					t.Fatalf("cursor %+v: term %d is not an expr: term", cur, i)
				}
			}
			if b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(encode(t, cur), "=")); err != nil || len(b) == 0 || b[0] != '{' {
				t.Fatalf("cursor %+v is not velox's JSON: %q %v", cur, b, err)
			}
		}
		return p
	}
	p := page(gqlgraph.Args{First: new(n)})
	for len(fwd) <= most {
		fwd = append(fwd, userIDs(p.Rows)...)
		if !p.Info.HasNextPage {
			break
		}
		p = page(gqlgraph.Args{First: new(n), After: wire(t, *p.Info.EndCursor)})
	}
	p = page(gqlgraph.Args{Last: new(n)})
	for len(back) <= most {
		back = append(userIDs(p.Rows), back...)
		if !p.Info.HasPreviousPage {
			break
		}
		p = page(gqlgraph.Args{Last: new(n), Before: wire(t, *p.Info.StartCursor)})
	}
	return fwd, back
}

// TestConnectionMixedOrder: q ordered by expressions and by the node's own
// columns, in any order, a nullable column among them with each NULL
// placement, pages forward and backward at each page size through cursors
// that went over the wire, in the order a reference sort gives, NULLs and
// ties included, the orderBy argument and then the key breaking the ties.
// The column orderings take the expression path: every cursor is velox's
// JSON, with an expr: term for each of q's orderings.
func TestConnectionMixedOrder(t *testing.T) {
	db, us := mixedUsers(t)
	var cases [][]mixedOrder
	for _, desc := range []bool{false, true} {
		for nulls := range 3 {
			cases = append(cases,
				[]mixedOrder{age(desc, nulls)},
				[]mixedOrder{initial(!desc, 2), age(desc, nulls)},
				[]mixedOrder{age(desc, nulls), initial(desc, nulls), name(!desc)},
			)
		}
	}
	cases = append(cases, []mixedOrder{name(true), initial(false, 2)})
	asc, desc := veloxgql.OrderDirectionAsc, veloxgql.OrderDirectionDesc
	for _, c := range cases {
		var label []string
		os := make([]velox.Order[user], len(c))
		for i, o := range c {
			label, os[i] = append(label, o.name), o.o
		}
		for _, idDir := range []veloxgql.OrderDirection{asc, desc} {
			t.Run(strings.Join(label, ", ")+" then ID "+string(idDir), func(t *testing.T) {
				want := slices.Clone(us)
				slices.SortFunc(want, func(a, b user) int {
					for _, o := range c {
						if r := o.cmp(a, b); r != 0 {
							return r
						}
					}
					return dir(cmp.Compare(a.ID, b.ID), idDir == desc)
				})
				wantIDs := userIDs(ptrs(want))
				for n := 1; n <= 4; n++ {
					fwd, back := pageAll(t, db.Query[user]().OrderBy(os...), len(os), []gqlgraph.OrderTerm[user]{userID(idDir)}, n, len(us))
					if !slices.Equal(fwd, wantIDs) {
						t.Errorf("forward by %d:\n got %v\nwant %v", n, fwd, wantIDs)
					}
					if !slices.Equal(back, wantIDs) {
						t.Errorf("backward by %d:\n got %v\nwant %v", n, back, wantIDs)
					}
				}
			})
		}
	}
}

// ptrs is the addresses of us's users.
func ptrs(us []user) []*user {
	out := make([]*user, len(us))
	for i := range us {
		out[i] = &us[i]
	}
	return out
}

// TestConnectionMixedOrderMsgpack: a q with any ordering, a column alone
// too, takes no msgpack cursor (entgql's, which holds the orderBy field
// alone): it is taken under another order. Without one, the same cursor
// pages.
func TestConnectionMixedOrderMsgpack(t *testing.T) {
	ctx := context.Background()
	db, _ := mixedUsers(t)
	for name, q := range map[string]velox.Query[user]{
		"a column":                   db.Query[user]().OrderBy(users.Age.Asc()),
		"a column and an expression": db.Query[user]().OrderBy(users.Age.Asc().NullsLast(), velox.Text(velox.Calc[user]("lower(?)", users.Name)).Desc()),
	} {
		_, err := gqlgraph.PageWith(ctx, q, "User", gqlgraph.Args{First: new(2), After: msgpackCursor(t, "gqFpAqF2wA")}, nil, userKey())
		if !errors.Is(err, veloxgql.ErrBadCursor) || !strings.Contains(err.Error(), "another order") {
			t.Errorf("%s: %v, want a cursor taken under another order", name, err)
		}
	}
	p, err := gqlgraph.PageWith(ctx, db.Query[user](), "User", gqlgraph.Args{First: new(2), After: msgpackCursor(t, "gqFpAqF2wA")}, nil, userKey())
	if err != nil || !slices.Equal(userIDs(p.Rows), []int64{3, 4}) {
		t.Errorf("unordered: %v %v, want [3 4]", err, p)
	}
}

// TestConnectionColumnInQAndOrderBy: a column that q orders by and the
// orderBy argument names too is refused as bad pagination, never dropped
// from either; another orderBy field pages.
func TestConnectionColumnInQAndOrderBy(t *testing.T) {
	ctx := context.Background()
	db, _ := mixedUsers(t)
	q := db.Query[user]().OrderBy(velox.Calc[user]("length(?)", users.Name).Asc(), users.Age.Desc().NullsLast())
	_, err := gqlgraph.PageWith(ctx, q, "User", gqlgraph.Args{First: new(2)}, []gqlgraph.OrderTerm[user]{userAge(veloxgql.OrderDirectionAsc)}, userKey())
	if !errors.Is(err, veloxgql.ErrBadPagination) || !strings.Contains(err.Error(), "orderBy AGE orders by a column the connection is ordered by already") {
		t.Errorf("%v, want ErrBadPagination for AGE", err)
	}
	if _, err := gqlgraph.PageWith(ctx, q, "User", gqlgraph.Args{First: new(2)}, []gqlgraph.OrderTerm[user]{userName(veloxgql.OrderDirectionAsc)}, userKey()); err != nil {
		t.Errorf("orderBy NAME: %v", err)
	}
}

// TestConnectionColumnValueKind: a cursor's value of q's column ordering
// is checked against the column's kind: text for the nullable integer age
// is a bad cursor; a number pages. A column of a kind velox does not know,
// an encoded one, is refused as the application's error.
func TestConnectionColumnValueKind(t *testing.T) {
	ctx := context.Background()
	db, _ := mixedUsers(t)
	q := db.Query[user]().OrderBy(users.Age.Asc().NullsLast())
	p, err := gqlgraph.PageWith(ctx, q, "User", gqlgraph.Args{First: new(3)}, nil, userKey())
	if err != nil {
		t.Fatal(err)
	}
	cur := *p.Info.EndCursor
	for _, c := range []struct {
		v  any
		ok bool
	}{{"s:abc", false}, {"s:20", false}, {true, false}, {int64(20), true}, {nil, true}} {
		edited := cur
		edited.Terms = slices.Clone(cur.Terms)
		edited.Terms[0].Value = c.v
		_, err := gqlgraph.PageWith(ctx, q, "User", gqlgraph.Args{First: new(3), After: wire(t, edited)}, nil, userKey())
		if c.ok != (err == nil) || !c.ok && !errors.Is(err, veloxgql.ErrBadCursor) {
			t.Errorf("value %#v: %v", c.v, err)
		}
	}
	tags, _ := velox.ColumnByName[note]("tags")
	_, err = gqlgraph.PageWith(ctx, db.Query[note]().OrderBy(tags.Asc()), "Note", gqlgraph.Args{First: new(2)}, nil,
		[]gqlgraph.OrderTerm[note]{gqlgraph.TermOf[note]("id", veloxgql.OrderDirectionAsc, int(notes.ID.I))})
	if err == nil || !strings.Contains(err.Error(), "a column of q's order must be a string, a bool, a number or a time") {
		t.Errorf("an encoded column: %v", err)
	}
}

// TestConnectionColumnWithJoinedOrderBy: q's column ordering and an
// expression over the key page beside an orderBy that joins a relation,
// whose target has columns of the same names: each term names the node's
// own column.
func TestConnectionColumnWithJoinedOrderBy(t *testing.T) {
	ctx := context.Background()
	db, _, _ := edgeDB(t)
	q := db.Query[child]().OrderBy(children.Title.Desc(), velox.Integer(velox.Calc[child]("? % ?", children.ID, int64(2))).Asc())
	var got []int64
	args := gqlgraph.Args{First: new(2)}
	for range 5 {
		p, err := gqlgraph.PageWith(ctx, q, "Child", args, ownerNameOrder(veloxgql.OrderDirectionAsc), childKey())
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, childIDs(p.Rows)...)
		if !p.Info.HasNextPage {
			break
		}
		args.After = wire(t, *p.Info.EndCursor)
	}
	// Titles z, d, c, then b: 4 before 5 by id % 2; then a: 2 and 6 tie
	// by id % 2, and owner a's comes before owner b's.
	if want := []int64{7, 1, 3, 4, 5, 2, 6}; !slices.Equal(got, want) {
		t.Errorf("%v, want %v", got, want)
	}
}
