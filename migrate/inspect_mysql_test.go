package migrate_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/migrate"
)

// needMySQL skips t without VELOX_TEST_MYSQL_ADMIN.
func needMySQL(t *testing.T) {
	t.Helper()
	if os.Getenv("VELOX_TEST_MYSQL_ADMIN") == "" {
		t.Skip("needs VELOX_TEST_MYSQL_ADMIN")
	}
}

// mustInspectMySQL is Inspect on MySQL, or fails t.
func mustInspectMySQL(t *testing.T, db *sql.DB, tables ...migrate.TableRef) *migrate.Schema {
	t.Helper()
	s, err := migrate.Inspect(context.Background(), db, velox.MySQL, tables)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	return s
}

// mysqlDatabase makes the server-wide database name for t, dropped when t
// ends; MySQL's schema is a database.
func mysqlDatabase(t *testing.T, name string) {
	t.Helper()
	admin := openDB(t, "mysql", os.Getenv("VELOX_TEST_MYSQL_ADMIN"))
	mustExec(t, admin, "DROP DATABASE IF EXISTS `"+name+"`")
	mustExec(t, admin, "CREATE DATABASE `"+name+"`")
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP DATABASE IF EXISTS `" + name + "`"); err != nil {
			t.Errorf("drop %s: %v", name, err)
		}
	})
}

// noDriftMySQL applies fsys to a fresh MySQL database and checks that
// Drift against snapshot finds no change and no warning.
func noDriftMySQL(t *testing.T, fsys fs.FS, snapshot *migrate.Schema) {
	t.Helper()
	db, _ := freshDB(t, velox.MySQL)
	for _, tb := range snapshot.Tables {
		if tb.Schema != "" {
			mysqlDatabase(t, tb.Schema)
			break // the cases use one other database
		}
	}
	if _, err := migrate.Up(context.Background(), db, fsys); err != nil {
		t.Fatalf("Up: %v", err)
	}
	r, err := migrate.Drift(context.Background(), db, snapshot)
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if len(r.Changes) > 0 || len(r.Warnings) > 0 {
		inspected := mustInspectMySQL(t, db, refsOf(snapshot)...)
		b, _ := json.MarshalIndent(inspected, "", "  ")
		t.Errorf("changes %v, warnings %v\ninspected: %s", r.Changes, r.Warnings, b)
	}
}

