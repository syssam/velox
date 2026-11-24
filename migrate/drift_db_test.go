package migrate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// sqliteDrift is Drift of a SQLite database made by stmts against snap.
func sqliteDrift(t *testing.T, snap *migrate.Schema, stmts ...string) *migrate.DriftReport {
	t.Helper()
	r, err := migrate.Drift(context.Background(), sqliteDB(t, stmts...), snap)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestDriftSQLitePrimaryKey: a primary key dropped or narrowed by hand is a
// change, and Create refuses the database.
func TestDriftSQLitePrimaryKey(t *testing.T) {
	t.Parallel()
	codes := schemaOf(velox.SQLite, migrate.Table{Name: "codes", PrimaryKey: []string{"code"},
		Columns: []migrate.Column{{Name: "code", Type: "text"}, {Name: "n", Type: "integer"}}})
	friends := schemaOf(velox.SQLite, migrate.Table{Name: "friends", PrimaryKey: []string{"user_id", "friend_id"},
		Columns: []migrate.Column{{Name: "user_id", Type: "integer"}, {Name: "friend_id", Type: "integer"}}})
	for _, c := range []struct {
		name   string
		snap   *migrate.Schema
		create string
		want   string
	}{
		{"dropped", codes, `CREATE TABLE codes (code text NOT NULL, n integer NOT NULL)`,
			"alter-primary-key codes (none → code)"},
		{"narrowed", friends, `CREATE TABLE friends (user_id integer NOT NULL, friend_id integer NOT NULL, PRIMARY KEY (user_id))`,
			"alter-primary-key friends (user_id → user_id, friend_id)"},
	} {
		db := sqliteDB(t, c.create)
		r, err := migrate.Drift(context.Background(), db, c.snap)
		if err != nil {
			t.Fatal(err)
		}
		wantDrift(t, r, []string{c.want})
		e := createErr(t, migrate.Create(context.Background(), db, c.snap))
		if e.Code != "create" || !strings.Contains(e.Reason, c.want) {
			t.Errorf("%s: Create: %v; want Code create naming %q", c.name, e, c.want)
		}
	}
}

// TestDriftSQLiteExpressionDefault: an expression default that differs from
// the snapshot's, both read, is a change.
func TestDriftSQLiteExpressionDefault(t *testing.T) {
	t.Parallel()
	snap := func(def string) *migrate.Schema {
		return schemaOf(velox.SQLite, migrate.Table{Name: "t", PrimaryKey: []string{"id"}, Columns: []migrate.Column{
			{Name: "id", Type: "integer", Identity: true}, {Name: "at", Type: "text", Default: def}}})
	}
	create := func(def string) string {
		return `CREATE TABLE t (id integer NOT NULL PRIMARY KEY AUTOINCREMENT, at text NOT NULL DEFAULT ` + def + `)`
	}
	for _, c := range []struct{ db, snap string }{
		{"CURRENT_DATE", "CURRENT_TIMESTAMP"},
		{"(1+2)", "(1 + 1)"},
	} {
		wantDrift(t, sqliteDrift(t, snap(c.snap), create(c.db)), []string{"alter-column t.at (default)"})
	}
	// One spelling of one expression is no change.
	wantDrift(t, sqliteDrift(t, snap("(1 + 1)"), create("(1+1)")), nil)
}

// TestDriftSQLiteSpelling: a literal default in parentheses is the literal,
// and an INTEGER PRIMARY KEY without NOT NULL, a rowid, is never NULL.
func TestDriftSQLiteSpelling(t *testing.T) {
	t.Parallel()
	snap := schemaOf(velox.SQLite, migrate.Table{Name: "t", PrimaryKey: []string{"id"}, Columns: []migrate.Column{
		{Name: "id", Type: "integer", Identity: true}, {Name: "status", Type: "text", Default: "'active'"}}})
	wantDrift(t, sqliteDrift(t, snap, `CREATE TABLE t ("id" integer PRIMARY KEY AUTOINCREMENT, status text NOT NULL DEFAULT ('active'))`), nil)
	plain := schemaOf(velox.SQLite, migrate.Table{Name: "t", PrimaryKey: []string{"id"}, Columns: []migrate.Column{
		{Name: "id", Type: "integer"}, {Name: "n", Type: "integer", Default: "(1)"}}})
	wantDrift(t, sqliteDrift(t, plain, `CREATE TABLE t (id INTEGER PRIMARY KEY, n integer NOT NULL DEFAULT 1)`), nil)
	// Another literal is a change, and a key that is no rowid can hold NULL.
	wantDrift(t, sqliteDrift(t, snap, `CREATE TABLE t (id integer PRIMARY KEY AUTOINCREMENT, status text NOT NULL DEFAULT ('banned'))`),
		[]string{"alter-column t.status (default)"})
	text := schemaOf(velox.SQLite, migrate.Table{Name: "t", PrimaryKey: []string{"code"}, Columns: []migrate.Column{{Name: "code", Type: "text"}}})
	wantDrift(t, sqliteDrift(t, text, `CREATE TABLE t (code text PRIMARY KEY)`), []string{"alter-column t.code (nullable)"})
}
