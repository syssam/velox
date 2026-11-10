package apitest

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// useTxDB opens a SQLite database with the invoices table and no rows.
func useTxDB(t *testing.T) *velox.DB {
	t.Helper()
	db := connectInvoices(t, ":memory:")
	if _, err := velox.Pool(db).Exec(`DELETE FROM invoices`); err != nil {
		t.Fatal(err)
	}
	return db
}

func invoiceCount(t *testing.T, db *velox.DB) int64 {
	t.Helper()
	n, err := db.Query[testmodel.Invoice]().Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// beginUse begins a transaction on db's pool and returns it with its UseTx DB.
func beginUse(t *testing.T, db *velox.DB) (*sql.Tx, *velox.DB) {
	t.Helper()
	tx, err := velox.Pool(db).BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	vtx, err := velox.UseTx(db, tx)
	if err != nil {
		t.Fatal(err)
	}
	return tx, vtx
}

// TestUseTxRunsOnTheCallersTransaction: raw SQL and velox statements share
// one transaction, and the caller's Commit or Rollback decides both.
func TestUseTxRunsOnTheCallersTransaction(t *testing.T) {
	for _, commit := range []bool{true, false} {
		db := useTxDB(t)
		tx, vtx := beginUse(t, db)
		if _, err := tx.ExecContext(ctx, `INSERT INTO invoices (id, org_id, number, total, paid, note, data) VALUES (10, 1, 'raw', 0, false, '', x'')`); err != nil {
			t.Fatal(err)
		}
		if err := vtx.Insert(&testmodel.Invoice{OrgID: 1, Number: "velox", Data: []byte{}}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		// vtx neither committed nor rolled back: tx is open and sees both rows.
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM invoices`).Scan(&n); err != nil || n != 2 {
			t.Fatalf("commit=%v: inside tx: %d rows, err %v; want 2", commit, n, err)
		}
		var err error
		if commit {
			err = tx.Commit()
		} else {
			err = tx.Rollback()
		}
		if err != nil {
			t.Fatalf("commit=%v: ending tx: %v", commit, err)
		}
		want := int64(0)
		if commit {
			want = 2
		}
		if got := invoiceCount(t, db); got != want {
			t.Errorf("commit=%v: %d rows after, want %d", commit, got, want)
		}
	}
}

// TestUseTxDBEndsWithTheTx: when the caller ends tx, the DB's statements fail
// with database/sql's error, and nothing reaches the pool.
func TestUseTxDBEndsWithTheTx(t *testing.T) {
	db := useTxDB(t)
	tx, vtx := beginUse(t, db)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	err := vtx.Insert(&testmodel.Invoice{OrgID: 1, Number: "late", Data: []byte{}}).Exec(ctx)
	if !errors.Is(err, sql.ErrTxDone) {
		t.Errorf("a statement after Commit: %v, want sql.ErrTxDone", err)
	}
	if n := invoiceCount(t, db); n != 0 {
		t.Errorf("%d rows: a statement after Commit reached the pool", n)
	}
}

// TestUseTxNestedTxIsASavepoint: a failed nested Tx rolls back to its
// savepoint and leaves the outer work, and the caller's commit keeps it.
func TestUseTxNestedTxIsASavepoint(t *testing.T) {
	db := useTxDB(t)
	tx, vtx := beginUse(t, db)
	insert := func(ctx context.Context, d *velox.DB, num string) error {
		return d.Insert(&testmodel.Invoice{OrgID: 1, Number: num, Data: []byte{}}).Exec(ctx)
	}
	if err := insert(ctx, vtx, "outer"); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	if err := vtx.Tx(ctx, func(ctx context.Context, in *velox.DB) error {
		if err := insert(ctx, in, "inner"); err != nil {
			return err
		}
		return boom
	}); err != boom {
		t.Errorf("nested Tx = %v, want boom", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query[testmodel.Invoice]().Unbounded().All(ctx)
	if err != nil || len(rows) != 1 || rows[0].Number != "outer" {
		t.Errorf("rows %v, err %v; want only the outer row", rows, err)
	}
}

// TestUseTxRefusals: what UseTx will not take is an error that sends nothing.
// The refusals come before tx is touched, so a *sql.Tx that was never begun
// stands in where the pool's only connection is the Tx's.
func TestUseTxRefusals(t *testing.T) {
	db := useTxDB(t)
	never := new(sql.Tx)

	if _, err := velox.UseTx(db, nil); err == nil || !strings.Contains(err.Error(), "tx is nil") {
		t.Errorf("nil tx: %v", err)
	}
	err := db.Tx(ctx, func(ctx context.Context, vtx *velox.DB) error {
		_, err := velox.UseTx(vtx, never)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "in a transaction already") {
		t.Errorf("inside Tx: %v", err)
	}
	// A connection that does not run on a *sql.Tx, as veloxpgx's does not.
	e := &fakeconn.Exec{}
	if _, err := velox.UseTx(openFake(velox.Postgres, e), never); err == nil || !strings.Contains(err.Error(), "does not run on a *sql.Tx") {
		t.Errorf("a connection that is no Adopter: %v", err)
	}
	s := openShards(false)
	if _, err := velox.UseTx(s.db, never); err == nil || !strings.Contains(err.Error(), "sharded") {
		t.Errorf("sharded DB: %v", err)
	}
	for _, x := range []*fakeconn.Exec{e, s.euP, s.euR, s.usP} {
		if len(x.Log) != 0 {
			t.Errorf("a refusal sent %v", x.Log)
		}
	}
}

// TestUseTxWithReplicasRunsOnThePrimary: a DB with replicas runs UseTx's
// statements, reads too, on the caller's tx of the primary, so a read sees the
// tx's own uncommitted write and not a replica's rows.
func TestUseTxWithReplicasRunsOnThePrimary(t *testing.T) {
	primary := connectInvoices(t, filepath.Join(t.TempDir(), "p.db"))
	replica := connectInvoices(t, filepath.Join(t.TempDir(), "r.db")) // three rows
	if _, err := velox.Pool(primary).Exec(`DELETE FROM invoices`); err != nil {
		t.Fatal(err)
	}
	db := velox.Open(velox.SQLite, velox.Pool(primary), velox.WithReplicas(velox.SQLConn(velox.Pool(replica)))).WithoutPolicy("velox tests")
	_, vtx := beginUse(t, db)
	noHang(t, func() {
		if err := vtx.Insert(&testmodel.Invoice{OrgID: 1, Number: "p", Data: []byte{}}).Exec(ctx); err != nil {
			t.Error(err)
			return
		}
		n, err := vtx.Query[testmodel.Invoice]().Count(ctx)
		if err != nil || n != 1 {
			t.Errorf("read %d rows (err %v) through UseTx, want the primary tx's 1, not the replica's 3", n, err)
		}
	})
}
