package gqlgraph_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

// The tests of compat=veloxe's paging (ADR 0110): each case runs on a Type
// with Veloxe and, for the contrast, on one without.

// user is veloxe's examples/fullgql User, as the parity probe seeded it: a
// key, a name and a nullable age.
type user struct {
	ID   int64
	Name string
	Age  *int64
}

var users = struct {
	ID   velox.NumberCol[user, int64]
	Name velox.StringCol[user, string]
	Age  velox.NullNumberCol[user, int64]
}{ID: velox.NumberCol[user, int64]{I: 0}, Name: velox.StringCol[user, string]{I: 1}, Age: velox.NullNumberCol[user, int64]{I: 2}}

var userMeta velox.MetaOnce[user]

func (*user) VeloxMeta() *velox.Meta[user] { return userMeta.Load(userDescriptor) }

func userDescriptor() velox.Descriptor[user] {
	return velox.Descriptor[user]{
		Table:      "users",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}, {Field: "Age", Name: "age"}},
		PrimaryKey: []int{0},
		FieldPtr: func(x *user, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.Name
			case 2:
				return &x.Age
			}
			return nil
		},
	}
}

func userName(dir veloxgql.OrderDirection) gqlgraph.OrderTerm[user] {
	return gqlgraph.TermOf[user]("NAME", dir, int(users.Name.I))
}

func userAge(dir veloxgql.OrderDirection) gqlgraph.OrderTerm[user] {
	return gqlgraph.TermOf[user]("AGE", dir, int(users.Age.I))
}

func userKey() []gqlgraph.OrderTerm[user] {
	return []gqlgraph.OrderTerm[user]{gqlgraph.TermOf[user]("id", veloxgql.OrderDirectionAsc, int(users.ID.I))}
}

