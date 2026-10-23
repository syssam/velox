package migrate_test

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/gen/loadjson"
	"github.com/syssam/velox2/migrate"
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/field"
)

// rendered are the dialects Render writes.
var rendered = []velox.Dialect{velox.MySQL, velox.SQLite}

func render(t *testing.T, from, to *migrate.Schema, cs []migrate.Change, opts migrate.RenderOptions) []migrate.Script {
	t.Helper()
	ss, err := migrate.Render(from, to, cs, opts)
	if err != nil {
		t.Fatal(err)
	}
	return ss
}

// caseScripts is the case rendered on d as the goldens render it.
func caseScripts(t *testing.T, c testCase, d velox.Dialect) []migrate.Script {
	t.Helper()
	from, to := models(t, c, d)
	return render(t, from, to, diff(t, from, to, c.opts), migrate.RenderOptions{LockTimeout: 5 * time.Second, Allow: c.allow})
}

var scriptNames = map[migrate.ScriptKind]string{migrate.MainScript: "main", migrate.EnumScript: "enum", migrate.OnlineScript: "online"}

// scriptsText is each script as "-- script: <kind>" and its file body.
func scriptsText(ss []migrate.Script) string {
	var b strings.Builder
	for _, s := range ss {
		b.WriteString("-- script: " + scriptNames[s.Kind] + "\n")
		b.WriteString(migrate.WriteScript(s))
	}
	return b.String()
}

// sqls is the SQL of every statement of ss, in order.
func sqls(ss []migrate.Script) []string {
	var out []string
	for _, s := range ss {
		for _, st := range s.Statements {
			out = append(out, st.SQL)
		}
	}
	return out
}

func TestRenderGolden(t *testing.T) {
	for _, d := range rendered {
		for _, c := range cases {
			if !slices.Contains(c.on(), d) {
				continue
			}
			got := scriptsText(caseScripts(t, c, d))
			path := filepath.Join("testdata", "render", c.name+"."+d.String()+".sql")
			if *update {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("%s differs from the scripts (run with -update to write it):\n%s", path, got)
			}
		}
	}
}

// TestRenderMySQLIndexStatement: a table's index changes are one statement
// that builds in place, the add first.
func TestRenderMySQLIndexStatement(t *testing.T) {
	for name, want := range map[string]string{
		"fk-index-replaced": "ALTER TABLE `users` ADD INDEX `users_org_id_created_at` (`org_id`, `created_at`), DROP INDEX `users_org_id`, ALGORITHM=INPLACE, LOCK=NONE",
		"fk-index-restored": "ALTER TABLE `users` ADD INDEX `users_org_id` (`org_id`), DROP INDEX `users_org_id_created_at`, ALGORITHM=INPLACE, LOCK=NONE",
	} {
		got := sqls(caseScripts(t, caseNamed(name), velox.MySQL))
		if !slices.Equal(got, []string{want}) {
			t.Errorf("%s:\n%s\nwant\n%s", name, strings.Join(got, "\n"), want)
		}
	}
}

