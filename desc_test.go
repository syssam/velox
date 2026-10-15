package velox_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox2"
)

func TestDescMetadata(t *testing.T) {
	info := velox.InfoOf[dAuthor]() // export_test.go: the entity's EntityInfo, or the test fails on its error
	if info.Name != "velox_test.dAuthor" || info.Table != "authors" || info.Tenant != 1 || len(info.PrimaryKey) != 1 {
		t.Fatalf("%+v", info)
	}
	email, created := info.Columns[3], info.Columns[4]
	if !email.Nullable || !email.Sensitive || velox.GoType(email) != reflect.TypeFor[*string]() {
		t.Fatalf("email %+v", email)
	}
	if !created.Immutable || created.DefaultFunc == nil || created.Nullable {
		t.Fatalf("created_at %+v", created)
	}
}

func TestDescQueriesAndLoads(t *testing.T) {
	e := &velox.FakeExec{Results: [][][]any{
		{{int64(1), int64(7), "ada", nil, t0}},
		{{int64(10), int64(1), "notes"}, {int64(11), int64(1), "more"}},
	}}
	db := velox.OpenFake(velox.Postgres, e)
	as, err := db.Query[dAuthor]().Where(dAuthors.Name.EQ("ada")).With(dAuthors.Books).Limit(1).All(velox.WithTenant(ctx, int64(7)))
	if err != nil {
		t.Fatal(err)
	}
	books, ok := as[0].Books.Get()
	if !ok || len(books) != 2 || books[1].Title != "more" {
		t.Fatalf("books %+v %v", books, ok)
	}
	if want := `SELECT "id", "author_id", "title" FROM "books" WHERE "author_id" IN ($1) ORDER BY "id"`; e.Log[1] != want {
		t.Fatalf("relation SQL %s", e.Log[1])
	}
}

// TestDescLoadsOne loads the to-one direction of an EdgeTo: the book's
// AuthorID is the author's ID.
func TestDescLoadsOne(t *testing.T) {
	e := &velox.FakeExec{Results: [][][]any{
		{{int64(10), int64(1), "notes"}},
		{{int64(1), int64(7), "ada", nil, t0}},
	}}
	db := velox.OpenFake(velox.Postgres, e)
	bs, err := db.Query[dBook]().Where(dBooks.Title.EQ("notes")).With(dBooks.Author).Limit(1).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := bs[0].Author.Get()
	if !ok || a == nil || a.Name != "ada" {
		t.Fatalf("author %+v %v", a, ok)
	}
	if want := `SELECT "id", "org_id", "name", "email", "created_at" FROM "authors" WHERE "id" IN ($1)`; !strings.HasPrefix(e.Log[1], want) {
		t.Fatalf("relation SQL %s", e.Log[1])
	}
}

type badDesc struct{ ID int64 }

var badDescMeta velox.MetaOnce[badDesc]

func (*badDesc) VeloxMeta() *velox.Meta[badDesc] {
	return badDescMeta.Load(func() velox.Desc[badDesc] {
		return velox.Desc[badDesc]{Table: "b", Columns: []velox.ColumnDesc{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0}, Tenant: 3,
			Addr: func(x *badDesc, i int) any { return &x.ID }}
	})
}

type panicDesc struct{ ID int64 }

var panicDescMeta velox.MetaOnce[panicDesc]

func (*panicDesc) VeloxMeta() *velox.Meta[panicDesc] {
	return panicDescMeta.Load(func() velox.Desc[panicDesc] { panic("boom") })
}

func TestDescErrors(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	_, err := db.Query[badDesc]().Limit(1).All(ctx)
	var se *velox.SchemaError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "tenant") {
		t.Fatalf("got %v", err)
	}
	_, err = db.Query[panicDesc]().Limit(1).All(ctx)
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "panicked: boom") {
		t.Fatalf("got %v", err)
	}
	if err := velox.CheckEntity[dAuthor](); err != nil {
		t.Fatal(err)
	}
}

// dRow is a model for the refusals of buildDesc.
type dRow struct {
	ID     int64
	Flag   bool
	Opt    *bool
	Name   string
	Author velox.One[dAuthor]
}

var outside int64

// rowDesc is a valid Desc of dRow; each case of TestDescRefusals breaks it.
func rowDesc() velox.Desc[dRow] {
	return velox.Desc[dRow]{
		Table:      "rows",
		Schema:     "app",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Flag", Name: "flag"}, {Field: "Opt", Name: "opt", Default: true}, {Field: "Name", Name: "name"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Indexes:    []velox.IndexInfo{{Columns: []int{3}, Unique: true}},
		Relations:  []velox.Relation{velox.EdgeTo[dAuthor]("Author", 0, 0)},
		Addr: func(x *dRow, i int) any {
			return [...]any{&x.ID, &x.Flag, &x.Opt, &x.Name}[i]
		},
		RelAddr: func(x *dRow, i int) any { return &x.Author },
	}
}

