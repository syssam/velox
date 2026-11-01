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

// rebuildBody is the file the plan of guardSchema(false) to guardSchema(true)
// writes, as a stale branch would merge it.
func rebuildBody(t *testing.T) string {
	t.Helper()
	d := velox.SQLite
	fsys := dirOf(t, d, step{to: guardSchema(false), name: "base"}, step{to: guardSchema(true), name: "rebuild"})
	return string(fsys["0002_rebuild.sql"].Data)
}

// handTable is a database at guardSchema(false) whose t was replaced by hand
// with create, holding one row, and the directory with the rebuild's file, under
// the allow directives allow.
func handTable(t *testing.T, create string, allow []string, hand ...string) (*sql.DB, fstest.MapFS) {
	t.Helper()
	d := velox.SQLite
	db, _ := freshDB(t, d)
	base := dirOf(t, d, step{to: guardSchema(false), name: "base"})
	mustUp(t, db, base)
	if create != "" {
		mustExec(t, db, "DROP TABLE t")
		mustExec(t, db, create)
	}
	mustExec(t, db, "INSERT INTO o (id) VALUES (1)")
	mustExec(t, db, "INSERT INTO t (id, n, s, o) VALUES (1, 1, 'a', 1)")
	for _, q := range hand {
		mustExec(t, db, q)
	}
	body := rebuildBody(t)
	for _, a := range allow {
		body = strings.Replace(body, "-- velox:foreign-keys off\n", "-- velox:foreign-keys off\n-- velox:allow unmodeled-loss "+a+"\n", 1)
	}
	return db, extend(t, base, d, files("0002_rebuild.sql", body))
}

// TestUpRebuildRefusesUnsnapshottedColumn: a column the snapshot does not hold,
// which the rebuild's INSERT leaves out, is refused with its data untouched; the
// directive accepts it. A stale branch's rebuild does it with no hand edit.
func TestUpRebuildRefusesUnsnapshottedColumn(t *testing.T) {
	db, fsys := handTable(t, "", nil, "ALTER TABLE t ADD COLUMN secret text", "UPDATE t SET secret = 'x'")
	_, err := migrate.Up(context.Background(), db, fsys)
	wantUnmodeled(t, db, fsys, err, "t.secret", "velox:allow unmodeled-loss t.secret", "migrate hash")
	var secret string
	if err := db.QueryRow("SELECT secret FROM t").Scan(&secret); err != nil || secret != "x" {
		t.Errorf("secret = %q, %v", secret, err)
	}

	db, fsys = handTable(t, "", []string{"t.secret"}, "ALTER TABLE t ADD COLUMN secret text")
	mustUp(t, db, fsys)
	if _, err := db.Exec("SELECT secret FROM t"); err == nil {
		t.Error("the accepted loss did not happen")
	}

	// Branch A adds x; a stale branch B's rebuild, planned without it, merges after.
	d := velox.SQLite
	db, _ = freshDB(t, d)
	withX := guardSchema(false)
	withX.Tables[1].Columns = append(withX.Tables[1].Columns, migrate.Column{Name: "x", Type: "text", Nullable: true})
	base := dirOf(t, d, step{to: guardSchema(false), name: "base"}, step{to: withX, name: "add_x"})
	mustUp(t, db, base)
	mustExec(t, db, "INSERT INTO t (id, n, x) VALUES (1, 1, 'keep')")
	stale := extend(t, base, d, files("0003_rebuild.sql", rebuildBody(t)))
	_, err = migrate.Up(context.Background(), db, stale)
	var e *migrate.Error
	if !errors.As(err, &e) || e.Code != "unmodeled" || !strings.Contains(err.Error(), "t.x") {
		t.Fatalf("Up: %v", err)
	}
}