// mysqlSchema is a hand-made model of what the cases lack on MySQL:
// literal and expression defaults of each kind, a decimal, an enum check on
// varchar, checks with quotes, a foreign key that sets null, and comments.
func mysqlSchema() *migrate.Schema {
	return &migrate.Schema{Dialect: velox.MySQL, Tables: []migrate.Table{{
		Name: "g",
		Columns: []migrate.Column{
			{Name: "id", Type: "bigint", Identity: true},
			{Name: "qty", Type: "int", Default: "-1"},
			{Name: "price", Type: "decimal(10,2)", Nullable: true, Default: "0"},
			{Name: "cost", Type: "decimal(10,2)", Default: "12.5"},
			{Name: "ratio", Type: "double", Default: "1.5"},
			{Name: "created_on", Type: "date", Default: "'2020-01-01'"},
			{Name: "at", Type: "datetime(6)", Default: "CURRENT_TIMESTAMP(6)"},
			{Name: "stamp", Type: "datetime(6)", Default: "'2020-01-01 00:00:00'"},
			{Name: "clock", Type: "time", Default: "'10:00'"},
			{Name: "body", Type: "text", Default: `('it''s a \\ x')`},
			{Name: "note", Type: "text", Default: "('x')"},
			{Name: "role", Type: "enum('a','b','o''neil','c\\\\d')", Default: "'o''neil'"},
			{Name: "kind", Type: "varchar(8)", Default: "'a'", Comment: `it's a \ kind`},
			{Name: "motto", Type: "varchar(64)", Default: `'it''s a \\ test'`},
			{Name: "parent_id", Type: "bigint", Nullable: true},
			{Name: "active", Type: "tinyint(1)", Default: "1"},
			// Expressions that MySQL prints bare, and a line break.
			{Name: "n42", Type: "int", Default: "(42)"},
			{Name: "flag", Type: "tinyint(1)", Default: "(TRUE)"},
			{Name: "nul", Type: "int", Nullable: true, Default: "(NULL)"},
			{Name: "word", Type: "varchar(8)", Default: "('a')"},
			{Name: "lines", Type: "text", Default: "('a\nb')"},
			// A foreign key without an index of its own: MySQL makes one.
			{Name: "owner_id", Type: "bigint", Nullable: true},
		},
		PrimaryKey:     []string{"id"},
		PrimaryKeyName: "PRIMARY",
		Indexes: []migrate.Index{
			{Name: "g_created_on", Columns: []string{"created_on"}},
			{Name: "g_kind_role", Columns: []string{"kind", "role"}},
			{Name: "g_motto", Columns: []string{"motto"}},
			{Name: "g_parent_id", Columns: []string{"parent_id"}},
			{Name: "g_qty", Columns: []string{"qty"}, Unique: true},
		},
		ForeignKeys: []migrate.ForeignKey{
			{Name: "g_owner", Columns: []string{"owner_id"}, RefTable: migrate.TableRef{Name: "g"}, RefColumns: []string{"id"}},
			{Name: "g_parent", Columns: []string{"parent_id"},
				RefTable: migrate.TableRef{Name: "g"}, RefColumns: []string{"id"}, OnDelete: "SET NULL"},
		},
		Checks: []migrate.Check{
			{Name: "g_kind_check", Expr: "`kind` IN ('a', 'b')"},
			{Name: "g_line_check", Expr: "kind <> 'a\nb'"},
			{Name: "g_motto_check", Expr: `motto <> 'it''s' OR motto NOT LIKE 'a\\_%'`},
			{Name: "g_not_check", Expr: "NOT (qty = 7)"},
			{Name: "g_qty_check", Expr: "qty >= -5 OR qty IS NULL"},
		},
		Comment:   `it's g \ here`,
		Collation: "utf8mb4_bin",
	}}}
}

// mysqlDir is mysqlSchema's directory.
func mysqlDir(t *testing.T) fstest.MapFS {
	t.Helper()
	return dirOf(t, velox.MySQL, step{to: mysqlSchema(), name: "g"})
}

// TestInspectMySQLCases: every case's schema on MySQL, the golden
// directories and the hand-made schema, applied to a fresh database,
// inspect as their snapshot with no change and no warning.
func TestInspectMySQLCases(t *testing.T) {
	needMySQL(t)
	seen := map[string]bool{}
	for _, c := range cases {
		if !slices.Contains(c.on(), velox.MySQL) {
			continue
		}
		from, to := models(t, c, velox.MySQL)
		list := []*migrate.Schema{to}
		if !c.empty {
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
				noDriftMySQL(t, dirOf(t, velox.MySQL, step{to: s, name: "case"}), s)
			})
		}
	}
	for _, name := range goldenCases {
		t.Run("golden-"+name, func(t *testing.T) {
			fsys := os.DirFS(filepath.Join("testdata", "dir", name+".mysql"))
			dir, err := migrate.ReadDir(fsys)
			if err != nil {
				t.Fatal(err)
			}
			noDriftMySQL(t, fsys, dir.Snapshot)
		})
	}
	t.Run("hand-made", func(t *testing.T) { noDriftMySQL(t, mysqlDir(t), mysqlSchema()) })
}

