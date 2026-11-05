package scopetest_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"unsafe"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/scope"

	"github.com/syssam/velox/scope/scopetest"
)

var seq atomic.Int64

// next is a number no other row of the test uses.
func next() int64 { return seq.Add(1) }

// open returns a SQLite DB with the tables of the entities below.
func open(t *testing.T) *velox.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "scopetest.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	for _, s := range append([]string{
		`CREATE TABLE "rows" ("id" INTEGER PRIMARY KEY, "org_id" INTEGER NOT NULL, "name" TEXT NOT NULL)`,
		`CREATE TABLE "invoices" ("created_at" DATETIME NOT NULL, "updated_at" DATETIME NOT NULL, "id" INTEGER PRIMARY KEY,
			"org_id" INTEGER NOT NULL, "number" TEXT NOT NULL, "total" REAL NOT NULL, "paid" BOOLEAN NOT NULL,
			"paid_at" DATETIME, "note" TEXT NOT NULL, "data" BLOB NOT NULL)`,
	}, fixture.SoftDeleteDDL...) {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return velox.Open(velox.SQLite, raw)
}

// TestScopetestIsolates: Check passes an entity a mixin confines, generated
// or written by hand, with soft delete or without, and fails each entity
// that a part of the scope is missing from, naming what is missing.
func TestScopetestIsolates(t *testing.T) {
	db := open(t)
	bg := context.Background()
	scopetest.Isolates(t, bg, db, testmodel.Org, "org_id", 101, 102, func() *testmodel.Invoice {
		return &testmodel.Invoice{Number: fmt.Sprint("N-", next()), Data: []byte("d")}
	})
	scopetest.Isolates(t, bg, db, testmodel.Org, "org_id", 201, 202, func() *fixture.SDoc {
		return &fixture.SDoc{OwnerID: 1, Title: fmt.Sprint("T-", next())}
	})
	scopetest.Isolates(t, bg, db, testmodel.Org, "org_id", 301, 302, newRow[full])

	for _, c := range []struct {
		name  string
		check func() error
		want  string
	}{
		{"unscoped", func() error { return check[unscoped](db) }, "no hook fills it"},
		{"fill only", func() error { return check[fillOnly](db) }, "no policy confines reads"},
		{"no ErrNoScope", func() error { return check[openRead](db) }, "a read without a value"},
		{"no insert check", func() error { return check[noInsertCheck](db) }, "want scope.ErrMismatch"},
		{"insert without a value", func() error { return check[insertWithoutValue](db) }, "an insert without a value"},
		{"wrong column", func() error {
			return scopetest.Check(bg, db, testmodel.Org, "orgid", 1, 2, newRow[full])
		}, `has no column "orgid"`},
		{"key of another type", func() error {
			return scopetest.Check(bg, db, scope.NewKey[string]("org"), "org_id", "a", "b", newRow[full])
		}, `column "org_id" is a int64, not a string`},
		{"failed insert", func() error {
			return scopetest.Check(bg, db, testmodel.Org, "org_id", 1, 2, func() *full { return &full{Name: "banned"} })
		}, "an insert under 1"},
		{"no table", func() error { return check[noTable](db) }, "a count under"},
		{"read hides the insert", func() error { return check[hidden](db) }, "rows after its insert, want"},
		{"delete across scopes", func() error { return check[openWrite](db) }, "a delete under"},
		{"bad descriptor", func() error { return check[badField](db) }, `scopetest: `},
		{"row panics", func() error {
			return scopetest.Check(bg, db, testmodel.Org, "org_id", 1, 2, func() *full { panic("no row") })
		}, "scopetest: no row"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := c.check()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Check = %v; want an error naming %q", err, c.want)
			}
		})
	}
}

// TestIsolatesRefusesMutableColumn: a scope's column that is not Immutable
// fails Isolates, though the mixin's policy and hooks confine the entity:
// an update could move a row to another scope.
func TestIsolatesRefusesMutableColumn(t *testing.T) {
	db := open(t)
	rec := &recorder{TB: t}
	scopetest.Isolates(rec, context.Background(), db, testmodel.Org, "org_id", 1, 2, newRow[mutable])
	if want := `scopetest: scopetest_test.mutable: column "org_id" is mutable: make it Immutable`; !strings.Contains(rec.msg, want) {
		t.Errorf("Isolates failed with %q, want %q", rec.msg, want)
	}
}

// TestIsolatesRefusesNullableColumn: a scope's column that is nullable
// fails Isolates: a row whose column is NULL is in no scope.
func TestIsolatesRefusesNullableColumn(t *testing.T) {
	db := open(t)
	rec := &recorder{TB: t}
	scopetest.Isolates(rec, context.Background(), db, testmodel.Org, "org_id", 1, 2, func() *nullable {
		return &nullable{Name: fmt.Sprint("r-", next())}
	})
	if want := `scopetest: scopetest_test.nullable: column "org_id" is nullable`; !strings.Contains(rec.msg, want) {
		t.Errorf("Isolates failed with %q, want %q", rec.msg, want)
	}
}