// TestRenderMySQLOrder: the grouping keeps Diff's order. A unique index
// losing a dropped column goes before the column; a non-unique one is
// shrunk by MySQL and dropped with the adds; a foreign key whose only index
// goes first is dropped first and added after the new index.
func TestRenderMySQLOrder(t *testing.T) {
	for name, want := range map[string][]string{
		"drop-column-in-unique": {
			"ALTER TABLE `users` DROP INDEX `users_name_logins_unique`, ALGORITHM=INPLACE, LOCK=NONE",
			"ALTER TABLE `users` DROP COLUMN `logins`",
		},
		"drop-column-in-index": {
			"ALTER TABLE `users` DROP COLUMN `logins`",
			"ALTER TABLE `users` ADD INDEX `users_org_id` (`org_id`), DROP INDEX `users_org_id_logins`, ALGORITHM=INPLACE, LOCK=NONE",
		},
		"drop-column-in-unique-key": {
			"ALTER TABLE `users` DROP FOREIGN KEY `users_org_id_fkey`",
			"ALTER TABLE `users` DROP INDEX `users_org_id_logins_unique`, ALGORITHM=INPLACE, LOCK=NONE",
			"ALTER TABLE `users` DROP COLUMN `logins`",
			"ALTER TABLE `users` ADD INDEX `users_org_id` (`org_id`), ALGORITHM=INPLACE, LOCK=NONE",
			"ALTER TABLE `users` ADD CONSTRAINT `users_org_id_fkey` FOREIGN KEY (`org_id`) REFERENCES `orgs` (`id`)",
		},
		"rename-table": {
			"RENAME TABLE `users` TO `accounts`",
			"ALTER TABLE `accounts` RENAME INDEX `users_email_unique` TO `accounts_email_unique`, " +
				"RENAME INDEX `users_mentor_id` TO `accounts_mentor_id`, " +
				"RENAME INDEX `users_org_id` TO `accounts_org_id`, " +
				"DROP FOREIGN KEY `users_org_id_fkey`, " +
				"ADD CONSTRAINT `accounts_org_id_fkey` FOREIGN KEY (`org_id`) REFERENCES `orgs` (`id`)",
		},
		"on-delete-change": {
			"ALTER TABLE `posts` DROP FOREIGN KEY `posts_author_id_fkey`",
			"ALTER TABLE `posts` ADD CONSTRAINT `posts_author_id_fkey` FOREIGN KEY (`author_id`) REFERENCES `users` (`id`) ON DELETE RESTRICT",
		},
	} {
		got := sqls(caseScripts(t, caseNamed(name), velox.MySQL))
		if !slices.Equal(got, want) {
			t.Errorf("%s:\n%s\nwant\n%s", name, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
}

// TestRenderMySQLIndexRedefined: an index whose definition changes under
// its name is dropped and added in one statement, at the add.
func TestRenderMySQLIndexRedefined(t *testing.T) {
	from := mustTables(velox.MySQL, func(m *loadjson.Model) { addField(m, "User", field.String("legacy").Optional()) })
	to := mustTables(velox.MySQL, func(m *loadjson.Model) {
		addField(m, "User", field.String("legacy").Optional())
		ent(m, "User").Indexes[0].Fields = []string{"name", "legacy"}
	})
	cs := diff(t, from, to, migrate.DiffOptions{})
	want := []string{"ALTER TABLE `users` DROP INDEX `users_by_name`, ADD INDEX `users_by_name` (`name`, `legacy`), ALGORITHM=INPLACE, LOCK=NONE"}
	if got := sqls(render(t, from, to, cs, migrate.RenderOptions{})); !slices.Equal(got, want) {
		t.Errorf("got\n%s\nwant\n%s\nchanges\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"), strings.Join(lines(cs), "\n"))
	}
}

// Tag has a string ID.
type Tag struct{ schema.Schema }

func (Tag) Fields() []schema.Field {
	return []schema.Field{field.String("id"), field.String("label")}
}

func TestRenderSQLiteRebuild(t *testing.T) {
	ss := caseScripts(t, caseNamed("rebuild-parent"), velox.SQLite)
	if len(ss) != 1 || ss[0].Kind != migrate.MainScript || !ss[0].Directives.ForeignKeysOff {
		t.Fatalf("want one main script with foreign keys off, got %+v", ss)
	}
	want := []string{
		`CREATE TABLE "velox_new_users" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "org_id" integer NOT NULL,
  "name" text NOT NULL,
  "email" text NOT NULL,
  "bio" text,
  "role" text NOT NULL DEFAULT 'viewer',
  "status" text NOT NULL DEFAULT 'active',
  "logins" real NOT NULL DEFAULT 0,
  "created_at" datetime NOT NULL,
  "mentor_id" integer,
  CONSTRAINT "user_mentor" FOREIGN KEY ("mentor_id") REFERENCES "users" ("id"),
  CONSTRAINT "users_org_id_fkey" FOREIGN KEY ("org_id") REFERENCES "orgs" ("id"),
  CONSTRAINT "users_role_check" CHECK ("role" IN ('admin', 'viewer')),
  CONSTRAINT "users_status_check" CHECK ("status" IN ('active', 'banned', 'pending'))
)`,
		`INSERT INTO "velox_new_users" ("id", "org_id", "name", "email", "bio", "role", "status", "logins", "created_at", "mentor_id") SELECT "id", "org_id", "name", "email", "bio", "role", "status", "logins", "created_at", "mentor_id" FROM "users"`,
		`DELETE FROM sqlite_sequence WHERE name = 'velox_new_users'`,
		`INSERT INTO sqlite_sequence (name, seq) SELECT 'velox_new_users', seq FROM sqlite_sequence WHERE name = 'users'`,
		`DROP TABLE "users"`,
		`ALTER TABLE "velox_new_users" RENAME TO "users"`,
		`CREATE INDEX "users_by_name" ON "users" ("name")`,
		`CREATE UNIQUE INDEX "users_email_unique" ON "users" ("email")`,
		`CREATE INDEX "users_mentor_id" ON "users" ("mentor_id")`,
		`CREATE INDEX "users_org_id" ON "users" ("org_id")`,
	}
	if got := sqls(ss); !slices.Equal(got, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, ";\n"), strings.Join(want, ";\n"))
	}
	var reviews []string
	for _, st := range ss[0].Statements {
		if st.Review != "" {
			reviews = append(reviews, st.Review)
		}
	}
	if len(reviews) != 1 || !strings.HasPrefix(reviews[0], "sqlite-rebuild: ") || strings.Count(reviews[0], "sqlite-rebuild") != 1 {
		t.Errorf("reviews %q, want one sqlite-rebuild line", reviews)
	}

	// A table without AUTOINCREMENT has no counter to keep.
	from := mustTables(velox.SQLite, func(m *loadjson.Model) { addSchema(m, Tag{}) })
	to := mustTables(velox.SQLite, func(m *loadjson.Model) {
		addSchema(m, Tag{})
		fieldOf(m, "Tag", "label").Optional = true
		fieldOf(m, "Tag", "label").Nillable = true
	})
	got := sqls(render(t, from, to, diff(t, from, to, migrate.DiffOptions{}), migrate.RenderOptions{}))
	if len(got) == 0 || !strings.HasPrefix(got[0], `CREATE TABLE "velox_new_tags"`) || !strings.Contains(got[0], `PRIMARY KEY ("id")`) {
		t.Errorf("tags rebuild:\n%s", strings.Join(got, ";\n"))
	}
	for _, s := range got {
		if strings.Contains(s, "sqlite_sequence") || strings.Contains(s, "AUTOINCREMENT") {
			t.Errorf("a table with a string ID: %s", s)
		}
	}
}