// usersDB opens an in-memory SQLite database with the users of names and
// ages, of the keys 1, 2, …
func usersDB(t *testing.T, names []string, ages []any) *velox.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER)`); err != nil {
		t.Fatal(err)
	}
	for i, name := range names {
		if _, err := sqlDB.Exec(`INSERT INTO users (id, name, age) VALUES (?, ?, ?)`, i+1, name, ages[i]); err != nil {
			t.Fatal(err)
		}
	}
	return velox.Open(velox.SQLite, sqlDB)
}

// probeUsers opens the database of the parity probe's P cases: User-5 to
// User-1, with the ages 30, NULL, 20, NULL and 40.
func probeUsers(t *testing.T) *velox.DB {
	return usersDB(t, []string{"User-5", "User-4", "User-3", "User-2", "User-1"}, []any{30, nil, 20, nil, 40})
}

// legacy is s, a cursor veloxe wrote, as the client sends it back.
func legacy(t *testing.T, s string) *veloxgql.Cursor {
	t.Helper()
	var c veloxgql.Cursor
	if err := c.UnmarshalGQL(s); err != nil {
		t.Fatalf("UnmarshalGQL(%q): %v", s, err)
	}
	return &c
}

// TestVeloxeLegacyCursor: under compat=veloxe a cursor
// veloxe wrote (the parity probe's) pages as veloxe paged from it,
// reading its value as the current order's; one whose number of values
// is not the order's number of terms is refused as a cursor taken under
// another order. Without compat each fails with the error velox's decoding
// gives its text, as before.
func TestVeloxeLegacyCursor(t *testing.T) {
	ctx := context.Background()
	db := probeUsers(t)
	asc, desc := veloxgql.OrderDirectionAsc, veloxgql.OrderDirectionDesc
	for _, c := range []struct {
		name  string
		s     string // the cursor: after, or before when back
		back  bool
		order []gqlgraph.OrderTerm[user]
		want  []int64
		next  bool
		prev  bool
	}{
		// VP:P2: users(first: 2, after: "gqFpAqF2wA").
		{"P2 first 2 after id 2", "gqFpAqF2wA", false, nil, []int64{3, 4}, true, true},
		// VP:P4: users(last: 2, before: "gqFpBKF2wA").
		{"P4 last 2 before id 4", "gqFpBKF2wA", true, nil, []int64{2, 3}, true, true},
		// VP:P11b: users(first: 2, after: "gqFpAqF2plVzZXItNA", orderBy: {field: NAME, direction: DESC}).
		{"P11b name DESC after User-4", "gqFpAqF2plVzZXItNA", false, []gqlgraph.OrderTerm[user]{userName(desc)}, []int64{3, 4}, true, true},
		// The probe's AGE ASC cursor of id 3, age 20 (VP:P12): 1 and 5
		// follow, as on veloxe; the NULLs, which velox puts last (ADR
		// 0032), come after them.
		{"age ASC after age 20", "gqFpA6F2FA", false, []gqlgraph.OrderTerm[user]{userAge(asc)}, []int64{1, 5}, true, true},
		// The probe's last cursor in the default order: nothing after it.
		{"first 2 after id 5", "gqFpBaF2wA", false, nil, []int64{}, false, true},
	} {
		args := func() gqlgraph.Args {
			if c.back {
				return gqlgraph.Args{Last: new(2), Before: legacy(t, c.s)}
			}
			return gqlgraph.Args{First: new(2), After: legacy(t, c.s)}
		}
		got, err := gqlgraph.VeloxePageWith(ctx, db.Query[user](), "User", args(), c.order, userKey())
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if ids := userIDs(got.Rows); !slices.Equal(ids, c.want) || got.Info.HasNextPage != c.next || got.Info.HasPreviousPage != c.prev {
			t.Errorf("%s: %v next %v prev %v, want %v next %v prev %v", c.name, ids, got.Info.HasNextPage, got.Info.HasPreviousPage, c.want, c.next, c.prev)
		}
		// The page's own cursors are velox's.
		for _, cur := range got.Cursors {
			if cur.Type != "User" {
				t.Errorf("%s: a cursor %+v, want velox's", c.name, cur)
			}
		}
		// Without compat: the error of velox's decoding, which UnmarshalGQL
		// gave before.
		_, want := veloxgql.DecodeCursor(c.s)
		if _, err := gqlgraph.PageWith(ctx, db.Query[user](), "User", args(), c.order, userKey()); err == nil || err.Error() != want.Error() || !errors.Is(err, veloxgql.ErrBadCursor) {
			t.Errorf("%s without compat: %v, want %v", c.name, err, want)
		}
	}

	// Refused: values of another number than the order's terms, and a
	// value of another type.
	for _, c := range []struct {
		name  string
		s     string
		order []gqlgraph.OrderTerm[user]
		why   string
	}{
		{"a name cursor under the key", "gqFpAqF2plVzZXItNA", nil, "taken under another order"},
		{"a name cursor under name and age", "gqFpAqF2plVzZXItNA", []gqlgraph.OrderTerm[user]{userName(asc), userAge(asc)}, "taken under another order"},
		{"a key cursor under name and age", "gqFpAqF2wA", []gqlgraph.OrderTerm[user]{userName(asc), userAge(asc)}, "taken under another order"},
		{"two values under name", "gqFp0wAAAAAAAAAJoXaSoWLTAAAAAAAAAB4", []gqlgraph.OrderTerm[user]{userName(asc)}, "taken under another order"},
		// A nil value under a NOT NULL name: not a string.
		{"a key cursor under name", "gqFpAqF2wA", []gqlgraph.OrderTerm[user]{userName(asc)}, "not a string"},
		{"a name under age", "gqFpAqF2plVzZXItNA", []gqlgraph.OrderTerm[user]{userAge(asc)}, "not an int64"},
	} {
		_, err := gqlgraph.VeloxePageWith(ctx, db.Query[user](), "User", gqlgraph.Args{First: new(2), After: legacy(t, c.s)}, c.order, userKey())
		if !errors.Is(err, veloxgql.ErrBadCursor) || !strings.Contains(err.Error(), c.why) {
			t.Errorf("%s: %v, want ErrBadCursor, %s", c.name, err, c.why)
		}
	}

	// A key of two columns: veloxe's cursors had one, so none is read.
	twoKey := []gqlgraph.OrderTerm[user]{gqlgraph.TermOf[user]("name", asc, int(users.Name.I)), gqlgraph.TermOf[user]("id", asc, int(users.ID.I))}
	if _, err := gqlgraph.VeloxePageWith(ctx, db.Query[user](), "User", gqlgraph.Args{First: new(2), After: legacy(t, "gqFpAqF2wA")}, nil, twoKey); !errors.Is(err, veloxgql.ErrBadCursor) {
		t.Errorf("a key of two columns: %v, want ErrBadCursor", err)
	}
}

func userIDs(rows []*user) []int64 {
	out := make([]int64, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

// TestVeloxeTieOrder: under compat=veloxe an order of one
// term breaks ties by the key in the term's direction, as veloxe did
// (VP-ties T1, T2: NAME DESC gives 5, 3, 1, 4, 2); an order of two terms
// keeps velox's key ASC. Paging forward and backward by the cursors walks
// the same order with no gap and no duplicate. Without compat the key is
// ASC in every order.
func TestVeloxeTieOrder(t *testing.T) {
	ctx := context.Background()
	// VP-ties' users: B, A, B, A, B, all of one age.
	db := usersDB(t, []string{"B", "A", "B", "A", "B"}, []any{1, 1, 1, 1, 1})
	asc, desc := veloxgql.OrderDirectionAsc, veloxgql.OrderDirectionDesc
	for _, c := range []struct {
		name        string
		order       []gqlgraph.OrderTerm[user]
		veloxe, def []int64
	}{
		{"T1 name ASC", []gqlgraph.OrderTerm[user]{userName(asc)}, []int64{2, 4, 1, 3, 5}, []int64{2, 4, 1, 3, 5}},
		{"T1 name DESC", []gqlgraph.OrderTerm[user]{userName(desc)}, []int64{5, 3, 1, 4, 2}, []int64{1, 3, 5, 2, 4}},
		{"name DESC, age DESC", []gqlgraph.OrderTerm[user]{userName(desc), userAge(desc)}, []int64{1, 3, 5, 2, 4}, []int64{1, 3, 5, 2, 4}},
	} {
		for _, veloxe := range []bool{true, false} {
			pageWith, want := gqlgraph.PageWith[user], c.def
			if veloxe {
				pageWith, want = gqlgraph.VeloxePageWith[user], c.veloxe
			}
			page := func(args gqlgraph.Args) *gqlgraph.PageOf[user] {
				t.Helper()
				p, err := pageWith(ctx, db.Query[user](), "User", args, c.order, userKey())
				if err != nil {
					t.Fatalf("%s, veloxe %v: %v", c.name, veloxe, err)
				}
				return p
			}
			if got := userIDs(page(gqlgraph.Args{First: new(5)}).Rows); !slices.Equal(got, want) {
				t.Errorf("%s, veloxe %v: %v, want %v", c.name, veloxe, got, want)
			}
			// VP-ties T2: last 2 is the order's last two.
			if p := page(gqlgraph.Args{Last: new(2)}); !slices.Equal(userIDs(p.Rows), want[3:]) || p.Info.HasNextPage || !p.Info.HasPreviousPage {
				t.Errorf("%s, veloxe %v: last 2 %v %+v, want %v, a previous page only", c.name, veloxe, userIDs(p.Rows), p.Info, want[3:])
			}
			var walk []int64
			args := gqlgraph.Args{First: new(2)}
			for {
				p := page(args)
				walk = append(walk, userIDs(p.Rows)...)
				if !p.Info.HasNextPage {
					break
				}
				args.After = wire(t, *p.Info.EndCursor)
			}
			if !slices.Equal(walk, want) {
				t.Errorf("%s, veloxe %v: forward walk %v, want %v", c.name, veloxe, walk, want)
			}
			walk = nil
			args = gqlgraph.Args{Last: new(2)}
			for {
				p := page(args)
				walk = append(userIDs(p.Rows), walk...)
				if !p.Info.HasPreviousPage {
					break
				}
				args.Before = wire(t, *p.Info.StartCursor)
			}
			if !slices.Equal(walk, want) {
				t.Errorf("%s, veloxe %v: backward walk %v, want %v", c.name, veloxe, walk, want)
			}
		}
	}
}

// TestVeloxePageFlags: under compat=veloxe a forward page
// after a cursor has a previous page, and a backward page before one a next
// page, as veloxe said (VP:P2, VP:P4), even when the cursor is the first or
// the last row; without a cursor, and without compat, the flag is false.
func TestVeloxePageFlags(t *testing.T) {
	ctx := context.Background()
	db, _ := peopleDB(t)
	for _, veloxe := range []bool{true, false} {
		pageWith := gqlgraph.PageWith[person]
		if veloxe {
			pageWith = gqlgraph.VeloxePageWith[person]
		}
		all, err := pageWith(ctx, db.Query[person](), "Person", gqlgraph.Args{First: new(10)}, nil, personKey())
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			name       string
			args       gqlgraph.Args
			next, prev bool // under compat; without, the other is false
		}{
			{"first 3", gqlgraph.Args{First: new(3)}, true, false},
			{"first 3 after row 1", gqlgraph.Args{First: new(3), After: wire(t, all.Cursors[0])}, true, true},
			{"first 3 after row 10", gqlgraph.Args{First: new(3), After: wire(t, all.Cursors[9])}, false, true},
			{"last 3", gqlgraph.Args{Last: new(3)}, false, true},
			{"last 3 before row 10", gqlgraph.Args{Last: new(3), Before: wire(t, all.Cursors[9])}, true, true},
			{"last 3 before row 1", gqlgraph.Args{Last: new(3), Before: wire(t, all.Cursors[0])}, true, false},
			{"no size, before row 2", gqlgraph.Args{Before: wire(t, all.Cursors[1])}, true, false},
		} {
			p, err := pageWith(ctx, db.Query[person](), "Person", c.args, nil, personKey())
			if err != nil {
				t.Fatal(err)
			}
			next, prev := c.next, c.prev
			if !veloxe {
				if c.args.After != nil {
					prev = false
				}
				if c.args.Before != nil {
					next = false
				}
			}
			if p.Info.HasNextPage != next || p.Info.HasPreviousPage != prev {
				t.Errorf("%s, veloxe %v: next %v prev %v, want %v %v", c.name, veloxe, p.Info.HasNextPage, p.Info.HasPreviousPage, next, prev)
			}
		}
	}
}

// TestVeloxeForeignKeyPlain: under compat=veloxe a
// foreign key's predicates are the column's alone, as veloxe's were: a
// WithFilter filter hiding a child's owner changes no result and adds no
// EXISTS, and a false IsNil or NotNil adds nothing. TestForeignKeyRunTimeFilter
// is the same without compat.
func TestVeloxeForeignKeyPlain(t *testing.T) {
	_, raw, _ := edgeDB(t)
	l := &edgeLog{sql: map[string][]string{}}
	db := velox.Open(velox.SQLite, raw, velox.WithFilter(hideOwnerFilter), velox.WithAround(l.around))
	tru, fls := new(true), new(false)
	one, two := int64(1), int64(2)
	ownerEdge, ownerCol := int(children.Owner.I), int(children.OwnerID.I)
	target := gqlgraph.TableType[owner]("Owner")
	all := []int64{1, 2, 3, 4, 5, 6, 7}
	for _, c := range []struct {
		name string
		o    gqlgraph.Ops[int64]
		want []int64
	}{
		{"ownerIDIsNil true", gqlgraph.Ops[int64]{IsNil: tru}, []int64{7}},
		{"ownerIDIsNil false", gqlgraph.Ops[int64]{IsNil: fls}, all},
		{"ownerIDNotNil true", gqlgraph.Ops[int64]{NotNil: tru}, []int64{1, 2, 3, 4, 5, 6}},
		{"ownerIDNotNil false", gqlgraph.Ops[int64]{NotNil: fls}, all},
		{"ownerID 1", gqlgraph.Ops[int64]{EQ: &one}, []int64{1, 2, 3, 4}},
		{"ownerID 2", gqlgraph.Ops[int64]{EQ: &two}, []int64{5, 6}},
		{"ownerIDNEQ 2", gqlgraph.Ops[int64]{NEQ: &two}, []int64{1, 2, 3, 4}},
		{"ownerIDIn 1, 2", gqlgraph.Ops[int64]{In: []int64{1, 2}}, []int64{1, 2, 3, 4, 5, 6}},
		{"ownerIDNotIn 2", gqlgraph.Ops[int64]{NotIn: []int64{2}}, []int64{1, 2, 3, 4}},
		{"ownerIDGT 1", gqlgraph.Ops[int64]{GT: &one}, []int64{5, 6}},
	} {
		typ := gqlgraph.TableType[child]("Child")
		typ.Veloxe = true
		f := gqlgraph.NewFilter(context.Background(), typ)
		gqlgraph.EdgeIDField(f, "ownerID", ownerCol, ownerEdge, target, c.o)
		p, err := f.Pred()
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		tbl := untyped.TableOf[child]()
		for _, ctx := range []context.Context{context.Background(), hideOwner()} {
			l.take("children")
			rows, err := untyped.Query(db, tbl).Where(p).OrderBy(velox.OrderTerm{Column: 0}).AllowUnbounded().All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			got := []int64{}
			for i := range rows.Len() {
				got = append(got, *tbl.Field(rows.At(i), 0).(*int64))
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("%s: %v, want %v", c.name, got, c.want)
			}
			if ran := l.take("children"); len(ran) != 1 || strings.Contains(ran[0], "EXISTS") {
				t.Errorf("%s: %q, want one statement with no EXISTS", c.name, ran)
			}
		}
	}
}
