package migrate_test

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/codegen"
	"github.com/syssam/velox/internal/codegen/loadjson"
	"github.com/syssam/velox/migrate"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// sqlFiles is every .sql file of fsys with its text, for a failure's
// message.
func sqlFiles(fsys fs.FS) string {
	var b strings.Builder
	paths, _ := fs.Glob(fsys, "*.sql")
	for _, p := range paths {
		data, _ := fs.ReadFile(fsys, p)
		b.WriteString("--- " + p + "\n" + string(data))
	}
	return b.String()
}

// reportText is a drift report's changes and warnings, one a line.
func reportText(r *migrate.DriftReport) string {
	var b strings.Builder
	for _, c := range r.Changes {
		fmt.Fprintf(&b, "change: %v\n", c)
	}
	for _, w := range r.Warnings {
		b.WriteString("warning: " + w.String() + "\n")
	}
	return b.String()
}

// invariants checks §17's three after an apply of fsys: Drift against the
// model finds no change; Diff of the directory's snapshot and the model is
// empty; Drift against the snapshot finds no change and no warning, the
// notes' warnings included.
func invariants(t *testing.T, db *sql.DB, d velox.Dialect, fsys fs.FS, model *migrate.Schema) {
	t.Helper()
	ctx := context.Background()
	fail := func(what string, r *migrate.DriftReport) {
		t.Helper()
		msg := what
		if r != nil {
			msg += "\n" + reportText(r)
		}
		t.Errorf("%s on %v\nscripts:\n%s", msg, d, sqlFiles(fsys))
	}
	r, err := migrate.Drift(ctx, db, model)
	if err != nil {
		t.Fatalf("Drift against the model: %v", err)
	}
	if len(r.Changes) > 0 {
		fail("Drift against the model finds changes", r)
	}
	dir, err := migrate.ReadDir(fsys)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := migrate.Diff(dir.Snapshot, model, migrate.DiffOptions{})
	if err != nil {
		t.Fatalf("Diff of the snapshot and the model: %v", err)
	}
	if len(cs) > 0 {
		fail(fmt.Sprintf("Diff of the snapshot and the model: %v", cs), nil)
	}
	r, err = migrate.Drift(ctx, db, dir.Snapshot)
	if err != nil {
		t.Fatalf("Drift against the snapshot: %v", err)
	}
	if len(r.Changes) > 0 || len(r.Warnings) > 0 {
		fail("Drift against the snapshot is not empty", r)
	}
}

// otherDatabases makes, on MySQL, the server-wide databases the tables of
// ss name; PostgreSQL's plans create their schemas.
func otherDatabases(t *testing.T, d velox.Dialect, ss ...*migrate.Schema) {
	t.Helper()
	if d != velox.MySQL {
		return
	}
	seen := map[string]bool{}
	for _, s := range ss {
		for _, tb := range s.Tables {
			if tb.Schema != "" && !seen[tb.Schema] {
				seen[tb.Schema] = true
				mysqlDatabase(t, tb.Schema)
			}
		}
	}
}

// fromDir is the directory of from: its plan, which creates the sequences
// its serial columns own.
func fromDir(t *testing.T, d velox.Dialect, from *migrate.Schema) fstest.MapFS {
	t.Helper()
	return dirOf(t, d, step{to: from, name: "from"})
}

// migrated is what migrateCase leaves: the database, its directory, and
// the models.
type migrated struct {
	db       *sql.DB
	fsys     fstest.MapFS
	from, to *migrate.Schema
}

// migrateCase runs c on d as TestMigrateCases does: from applied and
// checked, a row in each table, the plan to to applied and checked, and the
// rows that to keeps read back the same.
func migrateCase(t *testing.T, c testCase, d velox.Dialect) migrated {
	t.Helper()
	from, to := models(t, c, d)
	return migrateSchemas(t, d, from, to, migrate.PlanOptions{Diff: c.opts, Allow: c.allow})
}

