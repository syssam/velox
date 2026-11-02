package apitest

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/syssam/velox"
)

// TestConnectRunsAndCloses: a DB from Connect runs statements on the pool Pool
// returns, which has one connection on SQLite, and Close closes that pool.
func TestConnectRunsAndCloses(t *testing.T) {
	db, err := velox.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	pool := velox.Pool(db)
	if pool == nil {
		t.Fatal("no pool")
	}
	if got := pool.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("MaxOpenConnections = %d, want 1", got)
	}
	if _, err := pool.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	// A second connection would be a second, empty :memory: database.
	if _, err := pool.Exec("INSERT INTO t (id) VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := pool.Ping(); err == nil {
		t.Error("the pool is open after Close")
	}
}

func TestConnectRefuses(t *testing.T) {
	for _, c := range []struct{ driver, dsn, want string }{
		{"oracle", "x", "no dialect"},
		{"mysql", "u:secret@tcp(h)/d", "parseTime"},
		{"sqlite3-not-registered", "x", "no dialect"},
	} {
		db, err := velox.Connect(c.driver, c.dsn)
		if err == nil || db != nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, %v; want an error with %q", c.driver, db, err, c.want)
		}
		if err != nil && strings.Contains(err.Error(), "secret") {
			t.Errorf("%s: the error shows the password: %v", c.driver, err)
		}
	}
}

// TestCloseLeavesAnOpenedPool: the pool given to Open stays its opener's.
func TestCloseLeavesAnOpenedPool(t *testing.T) {
	pool, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Close() }()
	db := velox.Open(velox.SQLite, pool)
	if velox.Pool(db) != pool {
		t.Error("Pool is not the pool Open was given")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := pool.Ping(); err != nil {
		t.Errorf("Close closed a pool it did not open: %v", err)
	}
}

// TestTransactionHasNoPool: a transaction's DB neither hands out the pool, whose
// statements would leave the transaction, nor closes it.
func TestTransactionHasNoPool(t *testing.T) {
	db, err := velox.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	err = db.Tx(context.Background(), func(_ context.Context, tx *velox.DB) error {
		if velox.Pool(tx) != nil {
			t.Error("a transaction's DB has a pool")
		}
		if err := tx.Close(); err == nil {
			t.Error("Close on a transaction's DB returned nil")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := velox.Pool(db).Ping(); err != nil {
		t.Errorf("the pool is closed: %v", err)
	}
}

// TestConnectSQLiteForeignKeys: Connect on SQLite enforces foreign keys with no
// pragma in the dsn, on every connection the pool opens, so a reference to a
// missing row fails instead of being stored. A dsn that sets foreign_keys
// itself is obeyed.
func TestConnectSQLiteForeignKeys(t *testing.T) {
	fk := func(t *testing.T, dsn string) (on int, insertErr error) {
		t.Helper()
		db, err := velox.Connect("sqlite", dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		pool := velox.Pool(db)
		pool.SetMaxIdleConns(0) // every statement on a new connection
		for _, q := range []string{
			"CREATE TABLE parents (id INTEGER PRIMARY KEY)",
			"CREATE TABLE children (id INTEGER PRIMARY KEY, parent_id INTEGER NOT NULL REFERENCES parents (id))",
		} {
			if _, err := pool.Exec(q); err != nil {
				t.Fatal(err)
			}
		}
		if err := pool.QueryRow("PRAGMA foreign_keys").Scan(&on); err != nil {
			t.Fatal(err)
		}
		_, insertErr = pool.Exec("INSERT INTO children (id, parent_id) VALUES (1, 99)")
		return on, insertErr
	}
	dir := t.TempDir()
	for _, dsn := range []string{dir + "/a.db", "file:" + dir + "/b.db?_pragma=busy_timeout(5000)"} {
		if on, err := fk(t, dsn); on != 1 || err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
			t.Errorf("%s: foreign_keys %d, insert %v; want 1 and a FOREIGN KEY error", dsn, on, err)
		}
	}
	if on, err := fk(t, dir+"/c.db?_pragma=foreign_keys(0)"); on != 0 || err != nil {
		t.Errorf("foreign_keys(0): %d, %v; want 0 and the insert stored", on, err)
	}
}
