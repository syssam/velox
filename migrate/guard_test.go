package migrate_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// guardSchema is SQLite's table t with the column n, NOT NULL or not: a
// change of it rebuilds t.
func guardSchema(nullable bool) *migrate.Schema {
	return &migrate.Schema{Dialect: velox.SQLite, Tables: []migrate.Table{
		{Name: "o", Columns: []migrate.Column{{Name: "id", Type: "integer"}, {Name: "s", Type: "text", Nullable: true}}, PrimaryKey: []string{"id"}},
		{Name: "t", Columns: []migrate.Column{{Name: "id", Type: "integer"}, {Name: "n", Type: "integer", Nullable: nullable}, {Name: "s", Type: "text", Nullable: true}, {Name: "o", Type: "integer", Nullable: true}}, PrimaryKey: []string{"id"}},
	}}
}

// guarded is a database at guardSchema(false) after the statements run on it
// by hand, with a row, and the directory that holds the plan that rebuilds t.
func guarded(t *testing.T, opts migrate.PlanOptions, hand ...string) (*sql.DB, fstest.MapFS) {
	t.Helper()
	d := velox.SQLite
	db, _ := freshDB(t, d)
	base := dirOf(t, d, step{to: guardSchema(false), name: "base"})
	mustUp(t, db, base)
	for _, q := range hand {
		mustExec(t, db, q)
	}
	mustExec(t, db, "INSERT INTO t (id, n, s) VALUES (1, 1, 'a')")
	return db, extend(t, base, d, step{to: guardSchema(true), name: "rebuild", opts: opts})
}