// migrateSchemas is migrateCase's steps for the models from and to.
func migrateSchemas(t *testing.T, d velox.Dialect, from, to *migrate.Schema, opts migrate.PlanOptions) migrated {
	t.Helper()
	db, _ := freshDB(t, d)
	otherDatabases(t, d, from, to)
	fsys := fromDir(t, d, from)
	mustUp(t, db, fsys)
	invariants(t, db, d, fsys, from)

	plan, err := migrate.NewPlan(from, to, opts)
	if err != nil {
		t.Fatal(err)
	}
	// A required column added to a table with rows fails, as its finding
	// says; that plan runs on empty tables.
	rows := !slices.ContainsFunc(plan.Findings, func(f migrate.Finding) bool { return f.Code == "add-required-column" })
	var before map[migrate.TableRef]map[string]string
	if rows {
		insertRows(t, db, d, from)
		before = rowsOf(t, db, d, from)
	}
	fsys = extend(t, fsys, d, step{to: to, opts: opts, name: "to"})
	if _, err := migrate.Up(context.Background(), db, fsys); err != nil {
		t.Fatalf("Up to to: %v\nscripts:\n%s", err, sqlFiles(fsys))
	}
	invariants(t, db, d, fsys, to)
	if rows {
		keptRows(t, db, d, opts.Diff, from, to, before)
	}
	return migrated{db: db, fsys: fsys, from: from, to: to}
}

// keptRows checks that each table of from that to keeps (renames followed)
// still has its row, and each column it keeps reads the same text.
func keptRows(t *testing.T, db *sql.DB, d velox.Dialect, opts migrate.DiffOptions, from, to *migrate.Schema, before map[migrate.TableRef]map[string]string) {
	t.Helper()
	kept := &migrate.Schema{Dialect: d}
	for _, tb := range from.Tables {
		ref := tb.Ref()
		if r, ok := opts.RenameTables[ref]; ok {
			ref = r
		}
		if tt, ok := to.Table(ref); ok {
			kept.Tables = append(kept.Tables, *tt)
		}
	}
	after := rowsOf(t, db, d, kept)
	for _, tb := range from.Tables {
		ref := tb.Ref()
		if r, ok := opts.RenameTables[ref]; ok {
			ref = r
		}
		got, ok := after[ref]
		if !ok {
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(before[tb.Ref()])) {
			col := name
			if r, ok := opts.RenameColumns[migrate.ColumnRef{Table: tb.Ref(), Column: name}]; ok {
				col = r
			}
			v, ok := got[col]
			if !ok {
				continue
			}
			if want := before[tb.Ref()][name]; v != want {
				t.Errorf("%s.%s reads %q after the migration; want %q", ref, col, v, want)
			}
		}
	}
}

// TestMigrateCases: every case on each of its dialects, from a fresh
// database: from applied, with a row in each table, then the plan to to;
// the three invariants hold after each apply, and the rows survive. The cases
// run in parallel: each has a database of its own.
func TestMigrateCases(t *testing.T) {
	t.Parallel()
	for _, d := range allDialects {
		for _, c := range cases {
			if !slices.Contains(c.on(), d) {
				continue
			}
			t.Run(c.name+"."+d.String(), func(t *testing.T) {
				t.Parallel()
				migrateCase(t, c, d)
			})
		}
	}
}

