package apitest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

func TestDescriptorQueriesAndLoads(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{{int64(1), int64(7), "ada", nil, t0}},
		{{int64(10), int64(1), "notes"}, {int64(11), int64(1), "more"}},
	}}
	db := openFake(velox.Postgres, e)
	as, err := db.Query[fixture.DAuthor]().Where(fixture.DAuthors.Name.EQ("ada")).With(fixture.DAuthors.Books).Limit(1).All(testmodel.Org.With(context.Background(), int64(7)))
	if err != nil {
		t.Fatal(err)
	}
	books, ok := as[0].Books.Get()
	if !ok || len(books) != 2 || books[1].Title != "more" {
		t.Fatalf("books %+v %v", books, ok)
	}
	if want := `SELECT "id", "author_id", "title" FROM "books" WHERE "author_id" = ANY($1) ORDER BY "id"`; e.Log[1] != want {
		t.Fatalf("relation SQL %s", e.Log[1])
	}
}

// TestDescriptorLoadsOne loads the to-one direction of an EdgeTo: the book's
// AuthorID is the author's ID.
func TestDescriptorLoadsOne(t *testing.T) {
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
	if want := `SELECT "id", "org_id", "name", "email", "created_at" FROM "authors" WHERE "id" = ANY($1)`; !strings.HasPrefix(e.Log[1], want) {
		t.Fatalf("relation SQL %s", e.Log[1])
	}
}

type badDescriptor struct{ ID int64 }

var badDescriptorMeta velox.MetaOnce[badDescriptor]

func (*badDescriptor) VeloxMeta() *velox.Meta[badDescriptor] {
	return badDescriptorMeta.Load(func() velox.Descriptor[badDescriptor] {
		return velox.Descriptor[badDescriptor]{Table: "b", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}}, PrimaryKey: []int{3},
			FieldPtr: func(x *badDescriptor, i int) any { return &x.ID }}
	})
}

type panicDescriptor struct{ ID int64 }

var panicDescriptorMeta velox.MetaOnce[panicDescriptor]

func (*panicDescriptor) VeloxMeta() *velox.Meta[panicDescriptor] {
	return panicDescriptorMeta.Load(func() velox.Descriptor[panicDescriptor] { panic("boom") })
}

func TestDescriptorErrors(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	_, err := db.Query[badDescriptor]().Limit(1).All(ctx)
	if _, ok := errors.AsType[*velox.SchemaError](err); !ok || !strings.Contains(err.Error(), "primary key") {
		t.Fatalf("got %v", err)
	}
	_, err = db.Query[panicDescriptor]().Limit(1).All(ctx)
	if _, ok := errors.AsType[*velox.SchemaError](err); !ok || !strings.Contains(err.Error(), "panicked: boom") {
		t.Fatalf("got %v", err)
	}
	if err := velox.CheckEntity[fixture.DAuthor](); err != nil {
		t.Fatal(err)
	}
	// CheckEntity reports the same errors, as *SchemaError.
	for name, err := range map[string]error{"bad Descriptor": velox.CheckEntity[badDescriptor](), "panicking Descriptor": velox.CheckEntity[panicDescriptor]()} {
		if _, ok := errors.AsType[*velox.SchemaError](err); !ok || err == nil {
			t.Errorf("CheckEntity of a %s: got %v, want a *SchemaError", name, err)
		}
	}
}
