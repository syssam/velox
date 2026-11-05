package apitest

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
)

// orgMixin is behaviour written once for any entity with an org_id column
// and a deleted_at column, as a mixin writes it: it cannot name the
// generated handles, so it names the columns.
type orgMixin struct{}

func (orgMixin) Policy() velox.Policy {
	return velox.Policy{Read: func(context.Context) (velox.Predicate, error) { return velox.FieldEQ("org_id", 7), nil }}
}

func (orgMixin) Hooks() []velox.Hook {
	return []velox.Hook{velox.Before(func(_ context.Context, m *velox.Mutation) error {
		return m.Fill("org_id", 7)
	}, velox.OpInsert)}
}

func (orgMixin) Interceptors() []velox.Interceptor {
	return []velox.Interceptor{{Filter: func(context.Context) (velox.Predicate, error) { return velox.FieldIsNull("deleted_at"), nil }}}
}

// fNote is an entity whose behaviour is orgMixin's.
type fNote struct {
	ID        int64
	OrgID     int64
	Title     string
	DeletedAt *time.Time
}

var fNoteMeta velox.MetaOnce[fNote]

func (*fNote) VeloxMeta() *velox.Meta[fNote] {
	return fNoteMeta.Load(func() velox.Descriptor[fNote] {
		return velox.Descriptor[fNote]{
			Table: "notes",
			Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id", Immutable: true},
				{Field: "Title", Name: "title"}, {Field: "DeletedAt", Name: "deleted_at"}},
			PrimaryKey: []int{0},
			Schema:     fixture.Fns{Policy: []func() velox.Policy{orgMixin{}.Policy}, Hooks: []func() []velox.Hook{orgMixin{}.Hooks}, Interceptors: []func() []velox.Interceptor{orgMixin{}.Interceptors}},

			FieldPtr: func(x *fNote, i int) any { return [...]any{&x.ID, &x.OrgID, &x.Title, &x.DeletedAt}[i] },
		}
	})
}

func fakeNotes() (*velox.DB, *fakeconn.Exec) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(1)}}, Affected: 1, Answer: func(sql string, _ []any) ([][]any, bool) {
		return nil, strings.HasPrefix(sql, "SELECT")
	}}
	return openFake(velox.Postgres, e), e
}

// TestMixinByName: a mixin's Policy, Hooks and Interceptors name their
// columns, which each statement resolves against the entity: the
// conditions reach the statements and the hook fills the row.
func TestMixinByName(t *testing.T) {
	if err := velox.CheckEntity[fNote](); err != nil {
		t.Fatal(err)
	}
	db, e := fakeNotes()
	ctx := context.Background()
	if _, err := db.Query[fNote]().Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `SELECT "id", "org_id", "title", "deleted_at" FROM "notes" WHERE "deleted_at" IS NULL AND "org_id" = $1 LIMIT $2`; e.SQL != want {
		t.Errorf("SQL\n%s\nwant\n%s", e.SQL, want)
	}
	if want := "[7 1]"; fmt.Sprint(e.Args) != want {
		t.Errorf("args %v, want %v", e.Args, want)
	}
	n := &fNote{ID: 1, Title: "a"}
	if err := db.Insert(n).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if n.OrgID != 7 || !strings.Contains(fmt.Sprint(e.Args), "7") {
		t.Errorf("the hook did not fill org_id: %+v, args %v", n, e.Args)
	}
}

// fNoteWhere returns the SQL of a query of fNote under cs, through the
// untyped surface.
func fNoteWhere(db *velox.DB, cs ...velox.Predicate) (string, []any, error) {
	return db.QueryTable(velox.TableOf[fNote]()).Where(cs...).Limit(1).SQL(context.Background())
}

// TestCondNullable: IsNull and NotNull on a nullable column are IS NULL and
// IS NOT NULL, and EQ of a time compares the value, passed by value.
func TestCondNullable(t *testing.T) {
	db, _ := fakeNotes()
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	q, args, err := fNoteWhere(db, velox.FieldNotNull("deleted_at"), velox.FieldEQ("deleted_at", ts), velox.FieldIn("title", "a", "b"))
	if err != nil {
		t.Fatal(err)
	}
	if want := `SELECT "id", "org_id", "title", "deleted_at" FROM "notes" WHERE "deleted_at" IS NOT NULL AND "deleted_at" = $1 AND "title" IN ($2, $3) AND "deleted_at" IS NULL AND "org_id" = $4 LIMIT $5`; q != want {
		t.Errorf("SQL\n%s\nwant\n%s", q, want)
	}
	if _, ok := args[0].(time.Time); !ok {
		t.Errorf("args %#v: the time is not passed by value", args)
	}
	if q, _, err := fNoteWhere(db, velox.FieldIsNull("deleted_at")); err != nil || !strings.Contains(q, `WHERE "deleted_at" IS NULL AND "deleted_at" IS NULL`) {
		t.Errorf("IsNull: %s, %v", q, err)
	}
}

// TestCondInvalid: EQ or In of nil, and IsNull or NotNull on a column that
// cannot hold NULL, each fail the statement.
func TestCondInvalid(t *testing.T) {
	db, e := fakeNotes()
	ts := time.Now()
	for name, c := range map[string]velox.Predicate{
		"EQ(nil)":       velox.FieldEQ("deleted_at", nil),
		"In(ts, nil)":   velox.FieldIn("deleted_at", ts, nil),
		"IsNull":        velox.FieldIsNull("org_id"),
		"NotNull":       velox.FieldNotNull("org_id"),
		"the zero Cond": {},
	} {
		if q, _, err := fNoteWhere(db, c); err == nil {
			t.Errorf("%s: no error, %s", name, q)
		}
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}