// golden is the render golden of a case on d.
func golden(t *testing.T, name string, d velox.Dialect) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "render", name+"."+d.String()+".sql"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestMigrateMySQLCombined covers the follow-up "MySQL's combined ALTER
// TABLEs": the goldens hold the combined statements, and each applies on
// MySQL with rows.
func TestMigrateMySQLCombined(t *testing.T) {
	t.Parallel()
	want := map[string][]string{
		"literals": {"ALTER TABLE `users` ADD COLUMN `motto` varchar(255) NOT NULL DEFAULT 'it''s a \\\\ test' COMMENT 'it''s a \\\\ comment', MODIFY COLUMN `role` enum('admin','viewer','o''neil') NOT NULL DEFAULT 'viewer';"},
		"reuse-dropped-name": {
			"ALTER TABLE `posts` DROP FOREIGN KEY `shared_fk`;",
			"ALTER TABLE `users` ADD INDEX `shared_idx` (`created_at`)",
			"ALTER TABLE `users` ADD CONSTRAINT `shared_fk` FOREIGN KEY (`mentor_id`)",
		},
		"rename-table": {
			"ALTER TABLE `users` DROP FOREIGN KEY `users_orgs_users`;",
			"ALTER TABLE `accounts` ADD CONSTRAINT `accounts_orgs_users` FOREIGN KEY (`org_id`)",
		},
		"drop-column-in-unique-key": {
			"ALTER TABLE `users` DROP FOREIGN KEY `users_orgs_users`;",
			"ALTER TABLE `users` ADD CONSTRAINT `users_orgs_users` FOREIGN KEY (`org_id`)",
		},
	}
	for _, name := range slices.Sorted(maps.Keys(want)) {
		text := golden(t, name, velox.MySQL)
		last := -1
		for _, s := range want[name] {
			i := strings.Index(text, s)
			if i < 0 || i < last {
				t.Errorf("%s.mysql.sql: no %q after the statements before it:\n%s", name, s, text)
			}
			last = i
		}
	}
	needMySQL(t)
	for _, name := range slices.Sorted(maps.Keys(want)) {
		t.Run(name, func(t *testing.T) { migrateCase(t, caseNamed(name), velox.MySQL) })
	}
	// An index dropped and added under one name, in one statement.
	t.Run("index-redefined", func(t *testing.T) {
		c := testCase{name: "index-redefined", to: func(m *loadjson.Model) {
			addField(m, "User", field.String("legacy").Optional().Default(""))
			ent(m, "User").Indexes[0].Fields = []string{"name", "legacy"}
		}}
		want := "ALTER TABLE `users` DROP INDEX `users_by_name`, ADD INDEX `users_by_name` (`name`, `legacy`)"
		if got := strings.Join(sqls(caseScripts(t, c, velox.MySQL)), "\n"); !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
		migrateCase(t, c, velox.MySQL)
	})
}

// TestMigrateFKIndexReplacedMySQL: a declared index replaces the one MySQL
// made for a foreign key, then goes again, on MySQL with rows, without error
// 1553 (ruling 46).
func TestMigrateFKIndexReplacedMySQL(t *testing.T) {
	t.Parallel()
	needMySQL(t)
	d := velox.MySQL
	base := mustTables(d, nil)
	with := mustTables(d, withFKIndex)
	db, _ := freshDB(t, d)
	fsys := dirOf(t, d, step{to: base, name: "base"})
	mustUp(t, db, fsys)
	insertRows(t, db, d, base)
	want := rowsOf(t, db, d, base)
	for i, s := range []*migrate.Schema{with, base} {
		fsys = extend(t, fsys, d, step{to: s, name: fmt.Sprintf("step%d", i)})
		if _, err := migrate.Up(context.Background(), db, fsys); err != nil {
			t.Fatalf("step %d: %v (error %d)\n%s", i, err, mysqlNumber(err), sqlFiles(fsys))
		}
		invariants(t, db, d, fsys, s)
	}
	if got := rowsOf(t, db, d, base); !maps.EqualFunc(got, want, maps.Equal) {
		t.Errorf("rows %v; want %v", got, want)
	}
}

