package migrate_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// TestUpRefusesMemoryDatabase: Up runs on a connection of its own and closes it,
// and a database in memory ends with its connection: Up returned nil and left no
// table. It says so, and a table already there, on the pool's connection, stays.
func TestUpRefusesMemoryDatabase(t *testing.T) {
	t.Parallel()
	fsys := dirOf(t, velox.SQLite, step{to: oneTable(velox.SQLite), name: "base"})
	for _, dsn := range []string{":memory:", "file::memory:", "file:one?mode=memory", "", "file::memory:?cache=shared"} {
		t.Run("one connection "+dsn, func(t *testing.T) {
			vdb, err := velox.Connect("sqlite", dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = vdb.Close() })
			db := velox.Pool(vdb)
			db.SetMaxOpenConns(1)
			mustExec(t, db, "CREATE TABLE mine (x integer)")
			_, err = migrate.Up(context.Background(), db, fsys)
			var e *migrate.Error
			if !errors.As(err, &e) || e.Code != "session" || !strings.Contains(err.Error(), "in memory") {
				t.Fatalf("Up: %v", err)
			}
			if count(t, db, "mine") != 0 {
				t.Error("the table is gone")
			}
		})
		t.Run("a pool "+dsn, func(t *testing.T) {
			db := openDB(t, "sqlite", dsn)
			r, err := migrate.Up(context.Background(), db, fsys)
			if strings.Contains(dsn, "cache=shared") {
				if err != nil {
					t.Fatalf("Up: %v", err)
				}
				return
			}
			var e *migrate.Error
			if !errors.As(err, &e) || e.Code != "session" {
				t.Fatalf("Up: %+v, %v", r, err)
			}
		})
	}
}

// TestUpKeepsSharedMemoryDatabase: a shared-cache database another connection
// keeps open does outlive Up: its tables are there after, and a second Up finds
// them applied.
func TestUpKeepsSharedMemoryDatabase(t *testing.T) {
	t.Parallel()
	fsys := dirOf(t, velox.SQLite, step{to: oneTable(velox.SQLite), name: "base"})
	db := openDB(t, "sqlite", "file:kept?mode=memory&cache=shared")
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if r := mustUp(t, db, fsys); len(r.Applied) != 1 {
		t.Fatalf("report %+v", r)
	}
	if n := count(t, db, "t"); n != 0 {
		t.Errorf("t has %d rows", n)
	}
	if r := mustUp(t, db, fsys); len(r.Applied) != 0 {
		t.Errorf("a second Up applied %+v", r.Applied)
	}
	if objectOf(t, db, "velox_migrate_memory_probe") {
		t.Error("the probe stayed")
	}

	// One connection in all: it ends with Up, so Up says so.
	alone := openDB(t, "sqlite", "file:alone?mode=memory&cache=shared")
	alone.SetMaxOpenConns(1)
	if _, err := migrate.Up(context.Background(), alone, fsys); err == nil {
		t.Error("Up on a shared database of one connection")
	}
}
