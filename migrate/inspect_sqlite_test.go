package migrate_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// TestParseCreateTable: velox's own CREATE TABLE and the hand-written forms
// give their constraint names, checks and AUTOINCREMENT; a text it cannot
// read gives false.
func TestParseCreateTable(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "dir", "create-tables.sqlite", "0001_create_orgs.sql"))
	if err != nil {
		t.Fatal(err)
	}
	pieces, err := migrate.Split(velox.SQLite, string(src))
	if err != nil {
		t.Fatal(err)
	}
	var users string
	for _, p := range pieces {
		if strings.HasPrefix(p.SQL, `CREATE TABLE "users"`) {
			users = p.SQL
		}
	}
	auto, checks, fks, ok := migrate.ParseCreateTable(users)
	wantChecks := []migrate.Check{
		{Name: "users_role_check", Expr: `"role" IN ('admin', 'viewer')`},
		{Name: "users_status_check", Expr: `"status" IN ('active', 'banned', 'pending')`},
	}
	wantKeys := []migrate.ForeignKey{{Name: "user_mentor", Columns: []string{"mentor_id"}, RefTable: migrate.TableRef{Name: "users"}, RefColumns: []string{"id"}}, {Name: "users_orgs_users", Columns: []string{"org_id"}, RefTable: migrate.TableRef{Name: "orgs"}, RefColumns: []string{"id"}}}
	if !ok || !auto || !reflect.DeepEqual(checks, wantChecks) || !reflect.DeepEqual(fks, wantKeys) {
		t.Errorf("users: %v %+v %v %v", auto, checks, fks, ok)
	}

	auto, checks, fks, ok = migrate.ParseCreateTable(`CREATE TABLE t (id integer PRIMARY KEY AUTOINCREMENT, ` +
		`p integer REFERENCES u(id), s text CHECK (s IN ('a')), [x y] text, ` +
		`CONSTRAINT "k" FOREIGN KEY (p) REFERENCES u (id), CONSTRAINT c2 CHECK (length(s) > 0) /* ; */)`)
	wantChecks = []migrate.Check{{Expr: "s IN ('a')"}, {Name: "c2", Expr: "length(s) > 0"}}
	if !ok || !auto || !reflect.DeepEqual(checks, wantChecks) || !reflect.DeepEqual(fks, []migrate.ForeignKey{
		{Name: "", Columns: []string{"p"}, RefTable: migrate.TableRef{Name: "u"}, RefColumns: []string{"id"}}, {Name: "k", Columns: []string{"p"}, RefTable: migrate.TableRef{Name: "u"}, RefColumns: []string{"id"}}}) {
		t.Errorf("hand-written: %v %+v %v %v", auto, checks, fks, ok)
	}

	// The four quote forms, comments, a named column constraint, and a key
	// of two columns.
	auto, checks, fks, ok = migrate.ParseCreateTable("CREATE TABLE IF NOT EXISTS main.`t` (\n" +
		"  -- a comment, with ( and '\n" +
		"  a integer PRIMARY KEY, b 'odd type' CONSTRAINT 'b_ref' REFERENCES u ON DELETE SET NULL,\n" +
		"  c varchar(10, 2) NOT NULL DEFAULT (')') COLLATE NOCASE CONSTRAINT [c check] CHECK (c <> ')'),\n" +
		"  CONSTRAINT `two` FOREIGN KEY (a, \"c\") REFERENCES u (x, y) DEFERRABLE INITIALLY DEFERRED,\n" +
		"  UNIQUE (b) ON CONFLICT REPLACE, CHECK (a > 0)\n" +
		") WITHOUT ROWID, STRICT")
	wantChecks = []migrate.Check{{Name: "c check", Expr: "c <> ')'"}, {Expr: "a > 0"}}
	if !ok || auto || !reflect.DeepEqual(checks, wantChecks) || !reflect.DeepEqual(fks, []migrate.ForeignKey{
		{Name: "b_ref", Columns: []string{"b"}, RefTable: migrate.TableRef{Name: "u"}, RefColumns: nil}, {Name: "two", Columns: []string{"a", "c"}, RefTable: migrate.TableRef{Name: "u"}, RefColumns: []string{"x", "y"}}}) {
		t.Errorf("quoted: %v %+v %v %v", auto, checks, fks, ok)
	}

	// A constraint's columns are the declared ones, matched by ASCII case;
	// comments are not part of a check; a table key may be AUTOINCREMENT;
	// two named keys on one column keep their names.
	auto, checks, fks, ok = migrate.ParseCreateTable(`CREATE TABLE fk (P integer, "Role" text, ` +
		`CONSTRAINT fkx FOREIGN KEY (p) REFERENCES ty(a), CONSTRAINT f2 FOREIGN KEY ("p") REFERENCES c8 (id), ` +
		`CHECK (p > 0 /* positive */ AND [ROLE] IN ('a') -- trailing
		), CHECK ("role" <> 'P'), PRIMARY KEY (p AUTOINCREMENT))`)
	wantChecks = []migrate.Check{{Expr: "P > 0 AND [Role] IN ('a')"}, {Expr: `"Role" <> 'P'`}}
	wantKeys = []migrate.ForeignKey{{Name: "fkx", Columns: []string{"P"}, RefTable: migrate.TableRef{Name: "ty"}, RefColumns: []string{"a"}}, {Name: "f2", Columns: []string{"P"}, RefTable: migrate.TableRef{Name: "c8"}, RefColumns: []string{"id"}}}
	if !ok || !auto || !reflect.DeepEqual(checks, wantChecks) || !reflect.DeepEqual(fks, wantKeys) {
		t.Errorf("case: %v %+v %+v %v", auto, checks, fks, ok)
	}

	for _, bad := range []string{
		`CREATE TABLE t AS SELECT 1`,
		`CREATE TABLE t (a text`,
		`CREATE TABLE t (a text CHECK (a <> 'x)`,
		`CREATE VIEW v AS SELECT 1`,
		`CREATE TABLE t (a text CHECK)`,
		`CREATE TABLE t (a text) WOBBLY`,
	} {
		if _, _, _, ok := migrate.ParseCreateTable(bad); ok {
			t.Errorf("%s: read", bad)
		}
	}
}