// TestIsolatesFails: Isolates fails its test with Check's error.
func TestIsolatesFails(t *testing.T) {
	db := open(t)
	rec := &recorder{TB: t}
	scopetest.Isolates(rec, context.Background(), db, testmodel.Org, "org_id", 1, 2, newRow[unscoped])
	if !strings.Contains(rec.msg, "no hook fills it") {
		t.Errorf("Isolates failed with %q", rec.msg)
	}
}

// recorder is a testing.TB whose Fatal records its message.
type recorder struct {
	testing.TB
	msg string
}

func (r *recorder) Helper() {}

func (r *recorder) Fatal(args ...any) { r.msg = fmt.Sprint(args...) }

// check runs Check on the row entity E with fresh values.
func check[E any, PE interface {
	*E
	velox.Entity[E]
	set(name string)
}](db *velox.DB) error {
	a, b := next()+1000, next()+1000
	return scopetest.Check(context.Background(), db, testmodel.Org, "org_id", a, b, newRow[E, PE])
}

// newRow returns a new row of E with a unique name.
func newRow[E any, PE interface {
	*E
	set(name string)
}]() *E {
	x := PE(new(E))
	x.set(fmt.Sprint("r-", next()))
	return (*E)(x)
}

// The row entities share the table rows and the shape id, org_id, name.
// Each binds another part of the scope.
type (
	full               struct{ row }
	unscoped           struct{ row }
	fillOnly           struct{ row }
	openRead           struct{ row }
	noInsertCheck      struct{ row }
	insertWithoutValue struct{ row }
	noTable            struct{ row }
	mutable            struct{ row }
	hidden             struct{ row }
	openWrite          struct{ row }
	badField           struct{ row }
)

type row struct {
	ID    int64
	OrgID int64
	Name  string
}

func (r *row) set(name string) { r.Name = name }

// rowColumns are the row entities' columns; org_id is Immutable.
var rowColumns = []velox.ColumnDescriptor{
	{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id", Immutable: true}, {Field: "Name", Name: "name"},
}

// describe returns the descriptor of the row entity E, on table, with p and
// h bound when not nil.
func describe[E any](table string, p []func() velox.Policy, h []func() []velox.Hook) velox.Descriptor[E] {
	return velox.Descriptor[E]{
		Table:      table,
		Columns:    rowColumns,
		PrimaryKey: []int{0},
		FieldPtr: func(x *E, i int) any {
			r := (*row)(unsafe.Pointer(x))
			return [...]any{&r.ID, &r.OrgID, &r.Name}[i]
		},
		Policy: p,
		Hooks:  h,
	}
}

// orgPolicy and orgHooks list the scope's policy and hooks, as velox-gen
// lists a mixin's.
var (
	orgPolicy = []func() velox.Policy{fixture.OrgPolicy}
	orgHooks  = []func() []velox.Hook{fixture.OrgHooks}
)

// openReadPolicy is the scope's policy whose Read lets everything through
// without a value.
func openReadPolicy() velox.Policy {
	p := fixture.OrgPolicy()
	read := p.Read
	p.Read = func(ctx context.Context) (velox.Cond, error) {
		if _, ok := testmodel.Org.From(ctx); !ok {
			return velox.Allow(), nil
		}
		return read(ctx)
	}
	return p
}

// noInsertPolicy is the scope's policy whose Insert checks only that the
// context has a value, and not that the row is in it.
func noInsertPolicy() velox.Policy {
	p := fixture.OrgPolicy()
	p.Insert = func(ctx context.Context, _ velox.Row) error {
		if _, ok := testmodel.Org.From(ctx); !ok {
			return scope.ErrNoScope
		}
		return nil
	}
	return p
}

// insertWithoutValuePolicy is the scope's policy whose Insert lets a row
// through without a value.
func insertWithoutValuePolicy() velox.Policy {
	p := fixture.OrgPolicy()
	ins := p.Insert
	p.Insert = func(ctx context.Context, r velox.Row) error {
		if _, ok := testmodel.Org.From(ctx); !ok {
			return nil
		}
		return ins(ctx, r)
	}
	return p
}

// banned refuses the name "banned".
func banned() velox.Policy {
	return velox.Policy{Insert: func(_ context.Context, r velox.Row) error {
		if r.Value("name") == "banned" {
			return velox.Deny("banned")
		}
		return nil
	}}
}

var (
	fullMeta               velox.MetaOnce[full]
	unscopedMeta           velox.MetaOnce[unscoped]
	fillOnlyMeta           velox.MetaOnce[fillOnly]
	openReadMeta           velox.MetaOnce[openRead]
	noInsertCheckMeta      velox.MetaOnce[noInsertCheck]
	insertWithoutValueMeta velox.MetaOnce[insertWithoutValue]
	noTableMeta            velox.MetaOnce[noTable]
	mutableMeta            velox.MetaOnce[mutable]
	nullableMeta           velox.MetaOnce[nullable]
	hiddenMeta             velox.MetaOnce[hidden]
	openWriteMeta          velox.MetaOnce[openWrite]
	badFieldMeta           velox.MetaOnce[badField]
)

// hiddenPolicy is the scope's policy whose Read hides every row, so that
// a's insert is not among a's rows.
func hiddenPolicy() velox.Policy {
	p := fixture.OrgPolicy()
	read := p.Read
	p.Read = func(ctx context.Context) (velox.Cond, error) {
		if _, err := read(ctx); err != nil {
			return velox.Cond{}, err
		}
		return velox.C("org_id").EQ(int64(-1)), nil
	}
	return p
}

// openWriteHooks are the scope's hooks and one that claims a delete
// affected a row, as a hook that deletes across scopes would.
func openWriteHooks() []velox.Hook {
	return append(fixture.OrgHooks(), velox.On(func(velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(_ context.Context, m *velox.Mutation) error {
			m.SetAffected(1)
			return nil
		})
	}, velox.OpDelete))
}

