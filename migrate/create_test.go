package migrate_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// createErr is the *Error of Create's refusal, or fails t.
func createErr(t *testing.T, err error) *migrate.Error {
	t.Helper()
	e, ok := errors.AsType[*migrate.Error](err)
	if !ok {
		t.Fatalf("Create: %v, want an *Error", err)
	}
	return e
}

// TestCreate: on each dialect Create makes the base model's tables, which
// Drift then finds as the model says; a second Create does nothing; a
// database that holds part of the model, or holds it otherwise, is refused
// and left as it was; and no history table is made.
func TestCreate(t *testing.T) {
	t.Parallel()
	for _, d := range []velox.Dialect{velox.SQLite, velox.Postgres, velox.MySQL} {
		t.Run(d.String(), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			model := mustTables(d, nil)
			db, _ := freshDB(t, d)
			if err := migrate.Create(ctx, db, model); err != nil {
				t.Fatalf("Create: %v", err)
			}
			noChanges(t, db, model)
			if err := migrate.Create(ctx, db, model); err != nil {
				t.Errorf("a second Create: %v", err)
			}
			held, err := migrate.Inspect(ctx, db, d, []migrate.TableRef{{Name: "velox_migrations"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(held.Tables) != 0 {
				t.Errorf("Create made a history table: %v", held.Tables)
			}

			// A table that differs is refused, naming the change.
			other := mustTables(d, nil)
			table(other, "users").Columns = append(table(other, "users").Columns, migrate.Column{Name: "nick", Type: table(other, "users").Columns[1].Type, Nullable: true})
			e := createErr(t, migrate.Create(ctx, db, other))
			if e.Code != "create" || !strings.Contains(e.Reason, "nick") {
				t.Errorf("Create of a changed model: %v; want Code create naming nick", e)
			}
			noChanges(t, db, model)
		})
	}
}

// TestCreatePartial: a database that holds one of the model's tables is
// refused, and Create makes none of the others.
func TestCreatePartial(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	model := mustTables(velox.SQLite, nil)
	db, _ := freshDB(t, velox.SQLite)
	one := &migrate.Schema{Dialect: velox.SQLite, Tables: []migrate.Table{*table(model, "users")}}
	if err := migrate.Create(ctx, db, one); err != nil {
		t.Fatalf("Create of one table: %v", err)
	}
	e := createErr(t, migrate.Create(ctx, db, model))
	if e.Code != "create" || !strings.Contains(e.Reason, "create-table") {
		t.Errorf("Create over part of the model: %v; want Code create listing create-table", e)
	}
	held, err := migrate.Inspect(ctx, db, velox.SQLite, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(held.Tables) != 1 {
		t.Errorf("Create made tables of a refused model: %d tables", len(held.Tables))
	}
}

// TestCreateRefusals: a nil database or schema, an unknown dialect and a
// SQLite database in memory of each connection of a pool of two are refused
// with their codes, the last saying what works.
func TestCreateRefusals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	model := mustTables(velox.SQLite, nil)
	db, _ := freshDB(t, velox.SQLite)
	for _, c := range []struct {
		name string
		db   *sql.DB
		s    *migrate.Schema
		code string
	}{
		{"nil db", nil, model, "nil"},
		{"nil schema", db, nil, "nil"},
		{"no dialect", db, &migrate.Schema{}, "dialect"},
	} {
		if e := createErr(t, migrate.Create(ctx, c.db, c.s)); e.Code != c.code {
			t.Errorf("%s: %v; want Code %s", c.name, e, c.code)
		}
	}
	mem := openDB(t, "sqlite", ":memory:")
	mem.SetMaxOpenConns(2)
	const want = "the SQLite database is in memory and no other connection of the pool shares it, so Create would make the tables in one connection's database, which the application may never see; " +
		"name a file, use a shared-cache database (file:x?mode=memory&cache=shared), or give the pool one connection: velox.Connect's SQLite pool has one, or call db.SetMaxOpenConns(1)"
	if e := createErr(t, migrate.Create(ctx, mem, model)); e.Code != "session" || e.Reason != want {
		t.Errorf("in memory: %v; want Code session, %q", e, want)
	}
}

// TestCreateInMemoryPoolOfOne: Create makes the tables in a database in
// memory of a pool of one connection, as velox.Connect opens SQLite, and
// hands the connection back as it found it, so the tables stay; a second
// Create finds them. A pool told to close idle connections ends the
// database, and Create says so.
func TestCreateInMemoryPoolOfOne(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	model := mustTables(velox.SQLite, nil)
	for _, dsn := range []string{":memory:", "file::memory:", "file:create_one?mode=memory&cache=shared&_fk=1"} {
		t.Run(dsn, func(t *testing.T) {
			vdb, err := velox.Connect("sqlite", dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = vdb.Close() })
			db := velox.Pool(vdb)
			mustExec(t, db, "PRAGMA busy_timeout = 1234")
			mustExec(t, db, "CREATE TABLE mine (x integer)")
			mustExec(t, db, "INSERT INTO mine VALUES (1)")
			t.Cleanup(func() {
				// Neither Create nor Drift closed the connection, which
				// holds the database.
				if count(t, db, "mine") != 1 {
					t.Error("the database in memory is gone")
				}
			})
			if err := migrate.Create(ctx, db, model); err != nil {
				t.Fatalf("Create: %v", err)
			}
			noChanges(t, db, model)
			if err := migrate.Create(ctx, db, model); err != nil {
				t.Errorf("a second Create: %v", err)
			}
			var busy int
			if err := db.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil || busy != 1234 {
				t.Errorf("busy_timeout %d, %v; want 1234, as it was", busy, err)
			}
			if s := db.Stats(); s.OpenConnections != 1 {
				t.Errorf("%d connections open", s.OpenConnections)
			}
		})
	}

	// A Create that fails rolls back, and the connection goes back with the
	// database, which the next Create makes the tables in.
	vdb, err := velox.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vdb.Close() })
	db := velox.Pool(vdb)
	mustExec(t, db, "CREATE TABLE mine (x integer)")
	bad := mustTables(velox.SQLite, nil)
	table(bad, "users").Columns[1].Type = "no such(type"
	if err := migrate.Create(ctx, db, bad); err == nil {
		t.Fatal("Create of a bad type succeeded")
	}
	if err := migrate.Create(ctx, db, model); err != nil {
		t.Fatalf("Create after a failed one: %v", err)
	}
	_ = count(t, db, "mine") // fails t when the database is gone

	vdb, err = velox.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vdb.Close() })
	db = velox.Pool(vdb)
	db.SetMaxIdleConns(0)
	const want = "the SQLite database is in memory, and its pool closed its one connection after Create made the tables, which went with it; " +
		"keep the connection open: no SetMaxIdleConns(0), SetConnMaxIdleTime or SetConnMaxLifetime"
	if e := createErr(t, migrate.Create(ctx, db, model)); e.Code != "session" || e.Reason != want {
		t.Errorf("no idle connection: %v; want Code session, %q", e, want)
	}
}