// TestInspectMySQLPrimaryKeyName: MySQL names every primary key PRIMARY,
// whatever CREATE TABLE called it, so Diff against a snapshot of the same
// tables renames none.
func TestInspectMySQLPrimaryKeyName(t *testing.T) {
	needMySQL(t)
	_, to := models(t, caseNamed("add-index"), velox.MySQL)
	db, _ := freshDB(t, velox.MySQL)
	mustUp(t, db, dirOf(t, velox.MySQL, step{to: to, name: "case"}))
	mustExec(t, db, "CREATE TABLE named (id int, CONSTRAINT named_pkey PRIMARY KEY (id))")
	mustExec(t, db, "CREATE TABLE nokey (id int)")
	s := mustInspectMySQL(t, db)
	for _, tb := range s.Tables {
		want := "PRIMARY"
		if tb.Name == "nokey" {
			want = ""
		}
		if tb.PrimaryKeyName != want {
			t.Errorf("%s: primary key name %q, want %q", tb.Name, tb.PrimaryKeyName, want)
		}
	}
	if named := table(s, "named"); !slices.Equal(named.PrimaryKey, []string{"id"}) {
		t.Errorf("named's key %v", named.PrimaryKey)
	}
	cs, err := migrate.Diff(mustInspectMySQL(t, db, refsOf(to)...), to, migrate.DiffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if _, ok := c.(migrate.RenameConstraint); ok {
			t.Errorf("Diff renames: %v", c)
		}
	}
}

// TestInspectMySQLDefaults: a default reads back as the model writes it:
// a ' and a \ in a literal, a comment and an enum value; CURRENT_TIMESTAMP(6),
// an expression on a text column, numbers, a decimal and tinyint(1).
func TestInspectMySQLDefaults(t *testing.T) {
	needMySQL(t)
	_, lit := models(t, caseNamed("literals"), velox.MySQL)
	db, _ := freshDB(t, velox.MySQL)
	mustUp(t, db, dirOf(t, velox.MySQL, step{to: lit, name: "literals"}))
	got := table(mustInspectMySQL(t, db, migrate.TableRef{Name: "users"}), "users")
	want := table(lit, "users")
	for _, name := range []string{"motto", "role", "name", "logins"} {
		g, w := *column(got, name), *column(want, name)
		g.Pos, w.Pos = "", ""
		if g != w {
			t.Errorf("users.%s: %+v, want %+v", name, g, w)
		}
	}

	db, _ = freshDB(t, velox.MySQL)
	mustUp(t, db, mysqlDir(t))
	// Checks other than the enum check are as MySQL prints them, and so is
	// a time literal, which drift compares by value.
	s, want2 := mustInspectMySQL(t, db), mysqlSchema()
	s.Tables[0].Checks, want2.Tables[0].Checks = s.Tables[0].Checks[:1], want2.Tables[0].Checks[:1]
	// MySQL made g_owner's index; drift does not count it.
	s.Tables[0].Indexes = slices.DeleteFunc(s.Tables[0].Indexes, func(ix migrate.Index) bool { return ix.Name == "g_owner" })
	for name, printed := range map[string]string{"stamp": "'2020-01-01 00:00:00.000000'", "clock": "'10:00:00'", "flag": "(true)"} {
		if got := column(&s.Tables[0], name).Default; got != printed {
			t.Errorf("%s's default %s, want %s", name, got, printed)
		}
		column(&s.Tables[0], name).Default = column(&want2.Tables[0], name).Default
	}
	if !s.Equal(want2) {
		a, _ := json.MarshalIndent(s, "", "  ")
		t.Errorf("inspected %s", a)
	}
}

