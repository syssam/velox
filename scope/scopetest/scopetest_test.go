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
	_ "github.com/syssam/velox/internal/testmodel/runtime"
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
		}, `no column is named "orgid"`},
		{"failed insert", func() error {
			return scopetest.Check(bg, db, testmodel.Org, "org_id", 1, 2, func() *full { return &full{Name: "banned"} })
		}, "an insert under 1"},
		{"no table", func() error { return check[noTable](db) }, "a count under"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := c.check()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Check = %v; want an error naming %q", err, c.want)
			}
		})
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
)

type row struct {
	ID    int64
	OrgID int64
	Name  string
}

func (r *row) set(name string) { r.Name = name }

// describe returns the descriptor of the row entity E, on table, with p and
// h bound when not nil.
func describe[E any](table string, p []func() velox.BoundPolicy, h []func() velox.BoundHooks) velox.Descriptor[E] {
	return velox.Descriptor[E]{
		Table: table,
		Columns: []velox.ColumnDescriptor{
			{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id", Immutable: true}, {Field: "Name", Name: "name"},
		},
		PrimaryKey: []int{0},
		FieldPtr: func(x *E, i int) any {
			r := (*row)(unsafe.Pointer(x))
			return [...]any{&r.ID, &r.OrgID, &r.Name}[i]
		},
		Policy: p,
		Hooks:  h,
	}
}

// openReadPolicy is the scope's policy whose Read lets everything through
// without a value.
func openReadPolicy[E any]() velox.Policy[E] {
	p := fixture.OrgPolicy[E]()
	read := p.Read
	p.Read = func(ctx context.Context) (velox.Pred[E], error) {
		if _, ok := testmodel.Org.From(ctx); !ok {
			return velox.Allow[E](), nil
		}
		return read(ctx)
	}
	return p
}

// noInsertPolicy is the scope's policy whose Insert checks only that the
// context has a value, and not that the row is in it.
func noInsertPolicy[E any]() velox.Policy[E] {
	p := fixture.OrgPolicy[E]()
	p.Insert = func(ctx context.Context, _ *E) error {
		if _, ok := testmodel.Org.From(ctx); !ok {
			return scope.ErrNoScope
		}
		return nil
	}
	return p
}

// insertWithoutValuePolicy is the scope's policy whose Insert lets a row
// through without a value.
func insertWithoutValuePolicy[E any]() velox.Policy[E] {
	p := fixture.OrgPolicy[E]()
	ins := p.Insert
	p.Insert = func(ctx context.Context, x *E) error {
		if _, ok := testmodel.Org.From(ctx); !ok {
			return nil
		}
		return ins(ctx, x)
	}
	return p
}

// banned refuses the name "banned".
func banned(_ context.Context, x *full) error {
	if x.Name == "banned" {
		return velox.Deny("banned")
	}
	return nil
}

var (
	fullMeta               velox.MetaOnce[full]
	unscopedMeta           velox.MetaOnce[unscoped]
	fillOnlyMeta           velox.MetaOnce[fillOnly]
	openReadMeta           velox.MetaOnce[openRead]
	noInsertCheckMeta      velox.MetaOnce[noInsertCheck]
	insertWithoutValueMeta velox.MetaOnce[insertWithoutValue]
	noTableMeta            velox.MetaOnce[noTable]
)

func (*full) VeloxMeta() *velox.Meta[full] {
	return fullMeta.Load(func() velox.Descriptor[full] {
		return describe[full]("rows", velox.BindPolicy(fixture.OrgPolicy[full], func() velox.Policy[full] {
			return velox.Policy[full]{Insert: banned}
		}), velox.BindHooks(fixture.OrgHooks[full]))
	})
}

func (*unscoped) VeloxMeta() *velox.Meta[unscoped] {
	return unscopedMeta.Load(func() velox.Descriptor[unscoped] { return describe[unscoped]("rows", nil, nil) })
}

func (*fillOnly) VeloxMeta() *velox.Meta[fillOnly] {
	return fillOnlyMeta.Load(func() velox.Descriptor[fillOnly] {
		return describe[fillOnly]("rows", nil, velox.BindHooks(fixture.OrgHooks[fillOnly]))
	})
}

func (*openRead) VeloxMeta() *velox.Meta[openRead] {
	return openReadMeta.Load(func() velox.Descriptor[openRead] {
		return describe[openRead]("rows", velox.BindPolicy(openReadPolicy[openRead]), velox.BindHooks(fixture.OrgHooks[openRead]))
	})
}

func (*noInsertCheck) VeloxMeta() *velox.Meta[noInsertCheck] {
	return noInsertCheckMeta.Load(func() velox.Descriptor[noInsertCheck] {
		return describe[noInsertCheck]("rows", velox.BindPolicy(noInsertPolicy[noInsertCheck]), velox.BindHooks(fixture.OrgHooks[noInsertCheck]))
	})
}

func (*insertWithoutValue) VeloxMeta() *velox.Meta[insertWithoutValue] {
	return insertWithoutValueMeta.Load(func() velox.Descriptor[insertWithoutValue] {
		return describe[insertWithoutValue]("rows", velox.BindPolicy(insertWithoutValuePolicy[insertWithoutValue]),
			velox.BindHooks(fixture.OrgHooks[insertWithoutValue]))
	})
}

func (*noTable) VeloxMeta() *velox.Meta[noTable] {
	return noTableMeta.Load(func() velox.Descriptor[noTable] {
		return describe[noTable]("no_such_table", velox.BindPolicy(fixture.OrgPolicy[noTable]), velox.BindHooks(fixture.OrgHooks[noTable]))
	})
}