// TestUpRebuildRefusesHandMade: what the model could hold but the snapshot does
// not goes with a rebuild like a column, and is refused by name; the directive
// lets it through. The database is what is read: nothing here is in a file.
func TestUpRebuildRefusesHandMade(t *testing.T) {
	const cols = `id integer NOT NULL PRIMARY KEY, n integer NOT NULL, s text, o integer`
	for _, c := range []struct {
		name, create string
		hand         []string
		object, says string
	}{
		{name: "unique", create: "CREATE TABLE t (" + cols + ", UNIQUE (s))", object: "t.(s)", says: "index"},
		{name: "check", create: "CREATE TABLE t (" + cols + ", CHECK (n > 0))", object: "t.check (n > 0)", says: "check"},
		{name: "cascade", create: "CREATE TABLE t (id integer NOT NULL PRIMARY KEY, n integer NOT NULL, s text, o integer REFERENCES o (id) ON DELETE CASCADE)", object: "t.(o)", says: "foreign key"},
		{name: "autoincrement", create: "CREATE TABLE t (id integer NOT NULL PRIMARY KEY AUTOINCREMENT, n integer NOT NULL, s text, o integer)", object: "t.id", says: "AUTOINCREMENT"},
		{name: "default", create: "CREATE TABLE t (id integer NOT NULL PRIMARY KEY, n integer NOT NULL, s text DEFAULT 'x', o integer)", object: "t.s", says: "default"},
		{name: "not null", create: "CREATE TABLE t (id integer NOT NULL PRIMARY KEY, n integer NOT NULL, s text NOT NULL, o integer)", object: "t.s", says: "NOT NULL"},
		{name: "index", hand: []string{"CREATE INDEX t_s ON t (s)"}, object: "t.t_s", says: "index"},
	} {
		t.Run(c.name, func(t *testing.T) {
			db, fsys := handTable(t, c.create, nil, c.hand...)
			before := createText(t, db)
			_, err := migrate.Up(context.Background(), db, fsys)
			wantUnmodeled(t, db, fsys, err, c.object, c.says)
			if got := createText(t, db); got != before {
				t.Errorf("the table changed: %s", got)
			}
			db, fsys = handTable(t, c.create, []string{`"` + c.object + `"`}, c.hand...)
			mustUp(t, db, fsys)
		})
	}
}

// guardedRow is the row t holds after handTable's insert.
func guardedRow(t *testing.T, db *sql.DB) {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT n FROM t WHERE id = 1").Scan(&n); err != nil || n != 1 {
		t.Errorf("the row: %d, %v", n, err)
	}
}

// TestUpRebuildPlannedDropsPass: what the plan itself takes away of a table is
// in the file's change list, as -- velox:drops, so it needs no directive of the
// user's; take the line out and the same loss is refused.
func TestUpRebuildPlannedDropsPass(t *testing.T) {
	d := velox.SQLite
	base := guardSchema(false)
	tb := &base.Tables[1]
	tb.Checks = []migrate.Check{{Name: "t_n", Expr: `"n" > 0`}}
	tb.ForeignKeys = []migrate.ForeignKey{{Name: "t_o", Columns: []string{"o"}, RefTable: migrate.TableRef{Name: "o"}, RefColumns: []string{"id"}}}
	tb.Indexes = []migrate.Index{{Unique: true, Columns: []string{"s"}}}
	tb.Columns[2].Default = "'x'"
	to := guardSchema(true)
	setup := func(t *testing.T) (*sql.DB, fstest.MapFS) {
		t.Helper()
		db, _ := freshDB(t, d)
		fsys := dirOf(t, d, step{to: base, name: "base"})
		mustUp(t, db, fsys)
		mustExec(t, db, "INSERT INTO t (id, n) VALUES (1, 1)")
		return db, extend(t, fsys, d, step{to: to, name: "rebuild"})
	}
	db, fsys := setup(t)
	data := string(fsys["0002_rebuild.sql"].Data)
	for _, want := range []string{"-- velox:drops t.t_n\n", "-- velox:drops t.t_o\n", `-- velox:drops t.(s)` + "\n", "-- velox:drops t.s:default='x'\n", "-- velox:drops t.n:not-null\n"} {
		if !strings.Contains(data, want) {
			t.Errorf("no %q:\n%s", want, data)
		}
	}
	mustUp(t, db, fsys)
	guardedRow(t, db)

	for _, gone := range []string{"t.t_n", "t.t_o", "t.(s)", "t.s:default='x'", "t.n:not-null"} {
		db, fsys := setup(t)
		fsys = rewrite(t, fsys, "0002_rebuild.sql", func(s string) string { return strings.Replace(s, "-- velox:drops "+gone+"\n", "", 1) })
		_, err := migrate.Up(context.Background(), db, fsys)
		wantUnmodeled(t, db, fsys, err, strings.Split(gone, ":")[0])
	}
}

