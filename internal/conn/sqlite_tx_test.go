package conn_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox/internal/conn"
	_ "modernc.org/sqlite"
)

// oneConn is a SQLite database on one connection, so that the next statement
// runs on the connection a transaction used; and its executor.
func oneConn(t *testing.T) (*sql.DB, conn.Executor) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("CREATE TABLE t (id integer PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	return db, conn.SQLiteTimes(conn.NewSQL(db))
}

// canWrite fails t when the pool's connection cannot write.
func canWrite(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec("INSERT INTO t DEFAULT VALUES"); err != nil {
		t.Errorf("the connection cannot write: %v", err)
	}
}

// TestSQLiteReadOnlyRefusesAWrite: query_only holds in the transaction.
func TestSQLiteReadOnlyRefusesAWrite(t *testing.T) {
	db, ex := oneConn(t)
	ctx := context.Background()
	tx, ctl, err := ex.Begin(ctx, sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO t DEFAULT VALUES", nil); err == nil {
		t.Error("a read-only transaction wrote")
	}
	r, err := tx.Query(ctx, "SELECT COUNT(*) FROM t", nil)
	if err != nil {
		t.Fatalf("a read-only transaction cannot read: %v", err)
	}
	r.Close()
	if err := ctl.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	canWrite(t, db)
}

// TestSQLiteReadOnlyRestores: however the transaction ends, the connection it
// goes back to the pool on can write.
func TestSQLiteReadOnlyRestores(t *testing.T) {
	bg := context.Background()
	for name, end := range map[string]func(*testing.T, context.CancelFunc, conn.Tx){
		"commit":   func(t *testing.T, _ context.CancelFunc, c conn.Tx) { must(t, c.Commit(bg)) },
		"rollback": func(t *testing.T, _ context.CancelFunc, c conn.Tx) { must(t, c.Rollback(bg)) },
		"a failed commit": func(t *testing.T, cancel context.CancelFunc, c conn.Tx) {
			cancel()
			if err := c.Commit(bg); err == nil {
				t.Error("a commit on an ended context succeeded")
			}
		},
		"a rollback after the context ended": func(t *testing.T, cancel context.CancelFunc, c conn.Tx) {
			cancel()
			_ = c.Rollback(bg)
		},
		"two rollbacks": func(t *testing.T, _ context.CancelFunc, c conn.Tx) {
			must(t, c.Rollback(bg))
			if err := c.Rollback(bg); !errors.Is(err, sql.ErrTxDone) {
				t.Errorf("the second rollback: %v", err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			db, ex := oneConn(t)
			ctx, cancel := context.WithCancel(bg)
			defer cancel()
			_, ctl, err := ex.Begin(ctx, sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			end(t, cancel, ctl)
			canWrite(t, db)
		})
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Error(err)
	}
}

// TestSQLiteIsolation: SQLite's transactions are serializable, and it has
// nothing else to give.
func TestSQLiteIsolation(t *testing.T) {
	_, ex := oneConn(t)
	ctx := context.Background()
	for _, l := range []sql.IsolationLevel{sql.LevelDefault, sql.LevelSerializable} {
		_, ctl, err := ex.Begin(ctx, sql.TxOptions{Isolation: l})
		if err != nil {
			t.Errorf("%v: %v", l, err)
			continue
		}
		must(t, ctl.Rollback(ctx))
	}
	for _, l := range []sql.IsolationLevel{sql.LevelReadUncommitted, sql.LevelReadCommitted, sql.LevelWriteCommitted,
		sql.LevelRepeatableRead, sql.LevelSnapshot, sql.LevelLinearizable} {
		_, _, err := ex.Begin(ctx, sql.TxOptions{Isolation: l})
		if err == nil || !strings.Contains(err.Error(), l.String()) || !strings.Contains(err.Error(), "SQLite") {
			t.Errorf("%v: %v", l, err)
		}
	}
}