// TestRenderSQLiteRebuildsWhereLintSays: Render rebuilds exactly the tables
// Lint's sqlite-rebuild findings name.
func TestRenderSQLiteRebuildsWhereLintSays(t *testing.T) {
	for _, c := range cases {
		if !slices.Contains(c.on(), velox.SQLite) {
			continue
		}
		from, to := models(t, c, velox.SQLite)
		cs := diff(t, from, to, c.opts)
		want := map[string]bool{}
		for _, f := range migrate.Lint(from, cs) {
			if f.Code == "sqlite-rebuild" {
				ref := migrate.TableRef{Name: f.Target}
				if r, ok := c.opts.RenameTables[ref]; ok {
					ref = r
				}
				want[ref.String()] = true
			}
		}
		got := map[string]bool{}
		for _, s := range sqls(render(t, from, to, cs, migrate.RenderOptions{})) {
			if name, ok := strings.CutPrefix(s, `CREATE TABLE "velox_new_`); ok {
				got[name[:strings.IndexByte(name, '"')]] = true
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: rebuilds %v, Lint says %v", c.name, got, want)
		}
	}
}

// TestRenderSQLiteDropCycle: dropping tables that reference each other turns
// foreign keys off, as the first one dropped is still referenced.
func TestRenderSQLiteDropCycle(t *testing.T) {
	from := mustTables(velox.SQLite, func(m *loadjson.Model) { addSchema(m, Alpha{}, Beta{}) })
	to := mustTables(velox.SQLite, nil)
	ss := render(t, from, to, diff(t, from, to, migrate.DiffOptions{}), migrate.RenderOptions{})
	if !ss[0].Directives.ForeignKeysOff {
		t.Errorf("foreign keys stay on:\n%s", scriptsText(ss))
	}
	if ss := caseScripts(t, caseNamed("drop-table"), velox.SQLite); ss[0].Directives.ForeignKeysOff {
		t.Errorf("drop-table turns foreign keys off:\n%s", scriptsText(ss))
	}
	execSQLite(t, from, to, migrate.DiffOptions{})
}

func TestRenderQuotesIdentifiers(t *testing.T) {
	for d, want := range map[velox.Dialect][]string{
		velox.MySQL:  {"`a``b`", "`a\"b`"},
		velox.SQLite: {"\"a`b\"", `"a""b"`},
	} {
		to := &migrate.Schema{Dialect: d, Tables: []migrate.Table{{
			Name:       "t",
			Columns:    []migrate.Column{{Name: "a`b", Type: "text"}, {Name: `a"b`, Type: "text"}},
			PrimaryKey: []string{"a`b"},
		}}}
		from := &migrate.Schema{Dialect: d}
		got := strings.Join(sqls(render(t, from, to, diff(t, from, to, migrate.DiffOptions{}), migrate.RenderOptions{})), "\n")
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("%v: no %s in\n%s", d, w, got)
			}
		}
	}
}