// TestUpRebuildRenamedColumnNoLoss: a renamed column stays in the INSERT's
// SELECT list, and what is over it, a check, an index or a default, is the same
// after the rename: no refusal, under no directive.
func TestUpRebuildRenamedColumnNoLoss(t *testing.T) {
	d := velox.SQLite
	schema := func(name string, nullable bool) *migrate.Schema {
		s := oneTable(d, migrate.Column{Name: name, Type: "text", Nullable: nullable, Default: "'x'"})
		s.Tables[0].Checks = []migrate.Check{{Name: "t_c", Expr: `"` + name + `" <> ''`}}
		s.Tables[0].Indexes = []migrate.Index{{Unique: true, Columns: []string{name}}}
		return s
	}
	db, _ := freshDB(t, d)
	fsys := dirOf(t, d, step{to: schema("a", false), name: "base"})
	mustUp(t, db, fsys)
	mustExec(t, db, "INSERT INTO t (id, a) VALUES (1, 'v')")
	fsys = extend(t, fsys, d, step{to: schema("b", true), name: "rename", opts: migrate.PlanOptions{Diff: migrate.DiffOptions{
		RenameColumns: map[migrate.ColumnRef]string{{Table: migrate.TableRef{Name: "t"}, Column: "a"}: "b"},
	}}})
	if _, err := migrate.Up(context.Background(), db, fsys); err != nil {
		t.Fatalf("Up: %v\n%s", err, sqlFiles(fsys))
	}
	var b string
	if err := db.QueryRow("SELECT b FROM t").Scan(&b); err != nil || b != "v" {
		t.Errorf("b = %q, %v", b, err)
	}
}

// TestUpRebuildOtherTemporaryName: a rebuild under another temporary name is
// guarded like a generated one.
func TestUpRebuildOtherTemporaryName(t *testing.T) {
	d := velox.SQLite
	db, _ := freshDB(t, d)
	base := dirOf(t, d, step{to: guardSchema(false), name: "base"})
	mustUp(t, db, base)
	mustExec(t, db, "ALTER TABLE t ADD COLUMN secret text")
	fsys := extend(t, base, d, files("0002_rebuild.sql", `-- velox:foreign-keys off
CREATE TABLE tmp (id integer NOT NULL PRIMARY KEY, n integer, s text, o integer);
INSERT INTO tmp (id, n, s, o) SELECT id, n, s, o FROM t;
DROP TABLE t;
ALTER TABLE tmp RENAME TO t;
`))
	_, err := migrate.Up(context.Background(), db, fsys)
	wantUnmodeled(t, db, fsys, err, "t.secret")
}

// textKeyed is a table of a text key, with rowids 10 and 20, and the plan that
// rebuilds it.
func textKeyed(t *testing.T, cols ...migrate.Column) (*sql.DB, fstest.MapFS) {
	t.Helper()
	d := velox.SQLite
	schema := func(nullable bool) *migrate.Schema {
		all := append([]migrate.Column{{Name: "k", Type: "text"}, {Name: "s", Type: "text", Nullable: nullable}}, cols...)
		return &migrate.Schema{Dialect: d, Tables: []migrate.Table{{Name: "t", Columns: all, PrimaryKey: []string{"k"}}}}
	}
	db, _ := freshDB(t, d)
	fsys := dirOf(t, d, step{to: schema(false), name: "base"})
	mustUp(t, db, fsys)
	return db, extend(t, fsys, d, step{to: schema(true), name: "rebuild"})
}

