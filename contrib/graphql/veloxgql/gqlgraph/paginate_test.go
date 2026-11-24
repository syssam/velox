package gqlgraph_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
)

// person is a hand-written entity, as velox-gen would write it, for the
// paginator's tests: a key, a name that ties and an age.
type person struct {
	ID   int64
	Name string
	Age  int64
}

var people = struct {
	ID   velox.NumberCol[person, int64]
	Name velox.StringCol[person, string]
	Age  velox.NumberCol[person, int64]
}{
	ID:   velox.NumberCol[person, int64]{I: 0},
	Name: velox.StringCol[person, string]{I: 1},
	Age:  velox.NumberCol[person, int64]{I: 2},
}

var personMeta velox.MetaOnce[person]

func (*person) VeloxMeta() *velox.Meta[person] { return personMeta.Load(personDescriptor) }

func personDescriptor() velox.Descriptor[person] {
	return velox.Descriptor[person]{
		Table:      "people",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}, {Field: "Age", Name: "age"}},
		PrimaryKey: []int{0},
		FieldPtr: func(x *person, i int) any {
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

// The order terms of person, as the generated code makes them.
func nameTerm(dir veloxgql.OrderDirection) gqlgraph.OrderTerm[person] {
	return gqlgraph.TermOf[person]("NAME", dir, int(people.Name.I))
}

func ageTerm(dir veloxgql.OrderDirection) gqlgraph.OrderTerm[person] {
	return gqlgraph.TermOf[person]("AGE", dir, int(people.Age.I))
}

func personKey() []gqlgraph.OrderTerm[person] {
	return []gqlgraph.OrderTerm[person]{gqlgraph.TermOf[person]("id", veloxgql.OrderDirectionAsc, int(people.ID.I))}
}

// The ten people. Names tie: two a, three b, two d.
var names = map[int64]string{1: "c", 2: "a", 3: "b", 4: "a", 5: "d", 6: "b", 7: "e", 8: "b", 9: "d", 10: "f"}

// statements records the statements a DB runs: "page" for a SELECT of rows
// and "count" for a COUNT.
type statements struct {
	mu  sync.Mutex
	ran []string
}

func (s *statements) around(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
	s.mu.Lock()
	if st.Count {
		s.ran = append(s.ran, "count")
	} else {
		s.ran = append(s.ran, "page")
	}
	s.mu.Unlock()
	return next(ctx)
}

func (s *statements) take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.ran
	s.ran = nil
	return r
}

