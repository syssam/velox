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

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/codegen/loadjson"
	"github.com/syssam/velox/migrate"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

// rendered are the dialects Render writes.
var rendered = []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite}

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

// scriptsText is each script as "-- script: <kind>" and its file body.
func scriptsText(ss []migrate.Script) string {
	var b strings.Builder
	for _, s := range ss {
		b.WriteString("-- script: " + s.Kind.String() + "\n")
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
		"fk-index-replaced": "ALTER TABLE `users` ADD INDEX `user_org_id_created_at` (`org_id`, `created_at`), DROP INDEX `users_orgs_users`, ALGORITHM=INPLACE, LOCK=NONE",
		"fk-index-restored": "ALTER TABLE `users` ADD INDEX `users_orgs_users` (`org_id`), DROP INDEX `user_org_id_created_at`, ALGORITHM=INPLACE, LOCK=NONE",
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
			"ALTER TABLE `users` DROP INDEX `user_name_logins`, ALGORITHM=INPLACE, LOCK=NONE",
			"ALTER TABLE `users` DROP COLUMN `logins`",
		},
		"drop-column-in-index": {
			"ALTER TABLE `users` DROP COLUMN `logins`",
			"ALTER TABLE `users` ADD INDEX `users_orgs_users` (`org_id`), DROP INDEX `user_org_id_logins`, ALGORITHM=INPLACE, LOCK=NONE",
		},
		"drop-column-in-unique-key": {
			"ALTER TABLE `users` DROP FOREIGN KEY `users_orgs_users`",
			"ALTER TABLE `users` DROP INDEX `user_org_id_logins`, ALGORITHM=INPLACE, LOCK=NONE",
			"ALTER TABLE `users` DROP COLUMN `logins`",
			"ALTER TABLE `users` ADD INDEX `users_orgs_users` (`org_id`), ALGORITHM=INPLACE, LOCK=NONE",
			"ALTER TABLE `users` ADD CONSTRAINT `users_orgs_users` FOREIGN KEY (`org_id`) REFERENCES `orgs` (`id`)",
		},
		// A foreign key's rename is a drop before everything and an add
		// with the other keys, in two statements; its index, of its name,
		// is renamed with it.
		"rename-table": {
			"ALTER TABLE `users` DROP FOREIGN KEY `users_orgs_users`",
			"ALTER TABLE `posts` DROP FOREIGN KEY `posts_users_posts`",
			"RENAME TABLE `users` TO `accounts`",
			"ALTER TABLE `accounts` RENAME INDEX `users_orgs_users` TO `accounts_orgs_users`",
			"ALTER TABLE `posts` RENAME INDEX `posts_users_posts` TO `posts_accounts_posts`",
			"ALTER TABLE `accounts` ADD CONSTRAINT `accounts_orgs_users` FOREIGN KEY (`org_id`) REFERENCES `orgs` (`id`)",
			"ALTER TABLE `posts` ADD CONSTRAINT `posts_accounts_posts` FOREIGN KEY (`author_id`) REFERENCES `accounts` (`id`) ON DELETE CASCADE",
		},
		"reuse-dropped-name": {
			"ALTER TABLE `posts` DROP FOREIGN KEY `shared_fk`",
			"ALTER TABLE `users` DROP FOREIGN KEY `user_mentor`",
			"ALTER TABLE `users` RENAME INDEX `user_mentor` TO `shared_fk`",
			"ALTER TABLE `users` ADD INDEX `shared_idx` (`created_at`), ALGORITHM=INPLACE, LOCK=NONE",
			"ALTER TABLE `users` ADD CONSTRAINT `shared_fk` FOREIGN KEY (`mentor_id`) REFERENCES `users` (`id`) ON DELETE SET NULL",
			"DROP TABLE `posts`",
		},
		"on-delete-change": {
			"ALTER TABLE `posts` DROP FOREIGN KEY `posts_users_posts`",
			"ALTER TABLE `posts` ADD CONSTRAINT `posts_users_posts` FOREIGN KEY (`author_id`) REFERENCES `users` (`id`) ON DELETE RESTRICT",
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
	if len(ss) != 1 || ss[0].Kind != migrate.ScriptMain || !ss[0].Directives.ForeignKeysOff {
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
  CONSTRAINT "user_mentor" FOREIGN KEY ("mentor_id") REFERENCES "users" ("id") ON DELETE SET NULL,
  CONSTRAINT "users_orgs_users" FOREIGN KEY ("org_id") REFERENCES "orgs" ("id"),
  CONSTRAINT "users_role_check" CHECK ("role" IN ('admin', 'viewer')),
  CONSTRAINT "users_status_check" CHECK ("status" IN ('active', 'banned', 'pending'))
)`,
		`INSERT INTO "velox_new_users" ("id", "org_id", "name", "email", "bio", "role", "status", "logins", "created_at", "mentor_id") SELECT "id", "org_id", "name", "email", "bio", "role", "status", "logins", "created_at", "mentor_id" FROM "users"`,
		`DELETE FROM sqlite_sequence WHERE name = 'velox_new_users'`,
		`INSERT INTO sqlite_sequence (name, seq) SELECT 'velox_new_users', seq FROM sqlite_sequence WHERE name = 'users'`,
		`DROP TABLE "users"`,
		`ALTER TABLE "velox_new_users" RENAME TO "users"`,
		`CREATE INDEX "users_by_name" ON "users" ("name")`,
		`CREATE UNIQUE INDEX "users_email_key" ON "users" ("email")`,
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

	// A rebuilt table's index rename frees the old name at once; the
	// rebuild makes the new one.
	got := sqls(caseScripts(t, caseNamed("rename-table"), velox.SQLite))
	want = []string{`ALTER TABLE "users" RENAME TO "accounts"`, `DROP INDEX "users_email_key"`, `DROP INDEX "users_mentor_id"`, `DROP INDEX "users_org_id"`}
	if len(got) < len(want) || !slices.Equal(got[:len(want)], want) || !strings.HasPrefix(got[len(want)], `CREATE TABLE "velox_new_accounts"`) {
		t.Errorf("rename-table:\n%s", strings.Join(got, ";\n"))
	}

	// A table without AUTOINCREMENT has no counter to keep.
	from := mustTables(velox.SQLite, func(m *loadjson.Model) { addSchema(m, Tag{}) })
	to := mustTables(velox.SQLite, func(m *loadjson.Model) {
		addSchema(m, Tag{})
		fieldOf(m, "Tag", "label").Optional = true
		fieldOf(m, "Tag", "label").Nillable = true
	})
	got = sqls(render(t, from, to, diff(t, from, to, migrate.DiffOptions{}), migrate.RenderOptions{}))
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
// Lint's sqlite-rebuild findings name, and says so once on each rebuild.
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
		for _, ss := range render(t, from, to, cs, migrate.RenderOptions{}) {
			for _, st := range ss.Statements {
				name, ok := strings.CutPrefix(st.SQL, `CREATE TABLE "velox_new_`)
				if ok {
					got[name[:strings.IndexByte(name, '"')]] = true
				}
				// A rebuild's review line names the rebuild once, and
				// no entry twice.
				if n := strings.Count(st.Review, "sqlite-rebuild: "); n != 0 && (n != 1 || !ok) {
					t.Errorf("%s: review %q", c.name, st.Review)
				}
				if ok && !strings.Contains(st.Review, "sqlite-rebuild: ") {
					t.Errorf("%s: a rebuild without its review: %q", c.name, st.Review)
				}
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
		velox.Postgres: {"\"a`b\"", `"a""b"`},
		velox.MySQL:    {"`a``b`", "`a\"b`"},
		velox.SQLite:   {"\"a`b\"", `"a""b"`},
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
		velox.Postgres: {`DEFAULT 'it''s a \ test'`, `COMMENT ON COLUMN "users"."motto" IS 'it''s a \ comment'`, `ADD VALUE IF NOT EXISTS 'o''neil'`},
		velox.MySQL:    {`DEFAULT 'it''s a \\ test'`, `COMMENT 'it''s a \\ comment'`, `'o''neil'`},
		velox.SQLite:   {`DEFAULT 'it''s a \ test'`, `'o''neil'`},
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
		if lt := strings.Contains(text, "-- velox:lock-timeout 5s\n"); lt != (d != velox.SQLite) {
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
		"drop-index: drops index users.email; queries that used it get slower",
		"drop-column: drops column users.email and its data",
	}
	if !slices.Equal(got, want) {
		t.Errorf("reviews %q, want %q", got, want)
	}
}

// TestRenderUnits: a MySQL statement and an ADD VALUE are each a unit; an
// online unit is a run of statements that starts with an IF EXISTS drop;
// a statement of a script in a transaction is unit 0.
func TestRenderUnits(t *testing.T) {
	for _, d := range rendered {
		for _, c := range cases {
			if !slices.Contains(c.on(), d) {
				continue
			}
			for _, s := range caseScripts(t, c, d) {
				for i, st := range s.Statements {
					if s.Kind == migrate.ScriptOnline {
						first := i == 0 || st.Unit != s.Statements[i-1].Unit
						if i > 0 {
							want := s.Statements[i-1].Unit
							if first {
								want++
							}
							if st.Unit != want {
								t.Errorf("%s: online statement %d has unit %d, want %d", c.name, i, st.Unit, want)
							}
						}
						if first != strings.Contains(st.SQL, " IF EXISTS ") {
							t.Errorf("%s: online statement %d, first of its unit %v: %s", c.name, i, first, st.SQL)
						}
						continue
					}
					want := 0
					if d == velox.MySQL || s.Kind == migrate.ScriptEnum {
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
		{velox.Postgres, migrate.SetTableCollation{Table: users, To: "x"}},
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
	var e *migrate.Error
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

// execSQLite creates from in a new database, puts a row in each table,
// migrates it to to, and compares it with a database made fresh from to.
func execSQLite(t *testing.T, from, to *migrate.Schema, opts migrate.DiffOptions) {
	t.Helper()
	empty := &migrate.Schema{Dialect: velox.SQLite}
	db := openSQLite(t)
	runScripts(t, db, render(t, empty, from, diff(t, empty, from, migrate.DiffOptions{}), migrate.RenderOptions{}))
	cs := diff(t, from, to, opts)
	// A required column added to a table with rows fails, as its finding
	// says; that plan runs on empty tables.
	rows := !slices.ContainsFunc(migrate.Lint(from, cs), func(f migrate.Finding) bool { return f.Code == "add-required-column" })
	if rows {
		insertRows(t, db, velox.SQLite, from)
	}
	runScripts(t, db, render(t, from, to, cs, migrate.RenderOptions{}))

	fresh := openSQLite(t)
	runScripts(t, fresh, render(t, empty, to, diff(t, empty, to, migrate.DiffOptions{}), migrate.RenderOptions{}))
	if got, want := describe(t, db), describe(t, fresh); !slices.Equal(got, want) {
		t.Errorf("after the migration\n%s\nwant, as made fresh\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !rows {
		return
	}
	for _, tb := range from.Tables {
		ref := tb.Ref()
		if r, ok := opts.RenameTables[ref]; ok {
			ref = r
		}
		if _, ok := to.Table(ref); !ok {
			continue
		}
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM "` + ref.Name + `"`).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s has %d rows, %v; want its 1", ref, n, err)
		}
	}
}

// describe lists what the database holds, sorted: each table's columns
// (name, type, notnull, default, key), foreign keys (table, from, to, on
// delete), indexes (name, unique, partial, columns) and CHECK clauses.
func describe(t *testing.T, db *sql.DB) []string {
	t.Helper()
	strs := func(q string, args ...any) [][]string {
		rows, err := db.Query(q, args...)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		defer func() { _ = rows.Close() }()
		var out [][]string
		for rows.Next() {
			cols, _ := rows.Columns()
			v := make([]sql.NullString, len(cols))
			p := make([]any, len(cols))
			for i := range v {
				p[i] = &v[i]
			}
			if err := rows.Scan(p...); err != nil {
				t.Fatal(err)
			}
			row := make([]string, len(cols))
			for i, x := range v {
				row[i] = x.String
				if !x.Valid {
					row[i] = "NULL"
				}
			}
			out = append(out, row)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	var out []string
	for _, tb := range strs(`SELECT name, sql FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`) {
		name, ddl := tb[0], tb[1]
		for _, c := range strs(`SELECT name, type, "notnull", dflt_value, pk FROM pragma_table_info(?)`, name) {
			out = append(out, name+" column "+strings.Join(c, " "))
		}
		for _, fk := range strs(`SELECT "table", "from", "to", on_delete FROM pragma_foreign_key_list(?)`, name) {
			out = append(out, name+" foreign key "+strings.Join(fk, " "))
		}
		for _, ix := range strs(`SELECT name, "unique", partial FROM pragma_index_list(?) WHERE origin <> 'pk'`, name) {
			var cols []string
			for _, c := range strs(`SELECT name FROM pragma_index_info(?) ORDER BY seqno`, ix[0]) {
				cols = append(cols, c[0])
			}
			out = append(out, name+" index "+strings.Join(ix, " ")+" ("+strings.Join(cols, ", ")+")")
		}
		for _, ck := range checkClauses(ddl) {
			out = append(out, name+" "+ck)
		}
	}
	slices.Sort(out)
	return out
}

// checkClauses is each [CONSTRAINT "name"] CHECK (…) of a CREATE TABLE,
// parentheses matched outside string literals.
func checkClauses(ddl string) []string {
	var out []string
	for rest := ddl; ; {
		i := strings.Index(rest, "CHECK (")
		if i < 0 {
			return out
		}
		depth, quoted, j := 0, false, i+len("CHECK ")
		for ; j < len(rest); j++ {
			switch c := rest[j]; {
			case c == '\'':
				quoted = !quoted
			case quoted:
			case c == '(':
				depth++
			case c == ')':
				depth--
			}
			if depth == 0 {
				break
			}
		}
		start := i
		if k := strings.LastIndex(rest[:i], "CONSTRAINT "); k >= 0 && strings.Count(rest[k:i], `"`) == 2 && strings.HasSuffix(rest[:i], `" `) {
			start = k
		}
		out = append(out, rest[start:j+1])
		rest = rest[j+1:]
	}
}

func TestKindStrings(t *testing.T) {
	for k, want := range map[migrate.ScriptKind]string{
		migrate.ScriptMain: "main", migrate.ScriptEnum: "enum", migrate.ScriptOnline: "online", 7: "ScriptKind(7)",
	} {
		if got := k.String(); got != want {
			t.Errorf("ScriptKind %d: %q, want %q", k, got, want)
		}
	}
	all := migrate.ColumnType | migrate.ColumnNullable | migrate.ColumnDefault | migrate.ColumnIdentity | migrate.ColumnComment
	for c, want := range map[migrate.ColumnChange]string{
		0: "0", migrate.ColumnType: "type", migrate.ColumnNullable | migrate.ColumnComment: "nullable|comment",
		all: "type|nullable|default|identity|comment", migrate.ColumnDefault | 0x80: "default|0x80",
	} {
		if got := c.String(); got != want {
			t.Errorf("ColumnChange %#x: %q, want %q", uint8(c), got, want)
		}
	}
}

// TestRenderMySQLRefusesPrimaryKeyRename: MySQL names every primary key
// PRIMARY, so a rename of one is refused, whichever name it gives.
func TestRenderMySQLRefusesPrimaryKeyRename(t *testing.T) {
	s := mustTables(velox.MySQL, nil)
	named := migrate.Clone(s)
	table(named, "users").PrimaryKeyName = "users_pk"
	for _, tc := range []struct {
		from, to *migrate.Schema
		c        migrate.RenameConstraint
	}{
		{s, s, migrate.RenameConstraint{Table: users, From: "PRIMARY", To: "users_pkey"}},
		{s, s, migrate.RenameConstraint{Table: users, From: "users_pkey", To: "PRIMARY"}},
		{named, s, migrate.RenameConstraint{Table: users, From: "users_pk", To: "users_pkey"}},
		{s, named, migrate.RenameConstraint{Table: users, From: "users_pkey", To: "users_pk"}},
	} {
		_, err := migrate.Render(tc.from, tc.to, []migrate.Change{tc.c}, migrate.RenderOptions{})
		var e *migrate.Error
		if !errors.As(err, &e) || e.Code != "render" || !strings.Contains(e.Error(), "MySQL's primary key is always named PRIMARY") {
			t.Errorf("%s: %v, want the primary key's refusal", tc.c, err)
		}
	}
}

// TestRenderSQLiteUnnamed: a foreign key, check or unique index SQLite
// keeps no name for is written in the CREATE TABLE as SQLite reads it back:
// a key and a check with no CONSTRAINT, a unique index as UNIQUE (…).
func TestRenderSQLiteUnnamed(t *testing.T) {
	empty := &migrate.Schema{Dialect: velox.SQLite}
	to := &migrate.Schema{Dialect: velox.SQLite, Tables: []migrate.Table{
		{Name: "orgs", Columns: []migrate.Column{{Name: "id", Type: "integer", Identity: true}}, PrimaryKey: []string{"id"}},
		{
			Name: "users",
			Columns: []migrate.Column{
				{Name: "id", Type: "integer", Identity: true},
				{Name: "org_id", Type: "integer"},
				{Name: "email", Type: "text"},
			},
			PrimaryKey:  []string{"id"},
			Indexes:     []migrate.Index{{Columns: []string{"email"}, Unique: true}, {Name: "users_org", Columns: []string{"org_id"}}},
			ForeignKeys: []migrate.ForeignKey{{Columns: []string{"org_id"}, RefTable: migrate.TableRef{Name: "orgs"}, RefColumns: []string{"id"}}},
			Checks:      []migrate.Check{{Expr: "email <> ''"}},
		},
	}}
	got := sqls(render(t, empty, to, diff(t, empty, to, migrate.DiffOptions{}), migrate.RenderOptions{}))
	want := []string{
		"CREATE TABLE \"orgs\" (\n  \"id\" integer NOT NULL PRIMARY KEY AUTOINCREMENT\n)",
		"CREATE TABLE \"users\" (\n  \"id\" integer NOT NULL PRIMARY KEY AUTOINCREMENT,\n  \"org_id\" integer NOT NULL,\n  \"email\" text NOT NULL,\n" +
			"  FOREIGN KEY (\"org_id\") REFERENCES \"orgs\" (\"id\"),\n  CHECK (email <> ''),\n  UNIQUE (\"email\")\n)",
		`CREATE INDEX "users_org" ON "users" ("org_id")`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