func TestRenderLiterals(t *testing.T) {
	for d, want := range map[velox.Dialect][]string{
		velox.MySQL:  {`DEFAULT 'it''s a \\ test'`, `COMMENT 'it''s a \\ comment'`, `'o''neil'`},
		velox.SQLite: {`DEFAULT 'it''s a \ test'`, `'o''neil'`},
	} {
		got := scriptsText(caseScripts(t, caseNamed("literals"), d))
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("%v: no %s in\n%s", d, w, got)
			}
		}
	}
}

func TestRenderDirectives(t *testing.T) {
	from := func(d velox.Dialect) *migrate.Schema {
		return mustTables(d, func(m *loadjson.Model) { addField(m, "User", field.String("legacy").Optional()) })
	}
	for _, d := range rendered {
		f, to := from(d), mustTables(d, nil)
		cs := diff(t, f, to, migrate.DiffOptions{})
		allowed := render(t, f, to, cs, migrate.RenderOptions{LockTimeout: 5 * time.Second, Allow: []migrate.Allow{{Code: "drop-column"}}})
		text := scriptsText(allowed)
		if !strings.Contains(text, "-- velox:allow drop-column users.legacy\n") {
			t.Errorf("%v: no allow directive:\n%s", d, text)
		}
		if want := []migrate.Allow{{Code: "drop-column", Target: "users.legacy"}}; !reflect.DeepEqual(allowed[0].Directives.Allow, want) {
			t.Errorf("%v: Allow %v, want %v", d, allowed[0].Directives.Allow, want)
		}
		if lt := strings.Contains(text, "-- velox:lock-timeout 5s\n"); lt != (d == velox.MySQL) {
			t.Errorf("%v: lock-timeout directive %v:\n%s", d, lt, text)
		}
		plain := scriptsText(render(t, f, to, cs, migrate.RenderOptions{}))
		if strings.Contains(plain, "velox:allow") || strings.Contains(plain, "velox:lock-timeout") {
			t.Errorf("%v: directives without options:\n%s", d, plain)
		}
	}
}

// TestRenderReviewLines: a statement's review line holds the review and
// destructive findings of its changes, on a change's first statement only.
func TestRenderReviewLines(t *testing.T) {
	ss := caseScripts(t, caseNamed("drop-column"), velox.MySQL)
	var got []string
	for _, st := range ss[0].Statements {
		got = append(got, st.Review)
	}
	want := []string{
		"drop-index: drops index users.users_email_unique; queries that used it get slower",
		"drop-column: drops column users.email and its data",
	}
	if !slices.Equal(got, want) {
		t.Errorf("reviews %q, want %q", got, want)
	}
}

func TestRenderUnits(t *testing.T) {
	for _, d := range rendered {
		for _, c := range cases {
			if !slices.Contains(c.on(), d) {
				continue
			}
			for _, s := range caseScripts(t, c, d) {
				for i, st := range s.Statements {
					want := 0
					if d == velox.MySQL {
						want = i
					}
					if st.Unit != want {
						t.Errorf("%s on %v: statement %d has unit %d, want %d", c.name, d, i, st.Unit, want)
					}
				}
			}
		}
	}
}

