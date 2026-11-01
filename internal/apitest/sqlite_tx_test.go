package apitest

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/syssam/velox"
)

func sqliteOneConn(t *testing.T) (*velox.DB, *sql.DB) {
	t.Helper()
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	raw.SetMaxOpenConns(1)
	if _, err := raw.Exec("CREATE TABLE t (id integer PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	return velox.Open(velox.SQLite, raw), raw
}

// TestSQLiteTxReadOnly: an insert in TxReadOnly fails, and however fn ends the
// connection writes again.
func TestSQLiteTxReadOnly(t *testing.T) {
	insert := func(ctx context.Context, tx *velox.DB) error {
		_, err := tx.Exec(ctx, "test", "INSERT INTO t DEFAULT VALUES")
		return err
	}
	after := func(t *testing.T, db *velox.DB, raw *sql.DB, committed int) {
		t.Helper()
		var n int
		if err := raw.QueryRow("SELECT COUNT(*) FROM t").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != committed {
			t.Errorf("%d rows, want %d", n, committed)
		}
		if err := db.Tx(ctx, insert); err != nil {
			t.Errorf("the next transaction cannot write: %v", err)
		}
	}
	t.Run("a write fails and fn's error rolls back", func(t *testing.T) {
		db, raw := sqliteOneConn(t)
		err := db.Tx(ctx, insert, velox.TxReadOnly())
		if err == nil || !strings.Contains(err.Error(), "readonly") {
			t.Errorf("%v", err)
		}
		after(t, db, raw, 0)
	})
	t.Run("a swallowed write fails and fn's nil commits nothing", func(t *testing.T) {
		db, raw := sqliteOneConn(t)
		err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			if insert(ctx, tx) == nil {
				t.Error("the insert ran")
			}
			return nil
		}, velox.TxReadOnly())
		if err != nil {
			t.Error(err)
		}
		after(t, db, raw, 0)
	})
	t.Run("a panic", func(t *testing.T) {
		db, raw := sqliteOneConn(t)
		func() {
			defer func() { _ = recover() }()
			_ = db.Tx(ctx, func(context.Context, *velox.DB) error { panic("boom") }, velox.TxReadOnly())
		}()
		after(t, db, raw, 0)
	})
	t.Run("a canceled context", func(t *testing.T) {
		db, raw := sqliteOneConn(t)
		cctx, cancel := context.WithCancel(ctx)
		defer cancel()
		err := db.Tx(cctx, func(context.Context, *velox.DB) error { cancel(); return nil }, velox.TxReadOnly())
		if err == nil {
			t.Error("a canceled transaction committed")
		}
		after(t, db, raw, 0)
	})
	t.Run("savepoints and reads work", func(t *testing.T) {
		db, raw := sqliteOneConn(t)
		err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			return tx.Tx(ctx, func(context.Context, *velox.DB) error { return nil })
		}, velox.TxReadOnly())
		if err != nil {
			t.Error(err)
		}
		after(t, db, raw, 0)
	})
}

// TestSQLiteTxIsolation: Serializable is what SQLite gives; another level is
// refused rather than pretended, before fn runs.
func TestSQLiteTxIsolation(t *testing.T) {
	db, _ := sqliteOneConn(t)
	ran := false
	fn := func(context.Context, *velox.DB) error { ran = true; return nil }
	if err := db.Tx(ctx, fn, velox.TxIsolation(sql.LevelSerializable)); err != nil || !ran {
		t.Errorf("Serializable: %v, ran %v", err, ran)
	}
	ran = false
	err := db.Tx(ctx, fn, velox.TxIsolation(sql.LevelReadCommitted))
	if err == nil || ran || !strings.HasPrefix(err.Error(), "velox: begin: ") || !strings.Contains(err.Error(), "Read Committed") {
		t.Errorf("Read Committed: %v, ran %v", err, ran)
	}
}