// TestMigrateSQLiteDropCycle covers the follow-up on SQLite's drop cycle:
// two tables that reference each other, with rows, are dropped through Up
// with foreign keys off, and foreign keys are on after; the same file with
// an orphan row added fails foreign_key_check and is rolled back.
func TestMigrateSQLiteDropCycle(t *testing.T) {
	t.Parallel()
	d := velox.SQLite
	from := mustTables(d, caseNamed("create-tables").to)
	to := mustTables(d, nil)
	setup := func(t *testing.T) (*sql.DB, fstest.MapFS) {
		db, _ := freshDB(t, d)
		fsys := dirOf(t, d, step{to: from, name: "from"})
		mustUp(t, db, fsys)
		insertRows(t, db, d, from)
		var a, b int
		if err := db.QueryRow(`SELECT beta_id FROM alphas`).Scan(&a); err != nil || a != 1 {
			t.Fatalf("alphas.beta_id %d, %v", a, err)
		}
		if err := db.QueryRow(`SELECT alpha_id FROM beta`).Scan(&b); err != nil || b != 1 {
			t.Fatalf("beta.alpha_id %d, %v", b, err)
		}
		return db, extend(t, fsys, d, step{to: to, name: "drop", opts: migrate.PlanOptions{Allow: allow("drop-table")}})
	}
	plan := func(fsys fstest.MapFS) string {
		paths, _ := fs.Glob(fsys, "0002_*.sql")
		if len(paths) != 1 {
			t.Fatalf("plan files %v", paths)
		}
		return paths[0]
	}
	t.Run("applied", func(t *testing.T) {
		db, fsys := setup(t)
		if !strings.Contains(string(fsys[plan(fsys)].Data), "-- velox:foreign-keys off") {
			t.Fatalf("foreign keys stay on:\n%s", sqlFiles(fsys))
		}
		mustUp(t, db, fsys)
		invariants(t, db, d, fsys, to)
		if hasTable(t, db, "alphas") || hasTable(t, db, "beta") {
			t.Error("the tables are still there")
		}
		var on int
		if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&on); err != nil || on != 1 {
			t.Errorf("foreign_keys %d, %v; want 1", on, err)
		}
	})
	t.Run("orphan", func(t *testing.T) {
		db, fsys := setup(t)
		p := plan(fsys)
		fsys[p] = &fstest.MapFile{Data: append(fsys[p].Data, "INSERT INTO \"posts\" (\"author_id\", \"title\", \"status\") VALUES (999, 't', 'active');\n"...)}
		sum, err := migrate.Sum(fsys)
		if err != nil {
			t.Fatal(err)
		}
		fsys["velox.sum"] = &fstest.MapFile{Data: sum}
		_, err = migrate.Up(context.Background(), db, fsys)
		if err == nil || !strings.Contains(err.Error(), "foreign") {
			t.Fatalf("Up: %v; want a foreign key failure", err)
		}
		if !hasTable(t, db, "alphas") || !hasTable(t, db, "beta") {
			t.Error("the drops were not rolled back")
		}
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM posts`).Scan(&n); err != nil || n != 1 {
			t.Errorf("posts %d, %v; want the 1 row from before", n, err)
		}
		if rows := historyOf(t, db); len(rows) != 1 {
			t.Errorf("history %+v; want version 1 alone", rows)
		}
	})
}

// pgRows makes n rows in orgs, users and posts on PostgreSQL: row i has ID
// i (10 for the last, past a gap), org and author i, and role(i) and
// status(i).
func pgRows(t *testing.T, db *sql.DB, n int, role, status func(i int) string) []int {
	t.Helper()
	var ids []int
	for i := 1; i <= n; i++ {
		id := i
		if i == n {
			id = 10
		}
		ids = append(ids, id)
		mustExec(t, db, fmt.Sprintf(`INSERT INTO orgs (id, name) VALUES (%d, 'o%d')`, id, id))
		mustExec(t, db, fmt.Sprintf(`INSERT INTO users (id, org_id, name, email, bio, role, status, logins, created_at) VALUES (%d, %d, 'u%d', 'u%d@x', 'b', '%s', '%s', %d, '2026-01-01 00:00:00+00')`,
			id, id, id, id, role(i), status(i), 2147483647-i))
		mustExec(t, db, fmt.Sprintf(`INSERT INTO posts (id, author_id, title, status) VALUES (%d, %d, 't%d', '%s')`, id, id, id, status(i)))
	}
	return ids
}

// pgStrings is the single text column of q's rows.
func pgStrings(t *testing.T, db *sql.DB, q string) []string {
	t.Helper()
	rows, err := db.Query(q)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestMigrateRowsSurvive covers the follow-up "the rows", on PostgreSQL
// with 3 rows a table: casts keep the values, an enum swap keeps the labels
// the rows hold, an identity added to a plain or serial key starts past the
// largest ID, and SET NOT NULL validates over rows with no NULL.
func TestMigrateRowsSurvive(t *testing.T) {
	t.Parallel()
	needPostgres(t)
	d := velox.Postgres
	constant := func(v string) func(int) string { return func(int) string { return v } }
	alternate := func(a, b string) func(int) string {
		return func(i int) string {
			if i%2 == 0 {
				return b
			}
			return a
		}
	}
	// nextID: an insert without id gets the largest ID plus one.
	nextID := func(t *testing.T, db *sql.DB, ids []int) {
		t.Helper()
		var id int
		if err := db.QueryRow(`INSERT INTO users (org_id, name, email, created_at) VALUES (1, 'n', 'n@x', now()) RETURNING id`).Scan(&id); err != nil || id != slices.Max(ids)+1 {
			t.Errorf("the next ID is %d, %v; want %d", id, err, slices.Max(ids)+1)
		}
	}
	const dump = `SELECT u.id || ' ' || u.org_id || ' ' || u.name || ' ' || u.email || ' ' || u.role || ' ' || u.status || ' ' || u.logins || ' ' || p.status || ' ' || o.id FROM users u JOIN posts p ON p.author_id = u.id JOIN orgs o ON o.id = u.org_id ORDER BY u.id`
	for _, tc := range []struct {
		name         string
		role, status func(int) string
		check        func(t *testing.T, db *sql.DB, ids []int)
	}{
		{name: "key-type-change"},
		{name: "widen-int"},
		{name: "enum-remove", role: constant("viewer")},
		{name: "enum-reorder", role: alternate("admin", "viewer")},
		{name: "enum-shared", status: alternate("active", "pending")},
		{name: "add-identity", check: nextID},
		{name: "serial-to-identity", check: nextID},
		{name: "set-not-null", check: func(t *testing.T, db *sql.DB, ids []int) {
			var nullable string
			if err := db.QueryRow(`SELECT is_nullable FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'bio'`).Scan(&nullable); err != nil || nullable != "NO" {
				t.Errorf("bio is_nullable %s, %v", nullable, err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := caseNamed(tc.name)
			if tc.role == nil {
				tc.role = constant("viewer")
			}
			if tc.status == nil {
				tc.status = constant("active")
			}
			from, to := models(t, c, d)
			db, _ := freshDB(t, d)
			fsys := fromDir(t, d, from)
			mustUp(t, db, fsys)
			ids := pgRows(t, db, 3, tc.role, tc.status)
			want := pgStrings(t, db, dump)
			fsys = extend(t, fsys, d, step{to: to, name: "to", opts: migrate.PlanOptions{Diff: c.opts, Allow: c.allow}})
			if _, err := migrate.Up(context.Background(), db, fsys); err != nil {
				t.Fatalf("Up: %v\n%s", err, sqlFiles(fsys))
			}
			invariants(t, db, d, fsys, to)
			if got := pgStrings(t, db, dump); !slices.Equal(got, want) {
				t.Errorf("rows\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
			if tc.check != nil {
				tc.check(t, db, ids)
			}
		})
	}
}

// TestMigrateRowsSurvivePostgresUnits: an online file of two units, with
// rows, fails in its second; the resume runs that unit again and leaves the
// first's index as it was built.
func TestMigrateRowsSurvivePostgresUnits(t *testing.T) {
	t.Parallel()
	needPostgres(t)
	d := velox.Postgres
	from := mustTables(d, nil)
	to := mustTables(d, func(m *loadjson.Model) {
		fieldOf(m, "User", "name").Unique = true
		fieldOf(m, "Org", "name").Unique = true
	})
	db, _ := freshDB(t, d)
	fsys := dirOf(t, d, step{to: from, name: "from"})
	mustUp(t, db, fsys)
	pgRows(t, db, 3, func(int) string { return "viewer" }, func(int) string { return "active" })
	mustExec(t, db, `UPDATE users SET name = 'same'`) // the second unit's index fails
	fsys = extend(t, fsys, d, step{to: to, name: "to", opts: migrate.PlanOptions{Allow: allow("add-unique")}})
	_, err := migrate.Up(context.Background(), db, fsys)
	de := dirtyOf(t, err)
	if code, _ := velox.DriverCode(err); code != "23505" || de.Statement != 4 {
		t.Fatalf("%v at statement %d; want a unique violation at statement 4\n%s", err, de.Statement, sqlFiles(fsys))
	}
	rows := historyOf(t, db)
	if last := rows[len(rows)-1]; !last.Dirty || last.Statements != 2 {
		t.Fatalf("history %+v; want the first unit's 2 statements", rows)
	}
	oid := pgStrings(t, db, `SELECT 'orgs_name_key'::regclass::oid::text`)
	mustExec(t, db, `UPDATE users SET name = 'u' || id`)
	mustUp(t, db, fsys, migrate.WithResume())
	invariants(t, db, d, fsys, to)
	if again := pgStrings(t, db, `SELECT 'orgs_name_key'::regclass::oid::text`); !slices.Equal(again, oid) {
		t.Errorf("the first unit ran again: index %v, was %v", again, oid)
	}
	if n := pgStrings(t, db, `SELECT count(*)::text FROM users`); !slices.Equal(n, []string{"3"}) {
		t.Errorf("users %v; want 3", n)
	}
}

// TestMigrateLiterals: after literals, on each dialect, a row inserted
// without motto reads back the default with its quote and backslash, the enum
// value o'neil reads back, and so do the comments through Inspect (ruling
// 53).
func TestMigrateLiterals(t *testing.T) {
	t.Parallel()
	for _, d := range allDialects {
		t.Run(d.String(), func(t *testing.T) {
			m := migrateCase(t, caseNamed("literals"), d)
			q := func(s string) string {
				if d == velox.MySQL {
					return strings.ReplaceAll(s, `"`, "`")
				}
				return s
			}
			for _, s := range []string{
				`INSERT INTO "users" ("id", "org_id", "name", "email", "bio", "created_at") VALUES (2, 1, 'n', 'n@x', 'b', '2026-01-01 00:00:00')`,
				`INSERT INTO "users" ("id", "org_id", "name", "email", "bio", "created_at", "role") VALUES (3, 1, 'm', 'm@x', 'b', '2026-01-01 00:00:00', 'o''neil')`,
			} {
				mustExec(t, m.db, q(s))
			}
			var motto, role string
			if err := m.db.QueryRow(q(`SELECT "motto" FROM "users" WHERE "name" = 'n'`)).Scan(&motto); err != nil || motto != `it's a \ test` {
				t.Errorf("motto %q, %v; want %q", motto, err, `it's a \ test`)
			}
			if err := m.db.QueryRow(q(`SELECT "role" FROM "users" WHERE "name" = 'm'`)).Scan(&role); err != nil || role != "o'neil" {
				t.Errorf("role %q, %v; want o'neil", role, err)
			}
			if d == velox.SQLite {
				return // SQLite keeps no comments
			}
			s, err := migrate.Inspect(context.Background(), m.db, d, []migrate.TableRef{users})
			if err != nil {
				t.Fatal(err)
			}
			tb, ok := s.Table(users)
			if !ok {
				t.Fatal("no users")
			}
			if got := column(tb, "motto").Comment; got != `it's a \ comment` {
				t.Errorf("motto's comment %q", got)
			}
		})
	}
}