func TestRenderRefuses(t *testing.T) {
	s := func(d velox.Dialect) *migrate.Schema { return mustTables(d, nil) }
	for _, tc := range []struct {
		d velox.Dialect
		c migrate.Change
	}{
		{velox.MySQL, migrate.RenameSequence{Table: users, Column: "id", From: "a", To: "b"}},
		{velox.MySQL, migrate.CreateEnum{Enum: migrate.Enum{Name: "e", Values: []string{"a"}}}},
		{velox.SQLite, migrate.SetTableComment{Table: users, To: "x"}},
		{velox.SQLite, migrate.SetTableCollation{Table: users, To: "x"}},
		{velox.SQLite, migrate.AlterColumn{Table: users, What: migrate.ColumnComment}},
	} {
		_, err := migrate.Render(s(tc.d), s(tc.d), []migrate.Change{tc.c}, migrate.RenderOptions{})
		var e *migrate.Error
		if !errors.As(err, &e) || e.Code != "render" {
			t.Errorf("%v %s: %v, want a render error", tc.d, tc.c.Kind(), err)
		}
	}
	_, err := migrate.Render(s(velox.Postgres), s(velox.Postgres), nil, migrate.RenderOptions{})
	var e *migrate.Error
	if !errors.As(err, &e) || e.Code != "render" || e.Reason != "PostgreSQL rendering is not built yet" {
		t.Errorf("PostgreSQL: %v", err)
	}
	for _, d := range rendered {
		if _, err := migrate.Render(s(d), s(d), []migrate.Change{nil}, migrate.RenderOptions{}); !errors.As(err, &e) || e.Code != "render" {
			t.Errorf("%v: a nil change: %v", d, err)
		}
		if ss, err := migrate.Render(s(d), s(d), nil, migrate.RenderOptions{LockTimeout: time.Second}); err != nil || len(ss) != 0 {
			t.Errorf("%v: no changes give %v, %v; want no script", d, ss, err)
		}
	}
	if _, err := migrate.Render(nil, s(velox.MySQL), nil, migrate.RenderOptions{}); !errors.As(err, &e) {
		t.Errorf("nil from: %v", err)
	}
	if _, err := migrate.Render(s(velox.MySQL), s(velox.SQLite), nil, migrate.RenderOptions{}); !errors.As(err, &e) {
		t.Errorf("two dialects: %v", err)
	}
}

func TestRenderDoesNotMutate(t *testing.T) {
	eachCase(t, func(t *testing.T, c testCase, from, to *migrate.Schema) {
		if from.Dialect == velox.Postgres {
			return
		}
		cs := diff(t, from, to, c.opts)
		f, w, want := migrate.Clone(from), migrate.Clone(to), diff(t, from, to, c.opts)
		render(t, from, to, cs, migrate.RenderOptions{LockTimeout: time.Second, Allow: c.allow})
		if !reflect.DeepEqual(from, f) || !reflect.DeepEqual(to, w) || !reflect.DeepEqual(cs, want) {
			t.Error("Render changed its arguments")
		}
	})
}

func TestRenderDeterministic(t *testing.T) {
	eachCase(t, func(t *testing.T, c testCase, from, to *migrate.Schema) {
		if from.Dialect == velox.Postgres {
			return
		}
		cs := diff(t, from, to, c.opts)
		opts := migrate.RenderOptions{LockTimeout: time.Second, Allow: c.allow}
		a, b := render(t, from, to, cs, opts), render(t, from, to, cs, opts)
		if !reflect.DeepEqual(a, b) || scriptsText(a) != scriptsText(b) {
			t.Errorf("two calls differ:\n%s\n--\n%s", scriptsText(a), scriptsText(b))
		}
	})
}

// TestRenderSQLiteExecutes runs every SQLite case: from is created by its
// own scripts, then the case's script runs, and the database has to's
// tables, columns and indexes.
func TestRenderSQLiteExecutes(t *testing.T) {
	for _, c := range cases {
		if !slices.Contains(c.on(), velox.SQLite) {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			from, to := models(t, c, velox.SQLite)
			execSQLite(t, from, to, c.opts)
		})
	}
}

// TestRenderSQLiteRebuildKeepsRows: a rebuilt parent keeps its rows, its
// ON DELETE CASCADE children, and its AUTOINCREMENT counter.
func TestRenderSQLiteRebuildKeepsRows(t *testing.T) {
	c := caseNamed("rebuild-parent")
	from, to := models(t, c, velox.SQLite)
	db := openSQLite(t)
	runScripts(t, db, render(t, &migrate.Schema{Dialect: velox.SQLite}, from, diff(t, &migrate.Schema{Dialect: velox.SQLite}, from, migrate.DiffOptions{}), migrate.RenderOptions{}))
	for _, q := range []string{
		`INSERT INTO orgs (name) VALUES ('o')`,
		`INSERT INTO users (org_id, name, email, role, status, logins, created_at) VALUES (1, 'a', 'a@x', 'viewer', 'active', 3, '2026-01-01')`,
		`INSERT INTO users (org_id, name, email, role, status, logins, created_at) VALUES (1, 'b', 'b@x', 'viewer', 'active', 4, '2026-01-01')`,
		`DELETE FROM users WHERE id = 2`,
		`INSERT INTO posts (author_id, title, status) VALUES (1, 't', 'active')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	runScripts(t, db, render(t, from, to, diff(t, from, to, c.opts), migrate.RenderOptions{}))
	var posts, logins int
	if err := db.QueryRow(`SELECT count(*) FROM posts`).Scan(&posts); err != nil || posts != 1 {
		t.Errorf("posts %d, %v; want 1", posts, err)
	}
	if err := db.QueryRow(`SELECT logins FROM users WHERE id = 1`).Scan(&logins); err != nil || logins != 3 {
		t.Errorf("logins %d, %v; want 3", logins, err)
	}
	if _, err := db.Exec(`INSERT INTO users (org_id, name, email, created_at) VALUES (1, 'c', 'c@x', '2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	var id int
	if err := db.QueryRow(`SELECT max(id) FROM users`).Scan(&id); err != nil || id != 3 {
		t.Errorf("the next ID is %d, %v; want 3, past the deleted 2", id, err)
	}
}

func openSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1) // one connection: one in-memory database
	return db
}