// mustInspectSQLite is Inspect on SQLite, or fails t.
func mustInspectSQLite(t *testing.T, db *sql.DB, tables ...migrate.TableRef) *migrate.Schema {
	t.Helper()
	s, err := migrate.Inspect(context.Background(), db, velox.SQLite, tables)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	return s
}

// noDriftSQLite applies fsys to a fresh SQLite database and checks that
// Drift against snapshot finds no change and no warning.
func noDriftSQLite(t *testing.T, fsys fs.FS, snapshot *migrate.Schema) {
	t.Helper()
	db, _ := freshDB(t, velox.SQLite)
	if _, err := migrate.Up(context.Background(), db, fsys); err != nil {
		t.Fatalf("Up: %v", err)
	}
	r, err := migrate.Drift(context.Background(), db, snapshot)
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if len(r.Changes) > 0 || len(r.Warnings) > 0 {
		inspected := mustInspectSQLite(t, db, refsOf(snapshot)...)
		b, _ := json.MarshalIndent(inspected, "", "  ")
		t.Errorf("changes %v, warnings %v\ninspected: %s", r.Changes, r.Warnings, b)
	}
}

// sqliteSchema is a hand-made model of what the cases lack on SQLite:
// partial indexes, literal and expression defaults, a check with a quote,
// and a foreign key that sets null. Its types are those of a STRICT table.
func sqliteSchema() *migrate.Schema {
	return &migrate.Schema{Dialect: velox.SQLite, Tables: []migrate.Table{{
		Name: "g",
		Columns: []migrate.Column{
			{Name: "id", Type: "integer", Identity: true},
			{Name: "qty", Type: "integer", Default: "-1"},
			{Name: "price", Type: "real", Nullable: true, Default: "0.5"},
			{Name: "kind", Type: "text", Default: "'a'"},
			{Name: "motto", Type: "text", Default: "'it''s'"},
			{Name: "at", Type: "text", Default: "CURRENT_TIMESTAMP"},
			{Name: "day", Type: "text", Default: "(date('now'))"},
			{Name: "parent_id", Type: "integer", Nullable: true},
			{Name: "active", Type: "integer", Default: "1"},
		},
		PrimaryKey: []string{"id"},
		Indexes: []migrate.Index{
			{Name: "g_kind", Columns: []string{"kind"}, Where: "kind <> 'b'"},
			{Name: "g_parent_id", Columns: []string{"parent_id"}},
			{Name: "g_qty", Columns: []string{"qty"}, Unique: true, Where: "qty >= -1 AND active"},
		},
		ForeignKeys: []migrate.ForeignKey{{Name: "g_parent", Columns: []string{"parent_id"},
			RefTable: migrate.TableRef{Name: "g"}, RefColumns: []string{"id"}, OnDelete: "SET NULL"}},
		Checks: []migrate.Check{
			{Name: "g_kind_check", Expr: `"kind" IN ('a', 'b')`},
			{Name: "g_motto_check", Expr: "motto <> 'it''s not'"},
			{Name: "g_qty_check", Expr: "qty >= -5 OR qty IS NULL"},
		},
	}}}
}

