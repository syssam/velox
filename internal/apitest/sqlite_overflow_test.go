package apitest

import (
	"database/sql"
	"math"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

var Its = testmodel.Items

// TestSQLiteArithmeticOverflowFails: SQLite makes an integer that overflows a REAL,
// and stores it, and every later read of the row would fail Scan. The update fails,
// and leaves the row as it was.
func TestSQLiteArithmeticOverflowFails(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	raw.SetMaxOpenConns(1)
	if _, err := raw.Exec(`CREATE TABLE items (id integer PRIMARY KEY, invoice_id integer NOT NULL, sku text NOT NULL, qty integer NOT NULL);
		INSERT INTO items VALUES (1, 1, 'top', 9223372036854775807), (2, 1, 'bottom', -9223372036854775808), (3, 1, 'mid', 5)`); err != nil {
		t.Fatal(err)
	}
	db := velox.Open(velox.SQLite, raw)
	for name, c := range map[string]struct {
		id  int64
		set velox.Setter[testmodel.Item]
	}{"Add at the top": {1, Its.Qty.Add(1)}, "Sub at the bottom": {2, Its.Qty.Sub(1)}, "Add a negative at the bottom": {2, Its.Qty.Add(-1)}, "Add the largest": {3, Its.Qty.Add(math.MaxInt64)}} {
		t.Run(name, func(t *testing.T) {
			before, err := db.Query[testmodel.Item]().Where(Its.ID.EQ(c.id)).One(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.Update[testmodel.Item]().Set(c.set).Where(Its.ID.EQ(c.id)).Exec(ctx)
			if err == nil || !strings.Contains(err.Error(), "integer overflow") {
				t.Errorf("the update: %v", err)
			}
			after, err := db.Query[testmodel.Item]().Where(Its.ID.EQ(c.id)).One(ctx)
			if err != nil {
				t.Fatalf("the row no longer reads: %v", err)
			}
			if after.Qty != before.Qty {
				t.Errorf("qty %d, was %d", after.Qty, before.Qty)
			}
		})
	}
	if _, err := db.Update[testmodel.Item]().Set(Its.Qty.Add(math.MaxInt64 - 5)).Where(Its.ID.EQ(3)).Exec(ctx); err != nil {
		t.Errorf("up to the largest: %v", err)
	}
	if got, err := db.Query[testmodel.Item]().Where(Its.ID.EQ(3)).One(ctx); err != nil || got.Qty != math.MaxInt64 {
		t.Errorf("%v, %v", got, err)
	}
}

// TestSQLiteArithmeticRendering: the guard is SQLite's and an integer column's. A
// float cannot overflow to a REAL, and the other dialects raise by themselves.
func TestSQLiteArithmeticRendering(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	const guarded = `UPDATE "items" SET "qty" = CASE WHEN typeof("qty" + ?) = 'real' THEN abs(-9223372036854775808) ELSE "qty" + ? END WHERE "id" = ?`
	if _, err := openFake(velox.SQLite, e).Update[testmodel.Item]().Set(Its.Qty.Add(2)).Where(Its.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, guarded, int64(2), int64(2), int64(1))
	if _, err := openFake(velox.Postgres, e).Update[testmodel.Item]().Set(Its.Qty.Sub(2)).Where(Its.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `UPDATE "items" SET "qty" = "qty" - $1 WHERE "id" = $2`, int64(2), int64(1))
	if _, err := openFake(velox.SQLite, e).Update[testmodel.Invoice]().Set(I.Total.Add(2)).Where(I.ID.EQ(1)).Exec(velox.WithoutPolicy(ctx, "test")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.SQL, `"total" = "total" + ?`) {
		t.Errorf("a float: %s", e.SQL)
	}
}