// runScripts runs ss as the runner will: foreign keys off before the
// transaction when a script asks, on otherwise, and checked before COMMIT.
func runScripts(t *testing.T, db *sql.DB, ss []migrate.Script) {
	t.Helper()
	for _, s := range ss {
		fk := "ON"
		if s.Directives.ForeignKeysOff {
			fk = "OFF"
		}
		if _, err := db.Exec("PRAGMA foreign_keys = " + fk); err != nil {
			t.Fatal(err)
		}
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		for _, st := range s.Statements {
			if _, err := tx.Exec(st.SQL); err != nil {
				_ = tx.Rollback()
				t.Fatalf("%s: %v\n%s", st.SQL, err, scriptsText(ss))
			}
		}
		rows, err := tx.Query("PRAGMA foreign_key_check")
		if err != nil {
			t.Fatal(err)
		}
		bad := rows.Next()
		_ = rows.Close()
		if bad {
			t.Fatalf("foreign_key_check reports rows\n%s", scriptsText(ss))
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
}

// execSQLite creates from in a new database, migrates it to to, and checks
// the result against to.
func execSQLite(t *testing.T, from, to *migrate.Schema, opts migrate.DiffOptions) {
	t.Helper()
	db := openSQLite(t)
	empty := &migrate.Schema{Dialect: velox.SQLite}
	runScripts(t, db, render(t, empty, from, diff(t, empty, from, migrate.DiffOptions{}), migrate.RenderOptions{}))
	if got, want := shape(t, db), modelShape(from); !slices.Equal(got, want) {
		t.Fatalf("from's own scripts give\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	runScripts(t, db, render(t, from, to, diff(t, from, to, opts), migrate.RenderOptions{}))
	if got, want := shape(t, db), modelShape(to); !slices.Equal(got, want) {
		t.Errorf("after the migration\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// shape lists the database's tables with their columns, and its indexes
// with their tables, sorted.
func shape(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT type, name, tbl_name FROM sqlite_master WHERE type IN ('table', 'index') AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	var out, tables []string
	for rows.Next() {
		var typ, name, tbl string
		if err := rows.Scan(&typ, &name, &tbl); err != nil {
			t.Fatal(err)
		}
		if typ == "index" {
			out = append(out, "index "+tbl+"."+name)
			continue
		}
		tables = append(tables, name)
	}
	_ = rows.Close()
	for _, tb := range tables {
		cols, err := db.Query(`SELECT name, "notnull" FROM pragma_table_info(?)`, tb)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for cols.Next() {
			var name string
			var notNull bool
			if err := cols.Scan(&name, &notNull); err != nil {
				t.Fatal(err)
			}
			if !notNull {
				name += " null"
			}
			names = append(names, name)
		}
		_ = cols.Close()
		slices.Sort(names)
		out = append(out, "table "+tb+": "+strings.Join(names, ", "))
	}
	slices.Sort(out)
	return out
}

// modelShape is shape's list for the model s.
func modelShape(s *migrate.Schema) []string {
	var out []string
	for _, tb := range s.Tables {
		var names []string
		for _, c := range tb.Columns {
			name := c.Name
			if c.Nullable {
				name += " null"
			}
			names = append(names, name)
		}
		slices.Sort(names)
		out = append(out, "table "+tb.Name+": "+strings.Join(names, ", "))
		for _, ix := range tb.Indexes {
			out = append(out, "index "+tb.Name+"."+ix.Name)
		}
	}
	slices.Sort(out)
	return out
}