// sqliteDir is sqliteSchema's directory.
func sqliteDir(t *testing.T) fstest.MapFS {
	t.Helper()
	return dirOf(t, velox.SQLite, step{to: sqliteSchema(), name: "g"})
}

// TestInspectSQLiteCases: every case's schema on SQLite, the golden
// directories and the hand-made schema, applied to a fresh database,
// inspect as their snapshot with no change and no warning.
func TestInspectSQLiteCases(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, c := range cases {
		if !slices.Contains(c.on(), velox.SQLite) {
			continue
		}
		from, to := models(t, c, velox.SQLite)
		list := []*migrate.Schema{to}
		if c.fromDB == nil && !c.empty {
			list = append(list, from)
		}
		for _, s := range list {
			b, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			if seen[string(b)] {
				continue
			}
			seen[string(b)] = true
			t.Run(c.name, func(t *testing.T) {
				t.Parallel()
				noDriftSQLite(t, dirOf(t, velox.SQLite, step{to: s, name: "case"}), s)
			})
		}
		// The directory of from then to, rebuilds included.
		if c.fromDB == nil {
			t.Run(c.name+"-steps", func(t *testing.T) {
				t.Parallel()
				noDriftSQLite(t, caseDir(t, c, velox.SQLite), to)
			})
		}
	}
	for _, name := range goldenCases {
		t.Run("golden-"+name, func(t *testing.T) {
			t.Parallel()
			fsys := os.DirFS(filepath.Join("testdata", "dir", name+".sqlite"))
			dir, err := migrate.ReadDir(fsys)
			if err != nil {
				t.Fatal(err)
			}
			noDriftSQLite(t, fsys, dir.Snapshot)
		})
	}
	t.Run("hand-made", func(t *testing.T) { noDriftSQLite(t, sqliteDir(t), sqliteSchema()) })
}