// peopleDB opens an in-memory SQLite database with the ten people, through
// velox with an Around hook that records the statements.
func peopleDB(t *testing.T) (*velox.DB, *statements) {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1) // an in-memory database lives as long as its one connection
	if _, err := sqlDB.Exec(`CREATE TABLE people (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for id := int64(1); id <= 10; id++ {
		if _, err := sqlDB.Exec(`INSERT INTO people (id, name, age) VALUES (?, ?, ?)`, id, names[id], 100-id); err != nil {
			t.Fatal(err)
		}
	}
	st := &statements{}
	return velox.Open(velox.SQLite, sqlDB, velox.WithAround(st.around)), st
}

// wire is c as the client sends it back: encoded and decoded.
func wire(t *testing.T, c veloxgql.Cursor) *veloxgql.Cursor {
	t.Helper()
	d, err := veloxgql.DecodeCursor(encode(t, c))
	if err != nil {
		t.Fatal(err)
	}
	return &d
}

func ids(rows []*person) []int64 {
	out := make([]int64, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

// TestPaginate is Review Focus 2: under each order, paging forward and
// backward from every cursor gives the rows in order with no duplicate and
// no gap, the right hasNextPage and hasPreviousPage, and start and end
// cursors; each page runs one page query and no COUNT.
func TestPaginate(t *testing.T) {
	ctx := context.Background()
	db, st := peopleDB(t)
	for _, c := range []struct {
		name  string
		order func() []gqlgraph.OrderTerm[person]
		want  []int64 // every row, in the order
	}{
		{"key", func() []gqlgraph.OrderTerm[person] { return nil }, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
		{"name ASC", func() []gqlgraph.OrderTerm[person] {
			return []gqlgraph.OrderTerm[person]{nameTerm(veloxgql.OrderDirectionAsc)}
		},
			[]int64{2, 4, 3, 6, 8, 1, 5, 9, 7, 10}},
		{"name DESC", func() []gqlgraph.OrderTerm[person] {
			return []gqlgraph.OrderTerm[person]{nameTerm(veloxgql.OrderDirectionDesc)}
		}, []int64{10, 7, 9, 5, 1, 8, 6, 3, 4, 2}}, // ties by the key DESC, as entgql
		{"name DESC, age ASC", func() []gqlgraph.OrderTerm[person] {
			return []gqlgraph.OrderTerm[person]{nameTerm(veloxgql.OrderDirectionDesc), ageTerm(veloxgql.OrderDirectionAsc)}
		}, []int64{10, 7, 9, 5, 1, 8, 6, 3, 4, 2}},
	} {
		t.Run(c.name, func(t *testing.T) {
			page := func(args gqlgraph.Args) *gqlgraph.PageOf[person] {
				t.Helper()
				st.take()
				p, err := gqlgraph.PageWith(ctx, db.Query[person](), "Person", args, c.order(), personKey())
				if err != nil {
					t.Fatalf("%+v: %v", args, err)
				}
				if ran := st.take(); !slices.Equal(ran, []string{"page"}) {
					t.Fatalf("%+v ran %v, want one page query", args, ran)
				}
				if len(p.Rows) != len(p.Cursors) {
					t.Fatalf("%d rows, %d cursors", len(p.Rows), len(p.Cursors))
				}
				for i, r := range p.Rows {
					if p.Cursors[i].Type != "Person" || p.Cursors[i].Key != r.ID {
						t.Fatalf("row %d's cursor %+v", r.ID, p.Cursors[i])
					}
				}
				switch {
				case len(p.Rows) == 0 && (p.Info.StartCursor != nil || p.Info.EndCursor != nil):
					t.Fatalf("an empty page has cursors %+v", p.Info)
				case len(p.Rows) > 0 && (p.Info.StartCursor != &p.Cursors[0] || p.Info.EndCursor != &p.Cursors[len(p.Cursors)-1]):
					t.Fatalf("start and end cursors %+v, want the first and the last row's", p.Info)
				}
				return p
			}
			check := func(what string, p *gqlgraph.PageOf[person], want []int64, next, prev bool) {
				t.Helper()
				if got := ids(p.Rows); !slices.Equal(got, want) || p.Info.HasNextPage != next || p.Info.HasPreviousPage != prev {
					t.Errorf("%s: %v next %v prev %v, want %v next %v prev %v", what, got, p.Info.HasNextPage, p.Info.HasPreviousPage, want, next, prev)
				}
			}
			all := page(gqlgraph.Args{First: new(10)})
			check("first 10", all, c.want, false, false)
			check("first 11", page(gqlgraph.Args{First: new(11)}), c.want, false, false)
			check("first 0", page(gqlgraph.Args{First: new(0)}), []int64{}, true, false)
			check("first 3", page(gqlgraph.Args{First: new(3)}), c.want[:3], true, false)
			check("last 3", page(gqlgraph.Args{Last: new(3)}), c.want[7:], false, true)
			check("last 10", page(gqlgraph.Args{Last: new(10)}), c.want, false, false)
			check("last 0", page(gqlgraph.Args{Last: new(0)}), []int64{}, false, true)

			for i := range c.want {
				cur := wire(t, all.Cursors[i])
				// first 3 after each cursor.
				end := min(i+4, len(c.want))
				check("first 3 after "+names[c.want[i]], page(gqlgraph.Args{First: new(3), After: cur}), c.want[i+1:end], end < len(c.want), true)
				// last 3 before each cursor: last 2 before the first row is
				// empty, with no page before it. A page at a cursor has one
				// on the cursor's side, as entgql says.
				start := max(i-3, 0)
				check("last 3 before "+names[c.want[i]], page(gqlgraph.Args{Last: new(3), Before: cur}), c.want[start:i], true, start > 0)
				check("last 2 before", page(gqlgraph.Args{Last: new(2), Before: cur}), c.want[max(i-2, 0):i], true, i > 2)
			}

			// Walking the pages forward, then backward, gives every row once.
			var walk []int64
			args := gqlgraph.Args{First: new(3)}
			for {
				p := page(args)
				walk = append(walk, ids(p.Rows)...)
				if !p.Info.HasNextPage {
					break
				}
				args.After = wire(t, *p.Info.EndCursor)
			}
			if !slices.Equal(walk, c.want) {
				t.Errorf("forward walk %v, want %v", walk, c.want)
			}
			walk = nil
			args = gqlgraph.Args{Last: new(4)}
			for {
				p := page(args)
				walk = append(ids(p.Rows), walk...)
				if !p.Info.HasPreviousPage {
					break
				}
				args.Before = wire(t, *p.Info.StartCursor)
			}
			if !slices.Equal(walk, c.want) {
				t.Errorf("backward walk %v, want %v", walk, c.want)
			}
		})
	}
}

// TestPaginateDefaultPage: with neither first nor last, the page is
// MaxPage rows.
func TestPaginateDefaultPage(t *testing.T) {
	ctx := context.Background()
	db, _ := peopleDB(t)
	ctx = gqlgenx.NewExtension(db, gqlgenx.WithLimits(veloxgql.Limits{MaxPage: 4})).NewContext(ctx)
	p, err := gqlgraph.PageWith(ctx, db.Query[person](), "Person", gqlgraph.Args{}, nil, personKey())
	if err != nil || !slices.Equal(ids(p.Rows), []int64{1, 2, 3, 4}) || !p.Info.HasNextPage {
		t.Fatalf("%v %+v, want 1 to 4 and a next page", err, p)
	}
	p, err = gqlgraph.PageWith(ctx, db.Query[person](), "Person", gqlgraph.Args{After: wire(t, p.Cursors[3])}, nil, personKey())
	if err != nil || !slices.Equal(ids(p.Rows), []int64{5, 6, 7, 8}) || !p.Info.HasNextPage {
		t.Fatalf("after 4: %v %+v, want 5 to 8 and a next page", err, p)
	}
	// before alone pages backward, MaxPage rows.
	p, err = gqlgraph.PageWith(ctx, db.Query[person](), "Person", gqlgraph.Args{Before: wire(t, p.Cursors[3])}, nil, personKey())
	if err != nil || !slices.Equal(ids(p.Rows), []int64{4, 5, 6, 7}) || !p.Info.HasPreviousPage || !p.Info.HasNextPage {
		t.Fatalf("before 8: %v %+v, want 4 to 7, a previous page and a next one", err, p)
	}
	if _, err := gqlgraph.PageWith(ctx, db.Query[person](), "Person", gqlgraph.Args{First: new(5)}, nil, personKey()); !errors.Is(err, veloxgql.ErrPageTooLarge) {
		t.Errorf("first 5 over MaxPage 4: %v, want ErrPageTooLarge", err)
	}
}

// TestPaginateRefuses: bad arguments and cursors fail before any query.
func TestPaginateRefuses(t *testing.T) {
	ctx := context.Background()
	db, st := peopleDB(t)
	name := []gqlgraph.OrderTerm[person]{nameTerm(veloxgql.OrderDirectionAsc)}
	all, err := gqlgraph.PageWith(ctx, db.Query[person](), "Person", gqlgraph.Args{First: new(10)}, name, personKey())
	if err != nil {
		t.Fatal(err)
	}
	byName := wire(t, all.Cursors[2])
	other := wire(t, veloxgql.Cursor{Type: "Post", Terms: []veloxgql.CursorTerm{{Field: "NAME", Direction: veloxgql.OrderDirectionAsc, Value: "b"}}, Key: int64(3)})
	badValue := wire(t, veloxgql.Cursor{Type: "Person", Terms: []veloxgql.CursorTerm{{Field: "NAME", Direction: veloxgql.OrderDirectionAsc, Value: int64(1)}}, Key: int64(3)})
	badKey := wire(t, veloxgql.Cursor{Type: "Person", Terms: []veloxgql.CursorTerm{{Field: "NAME", Direction: veloxgql.OrderDirectionAsc, Value: "b"}}, Key: "x"})
	st.take()
	for _, c := range []struct {
		name  string
		args  gqlgraph.Args
		order []gqlgraph.OrderTerm[person]
		want  []error
	}{
		{"first and last", gqlgraph.Args{First: new(1), Last: new(1)}, name, []error{veloxgql.ErrBadPagination}},
		{"first 101", gqlgraph.Args{First: new(101)}, name, []error{veloxgql.ErrPageTooLarge}},
		{"last 101", gqlgraph.Args{Last: new(101)}, name, []error{veloxgql.ErrPageTooLarge}},
		{"first -1", gqlgraph.Args{First: new(-1)}, name, []error{veloxgql.ErrBadPagination}},
		{"last -1", gqlgraph.Args{Last: new(-1)}, name, []error{veloxgql.ErrBadPagination}},
		{"after and before", gqlgraph.Args{After: byName, Before: byName}, name, []error{veloxgql.ErrBadPagination}},
		{"first and before", gqlgraph.Args{First: new(1), Before: byName}, name, []error{veloxgql.ErrBadPagination}},
		{"last and after", gqlgraph.Args{Last: new(1), After: byName}, name, []error{veloxgql.ErrBadPagination}},
		{"name cursor under age", gqlgraph.Args{After: byName}, []gqlgraph.OrderTerm[person]{ageTerm(veloxgql.OrderDirectionAsc)}, []error{veloxgql.ErrBadCursor}},
		{"name ASC cursor under name DESC", gqlgraph.Args{Before: byName}, []gqlgraph.OrderTerm[person]{nameTerm(veloxgql.OrderDirectionDesc)}, []error{veloxgql.ErrBadCursor}},
		{"name cursor under the key", gqlgraph.Args{After: byName}, nil, []error{veloxgql.ErrBadCursor}},
		{"another type's cursor", gqlgraph.Args{After: other}, name, []error{veloxgql.ErrBadCursor}},
		{"a value of another type", gqlgraph.Args{After: badValue}, name, []error{veloxgql.ErrBadCursor}},
		{"a key of another type", gqlgraph.Args{After: badKey}, name, []error{veloxgql.ErrBadCursor}},
		{"an invalid direction", gqlgraph.Args{}, []gqlgraph.OrderTerm[person]{nameTerm("UP")}, []error{veloxgql.ErrBadPagination}},
	} {
		_, err := gqlgraph.PageWith(ctx, db.Query[person](), "Person", c.args, c.order, personKey())
		for _, want := range c.want {
			if !errors.Is(err, want) {
				t.Errorf("%s: %v, want %v", c.name, err, want)
			}
		}
		if err != nil && !strings.HasPrefix(err.Error(), "veloxgql: ") {
			t.Errorf("%s: %q does not start with veloxgql: ", c.name, err)
		}
		if ran := st.take(); len(ran) > 0 {
			t.Errorf("%s ran %v", c.name, ran)
		}
	}
	// The zero direction is ASC, as the SDL's default.
	p, err := gqlgraph.PageWith(ctx, db.Query[person](), "Person", gqlgraph.Args{First: new(2)}, []gqlgraph.OrderTerm[person]{nameTerm("")}, personKey())
	if err != nil || !slices.Equal(ids(p.Rows), []int64{2, 4}) || p.Cursors[0].Terms[0].Direction != veloxgql.OrderDirectionAsc {
		t.Errorf("the zero direction: %v %+v", err, p)
	}
}

// TestConnectionIsLazy: a connection runs its page query once,
// when Page is first called, and its COUNT only when TotalCount is.
func TestConnectionIsLazy(t *testing.T) {
	ctx := context.Background()
	db, st := peopleDB(t)
	q := db.Query[person]().Where(people.Name.NEQ("b"))
	c, err := gqlgraph.NewConnectionOf(ctx, q, "Person", gqlgraph.Args{First: new(2)}, nil, personKey())
	if err != nil {
		t.Fatal(err)
	}
	if ran := st.take(); len(ran) > 0 {
		t.Fatalf("NewConnection ran %v", ran)
	}
	// A count-only selection: one COUNT, of every matching row.
	if n, err := c.TotalCount(ctx); err != nil || n != 7 {
		t.Errorf("TotalCount = %d, %v, want 7", n, err)
	}
	if ran := st.take(); !slices.Equal(ran, []string{"count"}) {
		t.Errorf("TotalCount ran %v, want one COUNT", ran)
	}
	// Edges and PageInfo, called at once as gqlgen may: one page query.
	var wg sync.WaitGroup
	pages := make([]*gqlgraph.PageOf[person], 4)
	for i := range pages {
		wg.Go(func() {
			p, err := c.Page(ctx)
			if err != nil {
				t.Error(err)
			}
			pages[i] = p
		})
	}
	wg.Wait()
	if ran := st.take(); !slices.Equal(ran, []string{"page"}) {
		t.Errorf("Page ran %v, want one page query", ran)
	}
	for _, p := range pages {
		if p.Info.StartCursor != pages[0].Info.StartCursor || !slices.Equal(ids(p.Rows), []int64{1, 2}) || !p.Info.HasNextPage {
			t.Errorf("page %+v", p)
		}
	}
	// The count is not taken again.
	if n, err := c.TotalCount(ctx); err != nil || n != 7 {
		t.Errorf("TotalCount = %d, %v", n, err)
	}
	if ran := st.take(); len(ran) > 0 {
		t.Errorf("a second TotalCount ran %v", ran)
	}
}

// TestPaginateUnderTheRowCap: a full page is read whatever the DB's row cap,
// which may be at or below MaxPage: MaxPage bounds the page query.
func TestPaginateUnderTheRowCap(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec(`CREATE TABLE people (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for id := 1; id <= 150; id++ {
		if _, err := sqlDB.Exec(`INSERT INTO people (id, name, age) VALUES (?, 'p', ?)`, id, id); err != nil {
			t.Fatal(err)
		}
	}
	db := velox.Open(velox.SQLite, sqlDB, velox.WithMaxRows(100))
	if _, err := db.Query[person]().AllowUnbounded().All(ctx); !errors.Is(err, velox.ErrTooManyRows) {
		t.Fatalf("the cap: %v, want ErrTooManyRows", err)
	}
	for name, args := range map[string]gqlgraph.Args{"the default page": {}, "first 100": {First: new(100)}, "last 100": {Last: new(100)}} {
		p, err := gqlgraph.PageWith(ctx, db.Query[person](), "Person", args, nil, personKey())
		if err != nil {
			t.Errorf("%s under WithMaxRows(100): %v", name, err)
			continue
		}
		if len(p.Rows) != 100 || (!p.Info.HasNextPage && !p.Info.HasPreviousPage) {
			t.Errorf("%s under WithMaxRows(100): %d rows, %+v", name, len(p.Rows), p.Info)
		}
	}
}

// TestPaginateOrderTerms: an orderBy of more than MaxOrderTerms terms fails
// before any query.
func TestPaginateOrderTerms(t *testing.T) {
	ctx := context.Background()
	db, st := peopleDB(t)
	order := func(n int) []gqlgraph.OrderTerm[person] {
		var o []gqlgraph.OrderTerm[person]
		for range n {
			o = append(o, nameTerm(veloxgql.OrderDirectionAsc))
		}
		return o
	}
	st.take()
	if _, err := gqlgraph.PageWith(ctx, db.Query[person](), "Person", gqlgraph.Args{}, order(veloxgql.DefaultLimits().MaxOrderTerms+1), personKey()); !errors.Is(err, veloxgql.ErrBadPagination) {
		t.Errorf("%d terms: %v, want ErrBadPagination", veloxgql.DefaultLimits().MaxOrderTerms+1, err)
	}
	if ran := st.take(); len(ran) > 0 {
		t.Errorf("ran %v", ran)
	}
	if _, err := gqlgraph.PageWith(ctx, db.Query[person](), "Person", gqlgraph.Args{}, order(veloxgql.DefaultLimits().MaxOrderTerms), personKey()); err != nil {
		t.Errorf("%d terms: %v", veloxgql.DefaultLimits().MaxOrderTerms, err)
	}
}

// TestConnectionWithoutExtension is Review Focus 2: generated code may run
// outside a gqlgen server, in a job or a test, with no limits in its
// context. It then pages by the defaults, not by zeros: a connection with no
// first over 150 rows is a page of 100, with a next page.
func TestConnectionWithoutExtension(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec(`CREATE TABLE people (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for id := 1; id <= 150; id++ {
		if _, err := sqlDB.Exec(`INSERT INTO people (id, name, age) VALUES (?, 'p', ?)`, id, id); err != nil {
			t.Fatal(err)
		}
	}
	db := velox.Open(velox.SQLite, sqlDB)
	c, err := gqlgraph.NewConnectionOf(ctx, db.Query[person](), "Person", gqlgraph.Args{}, nil, personKey())
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Page(ctx)
	if err != nil || len(p.Rows) != 100 || !p.Info.HasNextPage {
		t.Fatalf("NewConnection under context.Background(): %v, %d rows, %+v; want 100 and a next page", err, len(p.Rows), p.Info)
	}
	if _, err := gqlgraph.NewConnectionOf(ctx, db.Query[person](), "Person", gqlgraph.Args{First: new(101)}, nil, personKey()); !errors.Is(err, veloxgql.ErrPageTooLarge) {
		t.Errorf("first 101 under context.Background(): %v, want ErrPageTooLarge", err)
	}

}

// TestPaginateClearsOffset: a base query's Offset is the application's, not
// the connection's: the page and the count start at the first row.
func TestPaginateClearsOffset(t *testing.T) {
	db, _ := peopleDB(t)
	ctx := gqlgenx.NewExtension(db).NewContext(context.Background())
	q := db.Query[person]().Limit(1).Offset(3)
	c, err := gqlgraph.NewConnectionOf(ctx, q, "Person", gqlgraph.Args{First: new(2)}, nil, personKey())
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Page(ctx)
	if err != nil || !slices.Equal(ids(p.Rows), []int64{1, 2}) || !p.Info.HasNextPage {
		t.Fatalf("%v %+v, want 1 and 2 and a next page", err, p)
	}
	if n, err := c.TotalCount(ctx); err != nil || n != 10 {
		t.Errorf("totalCount %d %v, want 10", n, err)
	}
}

// TestConnectionRefusesOrderedQuery: a base query with an OrderBy would put
// its order before the connection's and break every cursor after the first
// page. It is the application's bug, so the error is internal (uncoded), and
// nothing is queried.
func TestConnectionRefusesOrderedQuery(t *testing.T) {
	db, st := peopleDB(t)
	ctx := gqlgenx.NewExtension(db).NewContext(context.Background())
	q := db.Query[person]().OrderBy(people.Age.Asc())
	c, err := gqlgraph.NewConnectionOf(ctx, q, "Person", gqlgraph.Args{First: new(2)}, nil, personKey())
	want := "veloxgql: Paginate of Person: q may be ordered only by expressions (Calc); a column ordering comes from the orderBy argument"
	if c.Connection != nil || err == nil || err.Error() != want {
		t.Fatalf("%v %v, want no connection and %q", c, err, want)
	}
	if coded, ok := errors.AsType[interface {
		error
		Extensions() map[string]any
	}](err); ok {
		t.Errorf("the error is coded: %v", coded.Extensions())
	}
	if ran := st.take(); len(ran) != 0 {
		t.Errorf("ran %v", ran)
	}
}

// ownerNameOrder orders children by their owner's name: a joined term.
func ownerNameOrder(dir veloxgql.OrderDirection) []gqlgraph.OrderTerm[child] {
	return []gqlgraph.OrderTerm[child]{gqlgraph.JoinedTermOf[child, owner]("OWNER_NAME", dir, int(children.Owner.I), int(owners.Name.I))}
}

// A joined term orders by the related row's column and pages by cursors that
// hold it. A row with no related row stays, in the page and in the count, its
// term NULL: after every value ascending, before them descending.
func TestJoinedTermPages(t *testing.T) {
	db, _, _ := edgeDB(t)
	ctx := context.Background()
	page := func(args gqlgraph.Args, dir veloxgql.OrderDirection) (*gqlgraph.PageOf[child], int) {
		t.Helper()
		c, err := gqlgraph.NewConnectionOf(ctx, db.Query[child](), "Child", args, ownerNameOrder(dir), childKey())
		if err != nil {
			t.Fatal(err)
		}
		p, err := c.Page(ctx)
		if err != nil {
			t.Fatal(err)
		}
		n, err := c.TotalCount(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return p, n
	}
	asc, desc := veloxgql.OrderDirectionAsc, veloxgql.OrderDirectionDesc

	// Children 1-4 are owner a's, 5 and 6 owner b's; 7 has no owner.
	all, n := page(gqlgraph.Args{}, asc)
	if got := childIDs(all.Rows); !reflect.DeepEqual(got, []int64{1, 2, 3, 4, 5, 6, 7}) || n != 7 {
		t.Errorf("ascending: %v, totalCount %d", got, n)
	}
	// One term DESC: the key breaks its ties DESC, as entgql's does.
	if all, _ = page(gqlgraph.Args{}, desc); !reflect.DeepEqual(childIDs(all.Rows), []int64{7, 6, 5, 4, 3, 2, 1}) {
		t.Errorf("descending: %v", childIDs(all.Rows))
	}

	// Forward by twos, each page after the last cursor of the one before.
	var got []int64
	args := gqlgraph.Args{First: new(2)}
	for range 5 {
		p, _ := page(args, desc)
		got = append(got, childIDs(p.Rows)...)
		if !p.Info.HasNextPage {
			break
		}
		args.After = p.Info.EndCursor
		if f := args.After.Terms[0].Field; f != "OWNER_NAME" {
			t.Fatalf("the cursor's field is %q", f)
		}
		if v := args.After.Terms[0].Value; v != "a" && v != "b" {
			t.Fatalf("the cursor's value is %v, the owner's name", v)
		}
	}
	if !reflect.DeepEqual(got, []int64{7, 6, 5, 4, 3, 2, 1}) {
		t.Errorf("descending by twos: %v", got)
	}

	// Backward from the end, through a cursor that crossed the wire as JSON:
	// child 7's, whose owner's name is null.
	last, _ := page(gqlgraph.Args{Last: new(2)}, asc)
	if got := childIDs(last.Rows); !reflect.DeepEqual(got, []int64{6, 7}) || !last.Info.HasPreviousPage {
		t.Errorf("last 2: %v %+v", got, last.Info)
	}
	enc, err := veloxgql.EncodeCursor(last.Cursors[1])
	if err != nil {
		t.Fatal(err)
	}
	var wire veloxgql.Cursor
	if err := wire.UnmarshalGQLContext(ctx, enc); err != nil {
		t.Fatal(err)
	}
	if wire.Terms[0].Value != nil {
		t.Errorf("child 7's cursor holds %v, want null", wire.Terms[0].Value)
	}
	if p, _ := page(gqlgraph.Args{Last: new(2), Before: &wire}, asc); !reflect.DeepEqual(childIDs(p.Rows), []int64{5, 6}) {
		t.Errorf("last 2 before 7: %v", childIDs(p.Rows))
	}
	if p, _ := page(gqlgraph.Args{First: new(2), After: &wire}, asc); len(p.Rows) != 0 || p.Info.HasNextPage {
		t.Errorf("first 2 after 7: %v %+v", childIDs(p.Rows), p.Info)
	}

	// The same relation named by two terms is joined once.
	two := append(ownerNameOrder(asc), gqlgraph.JoinedTermOf[child, owner]("OWNER_AGE", asc, int(children.Owner.I), int(owners.Age.I)))
	c, err := gqlgraph.NewConnectionOf(ctx, db.Query[child](), "Child", gqlgraph.Args{}, two, childKey())
	if err != nil {
		t.Fatal(err)
	}
	if p, err := c.Page(ctx); err != nil || len(p.Rows) != 7 {
		t.Errorf("two terms of one relation: %v %v", err, p)
	}
}

// TestOrderViaKeepsRows: ordering by a related row's column changes neither
// which rows a connection holds nor its totalCount, when the related row is
// missing (child 7) or a filter hides it from the caller (owner 1): its term
// is NULL, and paging by twos through it, forward and backward, gives the
// whole order.
func TestOrderViaKeepsRows(t *testing.T) {
	_, raw, _ := edgeDB(t)
	db := velox.Open(velox.SQLite, raw, velox.WithFilter(hideOwnerFilter))
	ctx := context.WithValue(context.Background(), hideOwnerKey{}, true)
	conn := func(args gqlgraph.Args, order []gqlgraph.OrderTerm[child]) (*gqlgraph.PageOf[child], int) {
		t.Helper()
		c, err := gqlgraph.NewConnectionOf(ctx, db.Query[child](), "Child", args, order, childKey())
		if err != nil {
			t.Fatal(err)
		}
		p, err := c.Page(ctx)
		if err != nil {
			t.Fatal(err)
		}
		n, err := c.TotalCount(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return p, n
	}
	plain, pn := conn(gqlgraph.Args{}, nil)
	members := childIDs(plain.Rows)
	if !reflect.DeepEqual(members, []int64{1, 2, 3, 4, 5, 6, 7}) || pn != 7 {
		t.Fatalf("unordered: %v, totalCount %d", members, pn)
	}
	for _, c := range []struct {
		dir  veloxgql.OrderDirection
		want []int64
	}{
		// Owner 1 ("a") is hidden: only children 5 and 6 have an owner's name.
		{veloxgql.OrderDirectionAsc, []int64{5, 6, 1, 2, 3, 4, 7}},
		// DESC: the key breaks ties DESC too, as entgql's does.
		{veloxgql.OrderDirectionDesc, []int64{7, 4, 3, 2, 1, 6, 5}},
	} {
		order := ownerNameOrder(c.dir)
		all, n := conn(gqlgraph.Args{}, order)
		got := childIDs(all.Rows)
		if !reflect.DeepEqual(got, c.want) || n != pn {
			t.Errorf("%s: %v, totalCount %d; want %v, %d", c.dir, got, n, c.want, pn)
		}
		sorted := slices.Clone(got)
		slices.Sort(sorted)
		if !reflect.DeepEqual(sorted, members) {
			t.Errorf("%s: members %v, want %v", c.dir, sorted, members)
		}
		for i, r := range all.Rows {
			if v := all.Cursors[i].Terms[0].Value; (v == nil) != (r.ID <= 4 || r.ID == 7) {
				t.Errorf("%s: child %d's cursor holds %v", c.dir, r.ID, v)
			}
		}
		var fwd []int64
		args := gqlgraph.Args{First: new(2)}
		for range len(c.want) {
			p, _ := conn(args, order)
			fwd = append(fwd, childIDs(p.Rows)...)
			if !p.Info.HasNextPage {
				break
			}
			args.After = p.Info.EndCursor
		}
		if !reflect.DeepEqual(fwd, c.want) {
			t.Errorf("%s forward by twos: %v, want %v", c.dir, fwd, c.want)
		}
		var back []int64
		args = gqlgraph.Args{Last: new(2)}
		for range len(c.want) {
			p, _ := conn(args, order)
			back = append(childIDs(p.Rows), back...)
			if !p.Info.HasPreviousPage {
				break
			}
			args.Before = p.Info.StartCursor
		}
		if !reflect.DeepEqual(back, c.want) {
			t.Errorf("%s backward by twos: %v, want %v", c.dir, back, c.want)
		}
	}
}

// The connection of an edge, read for every parent in one statement, orders by a
// joined column too: each parent's page is what its own query gives.
func TestJoinedTermInABatchedEdgeConnection(t *testing.T) {
	db, _, log := edgeDB(t)
	ctx := operation(t, db, childrenQuery)
	xs := ownersPage(t, ctx, db, db.Query[owner](), gqlgraph.Args{})
	log.take("owners")
	ectx := field(t, ctx, "owners", "edges", "node", "children")
	args := gqlgraph.Args{First: new(2)}
	order := ownerNameOrder(veloxgql.OrderDirectionDesc)
	var loads, counts []string
	for _, x := range xs {
		got, n := childPage(t, ectx, db, x, db.Query[child](), args, order)
		for _, s := range log.take("children") {
			if strings.Contains(s, "COUNT(") {
				counts = append(counts, s)
			} else {
				loads = append(loads, s)
			}
		}
		want, wn := ownPage(t, db, x, db.Query[child](), args, order)
		log.take("children")
		samePage(t, x.Name, got, want)
		if n != wn {
			t.Errorf("%s: totalCount %d, want %d", x.Name, n, wn)
		}
	}
	oneEach(t, "the load", loads, "ROW_NUMBER()")
	oneEach(t, "the count", counts, "GROUP BY")
}

// TestOrderViaDeniedTarget: when the related entity's Read is denied
// outright, ordering by its column still pages every row, each related term
// NULL, as for a row whose related row is hidden; the connection and its
// totalCount do not fail. An inner join to the denied entity still fails.
func TestOrderViaDeniedTarget(t *testing.T) {
	_, raw, _ := edgeDB(t)
	deny := func(_ context.Context, _ *velox.DB, fe velox.FilterEntity, op velox.Op) (velox.Predicate, error) {
		if fe.Name() == "owner" && op == velox.OpSelect {
			return velox.Predicate{}, velox.Deny("owners are closed")
		}
		return velox.Allow(), nil
	}
	db := velox.Open(velox.SQLite, raw, velox.WithFilter(deny))
	ctx := context.Background()
	for _, dir := range []veloxgql.OrderDirection{veloxgql.OrderDirectionAsc, veloxgql.OrderDirectionDesc} {
		c, err := gqlgraph.NewConnectionOf(ctx, db.Query[child](), "Child", gqlgraph.Args{}, ownerNameOrder(dir), childKey())
		if err != nil {
			t.Fatal(err)
		}
		p, err := c.Page(ctx)
		if err != nil {
			t.Fatalf("%s: page: %v", dir, err)
		}
		want := []int64{1, 2, 3, 4, 5, 6, 7}
		if dir == veloxgql.OrderDirectionDesc {
			want = []int64{7, 6, 5, 4, 3, 2, 1}
		}
		if got := childIDs(p.Rows); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v, want %v", dir, got, want)
		}
		if n, err := c.TotalCount(ctx); err != nil || n != 7 {
			t.Errorf("%s: totalCount %d, %v; want 7", dir, n, err)
		}
	}
	if _, err := db.Query[child]().Join(children.Owner).Limit(10).All(ctx); !errors.Is(err, velox.ErrDenied) {
		t.Errorf("an inner join to a denied entity: %v, want ErrDenied", err)
	}
}
