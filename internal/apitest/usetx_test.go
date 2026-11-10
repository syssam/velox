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

// useTxDB opens a SQLite database with the invoices table and no rows, and
// returns it with its *sql.DB's pool.
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

// TestUseTxRunsOnTheCallersTransaction: raw SQL and velox statements share
// one transaction, and the caller's Commit or Rollback decides both.
func TestUseTxRunsOnTheCallersTransaction(t *testing.T) {
	for _, commit := range []bool{true, false} {
		db := useTxDB(t)
		sqlDB := velox.Pool(db)
		tx, err := sqlDB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		err = db.UseTx(ctx, tx, func(ctx context.Context, vtx *velox.DB) error {
			if _, err := tx.ExecContext(ctx, `INSERT INTO invoices (id, org_id, number, total, paid, note, data) VALUES (10, 1, 'raw', 0, false, '', x'')`); err != nil {
				return err
			}
			return vtx.Insert(&testmodel.Invoice{OrgID: 1, Number: "velox", Data: []byte{}}).Exec(ctx)
		})
		if err != nil {
			t.Fatalf("commit=%v: UseTx: %v", commit, err)
		}
		// UseTx neither committed nor rolled back: tx is still open and sees both rows.
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM invoices`).Scan(&n); err != nil || n != 2 {
			t.Fatalf("commit=%v: inside tx: %d rows, err %v; want 2", commit, n, err)
		}
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

// TestUseTxReturnsFnsError: the error comes back as fn returned it, and the
// transaction is still the caller's to end.
func TestUseTxReturnsFnsError(t *testing.T) {
	db := useTxDB(t)
	tx, err := velox.Pool(db).BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	got := db.UseTx(ctx, tx, func(ctx context.Context, vtx *velox.DB) error {
		if err := vtx.Insert(&testmodel.Invoice{OrgID: 1, Number: "x", Data: []byte{}}).Exec(ctx); err != nil {
			return err
		}
		return boom
	})
	if got != boom {
		t.Fatalf("UseTx = %v, want boom as is", got)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("the caller's Rollback after fn's error: %v", err)
	}
	if n := invoiceCount(t, db); n != 0 {
		t.Errorf("%d rows after the caller's rollback", n)
	}
}

// TestUseTxNestedTxIsASavepoint: a failed nested Tx rolls back to its
// savepoint and leaves the outer work, and the caller's commit keeps it.
func TestUseTxNestedTxIsASavepoint(t *testing.T) {
	db := useTxDB(t)
	tx, err := velox.Pool(db).BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	insert := func(ctx context.Context, d *velox.DB, num string) error {
		return d.Insert(&testmodel.Invoice{OrgID: 1, Number: num, Data: []byte{}}).Exec(ctx)
	}
	boom := errors.New("boom")
	err = db.UseTx(ctx, tx, func(ctx context.Context, vtx *velox.DB) error {
		if err := insert(ctx, vtx, "outer"); err != nil {
			return err
		}
		if err := vtx.Tx(ctx, func(ctx context.Context, in *velox.DB) error {
			if err := insert(ctx, in, "inner"); err != nil {
				return err
			}
			return boom
		}); err != boom {
			t.Errorf("nested Tx = %v, want boom", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query[testmodel.Invoice]().Unbounded().All(ctx)
	if err != nil || len(rows) != 1 || rows[0].Number != "outer" {
		t.Errorf("rows %v, err %v; want only the outer row", rows, err)
	}
}

// TestUseTxDBEndsWithFn: the DB fn gets is the transaction's only inside fn.
func TestUseTxDBEndsWithFn(t *testing.T) {
	db := useTxDB(t)
	tx, err := velox.Pool(db).BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	var kept *velox.DB
	var keptCtx context.Context
	if err := db.UseTx(ctx, tx, func(ctx context.Context, vtx *velox.DB) error {
		kept, keptCtx = vtx, ctx
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	err = kept.Insert(&testmodel.Invoice{OrgID: 1, Number: "late", Data: []byte{}}).Exec(keptCtx)
	if err == nil {
		t.Error("a statement on the DB after fn returned ran")
	}
}

// TestUseTxRefusals: what UseTx will not take is an error that sends nothing.
// The refusals come before tx is touched, so a *sql.Tx that was never begun
// stands in where the pool's only connection is the Tx's.
func TestUseTxRefusals(t *testing.T) {
	db := useTxDB(t)
	other := useTxDB(t)
	never := new(sql.Tx)
	noop := func(context.Context, *velox.DB) error { t.Error("fn ran"); return nil }

	if err := db.UseTx(ctx, nil, noop); err == nil || !strings.Contains(err.Error(), "tx is nil") {
		t.Errorf("nil tx: %v", err)
	}
	err := db.Tx(ctx, func(ctx context.Context, vtx *velox.DB) error {
		return vtx.UseTx(ctx, never, noop)
	})
	if err == nil || !strings.Contains(err.Error(), "in a transaction already") {
		t.Errorf("inside Tx: %v", err)
	}
	// A connection that does not run on a *sql.Tx, as veloxpgx's does not.
	fake := openFake(velox.Postgres, &fakeconn.Exec{})
	if err := fake.UseTx(ctx, never, noop); err == nil || !strings.Contains(err.Error(), "does not run on a *sql.Tx") {
		t.Errorf("a connection that is no Adopter: %v", err)
	}
	err = db.Tx(ctx, func(ctx context.Context, _ *velox.DB) error {
		return other.UseTx(ctx, never, noop)
	})
	if !errors.Is(err, velox.ErrOutsideTx) {
		t.Errorf("under another transaction's context: %v", err)
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
	tx, err := velox.Pool(primary).BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	noHang(t, func() {
		err = db.UseTx(ctx, tx, func(ctx context.Context, vtx *velox.DB) error {
			if err := vtx.Insert(&testmodel.Invoice{OrgID: 1, Number: "p", Data: []byte{}}).Exec(ctx); err != nil {
				return err
			}
			n, err := vtx.Query[testmodel.Invoice]().Count(ctx)
			if err == nil && n != 1 {
				t.Errorf("read %d rows inside UseTx, want the primary tx's 1, not the replica's 3", n)
			}
			return err
		})
		if err != nil {
			t.Error(err)
		}
	})
}