// objectOf reports whether sqlite_schema holds the object.
func objectOf(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name = ?", name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// createText is the CREATE text of t.
func createText(t *testing.T, db *sql.DB) string {
	t.Helper()
	var s string
	if err := db.QueryRow("SELECT sql FROM sqlite_schema WHERE name = 't'").Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// wantUnmodeled checks that err is the guard's refusal, naming each of names
// and saying what to do, and that the file is pending, unapplied.
func wantUnmodeled(t *testing.T, db *sql.DB, fsys fstest.MapFS, err error, names ...string) {
	t.Helper()
	var e *migrate.Error
	if !errors.As(err, &e) || e.Code != "unmodeled" {
		t.Fatalf("Up: %v; want an *Error of Code unmodeled\n%s", err, sqlFiles(fsys))
	}
	if e.Pos != "0002_rebuild.sql" {
		t.Errorf("Pos %q", e.Pos)
	}
	for _, want := range names {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q: %v", want, err)
		}
	}
	if st := mustStatus(t, db, fsys); len(st.Pending) != 1 {
		t.Errorf("pending %v", st.Pending)
	}
}

// TestUpRefusesRebuildLoss: a generated rebuild of a table that holds what the
// model cannot is refused before it changes anything, names each object and
// says what to do; the allow directive lets that loss through.
func TestUpRefusesRebuildLoss(t *testing.T) {
	for _, c := range []struct {
		name   string
		hand   []string
		object string // an object sqlite_schema holds and the rebuild loses
		text   string // what the CREATE text holds and the rebuild loses
		says   []string
		target string
	}{
		{name: "expression index", hand: []string{"CREATE UNIQUE INDEX t_lower ON t (lower(s))"}, object: "t_lower", says: []string{"t.t_lower", "over an expression"}, target: "t.t_lower"},
		{name: "index DESC", hand: []string{"CREATE INDEX t_desc ON t (s DESC)"}, says: []string{"t.t_desc", "DESC"}, target: "t.t_desc"},
		{name: "trigger", hand: []string{"CREATE TRIGGER t_ai AFTER INSERT ON t BEGIN UPDATE o SET s = 'x'; END"}, object: "t_ai", says: []string{"t.t_ai", "trigger"}, target: "t.t_ai"},
		{name: "collation", hand: []string{"ALTER TABLE t ADD COLUMN c text COLLATE NOCASE"}, text: "NOCASE", says: []string{"t.c", "COLLATE NOCASE"}, target: "t.c"},
		{name: "generated", hand: []string{"ALTER TABLE t ADD COLUMN g integer GENERATED ALWAYS AS (n + 1) VIRTUAL"}, text: "GENERATED", says: []string{"t.g", "generated"}, target: "t.g"},
		{name: "foreign key", hand: []string{"ALTER TABLE t ADD COLUMN f integer REFERENCES o (id) ON UPDATE CASCADE DEFERRABLE INITIALLY DEFERRED"}, text: "CASCADE", says: []string{"ON UPDATE CASCADE", "DEFERRABLE"}, target: "t"},
	} {
		t.Run(c.name, func(t *testing.T) {
			db, fsys := guarded(t, migrate.PlanOptions{}, c.hand...)
			_, err := migrate.Up(context.Background(), db, fsys)
			wantUnmodeled(t, db, fsys, err, append(c.says, "velox:allow unmodeled-loss "+c.target, "migrate hash")...)
			if c.object != "" && !objectOf(t, db, c.object) {
				t.Errorf("%s is gone after the refusal", c.object)
			}
			if c.text != "" && !strings.Contains(createText(t, db), c.text) {
				t.Errorf("%s is gone after the refusal: %s", c.text, createText(t, db))
			}
			var n int
			if err := db.QueryRow("SELECT n FROM t").Scan(&n); err != nil || n != 1 {
				t.Errorf("the row: %d, %v", n, err)
			}

			// The directive, written by hand or by -allow, lets it through.
			db, fsys = guarded(t, migrate.PlanOptions{Allow: []migrate.Allow{{Code: "unmodeled-loss", Target: c.target}}}, c.hand...)
			if !strings.Contains(string(fsys["0002_rebuild.sql"].Data), "-- velox:allow unmodeled-loss "+c.target+"\n") {
				t.Errorf("no directive:\n%s", fsys["0002_rebuild.sql"].Data)
			}
			mustUp(t, db, fsys)
			if c.object != "" && objectOf(t, db, c.object) {
				t.Errorf("%s survived", c.object)
			}

			// A directive for every target too; one for another target is none.
			db, fsys = guarded(t, allowAll("unmodeled-loss"), c.hand...)
			mustUp(t, db, fsys)
			db, fsys = guarded(t, migrate.PlanOptions{Allow: []migrate.Allow{{Code: "unmodeled-loss", Target: "t.other"}}}, c.hand...)
			_, err = migrate.Up(context.Background(), db, fsys)
			wantUnmodeled(t, db, fsys, err, c.target)
		})
	}
}

// TestUpRebuildGuardNamesEach: two objects are named together, and a directive
// for one leaves the other refused.
func TestUpRebuildGuardNamesEach(t *testing.T) {
	hand := []string{"CREATE UNIQUE INDEX t_lower ON t (lower(s))", "CREATE TRIGGER t_ai AFTER INSERT ON t BEGIN UPDATE o SET s = 'x'; END"}
	db, fsys := guarded(t, migrate.PlanOptions{}, hand...)
	_, err := migrate.Up(context.Background(), db, fsys)
	wantUnmodeled(t, db, fsys, err, "t.t_lower", "t.t_ai")
	db, fsys = guarded(t, migrate.PlanOptions{Allow: []migrate.Allow{{Code: "unmodeled-loss", Target: "t.t_ai"}}}, hand...)
	_, err = migrate.Up(context.Background(), db, fsys)
	wantUnmodeled(t, db, fsys, err, "t.t_lower")
	if strings.Contains(err.Error(), "t.t_ai:") {
		t.Errorf("an allowed loss is named: %v", err)
	}
}

// TestUpRebuildKeptByTheFile: what the file makes again is not lost, so no
// directive is needed: the index a statement makes after the rebuild, or the
// collation an edit keeps in the new table.
func TestUpRebuildKeptByTheFile(t *testing.T) {
	db, fsys := guarded(t, migrate.PlanOptions{}, "CREATE UNIQUE INDEX t_lower ON t (lower(s))")
	fsys = rewrite(t, fsys, "0002_rebuild.sql", func(s string) string { return s + "CREATE UNIQUE INDEX t_lower ON t (lower(s));\n" })
	mustUp(t, db, fsys)
	if !objectOf(t, db, "t_lower") {
		t.Error("the index is gone")
	}

	db, fsys = guarded(t, migrate.PlanOptions{}, "CREATE TABLE x (y text COLLATE NOCASE)")
	mustUp(t, db, fsys) // a table the rebuild does not touch costs nothing
	if !objectOf(t, db, "x") {
		t.Error("the other table is gone")
	}
}

// TestUpRebuildDropColumnCovers: a column the plan drops under -allow is not
// lost by accident: its collation goes with it.
func TestUpRebuildDropColumnCovers(t *testing.T) {
	d := velox.SQLite
	db, _ := freshDB(t, d)
	base := dirOf(t, d, step{to: guardSchema(false), name: "base"})
	mustUp(t, db, base)
	mustExec(t, db, "ALTER TABLE t ADD COLUMN c text COLLATE NOCASE")
	fsys := extend(t, base, d, files("0002_rebuild.sql", `-- velox:foreign-keys off
-- velox:allow drop-column t.c
CREATE TABLE "velox_new_t" ("id" integer NOT NULL PRIMARY KEY, "n" integer, "s" text, "o" integer);
INSERT INTO "velox_new_t" ("id", "n", "s", "o") SELECT "id", "n", "s", "o" FROM "t";
DROP TABLE "t";
ALTER TABLE "velox_new_t" RENAME TO "t";
`))
	mustUp(t, db, fsys)
}

// TestUpRebuildRefusesDependents: a view or another table's trigger that names
// the rebuilt table makes its RENAME fail, whatever a directive says: Up
// refuses, naming it, and a file that drops the view and makes it again runs.
func TestUpRebuildRefusesDependents(t *testing.T) {
	for _, c := range []struct{ name, hand, object string }{
		{"view", "CREATE VIEW v AS SELECT n FROM t", "v"},
		{"quoted view", `CREATE VIEW "my view" AS SELECT n FROM "t"`, "my view"},
		{"trigger", "CREATE TRIGGER o_ai AFTER INSERT ON o BEGIN UPDATE t SET n = 2; END", "o_ai"},
	} {
		t.Run(c.name, func(t *testing.T) {
			db, fsys := guarded(t, allowAll("unmodeled-loss"), c.hand)
			_, err := migrate.Up(context.Background(), db, fsys)
			wantUnmodeled(t, db, fsys, err, c.object)
			if !objectOf(t, db, c.object) {
				t.Errorf("%s is gone", c.object)
			}
		})
	}

	db, fsys := guarded(t, migrate.PlanOptions{}, "CREATE VIEW v AS SELECT n FROM t")
	fsys = rewrite(t, fsys, "0002_rebuild.sql", func(s string) string {
		return strings.Replace(s, "CREATE TABLE", "DROP VIEW v;\nCREATE TABLE", 1) + "CREATE VIEW v AS SELECT n FROM t;\n"
	})
	mustUp(t, db, fsys)
	var n int
	if err := db.QueryRow("SELECT n FROM v").Scan(&n); err != nil || n != 1 {
		t.Errorf("the view: %d, %v", n, err)
	}

	// A view of another table is no matter, nor one that only says the name.
	db, fsys = guarded(t, migrate.PlanOptions{}, "CREATE VIEW v AS SELECT s, 't' AS name FROM o")
	mustUp(t, db, fsys)
}

// TestSQLiteRenameFailsOnDependents pins why the guard refuses them: with no
// guard in the way, the rebuild's RENAME fails on each.
func TestSQLiteRenameFailsOnDependents(t *testing.T) {
	for _, hand := range []string{
		"CREATE VIEW v AS SELECT n FROM t",
		"CREATE TRIGGER o_ai AFTER INSERT ON o BEGIN UPDATE t SET n = 2; END",
	} {
		db, _ := freshDB(t, velox.SQLite)
		db.SetMaxOpenConns(1)
		mustExec(t, db, "CREATE TABLE o (id integer)")
		mustExec(t, db, "CREATE TABLE t (n integer)")
		mustExec(t, db, hand)
		mustExec(t, db, "CREATE TABLE velox_new_t (n integer)")
		mustExec(t, db, "DROP TABLE t")
		if _, err := db.Exec("ALTER TABLE velox_new_t RENAME TO t"); err == nil {
			t.Errorf("%s: RENAME ran; the guard's refusal is needless", hand)
		}
	}
}

// TestUpRebuildRenamedTable: the guard reads the table as the rebuild finds
// it, after the plan's rename.
func TestUpRebuildRenamedTable(t *testing.T) {
	d := velox.SQLite
	db, _ := freshDB(t, d)
	base := dirOf(t, d, step{to: guardSchema(false), name: "base"})
	mustUp(t, db, base)
	mustExec(t, db, "CREATE TRIGGER t_ai AFTER INSERT ON t BEGIN UPDATE o SET s = 'x'; END")
	to := guardSchema(true)
	to.Tables[1].Name = "t2"
	fsys := extend(t, base, d, step{to: to, name: "rebuild", opts: migrate.PlanOptions{Diff: migrate.DiffOptions{
		RenameTables: map[migrate.TableRef]migrate.TableRef{{Name: "t"}: {Name: "t2"}},
	}}})
	_, err := migrate.Up(context.Background(), db, fsys)
	wantUnmodeled(t, db, fsys, err, "t2.t_ai")
	if !objectOf(t, db, "t") || objectOf(t, db, "t2") {
		t.Error("the rename was not rolled back")
	}
}

// TestUpRebuildOnConflict: a constraint's ON CONFLICT clause is lost like the
// rest, so Inspect notes it and the guard refuses.
func TestUpRebuildOnConflict(t *testing.T) {
	d := velox.SQLite
	db, _ := freshDB(t, d)
	base := dirOf(t, d, files("0001_base.sql", `CREATE TABLE t (id integer NOT NULL PRIMARY KEY, n integer NOT NULL, s text, o integer, UNIQUE (s) ON CONFLICT REPLACE);`))
	mustUp(t, db, base)
	fsys := extend(t, base, d, files("0002_rebuild.sql", `-- velox:foreign-keys off
CREATE TABLE "velox_new_t" ("id" integer NOT NULL PRIMARY KEY, "n" integer, "s" text, "o" integer, UNIQUE ("s"));
INSERT INTO "velox_new_t" ("id", "n", "s", "o") SELECT "id", "n", "s", "o" FROM "t";
DROP TABLE "t";
ALTER TABLE "velox_new_t" RENAME TO "t";
`))
	_, err := migrate.Up(context.Background(), db, fsys)
	wantUnmodeled(t, db, fsys, err, "ON CONFLICT")
}

// TestUpRebuildNoLoss: a plain rebuild runs as ever.
func TestUpRebuildNoLoss(t *testing.T) {
	db, fsys := guarded(t, migrate.PlanOptions{})
	if r := mustUp(t, db, fsys); len(r.Applied) != 1 {
		t.Errorf("applied %v", r.Applied)
	}
}
