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