// TestCreateFails: a statement the database refuses fails Create, naming
// the statement, and the SQLite transaction leaves no table.
func TestCreateFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	model := mustTables(velox.SQLite, nil)
	table(model, "users").Columns[1].Type = "no such(type"
	db, _ := freshDB(t, velox.SQLite)
	err := migrate.Create(ctx, db, model)
	if err == nil || !strings.Contains(err.Error(), "CREATE TABLE") {
		t.Fatalf("Create of a bad type: %v; want the failing statement", err)
	}
	held, err := migrate.Inspect(ctx, db, velox.SQLite, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(held.Tables) != 0 {
		t.Errorf("a failed Create left %d tables", len(held.Tables))
	}
}

// TestCreateMySQLSQLMode: Create refuses a MySQL session whose sql_mode holds
// NO_BACKSLASH_ESCAPES when a statement holds a backslash in a literal, as
// Up refuses a file, and makes nothing.
func TestCreateMySQLSQLMode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, dsn := freshDB(t, velox.MySQL)
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Params = map[string]string{"sql_mode": "'NO_BACKSLASH_ESCAPES'"}
	db := openDB(t, "mysql", cfg.FormatDSN())
	model := &migrate.Schema{Dialect: velox.MySQL, Tables: []migrate.Table{{Name: "t", PrimaryKey: []string{"id"}, PrimaryKeyName: "PRIMARY",
		Columns: []migrate.Column{{Name: "id", Type: "bigint", Identity: true}, {Name: "s", Type: "varchar(10)", Default: `'a\\b'`}}}}}
	const want = "Create's statements hold a backslash in a literal, which NO_BACKSLASH_ESCAPES would store as two; turn the mode off for migrating"
	if e := createErr(t, migrate.Create(ctx, db, model)); e.Code != "session" || e.Reason != want {
		t.Errorf("Create: %v; want Code session, %q", e, want)
	}
	held, err := migrate.Inspect(ctx, db, velox.MySQL, []migrate.TableRef{{Name: "t"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(held.Tables) != 0 {
		t.Errorf("Create made %v", held.Tables)
	}
}
