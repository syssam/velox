package scope_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/scope"
)

// note is a hand-written entity in the shape velox-gen writes, scoped by
// org_id as a tenancy mixin scopes it: its Policy and Hooks are the mixin's.
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

// orgPolicy and orgHooks are what a tenancy mixin's methods return.
func orgPolicy[E any]() velox.Policy[E] { return scope.By[E](org, "org_id").Policy() }

func orgHooks[E any]() []velox.Hook[E] { return []velox.Hook[E]{scope.By[E](org, "org_id").Fill()} }

var noteMeta velox.MetaOnce[note]

func (*note) VeloxMeta() *velox.Meta[note] { return noteMeta.Load(noteDescriptor) }

// noteColumns are note's columns; org_id is Immutable.
var noteColumns = []velox.ColumnDescriptor{
	{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id", Immutable: true}, {Field: "Title", Name: "title"},
}

func noteDescriptor() velox.Descriptor[note] {
	return velox.Descriptor[note]{
		Policy:     velox.BindPolicy(orgPolicy[note]),
		Hooks:      velox.BindHooks(orgHooks[note]),
		Table:      "notes",
		Columns:    noteColumns,
		PrimaryKey: []int{0},
		FieldPtr: func(x *note, i int) any {
			return [...]any{&x.ID, &x.OrgID, &x.Title}[i]
		},
	}
}

// ruled is note's shape with a policy of its own after the scope's: only
// the title "ok" is visible, and "banned" cannot be inserted.
type ruled struct {
	ID    int64
	OrgID int64
	Title string
}

var ruledTitle = velox.StringCol[ruled, string]{I: 2}

var ruledMeta velox.MetaOnce[ruled]

func (*ruled) VeloxMeta() *velox.Meta[ruled] { return ruledMeta.Load(ruledDescriptor) }

func (*ruled) Policy() velox.Policy[ruled] {
	return velox.Policy[ruled]{
		Read: func(context.Context) (velox.Pred[ruled], error) { return ruledTitle.EQ("ok"), nil },
		Insert: func(_ context.Context, x *ruled) error {
			if x.Title == "banned" {
				return errors.New("banned title")
			}
			return nil
		},
	}
}

func ruledDescriptor() velox.Descriptor[ruled] {
	return velox.Descriptor[ruled]{
		Policy:     velox.BindPolicy(orgPolicy[ruled], (*ruled)(nil).Policy),
		Hooks:      velox.BindHooks(orgHooks[ruled]),
		Table:      "notes",
		Columns:    noteColumns,
		PrimaryKey: []int{0},
		FieldPtr: func(x *ruled, i int) any {
			return [...]any{&x.ID, &x.OrgID, &x.Title}[i]
		},
	}
}

// wrong is note's shape whose policy names org_id under the column and type
// the test sets.
type wrong struct {
	ID    int64
	OrgID *int64
	Title string
}

var wrongMeta velox.MetaOnce[wrong]

func (*wrong) VeloxMeta() *velox.Meta[wrong] { return wrongMeta.Load(wrongDescriptor) }

// wrongCols are wrong's columns, set by each case of TestByRefuses.
var wrongCols []velox.ColumnDescriptor

func wrongDescriptor() velox.Descriptor[wrong] {
	return velox.Descriptor[wrong]{
		Policy: velox.BindPolicy(func() velox.Policy[wrong] {
			return scope.By[wrong](scope.NewKey[*int64]("org"), "org_id").Policy()
		}),
		Table:      "notes",
		Columns:    wrongCols,
		PrimaryKey: []int{0},
		FieldPtr: func(x *wrong, i int) any {
			return [...]any{&x.ID, &x.OrgID, &x.Title}[i]
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

func TestScopeSQL(t *testing.T) {
	db := openNotes(t)
	q, args, err := db.Query[note]().Where(Notes.Title.EQ("t")).Limit(5).SQL(org.With(context.Background(), 7))
	if err != nil {
		t.Fatal(err)
	}
	if want := `SELECT "id", "org_id", "title" FROM "notes" WHERE "title" = ? AND "org_id" = ? LIMIT ?`; q != want || args[1] != int64(7) {
		t.Errorf("SQL\n got %s %v\nwant %s", q, args, want)
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

// TestScopeANDsWithTheEntitysPolicy: the entity's own policy, bound after
// the scope's, adds to it and cannot lift it.
func TestScopeANDsWithTheEntitysPolicy(t *testing.T) {
	db := openNotes(t)
	a, b := org.With(context.Background(), 1), org.With(context.Background(), 2)
	if err := db.Insert(&ruled{Title: "banned"}).Exec(a); err == nil {
		t.Error("the entity's Insert was skipped")
	}
	if err := db.Insert(&ruled{OrgID: 2, Title: "ok"}).Exec(a); !errors.Is(err, scope.ErrMismatch) {
		t.Errorf("insert for another scope: %v", err)
	}
	for _, title := range []string{"ok", "hidden"} {
		if err := db.Insert(&ruled{Title: title}).Exec(a); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Insert(&ruled{Title: "ok"}).Exec(b); err != nil {
		t.Fatal(err)
	}
	if n, err := db.Query[ruled]().Count(a); err != nil || n != 1 {
		t.Errorf("count = %d, %v; want 1: the entity's Read (title ok) and the scope must both hold", n, err)
	}
	if n, err := db.Query[ruled]().Count(b); err != nil || n != 1 {
		t.Errorf("another scope's count = %d, %v; want its one ok row", n, err)
	}
}

// TestWrapAddsToThePoliciesRules: Wrap, which a Descriptor's Scope uses,
// ANDs the scope with the policy it wraps.
func TestWrapAddsToThePoliciesRules(t *testing.T) {
	db := openNotes(t)
	s := scope.ByHandle(org, Notes.OrgID.EQ, func(x *note) *int64 { return &x.OrgID })
	p := s.Wrap(velox.Policy[note]{
		Read: func(context.Context) (velox.Pred[note], error) { return Notes.Title.EQ("ok"), nil },
		Insert: func(_ context.Context, x *note) error {
			if x.Title == "banned" {
				return errors.New("banned title")
			}
			return nil
		},
	})
	a := org.With(context.Background(), 1)
	if err := p.Insert(a, &note{OrgID: 1, Title: "banned"}); err == nil {
		t.Error("the wrapped Insert was skipped")
	}
	if err := p.Insert(a, &note{OrgID: 1, Title: "ok"}); err != nil {
		t.Error(err)
	}
	cond, err := p.Read(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Insert(&note{Title: "ok"}).Exec(a); err != nil {
		t.Fatal(err)
	}
	if n, err := db.Query[note]().Where(cond).Count(a); err != nil || n != 1 {
		t.Errorf("count = %d, %v", n, err)
	}
	failing := s.Wrap(velox.Policy[note]{Read: func(context.Context) (velox.Pred[note], error) {
		return velox.Pred[note]{}, errors.New("inner")
	}})
	if _, err := failing.Read(a); err == nil || err.Error() != "inner" {
		t.Errorf("the wrapped Read's error: %v", err)
	}
}

// TestByRefuses: By refuses a column that is missing, of another type,
// mutable or nullable, as the entity's *SchemaError at its first use.
func TestByRefuses(t *testing.T) {
	for _, c := range []struct {
		name string
		cols []velox.ColumnDescriptor
		want string
	}{
		{"TestByRefusesMutableColumn", []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id"}, {Field: "Title", Name: "title"}},
			`column "org_id" is mutable`},
		{"TestByRefusesNullableColumn", []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id", Immutable: true}, {Field: "Title", Name: "title"}},
			`column "org_id" is nullable`},
		{"TestByRefusesMissingColumn", []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org", Immutable: true}, {Field: "Title", Name: "title"}},
			`no column is named "org_id"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			wrongCols = c.cols
			wrongMeta = velox.MetaOnce[wrong]{}
			err := velox.CheckEntity[wrong]()
			var se *velox.SchemaError
			if !errors.As(err, &se) || !strings.Contains(err.Error(), c.want) {
				t.Errorf("CheckEntity = %v; want a *SchemaError naming %s", err, c.want)
			}
		})
	}
}

// TestByRefusesAnotherType: a key of another type than the column's field
// is refused at the entity's first use.
func TestByRefusesAnotherType(t *testing.T) {
	err := velox.CheckEntity[typed]()
	var se *velox.SchemaError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "not string") {
		t.Errorf("CheckEntity = %v; want a *SchemaError on the type", err)
	}
}

// typed is note's shape whose scope's key is a string.
type typed struct {
	ID    int64
	OrgID int64
	Title string
}

var typedMeta velox.MetaOnce[typed]

func (*typed) VeloxMeta() *velox.Meta[typed] {
	return typedMeta.Load(func() velox.Descriptor[typed] {
		return velox.Descriptor[typed]{
			Policy: velox.BindPolicy(func() velox.Policy[typed] {
				return scope.By[typed](scope.NewKey[string]("org"), "org_id").Policy()
			}),
			Table:      "notes",
			Columns:    noteColumns,
			PrimaryKey: []int{0},
			FieldPtr: func(x *typed, i int) any {
				return [...]any{&x.ID, &x.OrgID, &x.Title}[i]
			},
		}
	})
}
