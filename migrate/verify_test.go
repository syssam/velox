package migrate_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// verifyErr is the *Error of Verify's refusal, or fails t.
func verifyErr(t *testing.T, err error) *migrate.Error {
	t.Helper()
	var e *migrate.Error
	if !errors.As(err, &e) {
		t.Fatalf("Verify: %v, want an *Error", err)
	}
	return e
}

// lastFile is the path of fsys's last migration file.
func lastFile(t *testing.T, fsys fstest.MapFS) string {
	t.Helper()
	dir, err := migrate.ReadDir(fsys)
	if err != nil {
		t.Fatal(err)
	}
	return dir.Files[len(dir.Files)-1].Path
}

// TestVerify: a directory applied to an empty database matches its
// schema.json; a hand edit schema.json does not know fails, naming the
// change; a database that is not empty is refused.
func TestVerify(t *testing.T) {
	ctx := context.Background()
	fsys := caseDir(t, caseNamed("add-index"), velox.SQLite)
	db, _ := freshDB(t, velox.SQLite)
	if err := migrate.Verify(ctx, db, fsys); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	// A history table with no row is empty.
	db, _ = freshDB(t, velox.SQLite)
	mustUp(t, db, dirOf(t, velox.SQLite))
	if err := migrate.Verify(ctx, db, fsys); err != nil {
		t.Errorf("Verify after an empty Up: %v", err)
	}

	edited := cloneFS(fsys)
	last := lastFile(t, edited)
	edited[last].Data = append(edited[last].Data, "ALTER TABLE users ADD COLUMN extra text;\n"...)
	sum, err := migrate.Sum(edited)
	if err != nil {
		t.Fatal(err)
	}
	edited["velox.sum"] = &fstest.MapFile{Data: sum}
	db, _ = freshDB(t, velox.SQLite)
	e := verifyErr(t, migrate.Verify(ctx, db, edited))
	want := "migrate: the database differs from schema.json after applying the directory:\n  drop-column users.extra"
	if e.Code != "drift" || e.Error() != want {
		t.Errorf("Verify: %q (%s), want %q", e.Error(), e.Code, want)
	}

	db, _ = freshDB(t, velox.SQLite)
	mustExec(t, db, `CREATE TABLE x (id integer)`)
	if e := verifyErr(t, migrate.Verify(ctx, db, fsys)); e.Code != "drift" || e.Object != "x" || e.Reason != "Verify needs an empty database" {
		t.Errorf("a table: %+v", e)
	}

	db, _ = freshDB(t, velox.SQLite)
	mustUp(t, db, dirOf(t, velox.SQLite, files("0001_nothing.sql", "SELECT 1;\n")))
	if e := verifyErr(t, migrate.Verify(ctx, db, fsys)); e.Code != "drift" || e.Object != "velox_migrations" || e.Reason != "Verify needs an empty database" {
		t.Errorf("a history row: %+v", e)
	}

	if e := verifyErr(t, migrate.Verify(ctx, nil, fsys)); e.Code != "nil" {
		t.Errorf("no database: %+v", e)
	}
	if err := migrate.Verify(ctx, db, fstest.MapFS{}); err == nil || !strings.HasPrefix(err.Error(), "migrate: ") {
		t.Errorf("no directory: %v", err)
	}
}

// TestVerifyPostgres: a case's directory verifies on PostgreSQL.
func TestVerifyPostgres(t *testing.T) {
	needPostgres(t)
	db, _ := freshDB(t, velox.Postgres)
	if err := migrate.Verify(context.Background(), db, caseDir(t, caseNamed("add-index"), velox.Postgres)); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

// TestVerifyMySQL: a case's directory verifies on MySQL.
func TestVerifyMySQL(t *testing.T) {
	needMySQL(t)
	db, _ := freshDB(t, velox.MySQL)
	if err := migrate.Verify(context.Background(), db, caseDir(t, caseNamed("add-index"), velox.MySQL)); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

// TestVerifyStrayTable: a table a file makes and schema.json does not know
// fails Verify, naming it, on each dialect; on PostgreSQL in any schema, but a
// table of an extension's. A file's row of the history table is no stray.
func TestVerifyStrayTable(t *testing.T) {
	for _, d := range []velox.Dialect{velox.SQLite, velox.Postgres, velox.MySQL} {
		t.Run(d.String(), func(t *testing.T) {
			if d == velox.Postgres {
				needPostgres(t)
			}
			if d == velox.MySQL {
				needMySQL(t)
			}
			ctx := context.Background()
			fsys := caseDir(t, caseNamed("add-index"), d)
			edited := rewrite(t, fsys, lastFile(t, fsys), func(s string) string {
				return s + "CREATE TABLE stray (x integer);\n"
			})
			db, _ := freshDB(t, d)
			e := verifyErr(t, migrate.Verify(ctx, db, edited))
			if e.Code != "drift" || !strings.Contains(e.Reason, "table stray") {
				t.Errorf("Verify: %q (%s)", e.Error(), e.Code)
			}
		})
	}
	t.Run("postgres schema", func(t *testing.T) {
		needPostgres(t)
		ctx := context.Background()
		d := velox.Postgres
		fsys := caseDir(t, caseNamed("add-index"), d)
		edited := rewrite(t, fsys, lastFile(t, fsys), func(s string) string {
			return s + "CREATE SCHEMA other;\nCREATE TABLE other.stray (x integer);\n"
		})
		db, _ := freshDB(t, d)
		e := verifyErr(t, migrate.Verify(ctx, db, edited))
		if e.Code != "drift" || !strings.Contains(e.Reason, "table other.stray") {
			t.Errorf("Verify: %q (%s)", e.Error(), e.Code)
		}
	})
}