// TestInspectSQLiteAutoincrement: only an integer key with AUTOINCREMENT is
// an identity; an INTEGER PRIMARY KEY without it reuses a deleted top ID.
func TestInspectSQLiteAutoincrement(t *testing.T) {
	db, _ := freshDB(t, velox.SQLite)
	mustExec(t, db, `CREATE TABLE a (id integer PRIMARY KEY)`)
	mustExec(t, db, `CREATE TABLE b (id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT)`)
	mustExec(t, db, `CREATE TABLE c (id integer, n integer, PRIMARY KEY (id, n))`)
	mustExec(t, db, `CREATE TABLE d (id integer, PRIMARY KEY (id AUTOINCREMENT))`)
	s, notes, err := migrate.InspectNotes(context.Background(), db, velox.SQLite, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Errorf("notes %+v", notes)
	}
	want := map[string]bool{"a": false, "b": true, "c": false, "d": true}
	if len(s.Tables) != len(want) {
		t.Fatalf("tables %+v", s.Tables)
	}
	for _, tb := range s.Tables {
		if got := tb.Columns[0].Identity; got != want[tb.Name] {
			t.Errorf("%s.id: identity %v", tb.Name, got)
		}
	}
	if c := table(s, "c"); !slices.Equal(c.PrimaryKey, []string{"id", "n"}) {
		t.Errorf("c's key %v", c.PrimaryKey)
	}
	// Drift reports the difference as the change that would remove it.
	snap := migrate.Clone(s)
	table(snap, "a").Columns[0].Identity = true
	r, err := migrate.Drift(context.Background(), db, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) != 1 || !strings.HasPrefix(r.Changes[0].String(), "alter-column a.id") {
		t.Errorf("changes %v, warnings %v", r.Changes, r.Warnings)
	}
}

// TestInspectSQLiteFilter: nil reads every table of main but
// sqlite_sequence and the history table; a named table that is not there,
// or a view, is absent; main names the default schema.
func TestInspectSQLiteFilter(t *testing.T) {
	db, _ := freshDB(t, velox.SQLite)
	mustUp(t, db, dirOf(t, velox.SQLite, files("0001_tables.sql",
		"CREATE TABLE b (id integer PRIMARY KEY AUTOINCREMENT);\nCREATE TABLE a (id integer PRIMARY KEY, b_id integer REFERENCES b);\nINSERT INTO b DEFAULT VALUES;\n")))
	mustExec(t, db, `CREATE VIEW v AS SELECT 1 AS x`)
	if !hasTable(t, db, "sqlite_sequence") || !hasTable(t, db, "velox_migrations") {
		t.Fatal("no sqlite_sequence or history table to leave out")
	}
	names := func(s *migrate.Schema) []string {
		var out []string
		for _, tb := range s.Tables {
			out = append(out, tb.Ref().String())
		}
		return out
	}
	s := mustInspectSQLite(t, db)
	if got := names(s); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("nil: %v", got)
	}
	// A key without its columns references the primary key.
	want := []migrate.ForeignKey{{Columns: []string{"b_id"}, RefTable: migrate.TableRef{Name: "b"}, RefColumns: []string{"id"}}}
	if !reflect.DeepEqual(s.Tables[0].ForeignKeys, want) {
		t.Errorf("a's keys %+v", s.Tables[0].ForeignKeys)
	}
	got := names(mustInspectSQLite(t, db, migrate.TableRef{Name: "missing"}, migrate.TableRef{Name: "a"}, migrate.TableRef{Schema: "main", Name: "a"},
		migrate.TableRef{Name: "v"}, migrate.TableRef{Name: "sqlite_sequence"}, migrate.TableRef{Schema: "nowhere", Name: "b"}))
	if !slices.Equal(got, []string{"a"}) {
		t.Errorf("named: %v", got)
	}
}

