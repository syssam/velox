package apitest

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// sqliteTable is a file database on Connect's one connection, with a table.
func sqliteTable(t *testing.T) *velox.DB {
	t.Helper()
	db, err := velox.Connect("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(ctx, "test", "CREATE TABLE t (id integer PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	return db
}

// tableRows counts t's rows.
func tableRows(t *testing.T, db *velox.DB) int {
	t.Helper()
	var n int
	if err := velox.Pool(db).QueryRow("SELECT COUNT(*) FROM t").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// insertT inserts id into t.
func insertT(ctx context.Context, db *velox.DB, id string) error {
	_, err := db.Exec(ctx, "test", "INSERT INTO t (id) VALUES ("+id+")")
	return err
}

// TestSQLiteServerRollbackBreaksTheTransaction: a statement can make SQLite roll
// the whole transaction back, which leaves the connection in autocommit: a later
// write would be durable alone and the first lost. Later statements fail unsent,
// and fn's nil returns the failure.
func TestSQLiteServerRollbackBreaksTheTransaction(t *testing.T) {
	const big = "WITH RECURSIVE c(x) AS (SELECT 10 UNION ALL SELECT x + 1 FROM c WHERE x < 100000000) INSERT INTO t SELECT x FROM c"
	for name, fail := range map[string]func(context.Context, *velox.DB) error{
		"INSERT OR ROLLBACK": func(ctx context.Context, tx *velox.DB) error {
			_, err := tx.Exec(ctx, "test", "INSERT OR ROLLBACK INTO t (id) VALUES (1)")
			return err
		},
		"an interrupt": func(ctx context.Context, tx *velox.DB) error {
			sctx, cancel := context.WithTimeout(ctx, 5*time.Millisecond)
			defer cancel()
			_, err := tx.Exec(sctx, "test", big)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			db := sqliteTable(t)
			var failure, later error
			err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
				if err := insertT(ctx, tx, "1"); err != nil {
					return err
				}
				failure = fail(ctx, tx)
				later = insertT(ctx, tx, "2")
				return nil
			})
			if failure == nil {
				t.Fatal("the statement did not fail")
			}
			if !errors.Is(later, failure) || !errors.Is(err, failure) {
				t.Errorf("failure %v; later %v; Tx %v", failure, later, err)
			}
			if n := tableRows(t, db); n != 0 {
				t.Errorf("%d rows survive a transaction that failed", n)
			}
			if err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { return insertT(ctx, tx, "3") }); err != nil {
				t.Errorf("the next transaction: %v", err)
			}
		})
	}
}

// TestSQLiteServerRollbackFnError: fn's own error, after the database rolled the
// transaction back, is returned with no error from the rollback.
func TestSQLiteServerRollbackFnError(t *testing.T) {
	db := sqliteTable(t)
	boom := errors.New("boom")
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_ = insertT(ctx, tx, "1")
		_, _ = tx.Exec(ctx, "test", "INSERT OR ROLLBACK INTO t (id) VALUES (1)")
		return boom
	})
	if !errors.Is(err, boom) || strings.Contains(err.Error(), "rollback") {
		t.Errorf("Tx: %v", err)
	}
	if n := tableRows(t, db); n != 0 {
		t.Errorf("%d rows", n)
	}
}

// TestSQLiteServerRollbackInASavepoint: a nested Tx's statement rolled all of it
// back: the nested Tx fails, and the outer one cannot commit.
func TestSQLiteServerRollbackInASavepoint(t *testing.T) {
	db := sqliteTable(t)
	var nested, after error
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_ = insertT(ctx, tx, "1")
		nested = tx.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			_, err := tx.Exec(ctx, "test", "INSERT OR ROLLBACK INTO t (id) VALUES (1)")
			return err
		})
		after = insertT(ctx, tx, "2")
		return nil
	})
	if nested == nil || after == nil || err == nil {
		t.Errorf("nested %v; after %v; Tx %v", nested, after, err)
	}
	if n := tableRows(t, db); n != 0 {
		t.Errorf("%d rows", n)
	}
}

// TestSQLiteFailureKeepsTheTransaction: most failures roll back one statement. The
// transaction goes on and commits.
func TestSQLiteFailureKeepsTheTransaction(t *testing.T) {
	db := sqliteTable(t)
	var dup error
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		if err := insertT(ctx, tx, "1"); err != nil {
			return err
		}
		dup = insertT(ctx, tx, "1")
		return insertT(ctx, tx, "2")
	})
	if dup == nil || err != nil {
		t.Errorf("the duplicate %v; Tx %v", dup, err)
	}
	if n := tableRows(t, db); n != 2 {
		t.Errorf("%d rows, want 2", n)
	}
}

// TestSQLiteProbe: what a failed statement's probe says, on a fake. "Within a
// transaction" is alive, a transaction already ended needs no mark, and anything
// else counts as gone: a transaction wrongly refused fails, and one wrongly
// trusted loses its writes. Past the deadline nothing is sent: the transaction
// cannot commit, and its statements time out.
func TestSQLiteProbe(t *testing.T) {
	failed := errors.New("failed")
	alive := errors.New("SQL logic error: cannot start a transaction within a transaction (1)")
	for name, c := range map[string]struct {
		probe  error
		broken bool
	}{
		"open":      {alive, false},
		"gone":      {nil, true},
		"no answer": {errors.New("disk I/O error"), true},
		"ended":     {sql.ErrTxDone, false},
	} {
		t.Run(name, func(t *testing.T) {
			e := &fakeconn.Exec{Errs: []error{failed, c.probe}}
			var later error
			err := openFake(velox.SQLite, e).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
				_, _ = tx.Exec(ctx, "test", "UPDATE t SET a = 1")
				_, later = tx.Exec(ctx, "test", "UPDATE t SET a = 2")
				return nil
			})
			if len(e.Log) < 3 || e.Log[2] != "BEGIN" {
				t.Errorf("no probe: %q", e.Log)
			}
			if c.broken != errors.Is(err, failed) || c.broken != errors.Is(later, failed) || c.broken == slices.Contains(e.Log, "COMMIT") {
				t.Errorf("later %v; Tx %v; sent %q", later, err, e.Log)
			}
		})
	}
	t.Run("past the deadline", func(t *testing.T) {
		e := &fakeconn.Exec{Errs: []error{failed}}
		_ = openFake(velox.SQLite, e).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			time.Sleep(2 * time.Millisecond)
			_, _ = tx.Query[testmodel.Invoice]().Limit(1).All(ctx)
			return nil
		}, velox.TxTimeout(time.Millisecond))
		if slices.Contains(e.Log[1:], "BEGIN") {
			t.Errorf("a probe past the deadline: %q", e.Log)
		}
	})
}
