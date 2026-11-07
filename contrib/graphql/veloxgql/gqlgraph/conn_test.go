package gqlgraph_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
)

// personConn is person's connections, as the generated code writes them.
var personConn = gqlgraph.Type{
	Name:  "Person",
	Key:   []gqlgraph.Column{{Name: "id", Index: int(people.ID.I)}},
	Order: []gqlgraph.Column{{Name: "NAME", Index: int(people.Name.I)}, {Name: "AGE", Index: int(people.Age.I)}},
}

// TestPaginateBase: a q without the key, or without the column of an own
// order field, is refused with an internal error that names NewXConnection; an
// order field the type does not have is refused as the client's.
func TestPaginateBase(t *testing.T) {
	db, st := peopleDB(t)
	ctx := veloxgql.NewExtension(db).NewContext(context.Background())
	q := velox.Untyped(db.Query[person]())
	age := []gqlgraph.Order{{Field: "AGE"}}
	for _, c := range []struct {
		name  string
		q     velox.TableQuery
		order []gqlgraph.Order
		want  string
	}{
		{"no key", q.Columns(int(people.Name.I)), nil, "veloxgql: NewPersonConnection: q does not select the key, which the cursors hold"},
		{"no order column", q.Columns(int(people.ID.I), int(people.Name.I)), age, "veloxgql: NewPersonConnection: q does not select the column of AGE, which orderBy names and the cursors hold"},
	} {
		_, err := gqlgraph.Paginate(ctx, gqlgraph.Query(c.q), &personConn, gqlgraph.Args{Order: c.order})
		var coded interface{ Extensions() map[string]any }
		if err == nil || err.Error() != c.want || errors.As(err, &coded) {
			t.Errorf("%s: %v, want the internal error %q", c.name, err, c.want)
		}
	}
	unknown := []gqlgraph.Order{{Field: "HEIGHT"}}
	_, err := gqlgraph.Paginate(ctx, gqlgraph.Query(q), &personConn, gqlgraph.Args{Order: unknown})
	var coded interface{ Extensions() map[string]any }
	if want := `veloxgql: PersonOrder: "HEIGHT" is not a value of PersonOrderField`; err == nil || err.Error() != want || !errors.As(err, &coded) {
		t.Errorf("an unknown order field: %v, want the input error %q", err, want)
	}
	c, err := gqlgraph.Paginate(ctx, gqlgraph.Query(q), &personConn, gqlgraph.Args{First: new(3), Order: age})
	if err != nil {
		t.Fatal(err)
	}
	if ran := st.take(); len(ran) != 0 {
		t.Errorf("ran %v before the page", ran)
	}
	p, err := c.Page(ctx)
	if err != nil || len(p.Rows) != 3 {
		t.Fatalf("%v, %v", p, err)
	}
}

// gadget is an entity whose key and nullable order field are of the
// application's own Go types, which a cursor holds as their base types.
type (
	gadgetID int64
	score    int32
	gadget   struct {
		ID    gadgetID
		Score *score
	}
)

var gadgets = struct {
	ID    velox.NumberCol[gadget, gadgetID]
	Score velox.NullNumberCol[gadget, score]
}{ID: velox.NumberCol[gadget, gadgetID]{I: 0}, Score: velox.NullNumberCol[gadget, score]{I: 1}}

var gadgetMeta velox.MetaOnce[gadget]

func (*gadget) VeloxMeta() *velox.Meta[gadget] { return gadgetMeta.Load(gadgetDescriptor) }

func gadgetDescriptor() velox.Descriptor[gadget] {
	return velox.Descriptor[gadget]{
		Table:      "gadgets",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Score", Name: "score", Nullable: true}},
		PrimaryKey: []int{0},
		FieldPtr: func(x *gadget, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.Score
			}
			return nil
		},
	}
}