// TestUpRebuildKeepsRowids: a rebuild of a rowid table copies the rowid, on
// which external-content FTS5 depends, by a name no column takes.
func TestUpRebuildKeepsRowids(t *testing.T) {
	db, fsys := textKeyed(t)
	mustExec(t, db, "INSERT INTO t (rowid, k, s) VALUES (10, 'a', 'one'), (20, 'b', 'two')")
	mustExec(t, db, "CREATE VIRTUAL TABLE f USING fts5(s, content='t', content_rowid='rowid')")
	mustExec(t, db, "INSERT INTO f(f) VALUES ('rebuild')")
	mustUp(t, db, fsys)
	var rowid int
	if err := db.QueryRow("SELECT rowid FROM t WHERE k = 'b'").Scan(&rowid); err != nil || rowid != 20 {
		t.Errorf("rowid = %d, %v\n%s", rowid, err, sqlFiles(fsys))
	}
	if _, err := db.Exec("INSERT INTO f(f) VALUES ('integrity-check')"); err != nil {
		t.Errorf("FTS5: %v", err)
	}
	var k string
	if err := db.QueryRow("SELECT t.k FROM f JOIN t ON t.rowid = f.rowid WHERE f MATCH 'two'").Scan(&k); err != nil || k != "b" {
		t.Errorf("the match: %q, %v", k, err)
	}

	// A column named rowid is not the rowid.
	db, fsys = textKeyed(t, migrate.Column{Name: "rowid", Type: "text", Nullable: true})
	mustExec(t, db, "INSERT INTO t (_rowid_, k, s, rowid) VALUES (10, 'a', 'one', 'c')")
	mustUp(t, db, fsys)
	var c string
	if err := db.QueryRow("SELECT _rowid_, rowid FROM t").Scan(&rowid, &c); err != nil || rowid != 10 || c != "c" {
		t.Errorf("_rowid_, rowid = %d, %q, %v", rowid, c, err)
	}

	// An integer key is the rowid: nothing more to copy.
	_, plain := guarded(t, migrate.PlanOptions{})
	if data := string(plain["0002_rebuild.sql"].Data); strings.Contains(strings.ToLower(data), "rowid") {
		t.Errorf("an integer key copies the rowid:\n%s", data)
	}
}

// TestUpRebuildWithoutRowidSaysSo: a WITHOUT ROWID table has no rowid to copy,
// so under the directive that accepts the option's loss the file must lose the
// rowid too, and the refusal says which.
func TestUpRebuildWithoutRowidSaysSo(t *testing.T) {
	db, fsys := textKeyed(t)
	mustExec(t, db, "DROP TABLE t")
	mustExec(t, db, "CREATE TABLE t (k text NOT NULL PRIMARY KEY, s text NOT NULL) WITHOUT ROWID")
	mustExec(t, db, "INSERT INTO t (k, s) VALUES ('a', 'one')")
	fsys = rewrite(t, fsys, "0002_rebuild.sql", func(s string) string {
		return strings.Replace(s, "-- velox:foreign-keys off\n", "-- velox:foreign-keys off\n-- velox:allow unmodeled-loss t\n", 1)
	})
	_, err := migrate.Up(context.Background(), db, fsys)
	var e *migrate.Error
	if !errors.As(err, &e) || e.Code != "unmodeled" || !strings.Contains(err.Error(), "WITHOUT ROWID") || !strings.Contains(err.Error(), "rowid") {
		t.Fatalf("Up: %v", err)
	}
	fsys = rewrite(t, fsys, "0002_rebuild.sql", func(s string) string { return strings.ReplaceAll(s, "rowid, ", "") })
	if _, err := migrate.Up(context.Background(), db, fsys); err != nil {
		t.Fatalf("Up without the rowid: %v\n%s", err, sqlFiles(fsys))
	}
}

