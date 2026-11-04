package scope_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/scope"
)

// note is a hand-written entity in the shape velox-gen writes, scoped by OrgID.
type note struct {
	ID    int64
	OrgID int64
	Title string
}

var Notes = struct {
	ID    velox.NumberCol[note, int64]
	OrgID velox.NumberCol[note, int64]
	Title velox.StringCol[note, string]
}{
	velox.NumberCol[note, int64]{I: 0}, velox.NumberCol[note, int64]{I: 1}, velox.StringCol[note, string]{I: 2},
}

var org = scope.NewKey[int64]("org")

var noteScope = scope.By(org, Notes.OrgID.EQ, func(x *note) *int64 { return &x.OrgID })

var noteMeta velox.MetaOnce[note]

func (*note) VeloxMeta() *velox.Meta[note] { return noteMeta.Load(noteDescriptor) }

func (*note) Policy() velox.Policy[note] { return noteScope.Policy() }

func (*note) Hooks() []velox.Hook[note] { return []velox.Hook[note]{noteScope.Fill()} }

func noteDescriptor() velox.Descriptor[note] {
	return velox.Descriptor[note]{
		Policy: velox.BindPolicy((*note)(nil).Policy),
		Hooks:  velox.BindHooks((*note)(nil).Hooks),
		Table:  "notes",
		Columns: []velox.ColumnDescriptor{
			{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id", Immutable: true}, {Field: "Title", Name: "title"},
		},
		PrimaryKey: []int{0},
		FieldPtr: func(x *note, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.OrgID
			case 2:
				return &x.Title
			}
			return nil
		},
	}
}

// wrapped is note's shape with a policy of its own, around the scope: only the
// title "ok" is visible, and "banned" cannot be inserted.
type wrapped struct {
	ID    int64
	OrgID int64
	Title string
}

var Wrapped = struct {
	ID    velox.NumberCol[wrapped, int64]
	OrgID velox.NumberCol[wrapped, int64]
	Title velox.StringCol[wrapped, string]
}{
	velox.NumberCol[wrapped, int64]{I: 0}, velox.NumberCol[wrapped, int64]{I: 1}, velox.StringCol[wrapped, string]{I: 2},
}

var wrappedScope = scope.By(org, Wrapped.OrgID.EQ, func(x *wrapped) *int64 { return &x.OrgID })

var wrappedMeta velox.MetaOnce[wrapped]

func (*wrapped) VeloxMeta() *velox.Meta[wrapped] { return wrappedMeta.Load(wrappedDescriptor) }

func (*wrapped) Policy() velox.Policy[wrapped] {
	return wrappedScope.Wrap(velox.Policy[wrapped]{
		Read: func(context.Context) (velox.Pred[wrapped], error) { return Wrapped.Title.EQ("ok"), nil },
		Insert: func(_ context.Context, x *wrapped) error {
			if x.Title == "banned" {
				return errors.New("banned title")
			}
			return nil
		},
	})
}

func (*wrapped) Hooks() []velox.Hook[wrapped] { return []velox.Hook[wrapped]{wrappedScope.Fill()} }

func wrappedDescriptor() velox.Descriptor[wrapped] {
	return velox.Descriptor[wrapped]{
		Policy: velox.BindPolicy((*wrapped)(nil).Policy),
		Hooks:  velox.BindHooks((*wrapped)(nil).Hooks),
		Table:  "notes",
		Columns: []velox.ColumnDescriptor{
			{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id", Immutable: true}, {Field: "Title", Name: "title"},
		},
		PrimaryKey: []int{0},
		FieldPtr: func(x *wrapped, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.OrgID
			case 2:
				return &x.Title
			}
			return nil
		},
	}
}

