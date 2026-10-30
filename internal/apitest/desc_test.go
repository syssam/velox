package apitest

import (
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/apitest/fixture"
	"github.com/syssam/velox2/internal/fakeconn"
)

func TestDescQueriesAndLoads(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{{int64(1), int64(7), "ada", nil, t0}},
		{{int64(10), int64(1), "notes"}, {int64(11), int64(1), "more"}},
	}}
	db := openFake(velox.Postgres, e)
	as, err := db.Query[fixture.DAuthor]().Where(fixture.DAuthors.Name.EQ("ada")).With(fixture.DAuthors.Books).Limit(1).All(velox.WithTenant(ctx, int64(7)))
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
	e := &fakeconn.Exec{Results: [][][]any{
		{{int64(10), int64(1), "notes"}},
		{{int64(1), int64(7), "ada", nil, t0}},
	}}
	db := openFake(velox.Postgres, e)
	bs, err := db.Query[fixture.DBook]().Where(fixture.DBooks.Title.EQ("notes")).With(fixture.DBooks.Author).Limit(1).All(ctx)
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
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	_, err := db.Query[badDesc]().Limit(1).All(ctx)
	var se *velox.SchemaError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "tenant") {
		t.Fatalf("got %v", err)
	}
	_, err = db.Query[panicDesc]().Limit(1).All(ctx)
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "panicked: boom") {
		t.Fatalf("got %v", err)
	}
	if err := velox.CheckEntity[fixture.DAuthor](); err != nil {
		t.Fatal(err)
	}
	// CheckEntity reports the same errors, as *SchemaError.
	for name, err := range map[string]error{"bad Desc": velox.CheckEntity[badDesc](), "panicking Desc": velox.CheckEntity[panicDesc]()} {
		if !errors.As(err, &se) || err == nil {
			t.Errorf("CheckEntity of a %s: got %v, want a *SchemaError", name, err)
		}
	}
}
