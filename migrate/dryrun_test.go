package migrate_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// TestUpDryRunSQLiteRefusesAsUp: a dry run refuses what Up refuses, here a
// rebuild that would lose a hand ON UPDATE, and changes nothing: no file
// runs and the history keeps its rows.
func TestUpDryRunSQLiteRefusesAsUp(t *testing.T) {
	t.Parallel()
	d := velox.SQLite
	base := dirOf(t, d, step{to: keyed("CASCADE"), name: "base"})
	fsys := extend(t, base, d, step{to: keyed("SET NULL"), name: "action"})
	db, _ := freshDB(t, d)
	mustUp(t, db, base)
	mustExec(t, db, "DROP TABLE c")
	mustExec(t, db, "CREATE TABLE c (id integer NOT NULL PRIMARY KEY, p integer, CONSTRAINT c_fk FOREIGN KEY (p) REFERENCES p (id) ON DELETE CASCADE ON UPDATE CASCADE)")
	_, err := migrate.Up(context.Background(), db, fsys, migrate.WithDryRun())
	if e, ok := errors.AsType[*migrate.Error](err); !ok || e.Code != "unmodeled" || !strings.Contains(err.Error(), "ON UPDATE CASCADE") {
		t.Fatalf("dry run: %v", err)
	}
	if rows := historyOf(t, db); len(rows) != 1 {
		t.Errorf("history %+v, want version 1 alone", rows)
	}
	var sql string
	if err := db.QueryRow(`SELECT sql FROM sqlite_schema WHERE name = 'c'`).Scan(&sql); err != nil || !strings.Contains(sql, "ON UPDATE CASCADE") {
		t.Errorf("c is now %q, %v", sql, err)
	}
	if _, err := migrate.Up(context.Background(), db, fsys); err == nil {
		t.Error("Up passed where the dry run refused")
	}
}

// TestUpDryRunSQLite: a dry run checks every pending file after the ones
// before it, a file that alters a table an earlier one makes included,
// reports what it would apply, and leaves the database as it was, with no
// history table on a new one. A file that fails fails the dry run.
func TestUpDryRunSQLite(t *testing.T) {
	t.Parallel()
	d := velox.SQLite
	fsys := dirOf(t, d,
		files("0001_a.sql", "CREATE TABLE a (id integer PRIMARY KEY);\n"),
		files("0002_b.sql", "ALTER TABLE a ADD COLUMN b integer;\nINSERT INTO a (b) VALUES (1);\n"))
	db, _ := freshDB(t, d)
	r, err := migrate.Up(context.Background(), db, fsys, migrate.WithDryRun())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(versions(r.Applied), []uint64{1, 2}) || !r.Applied[0].AppliedAt.IsZero() {
		t.Errorf("report %+v", r)
	}
	if hasTable(t, db, "a") || hasTable(t, db, "velox_migrations") {
		t.Error("the dry run changed the database")
	}
	mustUp(t, db, fsys, migrate.WithTarget(1))
	r, err = migrate.Up(context.Background(), db, fsys, migrate.WithDryRun())
	if err != nil || !slices.Equal(versions(r.Applied), []uint64{2}) {
		t.Fatalf("dry run after 1: %+v, %v", r, err)
	}
	if rows := historyOf(t, db); len(rows) != 1 {
		t.Errorf("history %+v", rows)
	}

	bad := dirOf(t, d, files("0001_a.sql", "CREATE TABLE a (id integer PRIMARY KEY);\n"), files("0002_b.sql", "ALTER TABLE nope ADD COLUMN b integer;\n"))
	db, _ = freshDB(t, d)
	if _, err := migrate.Up(context.Background(), db, bad, migrate.WithDryRun()); err == nil || !strings.Contains(err.Error(), "0002_b.sql") {
		t.Errorf("a failing file: %v", err)
	}
	if hasTable(t, db, "a") {
		t.Error("the dry run changed the database")
	}
	if _, err := migrate.Up(context.Background(), db, fsys, migrate.WithDryRun(), migrate.WithBaseline(1)); err == nil {
		t.Error("a dry run with a baseline passed")
	}
}

// TestUpDryRunSQLiteForeignKeys: the copy has foreign keys as the session
// has them, so a file that orphans a row fails the dry run as it fails Up.
func TestUpDryRunSQLiteForeignKeys(t *testing.T) {
	t.Parallel()
	d := velox.SQLite
	fsys := dirOf(t, d,
		files("0001_a.sql", "CREATE TABLE p (id integer PRIMARY KEY);\nCREATE TABLE c (id integer PRIMARY KEY, p integer REFERENCES p (id));\n"),
		files("0002_b.sql", "INSERT INTO c (p) VALUES (7);\n"))
	db, _ := freshDB(t, d) // foreign keys on
	if _, err := migrate.Up(context.Background(), db, fsys, migrate.WithDryRun()); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Errorf("an orphan with foreign keys on: %v", err)
	}
	off := openDB(t, "sqlite", t.TempDir()+"/off.db")
	if _, err := migrate.Up(context.Background(), off, fsys, migrate.WithDryRun()); err != nil {
		t.Errorf("an orphan with foreign keys off: %v", err)
	}
}

// TestSchemaAt: what the files up to a version make, read from a scratch
// database, with the history table left out; a scratch database that is not
// empty, and files that fail on it, are refused.
func TestSchemaAt(t *testing.T) {
	t.Parallel()
	d := velox.SQLite
	fsys := dirOf(t, d,
		files("0001_a.sql", "CREATE TABLE a (id integer PRIMARY KEY);\n"),
		files("0002_b.sql", "CREATE TABLE b (id integer PRIMARY KEY);\n"))
	scratch, _ := freshDB(t, d)
	s, err := migrate.SchemaAt(context.Background(), scratch, fsys, 1)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tb := range s.Tables {
		names = append(names, tb.Name)
	}
	if !slices.Equal(names, []string{"a"}) {
		t.Errorf("tables %v, want a alone", names)
	}
	_, err = migrate.SchemaAt(context.Background(), scratch, fsys, 2)
	if e, ok := errors.AsType[*migrate.Error](err); !ok || e.Code != "drift" {
		t.Errorf("a used scratch database: %v", err)
	}
	bad := dirOf(t, d, files("0001_a.sql", "ALTER TABLE nope ADD COLUMN x integer;\n"))
	fresh, _ := freshDB(t, d)
	if _, err := migrate.SchemaAt(context.Background(), fresh, bad, 1); err == nil {
		t.Error("a failing file passed")
	} else if e, ok := errors.AsType[*migrate.Error](err); !ok || e.Code != "unverified" {
		t.Errorf("a failing file: %v", err)
	}
	if _, err := migrate.SchemaAt(context.Background(), nil, fsys, 1); err == nil {
		t.Error("a nil database passed")
	}
}
