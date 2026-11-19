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
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/schema"
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

// keyPolicy and keyHooks are what an InOrg-style tenancy mixin's Policy
// and Hooks return, for key and column.
func keyPolicy[V comparable](key *scope.Key[V], column string) velox.Policy {
	return velox.Policy{
		Read: func(ctx context.Context) (velox.Predicate, error) {
			v, err := key.Require(ctx)
			if err != nil {
				return velox.Predicate{}, err
			}
			return velox.FieldEQ(column, v), nil
		},
		Insert: func(ctx context.Context, r velox.Row) error {
			return key.Match(ctx, r.Value(column))
		},
	}
}

func keyHooks[V comparable](key *scope.Key[V], column string) []velox.Hook {
	return []velox.Hook{velox.Before(func(ctx context.Context, m *velox.Mutation) error {
		if v, ok := key.From(ctx); ok {
			return m.Fill(column, v)
		}
		return nil
	}, velox.OpInsert)}
}

// orgPolicy and orgHooks are the InOrg mixin's methods.
func orgPolicy() velox.Policy { return keyPolicy(org, "org_id") }

func orgHooks() []velox.Hook { return keyHooks(org, "org_id") }

var noteMeta velox.MetaOnce[note]

func (*note) VeloxMeta() *velox.Meta[note] { return noteMeta.Load(noteDescriptor) }