// TestInspectMySQLRestrict: RESTRICT and no action are one action on MySQL:
// either spelling in the snapshot gives no drift.
func TestInspectMySQLRestrict(t *testing.T) {
	needMySQL(t)
	snap := mysqlSchema()
	tb := &snap.Tables[0]
	tb.Columns = append(tb.Columns, migrate.Column{Name: "other_id", Type: "bigint", Nullable: true})
	tb.Indexes = append(tb.Indexes, migrate.Index{Name: "g_other_id", Columns: []string{"other_id"}})
	tb.ForeignKeys = []migrate.ForeignKey{
		{Name: "g_other", Columns: []string{"other_id"}, RefTable: migrate.TableRef{Name: "g"}, RefColumns: []string{"id"}, OnDelete: "RESTRICT"},
		{Name: "g_parent", Columns: []string{"parent_id"}, RefTable: migrate.TableRef{Name: "g"}, RefColumns: []string{"id"}},
	}
	db, _ := freshDB(t, velox.MySQL)
	mustUp(t, db, dirOf(t, velox.MySQL, step{to: snap, name: "g"}))
	fk := table(mustInspectMySQL(t, db), "g").ForeignKeys
	if len(fk) != 2 || fk[0].OnDelete != "RESTRICT" || fk[1].OnDelete != "" {
		t.Errorf("keys %+v", fk)
	}
	swapped := migrate.Clone(snap)
	swapped.Tables[0].ForeignKeys[0].OnDelete, swapped.Tables[0].ForeignKeys[1].OnDelete = "", "RESTRICT"
	for _, s := range []*migrate.Schema{snap, swapped} {
		r, err := migrate.Drift(context.Background(), db, s)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Changes) != 0 || len(r.Warnings) != 0 {
			t.Errorf("drift: %v, %v", r.Changes, r.Warnings)
		}
	}
}

