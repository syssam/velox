package untyped_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/untyped"

	_ "modernc.org/sqlite"
)

// TestTableOfNoEntity: the Table of a type that is not an entity holds the
// error that says so, and views no rows; an entity's Table is valid and the
// same each time.
func TestTableOfNoEntity(t *testing.T) {
	type plain struct{ ID int64 }
	if err := untyped.TableOf[plain]().Err(); err == nil || !strings.Contains(err.Error(), "is not an entity") {
		t.Errorf("TableOf of a plain struct: %v", err)
	}
	if untyped.RowsOf[plain](untyped.TableOf[testmodel.Ticket]().NewRows(2)) != nil {
		t.Error("RowsOf of a plain struct: not nil")
	}
	tk := untyped.TableOf[testmodel.Ticket]()
	if tk.Err() != nil || tk.Info() != untyped.TableOf[testmodel.Ticket]().Info() {
		t.Errorf("TableOf of an entity: %v", tk.Err())
	}
	if rows := untyped.RowsOf[testmodel.Ticket](tk.NewRows(2)); len(rows) != 2 {
		t.Errorf("RowsOf: %d rows", len(rows))
	}
}

// TestOrderValueTerm: an ordering by an expression reads its value beside
// each row; one by a column does not.
func TestOrderValueTerm(t *testing.T) {
	if !untyped.OrderValueTerm(untyped.OrderByTerm(untyped.Calc("1"), false)) {
		t.Error("an expression: false")
	}
	if untyped.OrderValueTerm(velox.OrderTerm{}) {
		t.Error("no expression: true")
	}
}

// TestRewrites: a DB reports a Rewrite hook, which raw SQL never passes.
func TestRewrites(t *testing.T) {
	db := velox.Open(velox.SQLite, nil)
	if untyped.Rewrites(db) {
		t.Error("a DB with no hook: true")
	}
	rw := velox.WithRewrite(func(context.Context, *velox.Statement) error { return nil })
	if !untyped.Rewrites(velox.Open(velox.SQLite, nil, rw)) {
		t.Error("a DB with a Rewrite hook: false")
	}
}

// TestSourceDBNone: a row no statement made, and a value that is not a row
// of the Table's entity, have no source DB.
func TestSourceDBNone(t *testing.T) {
	tk := untyped.TableOf[testmodel.Ticket]()
	if db := untyped.SourceDB(tk, &testmodel.Ticket{ID: 1}); db != nil {
		t.Errorf("a hand-built row: %v, want nil", db)
	}
	if db := untyped.SourceDB(tk, "not a row"); db != nil {
		t.Errorf("not a row: %v, want nil", db)
	}
}

// TestOrderValueRoundTrip: a value EncodeOrderValue writes reads back
// through DecodeOrderValue; a value it does not write is an error.
func TestOrderValueRoundTrip(t *testing.T) {
	enc, err := untyped.EncodeOrderValue("abc")
	if err != nil {
		t.Fatal(err)
	}
	if dec, err := untyped.DecodeOrderValue(enc); err != nil || dec != "abc" {
		t.Errorf("round trip of text: %v, %v", dec, err)
	}
	if _, err := untyped.DecodeOrderValue(struct{}{}); err == nil {
		t.Error("decoding a struct: no error")
	}
}

// TestInsertBulkOtherTable: a query of another Table fails the bulk insert.
func TestInsertBulkOtherTable(t *testing.T) {
	db := velox.Open(velox.SQLite, nil)
	tk, inv := untyped.TableOf[testmodel.Ticket](), untyped.TableOf[testmodel.Invoice]()
	q := untyped.Insert(db, inv, &testmodel.Invoice{})
	if _, _, err := untyped.InsertBulk(db, tk, q).SQL(context.Background()); err == nil {
		t.Error("an insert of another Table: no error")
	}
}

// TestSourceDBOfARead: a row a statement read gives the DB it was read on.
func TestSourceDBOfARead(t *testing.T) {
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := raw.Exec(`CREATE TABLE tickets (id INTEGER PRIMARY KEY, created_by TEXT, org_id INTEGER, owner TEXT, title TEXT, done INTEGER, invoice_id INTEGER, parent_id INTEGER);
INSERT INTO tickets VALUES (1, 'u', 1, 'o', 't', 0, 1, NULL)`); err != nil {
		t.Fatal(err)
	}
	db := velox.Open(velox.SQLite, raw)
	tk, err := db.Raw[testmodel.Ticket]("test", `SELECT id, created_by, org_id, owner, title, done, invoice_id, parent_id FROM tickets`).One(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := untyped.SourceDB(untyped.TableOf[testmodel.Ticket](), tk); got == nil {
		t.Error("a row db read: no source DB")
	}
}

// TestQueryDB: a TableQuery gives the DB Query or FromQuery was given; the
// zero TableQuery none.
func TestQueryDB(t *testing.T) {
	db := velox.Open(velox.SQLite, nil)
	if got := untyped.QueryDB(untyped.Query(db, untyped.TableOf[testmodel.Ticket]())); got != db {
		t.Errorf("QueryDB of Query = %p, want %p", got, db)
	}
	if got := untyped.QueryDB(untyped.FromQuery(db.Query[testmodel.Ticket]())); got != db {
		t.Errorf("QueryDB of FromQuery = %p, want %p", got, db)
	}
	if got := untyped.QueryDB(untyped.TableQuery{}); got != nil {
		t.Errorf("QueryDB of the zero TableQuery = %p, want nil", got)
	}
}