// TestInspectSQLiteUnheld: what the model cannot hold is one note per
// object naming it; an index over an expression is left out, any other is
// read in the form the model has.
func TestInspectSQLiteUnheld(t *testing.T) {
	ctx := context.Background()
	db, _ := freshDB(t, velox.SQLite)
	for _, q := range []string{
		`CREATE TABLE p (id integer PRIMARY KEY, a text COLLATE NOCASE, b integer, c integer, g integer AS (b + 1) STORED, v integer GENERATED ALWAYS AS (b + 2))`,
		`CREATE TABLE w (k text PRIMARY KEY, n integer) WITHOUT ROWID`,
		`CREATE TABLE s (id integer PRIMARY KEY, n integer) STRICT`,
		`CREATE TABLE u (id integer PRIMARY KEY, p1 integer REFERENCES p (id) ON UPDATE CASCADE,
			p2 integer, CONSTRAINT u_p2 FOREIGN KEY (p2) REFERENCES p (id) DEFERRABLE INITIALLY DEFERRED)`,
		`CREATE INDEX p_desc ON p (b DESC, c)`,
		`CREATE INDEX p_coll ON p (b COLLATE NOCASE)`,
		`CREATE INDEX p_expr ON p (lower(a))`,
		`CREATE INDEX p_plain ON p (c)`,
	} {
		mustExec(t, db, q)
	}
	s, notes, err := migrate.InspectNotes(ctx, db, velox.SQLite, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"p.a":      {"COLLATE NOCASE"},
		"p.g":      {"generated"},
		"p.v":      {"generated"},
		"p.p_desc": {"DESC"},
		"p.p_coll": {"COLLATE NOCASE"},
		"p.p_expr": {"expression"},
		"w":        {"WITHOUT ROWID"},
		"s":        {"STRICT"},
		"u":        {"ON UPDATE CASCADE"},
		"u.u_p2":   {"DEFERRABLE"},
	}
	got := map[string]string{}
	for _, n := range notes {
		if _, twice := got[n.Object]; twice {
			t.Errorf("two notes on %s", n.Object)
		}
		got[n.Object] = n.Message
	}
	for object, words := range want {
		for _, w := range words {
			if !strings.Contains(got[object], w) {
				t.Errorf("%s: note %q, want it to name %s", object, got[object], w)
			}
		}
	}
	if len(got) != len(want) {
		t.Errorf("notes %v", got)
	}
	var indexes []string
	for _, ix := range table(s, "p").Indexes {
		indexes = append(indexes, ix.Name)
	}
	if !slices.Equal(indexes, []string{"p_coll", "p_desc", "p_plain"}) {
		t.Errorf("p's indexes %v", indexes)
	}
	var cols []string
	for _, c := range table(s, "p").Columns {
		cols = append(cols, c.Name)
	}
	if !slices.Equal(cols, []string{"id", "a", "b", "c"}) {
		t.Errorf("p's columns %v", cols)
	}
	r, err := migrate.Drift(ctx, db, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) != 0 || len(r.Warnings) != len(want) {
		t.Errorf("drift: changes %v, warnings %v", r.Changes, r.Warnings)
	}
}

