package apitest

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/untyped"
)

// zDef has NOT NULL columns whose declared database default is not the
// zero value, of each kind: a decimal rate, a bool, a string and an int;
// seats has a Go Default beside its database default, and quota a Go
// Default only.
type zDef struct {
	ID     int64
	Name   string
	Rate   float64
	Active bool
	Plan   string
	Seats  int64
	Quota  int64
}

var zDefs = struct {
	ID     velox.NumberCol[zDef, int64]
	Name   velox.StringCol[zDef, string]
	Rate   velox.NumberCol[zDef, float64]
	Active velox.Col[zDef, bool]
	Plan   velox.StringCol[zDef, string]
	Seats  velox.NumberCol[zDef, int64]
	Quota  velox.NumberCol[zDef, int64]
}{
	velox.NumberCol[zDef, int64]{I: 0}, velox.StringCol[zDef, string]{I: 1},
	velox.NumberCol[zDef, float64]{I: 2}, velox.Col[zDef, bool]{I: 3},
	velox.StringCol[zDef, string]{I: 4}, velox.NumberCol[zDef, int64]{I: 5},
	velox.NumberCol[zDef, int64]{I: 6},
}

var zDefMeta velox.MetaOnce[zDef]

func (*zDef) VeloxMeta() *velox.Meta[zDef] {
	return zDefMeta.Load(func() velox.Descriptor[zDef] {
		return velox.Descriptor[zDef]{
			Table: "zdefs",
			Columns: []velox.ColumnDescriptor{
				{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"},
				{Field: "Rate", Name: "rate", DBDefault: allDialects, Decimal: [2]uint8{10, 4}},
				{Field: "Active", Name: "active", DBDefault: allDialects},
				{Field: "Plan", Name: "plan", DBDefault: allDialects},
				{Field: "Seats", Name: "seats", DBDefault: allDialects, Default: int64(100)},
				{Field: "Quota", Name: "quota", Default: int64(5)},
			},
			PrimaryKey: []int{0},
			FieldPtr: func(x *zDef, i int) any {
				return [...]any{&x.ID, &x.Name, &x.Rate, &x.Active, &x.Plan, &x.Seats, &x.Quota}[i]
			},
		}
	})
}

func zDB(t *testing.T) (*velox.DB, *sql.DB) {
	t.Helper()
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "z.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	exec(t, raw, `CREATE TABLE zdefs (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE,
		rate NUMERIC NOT NULL DEFAULT 1.5, active BOOLEAN NOT NULL DEFAULT true, plan TEXT NOT NULL DEFAULT 'free',
		seats INTEGER NOT NULL DEFAULT 100, quota INTEGER NOT NULL DEFAULT 5)`)
	return velox.Open(velox.SQLite, raw), raw
}

// zeros are the setters of every column's zero value.
func zeros() []velox.Setter[zDef] {
	return []velox.Setter[zDef]{zDefs.Rate.Set(0), zDefs.Active.Set(false), zDefs.Plan.Set(""), zDefs.Seats.Set(0), zDefs.Quota.Set(0)}
}

// stored reads the row named name as the database holds it.
func zStored(t *testing.T, raw *sql.DB, name string) zDef {
	t.Helper()
	var z zDef
	if err := raw.QueryRow("SELECT id, name, rate, active, plan, seats, quota FROM zdefs WHERE name = ?", name).
		Scan(&z.ID, &z.Name, &z.Rate, &z.Active, &z.Plan, &z.Seats, &z.Quota); err != nil {
		t.Fatal(err)
	}
	return z
}

func zIsZero(z zDef) bool {
	return z.Rate == 0 && !z.Active && z.Plan == "" && z.Seats == 0 && z.Quota == 0
}

func zIsDefault(z zDef) bool {
	return z.Rate == 1.5 && z.Active && z.Plan == "free" && z.Seats == 100 && z.Quota == 5
}

// TestSetterZeroIsStored: a value a setter writes is stored, its zero
// included, over the column's declared database default and over a Go
// Default, as ent's create builder writes every field SetX was called for
// (ADR 0136). A zero the struct holds, which no setter names, is unset and
// gets its default. Both inserts are cached as two shapes.
func TestSetterZeroIsStored(t *testing.T) {
	db, raw := zDB(t)
	const setSQL = `INSERT INTO "zdefs" ("name", "rate", "active", "plan", "seats", "quota") VALUES (?, ?, ?, ?, ?, ?) RETURNING "id"`
	const unsetSQL = `INSERT INTO "zdefs" ("name", "seats", "quota") VALUES (?, ?, ?) RETURNING "id", "rate", "active", "plan"`
	for i, name := range []string{"s1", "s2"} { // the second from the render cache
		x := zDef{Name: name}
		q := db.Insert(&x).Set(zeros()...)
		if got, _, err := q.SQL(ctx); err != nil || got != setSQL {
			t.Errorf("%d: set SQL\n got %s, %v\nwant %s", i, got, err, setSQL)
		}
		if err := q.Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if !zIsZero(x) || !zIsZero(zStored(t, raw, name)) {
			t.Errorf("%d: setters' zeros: entity %+v, stored %+v; want zeros", i, x, zStored(t, raw, name))
		}
		u := zDef{Name: "u" + name}
		uq := db.Insert(&u)
		if got, _, err := uq.SQL(ctx); err != nil || got != unsetSQL {
			t.Errorf("%d: unset SQL\n got %s, %v\nwant %s", i, got, err, unsetSQL)
		}
		if err := uq.Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if !zIsDefault(u) || !zIsDefault(zStored(t, raw, u.Name)) {
			t.Errorf("%d: unset: entity %+v, stored %+v; want the defaults", i, u, zStored(t, raw, u.Name))
		}
	}
}

// TestSetterZeroInsertAll: InsertAll's setters store their zeros in every
// row; an upsert's inserted row stores them too.
func TestSetterZeroInsertAll(t *testing.T) {
	db, raw := zDB(t)
	xs := []zDef{{Name: "m1"}, {Name: "m2", Seats: 9}}
	if err := db.InsertAll(xs).Set(zeros()...).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	for _, x := range xs {
		if !zIsZero(x) || !zIsZero(zStored(t, raw, x.Name)) {
			t.Errorf("InsertAll %s: entity %+v, stored %+v; want zeros", x.Name, x, zStored(t, raw, x.Name))
		}
	}
	up := zDef{Name: "up"}
	if err := db.Insert(&up).Set(zeros()...).OnConflict(zDefs.Name).Update(zDefs.Plan).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if !zIsZero(zStored(t, raw, "up")) {
		t.Errorf("upsert's inserted row: stored %+v; want zeros", zStored(t, raw, "up"))
	}
	ups := []zDef{{Name: "u1"}, {Name: "u2"}}
	if err := db.InsertAll(ups).Set(zeros()...).OnConflict(zDefs.Name).DoNothing().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	for _, x := range ups {
		if !zIsZero(zStored(t, raw, x.Name)) {
			t.Errorf("InsertAll upsert %s: stored %+v; want zeros", x.Name, zStored(t, raw, x.Name))
		}
	}
}

// TestSetterZeroOtherDialects: on PostgreSQL and MySQL a setter's zero is a
// parameter of the INSERT, neither left out nor written as DEFAULT, and is
// not read back.
func TestSetterZeroOtherDialects(t *testing.T) {
	pg := openFake(velox.Postgres, &fakeconn.Exec{})
	x := zDef{Name: "p"}
	got, args, err := pg.Insert(&x).Set(zDefs.Rate.Set(0)).SQL(ctx)
	if want := `INSERT INTO "zdefs" ("name", "rate", "seats", "quota") VALUES ($1, $2, $3, $4) RETURNING "id", "active", "plan"`; err != nil || got != want {
		t.Errorf("PostgreSQL:\n got %s, %v\nwant %s", got, err, want)
	}
	if len(args) != 4 || args[1] != 0.0 {
		t.Errorf("PostgreSQL args %v", args)
	}
	xs := []zDef{{Name: "a", Rate: 2}, {Name: "b"}}
	got, _, err = pg.InsertAll(xs).Set(zDefs.Active.Set(false)).SQL(ctx)
	if want := `INSERT INTO "zdefs" ("name", "rate", "active", "seats", "quota") VALUES ($1, $2, $3, $4, $5), ($6, DEFAULT, $7, $8, $9) RETURNING "id", "rate", "plan"`; err != nil || got != want {
		t.Errorf("PostgreSQL InsertAll:\n got %s, %v\nwant %s", got, err, want)
	}
	my := openFake(velox.MySQL, &fakeconn.Exec{})
	got, _, err = my.Insert(&x).Set(zeros()...).SQL(ctx)
	if want := "INSERT INTO `zdefs` (`name`, `rate`, `active`, `plan`, `seats`, `quota`) VALUES (?, ?, ?, ?, ?, ?)"; err != nil || got != want {
		t.Errorf("MySQL:\n got %s, %v\nwant %s", got, err, want)
	}
}

// TestHookSetZeroIsStored: a hook's Set of an insert's column names it as
// a setter does, so its zero is stored over the declared database default,
// in Insert, InsertAll and the untyped insert (ADR 0136).
func TestHookSetZeroIsStored(t *testing.T) {
	zero := velox.Before(func(_ context.Context, m *velox.Mutation) error { return m.Set("seats", int64(0)) }, velox.OpInsert)
	_, raw := zDB(t)
	db := velox.Open(velox.SQLite, raw, velox.WithHooksAll(zero))
	x := zDef{Name: "h1"}
	q := db.Insert(&x)
	if got, _, err := q.SQL(ctx); err != nil || !strings.Contains(got, `"seats"`) {
		t.Errorf("SQL: %s, %v", got, err)
	}
	if err := q.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	xs := []zDef{{Name: "h2"}, {Name: "h3"}}
	if err := db.InsertAll(xs).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := untyped.Insert(db, untyped.TableOf[zDef](), &zDef{Name: "h4"}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"h1", "h2", "h3", "h4"} {
		if z := zStored(t, raw, name); z.Seats != 0 || z.Rate != 1.5 {
			t.Errorf("%s: stored %+v; want seats 0 and the other defaults", name, z)
		}
	}
}