// TestInspectMySQLEngine: a table that is not InnoDB is a note, and so a
// drift warning that is no change.
func TestInspectMySQLEngine(t *testing.T) {
	needMySQL(t)
	ctx := context.Background()
	db, _ := freshDB(t, velox.MySQL)
	mustExec(t, db, "CREATE TABLE m (id int PRIMARY KEY) ENGINE=MyISAM")
	s, notes, err := migrate.InspectNotes(ctx, db, velox.MySQL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Table != (migrate.TableRef{Name: "m"}) || notes[0].Object != "m" || !strings.Contains(notes[0].Message, "MyISAM") {
		t.Fatalf("notes %+v", notes)
	}
	r, err := migrate.Drift(ctx, db, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) != 0 || len(r.Warnings) != 1 || r.Warnings[0].Object != "m" {
		t.Errorf("drift: %v, %v", r.Changes, r.Warnings)
	}
}

// TestInspectMySQLFilter: nil reads every table of DATABASE() but the
// history table; a named table that is not there, or a view, is absent; a
// table of another database is read only when named with it, and its
// foreign key to DATABASE() names its table without a schema.
func TestInspectMySQLFilter(t *testing.T) {
	needMySQL(t)
	db, _ := freshDB(t, velox.MySQL)
	mustUp(t, db, dirOf(t, velox.MySQL, files("0001_tables.sql", "CREATE TABLE b (id integer PRIMARY KEY);\nCREATE TABLE a (id integer PRIMARY KEY, b_id integer, CONSTRAINT a_b FOREIGN KEY (b_id) REFERENCES b (id));\n")))
	var name string
	if err := db.QueryRow("SELECT DATABASE()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	other := fmt.Sprintf("velox_other_%d_%d", os.Getpid(), time.Now().UnixNano())
	mysqlDatabase(t, other)
	mustExec(t, db, "CREATE TABLE `"+other+"`.c (id integer PRIMARY KEY, b_id integer, CONSTRAINT c_b FOREIGN KEY (b_id) REFERENCES `"+name+"`.b (id) ON DELETE CASCADE)")
	mustExec(t, db, "CREATE VIEW v AS SELECT 1 AS x")

	names := func(s *migrate.Schema) []string {
		var out []string
		for _, tb := range s.Tables {
			out = append(out, tb.Ref().String())
		}
		return out
	}
	s := mustInspectMySQL(t, db)
	if got := names(s); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("nil: %v", got)
	}
	if fk := s.Tables[0].ForeignKeys; len(fk) != 1 || fk[0].RefTable != (migrate.TableRef{Name: "b"}) {
		t.Errorf("a's keys %+v", fk)
	}
	if got := names(mustInspectMySQL(t, db, migrate.TableRef{Name: "missing"}, migrate.TableRef{Name: "a"}, migrate.TableRef{Schema: name, Name: "a"},
		migrate.TableRef{Name: "c"}, migrate.TableRef{Name: "v"})); !slices.Equal(got, []string{"a"}) {
		t.Errorf("named: %v", got)
	}
	c := mustInspectMySQL(t, db, migrate.TableRef{Schema: other, Name: "c"})
	if got := names(c); !slices.Equal(got, []string{other + ".c"}) {
		t.Fatalf("%s.c: %v", other, got)
	}
	want := []migrate.ForeignKey{{Name: "c_b", Columns: []string{"b_id"}, RefTable: migrate.TableRef{Name: "b"}, RefColumns: []string{"id"}, OnDelete: "CASCADE"}}
	if !reflect.DeepEqual(c.Tables[0].ForeignKeys, want) {
		t.Errorf("%s.c's keys %+v", other, c.Tables[0].ForeignKeys)
	}
}

// TestInspectMySQLUnheld: what the model cannot hold is one note per
// object naming it; an index over an expression or a prefix is left out,
// any other is read in the form the model has.
func TestInspectMySQLUnheld(t *testing.T) {
	needMySQL(t)
	ctx := context.Background()
	db, _ := freshDB(t, velox.MySQL)
	for _, q := range []string{
		"CREATE TABLE p (id bigint PRIMARY KEY, a varchar(64) NOT NULL, b int NOT NULL, c int NOT NULL, " +
			"pt point NOT NULL SRID 0, " +
			"g int AS (b + 1) STORED, " +
			"at datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6), " +
			"ci varchar(10) COLLATE utf8mb4_0900_ai_ci NOT NULL, " +
			"hid int INVISIBLE, " +
			"INDEX p_desc (b DESC, c), FULLTEXT INDEX p_ft (a), SPATIAL INDEX p_sp (pt), INDEX p_pre (a(10)), " +
			"INDEX p_expr ((b + 1)), INDEX p_inv (c) INVISIBLE, INDEX p_plain (a), " +
			"CONSTRAINT p_unenforced CHECK (b > 0) NOT ENFORCED" +
			") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin",
		"CREATE TABLE k (a varchar(20) NOT NULL, b int NOT NULL, PRIMARY KEY (a(5), b DESC)) COLLATE=utf8mb4_bin",
		"CREATE TABLE u (id bigint PRIMARY KEY, p_id bigint, INDEX u_p_id (p_id), CONSTRAINT u_p FOREIGN KEY (p_id) REFERENCES p (id) ON UPDATE CASCADE) COLLATE=utf8mb4_bin",
	} {
		mustExec(t, db, q)
	}
	s, notes, err := migrate.InspectNotes(ctx, db, velox.MySQL, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"p.p_desc":       {"DESC"},
		"p.p_ft":         {"FULLTEXT"},
		"p.p_sp":         {"SPATIAL"},
		"p.p_pre":        {"prefix"},
		"p.p_expr":       {"expression"},
		"p.p_inv":        {"invisible"},
		"p.g":            {"generated"},
		"p.at":           {"ON UPDATE CURRENT_TIMESTAMP(6)"},
		"p.ci":           {"utf8mb4_0900_ai_ci"},
		"p.hid":          {"invisible"},
		"p.p_unenforced": {"NOT ENFORCED"},
		"u.u_p":          {"ON UPDATE CASCADE"},
		"k.PRIMARY":      {"prefix a(5)", "DESC"},
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
	if !slices.Equal(indexes, []string{"p_desc", "p_ft", "p_inv", "p_plain", "p_sp"}) {
		t.Errorf("p's indexes %v", indexes)
	}
	if g := column(table(s, "p"), "g"); g.Default != "" {
		t.Errorf("the generated column's default %q", g.Default)
	}
	r, err := migrate.Drift(ctx, db, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) != 0 || len(r.Warnings) != len(want) {
		t.Errorf("drift: changes %v, warnings %v", r.Changes, r.Warnings)
	}
}

// TestDriftMySQLLive: a hand change to the database after Up is the change
// that undoes it, and what the model cannot hold is a warning.
func TestDriftMySQLLive(t *testing.T) {
	needMySQL(t)
	for _, c := range []struct {
		name    string
		alter   []string
		changes []string
		warns   []string
	}{
		{name: "type", alter: []string{"ALTER TABLE g MODIFY COLUMN kind varchar(20) NOT NULL DEFAULT 'a' COMMENT 'it''s a \\\\ kind'"},
			changes: []string{"alter-column g.kind (type)"}},
		{name: "nullability", alter: []string{"ALTER TABLE g MODIFY COLUMN qty int NULL DEFAULT -1"},
			changes: []string{"alter-column g.qty (nullable)"}},
		{name: "default", alter: []string{"ALTER TABLE g ALTER COLUMN kind SET DEFAULT 'b'"},
			changes: []string{"alter-column g.kind (default)"}},
		{name: "time-default", alter: []string{"ALTER TABLE g ALTER COLUMN stamp SET DEFAULT '2020-01-01 00:00:01'"},
			changes: []string{"alter-column g.stamp (default)"}},
		{name: "on-delete", alter: []string{"ALTER TABLE g DROP FOREIGN KEY g_parent", "ALTER TABLE g ADD CONSTRAINT g_parent FOREIGN KEY (parent_id) REFERENCES g (id) ON DELETE CASCADE"},
			changes: []string{"drop-foreign-key g.g_parent", "add-foreign-key g.g_parent"}},
		{name: "check", alter: []string{"ALTER TABLE g DROP CHECK g_qty_check", "ALTER TABLE g ADD CONSTRAINT g_qty_check CHECK (qty >= -6 OR qty IS NULL)"},
			changes: []string{"drop-check g.g_qty_check", "add-check g.g_qty_check"}},
		{name: "enum-label", alter: []string{"ALTER TABLE g MODIFY COLUMN role enum('a','b','o''neil','c\\\\d','e') NOT NULL DEFAULT 'o''neil'"},
			changes: []string{"alter-column g.role (type)"}},
		{name: "fulltext", alter: []string{"ALTER TABLE g DROP INDEX g_motto", "ALTER TABLE g ADD FULLTEXT INDEX g_motto (motto)"},
			warns: []string{"g.g_motto"}},
		{name: "desc", alter: []string{"ALTER TABLE g DROP INDEX g_created_on, ADD INDEX g_created_on (created_on DESC)"},
			warns: []string{"g.g_created_on"}},
		{name: "on-update", alter: []string{"ALTER TABLE g DROP FOREIGN KEY g_parent", "ALTER TABLE g ADD CONSTRAINT g_parent FOREIGN KEY (parent_id) REFERENCES g (id) ON DELETE SET NULL ON UPDATE CASCADE"},
			warns: []string{"g.g_parent"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			db, _ := freshDB(t, velox.MySQL)
			mustUp(t, db, mysqlDir(t))
			for _, q := range c.alter {
				mustExec(t, db, q)
			}
			r, err := migrate.Drift(context.Background(), db, mysqlSchema())
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

// TestInspectMySQLBinaryDefaults: a binary or varbinary column's literal
// default is read from SHOW CREATE TABLE, information_schema cutting it at
// its first NUL; a binary type's expression and an ASCII _latin1 literal
// are the model's. Another default set by hand is a change.
func TestInspectMySQLBinaryDefaults(t *testing.T) {
	needMySQL(t)
	s := &migrate.Schema{Dialect: velox.MySQL, Tables: []migrate.Table{{
		Name: "bins",
		Columns: []migrate.Column{
			{Name: "id", Type: "bigint", Identity: true},
			{Name: "code", Type: "varbinary(8)", Default: "'a'"},
			{Name: "hex", Type: "varbinary(8)", Default: "0x61"},
			{Name: "uid", Type: "binary(16)", Default: "(UUID_TO_BIN(UUID()))"},
			{Name: "pad", Type: "binary(2)", Default: "'a'"},
			{Name: "quote", Type: "varbinary(8)", Default: "''''"},
			{Name: "data", Type: "blob", Default: "('a')"},
			{Name: "latin", Type: "text", Default: "(_latin1'a')"},
		},
		PrimaryKey:     []string{"id"},
		PrimaryKeyName: "PRIMARY",
		Collation:      "utf8mb4_bin",
	}}}
	dir := dirOf(t, velox.MySQL, step{to: s, name: "bins"})
	noDriftMySQL(t, dir, s)

	db, dsn := freshDB(t, velox.MySQL)
	mustUp(t, db, dir)
	mustExec(t, db, "ALTER TABLE bins ADD COLUMN scratch varbinary(8) DEFAULT 0x00ff")
	var name, create string
	if err := db.QueryRow("SHOW CREATE TABLE bins").Scan(&name, &create); err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(create) {
		if strings.Contains(line, "`pad`") || strings.Contains(line, "`scratch`") || strings.Contains(line, "`latin`") {
			t.Logf("SHOW CREATE TABLE: %s", strings.TrimSpace(line))
		}
	}
	mustExec(t, db, "ALTER TABLE bins DROP COLUMN scratch")

	// SHOW CREATE TABLE runs for bins, which has a binary default, and not
	// for mysqlSchema's g, which has none.
	gdb, _ := freshDB(t, velox.MySQL)
	mustUp(t, gdb, mysqlDir(t))
	for _, c := range []struct {
		db    *sql.DB
		table string
		want  int
	}{{db, "bins", 1}, {gdb, "g", 0}} {
		_, queries, err := migrate.InspectMySQLQueries(context.Background(), c.db, []migrate.TableRef{{Name: c.table}})
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, q := range queries {
			if strings.HasPrefix(q, "SHOW CREATE TABLE") {
				n++
			}
		}
		if n != c.want {
			t.Errorf("%s: %d SHOW CREATE TABLE, want %d", c.table, n, c.want)
		}
	}

	// A session whose SHOW CREATE TABLE quotes names with " or leaves them
	// bare finds no drift either.
	for _, set := range []string{
		"SET SESSION sql_mode = CONCAT(@@sql_mode, ',ANSI_QUOTES')",
		"SET SESSION sql_quote_show_create = OFF",
	} {
		one := openDB(t, "mysql", dsn)
		one.SetMaxOpenConns(1)
		mustExec(t, one, set)
		r, err := migrate.Drift(context.Background(), one, s)
		if err != nil {
			t.Fatalf("%s: %v", set, err)
		}
		if len(r.Changes) > 0 || len(r.Warnings) > 0 {
			t.Errorf("%s: changes %v, warnings %v", set, r.Changes, r.Warnings)
		}
	}

	mustExec(t, db, "ALTER TABLE bins ALTER COLUMN code SET DEFAULT 0x6100FF")
	r, err := migrate.Drift(context.Background(), db, s)
	if err != nil {
		t.Fatal(err)
	}
	var changes []string
	for _, ch := range r.Changes {
		changes = append(changes, ch.String())
	}
	if want := []string{"alter-column bins.code (default)"}; !slices.Equal(changes, want) || len(r.Warnings) > 0 {
		t.Errorf("changes %q, warnings %v; want %q", changes, r.Warnings, want)
	}
}

// TestInspectMySQLDoesNotMutate: Inspect leaves the tables it is given as
// they were, and Drift its snapshot; each gives the same twice.
func TestInspectMySQLDoesNotMutate(t *testing.T) {
	needMySQL(t)
	db, _ := freshDB(t, velox.MySQL)
	mustUp(t, db, mysqlDir(t))
	tables := []migrate.TableRef{{Name: "missing"}, {Name: "g"}}
	was := slices.Clone(tables)
	a := mustInspectMySQL(t, db, tables...)
	if !slices.Equal(tables, was) {
		t.Errorf("tables %v, was %v", tables, was)
	}
	b := mustInspectMySQL(t, db, tables...)
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if len(a.Tables) != 1 || !reflect.DeepEqual(a, b) || string(ja) != string(jb) {
		t.Errorf("two inspections differ:\n%s\n%s", ja, jb)
	}

	snapshot := mysqlSchema()
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