func openNotes(t *testing.T) *velox.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "n.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("CREATE TABLE notes (id INTEGER PRIMARY KEY, org_id INTEGER NOT NULL, title TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	db, err := velox.Connect("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestKey(t *testing.T) {
	ctx := org.With(context.Background(), 7)
	if v, ok := org.From(ctx); !ok || v != 7 {
		t.Errorf("From = %v, %v", v, ok)
	}
	if _, ok := org.From(org.With(ctx, 0)); ok {
		t.Error("the zero value is a scope value")
	}
	if _, ok := org.From(context.Background()); ok {
		t.Error("an empty context has a scope value")
	}
	other := scope.NewKey[int64]("other")
	if _, ok := other.From(ctx); ok {
		t.Error("another key of the same type read org's value")
	}
}

func TestScopeIsolatesReadsAndWrites(t *testing.T) {
	db := openNotes(t)
	a, b := org.With(context.Background(), 1), org.With(context.Background(), 2)
	n := note{Title: "t"}
	if err := db.Insert(&n).Exec(a); err != nil {
		t.Fatal(err)
	}
	if n.OrgID != 1 {
		t.Fatalf("Fill left OrgID %d, want 1 (in the caller's struct)", n.OrgID)
	}
	if _, err := db.Query[note]().Where(Notes.ID.EQ(n.ID)).One(b); !velox.IsNotFound(err) {
		t.Errorf("read across scopes: %v", err)
	}
	if c, err := db.Update[note]().Set(Notes.Title.Set("x")).Where(Notes.ID.EQ(n.ID)).Exec(b); err != nil || c != 0 {
		t.Errorf("update across scopes: %d, %v", c, err)
	}
	if c, err := db.Delete[note]().Where(Notes.ID.EQ(n.ID)).Exec(b); err != nil || c != 0 {
		t.Errorf("delete across scopes: %d, %v", c, err)
	}
	if c, err := db.Delete[note]().Where(Notes.ID.EQ(n.ID)).Exec(a); err != nil || c != 1 {
		t.Errorf("the owner's delete: %d, %v", c, err)
	}
}

func TestScopeFailsClosed(t *testing.T) {
	db := openNotes(t)
	for name, ctx := range map[string]context.Context{
		"no value":   context.Background(),
		"zero value": org.With(context.Background(), 0),
	} {
		if _, err := db.Query[note]().Count(ctx); !errors.Is(err, scope.ErrNoScope) {
			t.Errorf("%s: read: %v", name, err)
		}
		if err := db.Insert(&note{OrgID: 1, Title: "t"}).Exec(ctx); !errors.Is(err, scope.ErrNoScope) {
			t.Errorf("%s: insert: %v", name, err)
		}
		if _, err := db.Delete[note]().All().Exec(ctx); !errors.Is(err, scope.ErrNoScope) {
			t.Errorf("%s: delete: %v", name, err)
		}
	}
}

func TestScopeInsertMismatchAndImmutable(t *testing.T) {
	db := openNotes(t)
	a := org.With(context.Background(), 1)
	if err := db.Insert(&note{OrgID: 2, Title: "t"}).Exec(a); !errors.Is(err, scope.ErrMismatch) {
		t.Errorf("insert for another scope: %v", err)
	}
	if err := db.Insert(&note{Title: "t"}).Exec(a); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Update[note]().Set(Notes.OrgID.Set(2)).All().Exec(a); !errors.Is(err, velox.ErrImmutable) {
		t.Errorf("moving a row: %v", err)
	}
}

func TestScopeWithoutPolicy(t *testing.T) {
	db := openNotes(t)
	for o := int64(1); o <= 2; o++ {
		if err := db.Insert(&note{Title: "t"}).Exec(org.With(context.Background(), o)); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := db.Query[note]().Count(velox.WithoutPolicy(context.Background(), "audit")); err != nil || n != 2 {
		t.Errorf("WithoutPolicy count = %d, %v; want 2", n, err)
	}
}

func TestWrapAddsToThePoliciesRules(t *testing.T) {
	db := openNotes(t)
	a, b := org.With(context.Background(), 1), org.With(context.Background(), 2)
	if err := db.Insert(&wrapped{Title: "banned"}).Exec(a); err == nil {
		t.Error("the wrapped policy's Insert was skipped")
	}
	for _, title := range []string{"ok", "hidden"} {
		if err := db.Insert(&wrapped{Title: title}).Exec(a); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Insert(&wrapped{Title: "ok"}).Exec(b); err != nil {
		t.Fatal(err)
	}
	if n, err := db.Query[wrapped]().Count(a); err != nil || n != 1 {
		t.Errorf("count = %d, %v; want 1: the wrapped Read (title ok) and the scope must both hold", n, err)
	}
	if n, err := db.Query[wrapped]().Count(b); err != nil || n != 1 {
		t.Errorf("another scope's count = %d, %v; want its one ok row", n, err)
	}
}