// noteColumns are note's columns; org_id is Immutable.
var noteColumns = []velox.ColumnDescriptor{
	{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id", Immutable: true}, {Field: "Title", Name: "title"},
}

func noteDescriptor() velox.Descriptor[note] {
	return velox.Descriptor[note]{
		Schema: fixture.Fns{Policy: []func() velox.Policy{orgPolicy}, Hooks: []func() []velox.Hook{orgHooks}},

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

var ruledMeta velox.MetaOnce[ruled]

func (*ruled) VeloxMeta() *velox.Meta[ruled] { return ruledMeta.Load(ruledDescriptor) }

func (*ruled) Policy() velox.Policy {
	return velox.Policy{
		Read: func(context.Context) (velox.Predicate, error) { return velox.FieldEQ("title", "ok"), nil },
		Insert: func(_ context.Context, r velox.Row) error {
			if r.Value("title") == "banned" {
				return errors.New("banned title")
			}
			return nil
		},
	}
}

// Mixin lists the behaviour that runs before ruled's own.
func (*ruled) Mixin() []schema.Mixin {
	return fixture.Fns{Policy: []func() velox.Policy{orgPolicy}, Hooks: []func() []velox.Hook{orgHooks}}.Mixin()
}

func ruledDescriptor() velox.Descriptor[ruled] {
	return velox.Descriptor[ruled]{
		Schema:     (*ruled)(nil),
		Table:      "notes",
		Columns:    noteColumns,
		PrimaryKey: []int{0},
		FieldPtr: func(x *ruled, i int) any {
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

// TestKeyRequire: Require returns the context's value, and ErrNoScope,
// naming the key, when it has none or the zero value.
func TestKeyRequire(t *testing.T) {
	if v, err := org.Require(org.With(context.Background(), 7)); err != nil || v != 7 {
		t.Errorf("Require = %v, %v", v, err)
	}
	for name, ctx := range map[string]context.Context{
		"no value":         context.Background(),
		"zero value":       org.With(context.Background(), 0),
		"another key's":    scope.NewKey[int64]("other").With(context.Background(), 7),
		"after zero value": org.With(org.With(context.Background(), 7), 0),
	} {
		v, err := org.Require(ctx)
		if !errors.Is(err, scope.ErrNoScope) || v != 0 {
			t.Errorf("%s: Require = %v, %v; want ErrNoScope", name, v, err)
			continue
		}
		if !strings.HasSuffix(err.Error(), ": org") {
			t.Errorf("%s: %q does not name the key", name, err)
		}
	}
}

// TestKeyMatch: Match is nil for the context's value; ErrNoScope without
// one; ErrMismatch for another value, nil, or a value of another type,
// even one equal as a number.
func TestKeyMatch(t *testing.T) {
	ctx := org.With(context.Background(), 7)
	if err := org.Match(ctx, int64(7)); err != nil {
		t.Errorf("the context's value: %v", err)
	}
	for _, v := range []any{int64(8), int64(0), nil, 7, int32(7), "7"} {
		err := org.Match(ctx, v)
		if !errors.Is(err, scope.ErrMismatch) || errors.Is(err, scope.ErrNoScope) {
			t.Errorf("Match(%#v) = %v; want ErrMismatch", v, err)
		}
	}
	if err := org.Match(ctx, int64(8)); err == nil || !strings.Contains(err.Error(), "org is 8, the context's is 7") {
		t.Errorf("the mismatch does not name the key and both values: %v", err)
	}
	if err := org.Match(context.Background(), int64(7)); !errors.Is(err, scope.ErrNoScope) {
		t.Errorf("without a value: %v", err)
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
	if want := `SELECT "id", "org_id", "title" FROM "notes" WHERE "title" = ? AND "org_id" = ? LIMIT 5`; q != want || args[1] != int64(7) {
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
	if n, err := db.WithoutPolicy("audit").Query[note]().Count(context.Background()); err != nil || n != 2 {
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

// strOrg is a key of another type than note's org_id, an int64.
var strOrg = scope.NewKey[string]("org")

// typed is note's shape whose scope's key is a string; missing is note's
// shape whose scope names a column it does not have.
type typed struct {
	ID    int64
	OrgID int64
	Title string
}

type missing struct {
	ID    int64
	OrgID int64
	Title string
}

var (
	typedMeta   velox.MetaOnce[typed]
	missingMeta velox.MetaOnce[missing]
)

func (*typed) VeloxMeta() *velox.Meta[typed] {
	return typedMeta.Load(func() velox.Descriptor[typed] {
		return velox.Descriptor[typed]{
			Schema: fixture.Fns{Policy: []func() velox.Policy{func() velox.Policy { return keyPolicy(strOrg, "org_id") }}, Hooks: []func() []velox.Hook{func() []velox.Hook { return keyHooks(strOrg, "org_id") }}},

			Table:      "notes",
			Columns:    noteColumns,
			PrimaryKey: []int{0},
			FieldPtr:   func(x *typed, i int) any { return [...]any{&x.ID, &x.OrgID, &x.Title}[i] },
		}
	})
}

func (*missing) VeloxMeta() *velox.Meta[missing] {
	return missingMeta.Load(func() velox.Descriptor[missing] {
		return velox.Descriptor[missing]{
			Schema: fixture.Fns{Policy: []func() velox.Policy{orgPolicy}, Hooks: []func() []velox.Hook{orgHooks}},

			Table:      "notes",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org", Immutable: true}, {Field: "Title", Name: "title"}},
			PrimaryKey: []int{0},
			FieldPtr:   func(x *missing, i int) any { return [...]any{&x.ID, &x.OrgID, &x.Title}[i] },
		}
	})
}

// TestScopeKeyOfAnotherTypeFailsClosed: a key whose type is not the
// column's fails every statement and write, naming both types, though the
// entity's metadata builds: the column is checked where the condition
// runs, and by scopetest.
func TestScopeKeyOfAnotherTypeFailsClosed(t *testing.T) {
	if err := velox.CheckEntity[typed](); err != nil {
		t.Fatal(err)
	}
	db := openNotes(t)
	ctx := strOrg.With(context.Background(), "a")
	if _, err := db.Query[typed]().Count(ctx); err == nil || !strings.Contains(err.Error(), "int64") || errors.Is(err, scope.ErrNoScope) {
		t.Errorf("read: %v; want a failure on the type", err)
	}
	if err := db.Insert(&typed{Title: "t"}).Exec(ctx); err == nil || !strings.Contains(err.Error(), `Fill("org_id", a)`) {
		t.Errorf("insert: %v; want the hook's failure on the type", err)
	}
	if err := db.WithoutPolicy("test").Insert(&typed{Title: "t"}).Exec(ctx); err == nil {
		t.Error("insert without the policy: the hook's misuse did not fail it")
	}
}

// TestScopeMissingColumnFailsClosed: a scope that names a column the
// entity does not have fails its statements and writes, naming the column.
func TestScopeMissingColumnFailsClosed(t *testing.T) {
	db := openNotes(t)
	ctx := org.With(context.Background(), 1)
	if _, err := db.Query[missing]().Count(ctx); err == nil || !strings.Contains(err.Error(), `no column "org_id"`) {
		t.Errorf("read: %v", err)
	}
	if err := db.Insert(&missing{Title: "t"}).Exec(ctx); err == nil || !strings.Contains(err.Error(), `no column "org_id"`) {
		t.Errorf("insert: %v", err)
	}
}