// RowLimit is TestTablesRowLimit's 65535-byte table: the ID (8), n (4) and
// the NULL bitmap (1), a (8165*4+2), b (8164*4+2) and c (201+1).
type RowLimit struct{ schema.Schema }

func (RowLimit) Fields() []schema.Field {
	return []schema.Field{field.Int32("n").Optional().Nillable(), field.String("a").MaxLen(8165), field.String("b").MaxLen(8164), field.Bytes("c").MaxLen(201)}
}

// RowOver is RowLimit with c one byte longer: 65536 bytes.
type RowOver struct{ schema.Schema }

func (RowOver) Fields() []schema.Field {
	return []schema.Field{field.Int32("n").Optional().Nillable(), field.String("a").MaxLen(8165), field.String("b").MaxLen(8164), field.Bytes("c").MaxLen(202)}
}

// TestMigrateMySQLRowLimit: a table of exactly 65535 bytes builds, is
// created on MySQL 8.4 through Up and holds a row of full-length values;
// one byte more is refused at plan time (rulings 47 and 35).
func TestMigrateMySQLRowLimit(t *testing.T) {
	t.Parallel()
	d := velox.MySQL
	build := func(s schema.Interface) (*migrate.Schema, error) {
		g, err := codegen.Build(marshal(s))
		if err != nil {
			return nil, err
		}
		return codegen.Tables(g, d)
	}
	if _, err := build(RowOver{}); err == nil || !strings.Contains(err.Error(), "these need 65536") {
		t.Errorf("65536 bytes: %v; want the row limit's refusal", err)
	}
	s, err := build(RowLimit{})
	if err != nil {
		t.Fatalf("65535 bytes: %v", err)
	}
	needMySQL(t)
	db, _ := freshDB(t, d)
	fsys := dirOf(t, d, step{to: s, name: "row"})
	mustUp(t, db, fsys)
	invariants(t, db, d, fsys, s)
	mustExec(t, db, "INSERT INTO `row_limits` (`n`, `a`, `b`, `c`) VALUES (NULL, REPEAT('é', 8165), REPEAT('😀', 8164), REPEAT('x', 201))")
	var a, b, c int
	if err := db.QueryRow("SELECT CHAR_LENGTH(`a`), CHAR_LENGTH(`b`), LENGTH(`c`) FROM `row_limits`").Scan(&a, &b, &c); err != nil || a != 8165 || b != 8164 || c != 201 {
		t.Errorf("lengths %d %d %d, %v", a, b, c, err)
	}
}