// rebuildG makes table g again from its CREATE TABLE as edit changes it,
// with its indexes; g is empty.
func rebuildG(t *testing.T, db *sql.DB, edit func(string) string) {
	t.Helper()
	var create string
	if err := db.QueryRow(`SELECT sql FROM sqlite_schema WHERE type = 'table' AND name = 'g'`).Scan(&create); err != nil {
		t.Fatal(err)
	}
	var indexes []string
	rows, err := db.Query(`SELECT sql FROM sqlite_schema WHERE type = 'index' AND tbl_name = 'g' AND sql IS NOT NULL ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		indexes = append(indexes, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	edited := edit(create)
	if edited == create {
		t.Fatalf("the edit changes nothing in %s", create)
	}
	mustExec(t, db, `DROP TABLE g`)
	mustExec(t, db, edited)
	for _, q := range indexes {
		mustExec(t, db, q)
	}
}

// replace is an edit of the text from to to.
func replace(from, to string) func(string) string {
	return func(s string) string { return strings.Replace(s, from, to, 1) }
}

// TestDriftSQLiteLive: a table rebuilt by hand after Up gives the changes
// that undo the edit, and what the model cannot hold is a warning.
func TestDriftSQLiteLive(t *testing.T) {
	for _, c := range []struct {
		name    string
		edit    func(string) string
		alter   []string
		changes []string
		warns   []string
	}{
		{name: "type", edit: replace(`"kind" text NOT NULL`, `"kind" integer NOT NULL`),
			changes: []string{"alter-column g.kind (type)"}},
		{name: "nullability", edit: replace(`"qty" integer NOT NULL`, `"qty" integer`),
			changes: []string{"alter-column g.qty (nullable)"}},
		{name: "default", edit: replace(`DEFAULT 'a'`, `DEFAULT 'b'`),
			changes: []string{"alter-column g.kind (default)"}},
		{name: "on-delete", edit: replace(`ON DELETE SET NULL`, `ON DELETE CASCADE`),
			changes: []string{"drop-foreign-key g.g_parent", "add-foreign-key g.g_parent"}},
		{name: "check", edit: replace(`qty >= -5`, `qty >= -6`),
			changes: []string{"drop-check g.g_qty_check", "add-check g.g_qty_check"}},
		{name: "check-spelling", edit: replace(`qty >= -5 OR qty IS NULL`, `(qty >= -5) or  "qty" is null`)},
		{name: "predicate", alter: []string{`DROP INDEX g_kind`, `CREATE INDEX g_kind ON g (kind) WHERE kind <> 'c'`},
			changes: []string{"drop-index g.g_kind", "add-index g.g_kind"}},
		{name: "predicate-spelling", alter: []string{`DROP INDEX g_kind`, `CREATE INDEX g_kind ON g (kind) WHERE ("kind" <> 'b')`}},
		{name: "expression-index", alter: []string{`CREATE INDEX g_expr ON g (lower(kind))`},
			warns: []string{"g.g_expr"}},
		{name: "strict", edit: func(s string) string { return s + " STRICT" },
			warns: []string{"g"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			db, _ := freshDB(t, velox.SQLite)
			mustUp(t, db, sqliteDir(t))
			if c.edit != nil {
				rebuildG(t, db, c.edit)
			}
			for _, q := range c.alter {
				mustExec(t, db, q)
			}
			r, err := migrate.Drift(context.Background(), db, sqliteSchema())
			if err != nil {
				t.Fatal(err)
			}
			var changes, warns []string
			for _, ch := range r.Changes {
				changes = append(changes, ch.String())
			}
			for _, w := range r.Warnings {
				warns = append(warns, w.Object)
			}
			if !slices.Equal(changes, c.changes) || !slices.Equal(warns, c.warns) {
				t.Errorf("changes %q, warnings %v; want %q, %q", changes, r.Warnings, c.changes, c.warns)
			}
		})
	}
}

// TestDriftSQLite: a database made from a case's to has no drift; a column
// added by hand is the drop that undoes it; a table the snapshot does not
// name is never read, even one with what the model cannot hold.
func TestDriftSQLite(t *testing.T) {
	ctx := context.Background()
	_, to := models(t, caseNamed("add-index"), velox.SQLite)
	db, _ := freshDB(t, velox.SQLite)
	mustUp(t, db, dirOf(t, velox.SQLite, step{to: to, name: "case"}))
	mustExec(t, db, `CREATE TABLE other (k text PRIMARY KEY, n integer) WITHOUT ROWID, STRICT`)
	mustExec(t, db, `CREATE INDEX other_expr ON other (lower(k))`)
	r, err := migrate.Drift(ctx, db, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) != 0 || len(r.Warnings) != 0 {
		t.Errorf("drift: %v, %v", r.Changes, r.Warnings)
	}
	mustExec(t, db, `ALTER TABLE users ADD COLUMN extra text`)
	r, err = migrate.Drift(ctx, db, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) != 1 || r.Changes[0].String() != "drop-column users.extra" || len(r.Warnings) != 0 {
		t.Errorf("drift: %v, %v", r.Changes, r.Warnings)
	}
}

// TestInspectSQLiteDoesNotMutate: Inspect leaves the tables it is given as
// they were, and Drift its snapshot; each gives the same twice.
func TestInspectSQLiteDoesNotMutate(t *testing.T) {
	db, _ := freshDB(t, velox.SQLite)
	mustUp(t, db, sqliteDir(t))
	mustExec(t, db, `CREATE TABLE h (id integer PRIMARY KEY, a integer UNIQUE, b integer UNIQUE, CHECK (a > 0), CHECK (b > 0))`)
	tables := []migrate.TableRef{{Name: "missing"}, {Name: "g"}, {Name: "h"}}
	was := slices.Clone(tables)
	a := mustInspectSQLite(t, db, tables...)
	if !slices.Equal(tables, was) {
		t.Errorf("tables %v, was %v", tables, was)
	}
	b := mustInspectSQLite(t, db, tables...)
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if len(a.Tables) != 2 || !reflect.DeepEqual(a, b) || string(ja) != string(jb) {
		t.Errorf("two inspections differ:\n%s\n%s", ja, jb)
	}
	// SQLite keeps no name for a UNIQUE constraint's index or an unnamed
	// check: they are read unnamed, in order.
	h := table(a, "h")
	if len(h.Indexes) != 2 || h.Indexes[0].Name != "" || !slices.Equal(h.Indexes[0].Columns, []string{"a"}) ||
		len(h.Checks) != 2 || h.Checks[0] != (migrate.Check{Expr: "a > 0"}) {
		t.Errorf("h: %+v", h)
	}

	snapshot := sqliteSchema()
	snapshot.Tables[0].Pos = "schema.go:1"
	snapshot.Tables[0].Indexes = nil // changes for Drift to report
	snapshot.Tables[0].Checks[1].Expr = "motto <> 'its'"
	before := migrate.Clone(snapshot)
	ra, err := migrate.Drift(context.Background(), db, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot, before) {
		t.Errorf("the snapshot changed")
	}
	rb, err := migrate.Drift(context.Background(), db, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(ra.Changes) == 0 || !reflect.DeepEqual(ra, rb) {
		t.Errorf("two drifts differ, or find nothing:\n%v\n%v", ra, rb)
	}
}

// TestInspectSQLiteCase: SQLite matches names by ASCII case, so a key, a
// check or a predicate naming a column in another case is read with the
// column's declared name; a key's name and DEFERRABLE are kept, and
// comments are not part of a check or predicate.
func TestInspectSQLiteCase(t *testing.T) {
	ctx := context.Background()
	db, _ := freshDB(t, velox.SQLite)
	for _, q := range []string{
		`CREATE TABLE ty (a integer PRIMARY KEY)`,
		`CREATE TABLE fk (P integer, CONSTRAINT fkx FOREIGN KEY (p) REFERENCES TY(A) ON UPDATE CASCADE DEFERRABLE INITIALLY DEFERRED,
			CONSTRAINT fk_check CHECK (p > 0 /* positive */))`,
		"CREATE INDEX fk_p ON fk (p) WHERE p > 0 -- trailing",
	} {
		mustExec(t, db, q)
	}
	s, notes, err := migrate.InspectNotes(ctx, db, velox.SQLite, []migrate.TableRef{{Name: "fk"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Object != "fk.fkx" || !strings.Contains(notes[0].Message, "ON UPDATE CASCADE") || !strings.Contains(notes[0].Message, "DEFERRABLE") {
		t.Errorf("notes %+v", notes)
	}
	snapshot := &migrate.Schema{Dialect: velox.SQLite, Tables: []migrate.Table{{
		Name:        "fk",
		Columns:     []migrate.Column{{Name: "P", Type: "integer", Nullable: true}},
		Indexes:     []migrate.Index{{Name: "fk_p", Columns: []string{"P"}, Where: "P > 0"}},
		ForeignKeys: []migrate.ForeignKey{{Name: "fkx", Columns: []string{"P"}, RefTable: migrate.TableRef{Name: "ty"}, RefColumns: []string{"a"}}},
		Checks:      []migrate.Check{{Name: "fk_check", Expr: "P > 0"}},
	}}}
	if !s.Equal(snapshot) {
		b, _ := json.MarshalIndent(s, "", "  ")
		t.Errorf("inspected %s", b)
	}
	r, err := migrate.Drift(ctx, db, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) != 0 || len(r.Warnings) != 1 || r.Warnings[0].Object != "fk.fkx" {
		t.Errorf("drift: %v, %v", r.Changes, r.Warnings)
	}
}

// TestInspectSQLiteKeyNames: keys are matched to the pragma's by their
// columns, table and referenced columns, identical ones in order, so each
// keeps its own name.
func TestInspectSQLiteKeyNames(t *testing.T) {
	db, _ := freshDB(t, velox.SQLite)
	for _, q := range []string{
		`CREATE TABLE u (id integer PRIMARY KEY)`,
		`CREATE TABLE c8 (id integer PRIMARY KEY)`,
		`CREATE TABLE c9 (a integer, CONSTRAINT f1 FOREIGN KEY (a) REFERENCES u (id), CONSTRAINT f2 FOREIGN KEY (a) REFERENCES c8 (id),
			CONSTRAINT g1 FOREIGN KEY (a) REFERENCES u (id) ON DELETE CASCADE, CONSTRAINT g2 FOREIGN KEY (a) REFERENCES u (id) ON DELETE SET NULL)`,
	} {
		mustExec(t, db, q)
	}
	s := mustInspectSQLite(t, db, migrate.TableRef{Name: "c9"})
	got := map[string]string{}
	for _, fk := range s.Tables[0].ForeignKeys {
		got[fk.Name] = fk.RefTable.Name + " " + fk.OnDelete
	}
	want := map[string]string{"f1": "u ", "f2": "c8 ", "g1": "u CASCADE", "g2": "u SET NULL"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("keys %v, want %v", got, want)
	}
	if r, err := migrate.Drift(context.Background(), db, s); err != nil || len(r.Changes) != 0 || len(r.Warnings) != 0 {
		t.Errorf("drift: %+v, %v", r, err)
	}
}

// TestInspectSQLiteUnreadable: a CREATE TABLE SQLite accepts but the parser
// does not read is a note on the table, and the table is read without its
// checks.
func TestInspectSQLiteUnreadable(t *testing.T) {
	db, _ := freshDB(t, velox.SQLite)
	mustExec(t, db, `CREATE TABLE r (a text DEFAULT -'x', CONSTRAINT r_a CHECK (a <> ''))`)
	s, notes, err := migrate.InspectNotes(context.Background(), db, velox.SQLite, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Object != "r" || !strings.Contains(notes[0].Message, "cannot be read") {
		t.Errorf("notes %+v", notes)
	}
	if r := table(s, "r"); len(r.Columns) != 1 || len(r.Checks) != 0 {
		t.Errorf("r: %+v", r)
	}
}

// TestInspectSQLiteWaits: Inspect and Drift wait for a writer that holds
// the database, rather than fail at once.
func TestInspectSQLiteWaits(t *testing.T) {
	ctx := context.Background()
	db, dsn := freshDB(t, velox.SQLite)
	if _, err := db.Exec(`CREATE TABLE a (id integer PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	snap, err := migrate.Inspect(ctx, db, velox.SQLite, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"Inspect": func() error { _, err := migrate.Inspect(ctx, db, velox.SQLite, nil); return err },
		"Drift":   func() error { _, err := migrate.Drift(ctx, db, snap); return err },
	} {
		holder, err := openDB(t, "sqlite", dsn).Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := holder.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
			t.Fatal(err)
		}
		release := time.AfterFunc(300*time.Millisecond, func() { _, _ = holder.ExecContext(ctx, "ROLLBACK") })
		if err := call(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if release.Stop() {
			_, _ = holder.ExecContext(ctx, "ROLLBACK")
		}
		_ = holder.Close()
	}
}