func TestDescAcceptsAndConverts(t *testing.T) {
	info, err := velox.LoadDesc(rowDesc())
	if err != nil {
		t.Fatal(err)
	}
	if info.Schema != "app" || info.Tenant != -1 || len(info.Indexes) != 1 {
		t.Fatalf("%+v", info)
	}
	// A pointer field's Default is given as a value of its element type and
	// held as a pointer, which fillDefault copies for every row.
	if opt := info.Columns[2]; !opt.HasDefault {
		t.Fatalf("opt %+v", opt)
	} else if p, _ := opt.Default.(*bool); p == nil || !*p {
		t.Fatalf("opt %+v", opt)
	}
}

func TestDescRefusals(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(d *velox.Desc[dRow])
		want string
	}{
		{"no Addr", func(d *velox.Desc[dRow]) { d.Addr = nil }, "dRow: the generated metadata has no Addr"},
		{"Addr gives nil", func(d *velox.Desc[dRow]) {
			d.Addr = func(x *dRow, i int) any { return nil }
		}, "dRow.ID: Addr does not return a pointer into the entity for column 0"},
		{"Addr gives a value", func(d *velox.Desc[dRow]) {
			d.Addr = func(x *dRow, i int) any { return x.ID }
		}, "Addr does not return a pointer"},
		{"Addr gives a nil pointer", func(d *velox.Desc[dRow]) {
			d.Addr = func(x *dRow, i int) any { return (*int64)(nil) }
		}, "Addr does not return a pointer"},
		{"Addr points outside", func(d *velox.Desc[dRow]) {
			d.Addr = func(x *dRow, i int) any { return &outside }
		}, "Addr does not return a pointer into the entity"},
		{"no primary key", func(d *velox.Desc[dRow]) { d.PrimaryKey = nil }, "the primary key names no fields"},
		{"primary key out of range", func(d *velox.Desc[dRow]) { d.PrimaryKey = []int{4} }, "the primary key names column 4, of 4"},
		{"index out of range", func(d *velox.Desc[dRow]) { d.Indexes = []velox.IndexInfo{{Columns: []int{-1}}} }, "an index names column -1, of 4"},
		{"tenant out of range", func(d *velox.Desc[dRow]) { d.Tenant = 4 }, "the tenant names column 4, of 4"},
		{"tenant with a default", func(d *velox.Desc[dRow]) {
			d.Columns[3].DefaultFunc = func() any { return "x" }
			d.Tenant = 3
		}, "the tenant column cannot have Default"},
		{"Default on a bool", func(d *velox.Desc[dRow]) { d.Columns[1].Default = true }, "dRow.Flag: Default on bool/numeric field Flag would overwrite an explicit false or 0 on insert"},
		{"Default of another type", func(d *velox.Desc[dRow]) { d.Columns[3].Default = 7 }, "dRow.Name: Default is a int, not a string"},
		{"Immutable with an UpdateDefault", func(d *velox.Desc[dRow]) {
			d.Columns[3].Immutable = true
			d.Columns[3].UpdateDefault = func() any { return "x" }
		}, "dRow.Name: Immutable column Name has an UpdateDefault; every update would be refused"},
		{"duplicate column name", func(d *velox.Desc[dRow]) { d.Columns[3].Name = "flag" }, `column name "flag" is also the name of Flag`},
		{"panic in Addr", func(d *velox.Desc[dRow]) {
			d.Addr = func(x *dRow, i int) any { panic("addr") }
		}, "the generated metadata panicked: addr"},
		{"no RelAddr", func(d *velox.Desc[dRow]) { d.RelAddr = nil }, "has relations but no RelAddr"},
		{"RelAddr gives a column", func(d *velox.Desc[dRow]) {
			d.RelAddr = func(x *dRow, i int) any { return &x.ID }
		}, "dRow.Author: RelAddr does not return a *One[T] or *Many[T]"},
		{"relation not from EdgeTo", func(d *velox.Desc[dRow]) {
			d.Relations = []velox.Relation{velox.RelationTo[dAuthor]("Author")}
		}, "relation 0 was not made by EdgeTo or EdgeThrough"},
		{"EdgeTo owner column out of range", func(d *velox.Desc[dRow]) {
			d.Relations = []velox.Relation{velox.EdgeTo[dAuthor]("Author", 9, 0)}
		}, "dRow.Author: its key names column 9 of velox_test.dRow, which has 4"},
		{"EdgeTo target column out of range", func(d *velox.Desc[dRow]) {
			d.Relations = []velox.Relation{velox.EdgeTo[dAuthor]("Author", 0, 5)}
		}, "its key names column 5 of velox_test.dAuthor, which has 5"},
		{"EdgeTo key types differ", func(d *velox.Desc[dRow]) {
			d.Relations = []velox.Relation{velox.EdgeTo[dAuthor]("Author", 3, 0)}
		}, "the key types differ"},
		{"EdgeThrough", func(d *velox.Desc[dRow]) {
			d.Relations = []velox.Relation{velox.EdgeThrough[dBook, dAuthor]("Author", 0, 0, 1, 0)}
		}, "many-to-many relations arrive in Task 4"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := rowDesc()
			c.edit(&d)
			_, err := velox.LoadDesc(d)
			var se *velox.SchemaError
			if !errors.As(err, &se) || !strings.HasPrefix(err.Error(), "velox: velox_test.dRow") || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want a *SchemaError containing %q", err, c.want)
			}
		})
	}
}