// NaTeam has members, whose NULL-able key sets NoAction, and coaches, whose
// NULL-able key sets nothing: SET NULL, as ent's default.
type NaTeam struct{ schema.Schema }

func (NaTeam) Edges() []schema.Edge {
	return []schema.Edge{edge.To("members", NaMember.Type), edge.To("coached", NaMember.Type)}
}

type NaMember struct{ schema.Schema }

func (NaMember) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("team", NaTeam.Type).Ref("members").Unique().OnDelete(edge.NoAction),
		edge.From("coach", NaTeam.Type).Ref("coached").Unique(),
	}
}

// TestDriftNoAction: a database whose keys ent wrote, ON DELETE NO ACTION
// spelled out and ON DELETE SET NULL, has no drift from a schema whose
// edges set NoAction and nothing.
func TestDriftNoAction(t *testing.T) {
	t.Parallel()
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL} {
		t.Run(d.String(), func(t *testing.T) {
			g, err := codegen.Build(marshal(NaTeam{}, NaMember{}))
			if err != nil {
				t.Fatal(err)
			}
			model, err := codegen.Tables(g, d)
			if err != nil {
				t.Fatal(err)
			}
			db, _ := freshDB(t, d)
			fsys := fromDir(t, d, model)
			mustUp(t, db, fsys)
			members, ok := model.Table(migrate.TableRef{Name: "na_members"})
			if !ok || len(members.ForeignKeys) != 2 {
				t.Fatalf("na_members %+v", members)
			}
			q, drop := `"`, "DROP CONSTRAINT"
			if d == velox.MySQL {
				q, drop = "`", "DROP FOREIGN KEY"
			}
			for _, fk := range members.ForeignKeys {
				act := map[string]string{"": "NO ACTION", "SET NULL": "SET NULL"}[fk.OnDelete]
				if act == "" {
					t.Fatalf("%s: OnDelete %q", fk.Name, fk.OnDelete)
				}
				mustExec(t, db, fmt.Sprintf("ALTER TABLE %[1]sna_members%[1]s %[2]s %[1]s%[3]s%[1]s", q, drop, fk.Name))
				mustExec(t, db, fmt.Sprintf("ALTER TABLE %[1]sna_members%[1]s ADD CONSTRAINT %[1]s%[2]s%[1]s FOREIGN KEY (%[1]s%[3]s%[1]s) REFERENCES %[1]sna_teams%[1]s (%[1]sid%[1]s) ON DELETE %[4]s",
					q, fk.Name, fk.Columns[0], act))
			}
			r, err := migrate.Drift(context.Background(), db, model)
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Changes) > 0 {
				t.Errorf("drift:\n%s", reportText(r))
			}
		})
	}
}