// TestNamedTypeTerms: a connection pages through a nullable order field and
// a key of named Go types, forward and backward, from cursors off the wire.
func TestNamedTypeTerms(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec(`CREATE TABLE gadgets (id INTEGER PRIMARY KEY, score INTEGER)`); err != nil {
		t.Fatal(err)
	}
	scores := []any{int64(3), nil, int64(1), int64(3), nil, int64(2)}
	for i, s := range scores {
		if _, err := sqlDB.Exec(`INSERT INTO gadgets (id, score) VALUES (?, ?)`, i+1, s); err != nil {
			t.Fatal(err)
		}
	}
	db := velox.Open(velox.SQLite, sqlDB)
	ctx := veloxgql.NewExtension(db).NewContext(context.Background())
	conn := &gqlgraph.Type{
		Name:  "Gadget",
		Key:   []gqlgraph.Column{{Name: "id", Index: int(gadgets.ID.I)}},
		Order: []gqlgraph.Column{{Name: "SCORE", Index: int(gadgets.Score.I)}},
	}
	order := []gqlgraph.Order{{Field: "SCORE", Direction: veloxgql.OrderDirectionDesc}}
	page := func(args gqlgraph.Args) ([]gadgetID, *gqlgraph.Page) {
		t.Helper()
		args.Order = order
		c, err := gqlgraph.Paginate(ctx, gqlgraph.Query(velox.Untyped(db.Query[gadget]())), conn, args)
		if err != nil {
			t.Fatal(err)
		}
		p, err := c.Page(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var out []gadgetID
		for _, r := range p.Rows {
			out = append(out, (*gadget)(r).ID)
		}
		return out, p
	}
	all, p := page(gqlgraph.Args{First: new(6)})
	// NULL sorts as the largest value, so first descending.
	want := []gadgetID{2, 5, 1, 4, 6, 3}
	if !slices.Equal(all, want) {
		t.Fatalf("all: %v, want %v", all, want)
	}
	for i := range want {
		cur := wire(t, p.Cursors[i])
		next, _ := page(gqlgraph.Args{First: new(6), After: cur})
		if !slices.Equal(next, want[i+1:]) {
			t.Errorf("after %d: %v, want %v", want[i], next, want[i+1:])
		}
		prev, _ := page(gqlgraph.Args{Last: new(6), Before: cur})
		if !slices.Equal(prev, want[:i]) {
			t.Errorf("before %d: %v, want %v", want[i], prev, want[:i])
		}
	}
	bad := veloxgql.Cursor{Type: "Gadget", Terms: []veloxgql.CursorTerm{{Field: "SCORE", Direction: veloxgql.OrderDirectionDesc, Value: "high"}}, Key: 1}
	if _, err := gqlgraph.Paginate(ctx, gqlgraph.Query(velox.Untyped(db.Query[gadget]())), conn, gqlgraph.Args{After: wire(t, bad), Order: order}); !errors.Is(err, veloxgql.ErrBadCursor) {
		t.Errorf("a string score: %v, want ErrBadCursor", err)
	}
}

// TestConnColumns: a selection reads Read and its fields' columns, each once,
// an edge reads Read alone, and a field Conn does not know reads every
// column.
func TestConnColumns(t *testing.T) {
	c := &gqlgraph.Type{
		AlwaysRead: []int{0, 1},
		Fields:     []string{0: "id", 1: "title", 3: "status"},
		Edges:      []string{"author"},
	}
	for _, x := range []struct {
		fields []string
		want   []int
	}{
		{nil, []int{0, 1}},
		{[]string{"id", "title"}, []int{0, 1}},
		{[]string{"status", "author", "status"}, []int{0, 1, 3}},
		{[]string{"status", "postCount"}, nil},
	} {
		if got := c.Columns(x.fields); !slices.Equal(got, x.want) {
			t.Errorf("Columns(%q) = %v, want %v", x.fields, got, x.want)
		}
	}
	// Columns returns a fresh slice: appending to it leaves Read as it is.
	_ = append(c.Columns([]string{"status"}), 9)
	if !slices.Equal(c.AlwaysRead, []int{0, 1}) {
		t.Errorf("Read is %v after a selection", c.AlwaysRead)
	}
}