// parentChild is a database whose p is a table of the snapshot and whose c,
// which the snapshot does not hold, deletes with it, with a row in each.
func parentChild(t *testing.T) (*sql.DB, fstest.MapFS, *migrate.Schema) {
	t.Helper()
	d := velox.SQLite
	db, _ := freshDB(t, d)
	s := &migrate.Schema{Dialect: d, Tables: []migrate.Table{
		{Name: "p", Columns: []migrate.Column{{Name: "id", Type: "integer"}}, PrimaryKey: []string{"id"}},
		{Name: "q", Columns: []migrate.Column{{Name: "id", Type: "integer"}}, PrimaryKey: []string{"id"}},
	}}
	fsys := dirOf(t, d, step{to: s, name: "base"})
	mustUp(t, db, fsys)
	mustExec(t, db, "CREATE TABLE c (id integer PRIMARY KEY, p_id integer REFERENCES p (id) ON DELETE CASCADE)")
	mustExec(t, db, "INSERT INTO p (id) VALUES (1)")
	mustExec(t, db, "INSERT INTO c (id, p_id) VALUES (1, 1)")
	return db, fsys, s
}

// count is how many rows the table holds.
func count(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestUpDropTableRefusesChild: an allowed drop of a table an unmodeled child
// references, with foreign keys on, would delete the child's rows: refused,
// naming the child, until a directive accepts it or the file drops the child.
func TestUpDropTableRefusesChild(t *testing.T) {
	d := velox.SQLite
	const drop = "-- velox:allow drop-table p\nDROP TABLE p;\n"
	db, fsys, _ := parentChild(t)
	fsys = extend(t, fsys, d, files("0002_rebuild.sql", drop))
	_, err := migrate.Up(context.Background(), db, fsys)
	wantUnmodeled(t, db, fsys, err, "the table c", "velox:allow unmodeled-loss c")
	if n := count(t, db, "c"); n != 1 {
		t.Errorf("c has %d rows", n)
	}

	db, fsys, _ = parentChild(t)
	mustUp(t, db, extend(t, fsys, d, files("0002_x.sql", "-- velox:allow unmodeled-loss c\n"+drop)))

	db, fsys, _ = parentChild(t)
	mustUp(t, db, extend(t, fsys, d, files("0002_x.sql", "-- velox:allow drop-table p\nDROP TABLE c;\nDROP TABLE p;\n")))

	// A child dropped later in the file is the file's too, and so is one the file rebuilds.
	db, fsys, _ = parentChild(t)
	mustUp(t, db, extend(t, fsys, d, files("0002_x.sql", "-- velox:foreign-keys off\nDROP TABLE p;\nDROP TABLE c;\n")))

	db, fsys, _ = parentChild(t)
	mustUp(t, db, extend(t, fsys, d, files("0002_x.sql", `-- velox:foreign-keys off
-- velox:allow unmodeled-loss c.(p_id)
DROP TABLE p;
CREATE TABLE velox_new_c (id integer PRIMARY KEY, p_id integer);
INSERT INTO velox_new_c (id, p_id) SELECT id, p_id FROM c;
DROP TABLE c;
ALTER TABLE velox_new_c RENAME TO c;
`)))
	if n := count(t, db, "c"); n != 1 {
		t.Errorf("the rebuilt c has %d rows", n)
	}

	// A table nothing references is dropped as ever.
	db, fsys, _ = parentChild(t)
	mustUp(t, db, extend(t, fsys, d, files("0002_x.sql", "DROP TABLE q;\n")))
}

// TestUpRebuildForeignKeysOnRefused: a rebuild whose foreign-keys off was taken
// out would delete the children's rows through its DROP TABLE: Up refuses,
// naming them, where it returned nil.
func TestUpRebuildForeignKeysOnRefused(t *testing.T) {
	d := velox.SQLite
	schema := func(nullable bool) *migrate.Schema {
		return &migrate.Schema{Dialect: d, Tables: []migrate.Table{
			{Name: "c", Columns: []migrate.Column{{Name: "id", Type: "integer"}, {Name: "p_id", Type: "integer"}}, PrimaryKey: []string{"id"},
				ForeignKeys: []migrate.ForeignKey{{Name: "c_p", Columns: []string{"p_id"}, RefTable: migrate.TableRef{Name: "p"}, RefColumns: []string{"id"}, OnDelete: "CASCADE"}}},
			{Name: "p", Columns: []migrate.Column{{Name: "id", Type: "integer"}, {Name: "n", Type: "integer", Nullable: nullable}}, PrimaryKey: []string{"id"}},
		}}
	}
	db, _ := freshDB(t, d)
	fsys := dirOf(t, d, step{to: schema(false), name: "base"})
	mustUp(t, db, fsys)
	mustExec(t, db, "INSERT INTO p (id, n) VALUES (1, 1)")
	mustExec(t, db, "INSERT INTO c (id, p_id) VALUES (1, 1)")
	fsys = extend(t, fsys, d, step{to: schema(true), name: "rebuild"})
	fsys = rewrite(t, fsys, "0002_rebuild.sql", func(s string) string { return strings.Replace(s, "-- velox:foreign-keys off\n", "", 1) })
	_, err := migrate.Up(context.Background(), db, fsys)
	wantUnmodeled(t, db, fsys, err, "the table c")
	if n := count(t, db, "c"); n != 1 {
		t.Errorf("c has %d rows", n)
	}
}

// TestForeignKeyCheckSeesADroppedTablesTrigger: a trigger of a table the file
// drops may have written anywhere before it went: the check covers the whole
// database then.
func TestForeignKeyCheckSeesADroppedTablesTrigger(t *testing.T) {
	d := velox.SQLite
	db, _ := freshDB(t, d)
	base := dirOf(t, d, files("0001_base.sql", `CREATE TABLE z (id integer PRIMARY KEY);
CREATE TABLE y (z_id integer REFERENCES z (id));
CREATE TABLE x (id integer PRIMARY KEY);
INSERT INTO x (id) VALUES (1);
`))
	mustUp(t, db, base)
	db.SetMaxOpenConns(1)
	mustExec(t, db, "PRAGMA foreign_keys = OFF")
	mustExec(t, db, "CREATE TRIGGER x_ad AFTER DELETE ON x BEGIN INSERT INTO y (z_id) VALUES (999); END")
	fsys := extend(t, base, d, files("0002_x.sql", "-- velox:foreign-keys off\nDELETE FROM x;\nDROP TABLE x;\n"))
	_, err := migrate.Up(context.Background(), db, fsys)
	if err == nil || !strings.Contains(err.Error(), "foreign_key_check") {
		t.Fatalf("Up: %v", err)
	}
	if n := count(t, db, "x"); n != 1 {
		t.Errorf("x has %d rows: the file was not rolled back", n)
	}
}

// TestUpRebuildDependentsByReference: a view or trigger that only says a name
// the rebuilt table has, as a column of another table or an alias, is no use of
// it, and the RENAME runs; one that does use it is refused.
func TestUpRebuildDependentsByReference(t *testing.T) {
	for _, hand := range [][]string{
		{"ALTER TABLE o ADD COLUMN t integer", "CREATE VIEW v AS SELECT t FROM o"},
		{"ALTER TABLE o ADD COLUMN t integer", "CREATE TRIGGER o_au AFTER UPDATE ON o BEGIN UPDATE o SET t = 1 WHERE id = new.id AND t IS NULL; END"},
		{"CREATE VIEW v AS SELECT t.s FROM o AS t"},
	} {
		db, fsys := guarded(t, migrate.PlanOptions{}, hand...)
		if _, err := migrate.Up(context.Background(), db, fsys); err != nil {
			t.Errorf("%v: Up: %v", hand, err)
		}
	}
	for _, q := range []string{
		"CREATE VIEW v AS SELECT s FROM o WHERE id IN (SELECT id FROM t)",
		"CREATE VIEW v AS WITH a AS (SELECT n FROM T) SELECT n FROM a",
		"CREATE TRIGGER o_ai AFTER INSERT ON o BEGIN DELETE FROM t WHERE id = new.id; END",
	} {
		db, fsys := guarded(t, migrate.PlanOptions{}, q)
		_, err := migrate.Up(context.Background(), db, fsys)
		wantUnmodeled(t, db, fsys, err, "names t")
	}
}