func (*hidden) VeloxMeta() *velox.Meta[hidden] {
	return hiddenMeta.Load(func() velox.Descriptor[hidden] {
		return describe[hidden]("rows", []func() velox.Policy{hiddenPolicy}, orgHooks)
	})
}

func (*openWrite) VeloxMeta() *velox.Meta[openWrite] {
	return openWriteMeta.Load(func() velox.Descriptor[openWrite] {
		return describe[openWrite]("rows", orgPolicy, []func() []velox.Hook{openWriteHooks})
	})
}

// badField's descriptor has no primary key, so its metadata fails.
func (*badField) VeloxMeta() *velox.Meta[badField] {
	return badFieldMeta.Load(func() velox.Descriptor[badField] {
		d := describe[badField]("rows", orgPolicy, orgHooks)
		d.PrimaryKey = nil
		return d
	})
}

func (*full) VeloxMeta() *velox.Meta[full] {
	return fullMeta.Load(func() velox.Descriptor[full] {
		return describe[full]("rows", []func() velox.Policy{fixture.OrgPolicy, banned}, orgHooks)
	})
}

func (*unscoped) VeloxMeta() *velox.Meta[unscoped] {
	return unscopedMeta.Load(func() velox.Descriptor[unscoped] { return describe[unscoped]("rows", nil, nil) })
}

func (*fillOnly) VeloxMeta() *velox.Meta[fillOnly] {
	return fillOnlyMeta.Load(func() velox.Descriptor[fillOnly] {
		return describe[fillOnly]("rows", nil, orgHooks)
	})
}

func (*openRead) VeloxMeta() *velox.Meta[openRead] {
	return openReadMeta.Load(func() velox.Descriptor[openRead] {
		return describe[openRead]("rows", []func() velox.Policy{openReadPolicy}, orgHooks)
	})
}

func (*noInsertCheck) VeloxMeta() *velox.Meta[noInsertCheck] {
	return noInsertCheckMeta.Load(func() velox.Descriptor[noInsertCheck] {
		return describe[noInsertCheck]("rows", []func() velox.Policy{noInsertPolicy}, orgHooks)
	})
}

func (*insertWithoutValue) VeloxMeta() *velox.Meta[insertWithoutValue] {
	return insertWithoutValueMeta.Load(func() velox.Descriptor[insertWithoutValue] {
		return describe[insertWithoutValue]("rows", []func() velox.Policy{insertWithoutValuePolicy}, orgHooks)
	})
}

func (*noTable) VeloxMeta() *velox.Meta[noTable] {
	return noTableMeta.Load(func() velox.Descriptor[noTable] {
		return describe[noTable]("no_such_table", orgPolicy, orgHooks)
	})
}

// mutable is confined by the scope, but its org_id is not Immutable.
func (*mutable) VeloxMeta() *velox.Meta[mutable] {
	return mutableMeta.Load(func() velox.Descriptor[mutable] {
		d := describe[mutable]("rows", orgPolicy, orgHooks)
		d.Columns = []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id"}, {Field: "Name", Name: "name"}}
		return d
	})
}

// nullable is confined by the scope, but its org_id, Immutable, is
// nullable.
type nullable struct {
	ID    int64
	OrgID *int64
	Name  string
}

func (*nullable) VeloxMeta() *velox.Meta[nullable] {
	return nullableMeta.Load(func() velox.Descriptor[nullable] {
		return velox.Descriptor[nullable]{
			Table:      "rows",
			Columns:    rowColumns,
			PrimaryKey: []int{0},
			FieldPtr:   func(x *nullable, i int) any { return [...]any{&x.ID, &x.OrgID, &x.Name}[i] },
			Policy:     orgPolicy,
			Hooks:      orgHooks,
		}
	})
}
