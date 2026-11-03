package migrate_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/codegen/loadjson"
	"github.com/syssam/velox/migrate"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
)

// scriptOf is the script of kind k in ss, or nil.
func scriptOf(ss []migrate.Script, k migrate.ScriptKind) *migrate.Script {
	for i := range ss {
		if ss[i].Kind == k {
			return &ss[i]
		}
	}
	return nil
}

func stmtSQL(sts []migrate.Statement) []string {
	out := make([]string, len(sts))
	for i, st := range sts {
		out[i] = st.SQL
	}
	return out
}

// TestRenderOnlineUnits: an existing table's index, foreign key and SET NOT
// NULL are units of the online script, each starting with its IF EXISTS
// drop; the online script runs with no lock timeout, the main script with
// the plan's.
func TestRenderOnlineUnits(t *testing.T) {
	for name, want := range map[string][][]string{
		"add-index": {{
			`DROP INDEX CONCURRENTLY IF EXISTS "user_created_at"`,
			`CREATE INDEX CONCURRENTLY "user_created_at" ON "users" ("created_at")`,
		}},
		"add-foreign-key": {{
			`DROP INDEX CONCURRENTLY IF EXISTS "posts_reviewer_id"`,
			`CREATE INDEX CONCURRENTLY "posts_reviewer_id" ON "posts" ("reviewer_id")`,
		}, {
			`ALTER TABLE "posts" DROP CONSTRAINT IF EXISTS "posts_users_reviewer"`,
			`ALTER TABLE "posts" ADD CONSTRAINT "posts_users_reviewer" FOREIGN KEY ("reviewer_id") REFERENCES "users" ("id") ON DELETE SET NULL NOT VALID`,
			`ALTER TABLE "posts" VALIDATE CONSTRAINT "posts_users_reviewer"`,
		}},
		"set-not-null": {{
			`ALTER TABLE "users" DROP CONSTRAINT IF EXISTS "users_bio_not_null"`,
			`ALTER TABLE "users" ADD CONSTRAINT "users_bio_not_null" CHECK ("bio" IS NOT NULL) NOT VALID`,
			`ALTER TABLE "users" VALIDATE CONSTRAINT "users_bio_not_null"`,
			`ALTER TABLE "users" ALTER COLUMN "bio" SET NOT NULL`,
			`ALTER TABLE "users" DROP CONSTRAINT "users_bio_not_null"`,
		}},
	} {
		ss := caseScripts(t, caseNamed(name), velox.Postgres)
		if len(ss) != 1 || ss[0].Kind != migrate.ScriptOnline {
			t.Fatalf("%s: want the online script alone:\n%s", name, scriptsText(ss))
		}
		on := ss[0]
		if !on.Directives.NoTransaction || on.Directives.LockTimeoutSet {
			t.Errorf("%s: online directives %+v", name, on.Directives)
		}
		var got [][]string
		for i, st := range on.Statements {
			if i == 0 || st.Unit != on.Statements[i-1].Unit {
				got = append(got, nil)
			}
			got[len(got)-1] = append(got[len(got)-1], st.SQL)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: units\n%q\nwant\n%q", name, got, want)
		}
		text := scriptsText(ss)
		if !strings.HasPrefix(text, "-- script: online\n-- velox:no-transaction\n") || strings.Count(text, "-- velox:unit\n") != len(want) {
			t.Errorf("%s:\n%s", name, text)
		}
	}
	// An online-only plan records its allowance.
	if ss := caseScripts(t, caseNamed("set-not-null"), velox.Postgres); !reflect.DeepEqual(ss[0].Directives.Allow, []migrate.Allow{{Code: "set-not-null", Target: "users.bio"}}) {
		t.Errorf("set-not-null allows %v", ss[0].Directives.Allow)
	}

	// The main script keeps the lock timeout; the foreign key dropped
	// around the type change comes back in it, NOT VALID, and is validated
	// online.
	ss := caseScripts(t, caseNamed("key-type-change"), velox.Postgres)
	main, on := scriptOf(ss, migrate.ScriptMain), scriptOf(ss, migrate.ScriptOnline)
	if main == nil || on == nil || main.Directives.LockTimeout != 5*time.Second || !main.Directives.LockTimeoutSet || main.Directives.NoTransaction {
		t.Fatalf("key-type-change:\n%s", scriptsText(ss))
	}
	if want := []string{
		`ALTER TABLE "users" DROP CONSTRAINT "users_orgs_users"`,
		`ALTER TABLE "orgs" ALTER COLUMN "id" TYPE bigint USING "id"::bigint`,
		`ALTER TABLE "users" ALTER COLUMN "org_id" TYPE bigint USING "org_id"::bigint`,
		`ALTER TABLE "users" ADD CONSTRAINT "users_orgs_users" FOREIGN KEY ("org_id") REFERENCES "orgs" ("id") NOT VALID`,
	}; !slices.Equal(stmtSQL(main.Statements), want) {
		t.Errorf("key-type-change main:\n%s", scriptsText(ss))
	}
	if want := []string{`ALTER TABLE "users" VALIDATE CONSTRAINT "users_orgs_users"`}; !slices.Equal(stmtSQL(on.Statements), want) {
		t.Errorf("key-type-change online:\n%s", scriptsText(ss))
	}

	// An index on a table the plan drops goes in the main script, before
	// another table's index takes its name online.
	ss = caseScripts(t, caseNamed("reuse-dropped-name"), velox.Postgres)
	main, on = scriptOf(ss, migrate.ScriptMain), scriptOf(ss, migrate.ScriptOnline)
	if main == nil || on == nil || !slices.Contains(stmtSQL(main.Statements), `DROP INDEX "shared_idx"`) ||
		!slices.Contains(stmtSQL(on.Statements), `CREATE INDEX CONCURRENTLY "shared_idx" ON "users" ("created_at")`) {
		t.Errorf("reuse-dropped-name:\n%s", scriptsText(ss))
	}

	// So does an index whose name a new table takes, before the table.
	got := sqls(caseScripts(t, caseNamed("index-name-to-table"), velox.Postgres))
	if i, j := slices.Index(got, `DROP INDEX "tags"`), slices.IndexFunc(got, func(s string) bool { return strings.HasPrefix(s, `CREATE TABLE "tags"`) }); i < 0 || j < i {
		t.Errorf("index-name-to-table:\n%s", strings.Join(got, "\n"))
	}
}

// TestRenderEnumScript: added values are the enum script's, before the main
// script; a removal swaps the type, altering every column that uses it.
func TestRenderEnumScript(t *testing.T) {
	ss := caseScripts(t, caseNamed("enum-insert"), velox.Postgres)
	want := []string{`ALTER TYPE "users_role" ADD VALUE IF NOT EXISTS 'editor' BEFORE 'viewer'`}
	if len(ss) != 1 || ss[0].Kind != migrate.ScriptEnum || !slices.Equal(stmtSQL(ss[0].Statements), want) {
		t.Fatalf("enum-insert:\n%s", scriptsText(ss))
	}
	if d := ss[0].Directives; !d.NoTransaction || d.LockTimeout != 5*time.Second || !d.LockTimeoutSet {
		t.Errorf("enum-insert directives %+v", d)
	}

	ss = caseScripts(t, caseNamed("enum-shared"), velox.Postgres)
	want = []string{
		`ALTER TYPE "case_status" RENAME TO "case_status_velox_old"`,
		`CREATE TYPE "case_status" AS ENUM ('active', 'pending')`,
		`ALTER TABLE "posts" ALTER COLUMN "status" DROP DEFAULT, ALTER COLUMN "status" TYPE "case_status" USING "status"::text::"case_status", ALTER COLUMN "status" SET DEFAULT 'active'`,
		`ALTER TABLE "users" ALTER COLUMN "status" DROP DEFAULT, ALTER COLUMN "status" TYPE "case_status" USING "status"::text::"case_status", ALTER COLUMN "status" SET DEFAULT 'active'`,
		`DROP TYPE "case_status_velox_old"`,
	}
	if len(ss) != 1 || ss[0].Kind != migrate.ScriptMain || !slices.Equal(stmtSQL(ss[0].Statements), want) {
		t.Errorf("enum-shared:\n%s", scriptsText(ss))
	}

	// What casts to the type is dropped before the columns change and
	// made again after.
	ss = caseScripts(t, caseNamed("enum-swap-dependents"), velox.Postgres)
	want = []string{
		`ALTER TYPE "users_role" RENAME TO "users_role_velox_old"`,
		`CREATE TYPE "users_role" AS ENUM ('admin', 'viewer')`,
	}
	got := stmtSQL(ss[0].Statements)
	if len(ss) != 1 || len(got) != 8 || got[2] != `ALTER TABLE "users" DROP CONSTRAINT "users_role_known"` ||
		got[3] != `DROP INDEX "user_name"` ||
		!strings.HasPrefix(got[4], `ALTER TABLE "users" ALTER COLUMN "role" DROP DEFAULT, ALTER COLUMN "role" TYPE "users_role"`) ||
		got[5] != `CREATE INDEX "user_name" ON "users" ("name") WHERE role = 'admin'` ||
		got[6] != `ALTER TABLE "users" ADD CONSTRAINT "users_role_known" CHECK ("role" IN ('admin', 'viewer'))` ||
		got[7] != `DROP TYPE "users_role_velox_old"` || !slices.Equal(got[:2], want) {
		t.Errorf("enum-swap-dependents:\n%s", scriptsText(ss))
	}

	// A value added to a type the main script renames names the type as
	// it is before the main script runs.
	s := mustTables(velox.Postgres, nil)
	kind := migrate.TableRef{Name: "users_kind"}
	ss = render(t, s, s, []migrate.Change{
		migrate.RenameEnum{From: migrate.TableRef{Name: "users_role"}, To: kind},
		migrate.AlterEnum{From: migrate.Enum{Name: "users_kind", Values: []string{"admin", "viewer"}}, To: migrate.Enum{Name: "users_kind", Values: []string{"admin", "viewer", "owner"}}},
	}, migrate.RenderOptions{})
	if len(ss) != 2 || ss[0].Kind != migrate.ScriptEnum || ss[1].Kind != migrate.ScriptMain ||
		!slices.Equal(stmtSQL(ss[0].Statements), []string{`ALTER TYPE "users_role" ADD VALUE IF NOT EXISTS 'owner'`}) {
		t.Errorf("a renamed type:\n%s", scriptsText(ss))
	}
}

// TestRenderIdentity: an identity added to an existing column, and a serial
// turned into one, with their counters set past the column's values.
func TestRenderIdentity(t *testing.T) {
	add := `ALTER TABLE "users" ALTER COLUMN "id" ADD GENERATED BY DEFAULT AS IDENTITY (SEQUENCE NAME "users_id_seq")`
	setval := `SELECT setval(pg_get_serial_sequence('"users"', 'id'), coalesce(max("id"), 0) + 1, false) FROM "users"`
	for name, want := range map[string][]string{
		"add-identity": {add, setval},
		"serial-to-identity": {`ALTER TABLE "users" ALTER COLUMN "id" DROP DEFAULT`, `ALTER SEQUENCE "users_id_seq" OWNED BY NONE`,
			`ALTER SEQUENCE "users_id_seq" RENAME TO "users_id_seq_velox_old"`, add,
			`SELECT setval(pg_get_serial_sequence('"users"', 'id'), greatest(coalesce(max("id"), 0) + 1, (SELECT CASE WHEN is_called THEN last_value + 1 ELSE last_value END FROM "users_id_seq_velox_old")), false) FROM "users"`,
			`DROP SEQUENCE "users_id_seq_velox_old"`},
	} {
		ss := caseScripts(t, caseNamed(name), velox.Postgres)
		if len(ss) != 1 || !slices.Equal(stmtSQL(ss[0].Statements), want) {
			t.Errorf("%s:\n%s", name, scriptsText(ss))
			continue
		}
		if r := ss[0].Statements[0].Review; !strings.HasPrefix(r, "add-identity: ") || strings.Contains(r, "serial-to-identity") != (name == "serial-to-identity") {
			t.Errorf("%s: review %q", name, r)
		}
	}

	// A serial whose sequence from does not name: the name is read from
	// its default, as an identifier; a default it cannot read is refused.
	from, to := models(t, caseNamed("serial-to-identity"), velox.Postgres)
	id := column(table(from, "users"), "id")
	id.Sequence = ""
	for def, seq := range map[string][2]string{
		`nextval('users_id_seq'::regclass)`:          {`"users_id_seq"`, `"users_id_seq_velox_old"`},
		`nextval('Users_ID_Seq')`:                    {`"users_id_seq"`, `"users_id_seq_velox_old"`},
		`nextval('"Odd""seq"'::regclass)`:            {`"Odd""seq"`, `"Odd""seq_velox_old"`},
		`nextval('public."users_id_seq"'::regclass)`: {`"public"."users_id_seq"`, `"public"."users_id_seq_velox_old"`},
	} {
		id.Default = def
		got := sqls(render(t, from, to, diff(t, from, to, migrate.DiffOptions{}), migrate.RenderOptions{}))
		if len(got) != 6 || got[1] != "ALTER SEQUENCE "+seq[0]+" OWNED BY NONE" || got[3] != add || got[5] != "DROP SEQUENCE "+seq[1] {
			t.Errorf("%s:\n%s", def, strings.Join(got, "\n"))
		}
	}
	for _, def := range []string{`nextval(42)`, `nextval('a.b.c'::regclass)`, `nextval('"open'::regclass)`} {
		id.Default = def
		_, err := migrate.Render(from, to, diff(t, from, to, migrate.DiffOptions{}), migrate.RenderOptions{})
		var e *migrate.Error
		if !errors.As(err, &e) || e.Code != "render" || err.Error() != unnamedSequence {
			t.Errorf("%s: %v", def, err)
		}
	}
}

// unnamedSequence is Render's refusal of an identity change on users.id
// whose sequence from does not name.
const unnamedSequence = "migrate: users.id: the identity's sequence is not named in the snapshot; run velox-gen migrate baseline, or rename it by hand"

// TestRenderSequenceOnly: an identity whose sequence alone changes keeps its
// identity: the sequence is renamed when from names it, and Render refuses
// the change when from does not.
func TestRenderSequenceOnly(t *testing.T) {
	s := mustTables(velox.Postgres, nil)
	id := *column(table(s, "users"), "id")
	old := id
	old.Sequence = "users_legacy_seq"
	c := migrate.AlterColumn{Table: users, From: old, To: id, What: migrate.ColumnIdentity}
	if got, want := sqls(render(t, s, s, []migrate.Change{c}, migrate.RenderOptions{})), []string{`ALTER SEQUENCE "users_legacy_seq" RENAME TO "users_id_seq"`}; !slices.Equal(got, want) {
		t.Errorf("a named sequence: %q, want %q", got, want)
	}
	c.From.Sequence = ""
	_, err := migrate.Render(s, s, []migrate.Change{c}, migrate.RenderOptions{})
	var e *migrate.Error
	if !errors.As(err, &e) || e.Code != "render" || err.Error() != unnamedSequence {
		t.Errorf("an unnamed sequence: %v", err)
	}
}

// TestRenderTableRename: a table rename carries its derived names, on each
// dialect as the design's example shows.
func TestRenderTableRename(t *testing.T) {
	c := caseNamed("rename-table")
	if got, want := sqls(caseScripts(t, c, velox.Postgres)), []string{
		`ALTER TABLE "users" RENAME TO "accounts"`,
		`ALTER SEQUENCE "users_id_seq" RENAME TO "accounts_id_seq"`,
		`ALTER INDEX "users_email_key" RENAME TO "accounts_email_key"`,
		`ALTER INDEX "users_mentor_id" RENAME TO "accounts_mentor_id"`,
		`ALTER INDEX "users_org_id" RENAME TO "accounts_org_id"`,
		`ALTER TABLE "accounts" RENAME CONSTRAINT "users_orgs_users" TO "accounts_orgs_users"`,
		`ALTER TABLE "accounts" RENAME CONSTRAINT "users_pkey" TO "accounts_pkey"`,
		`ALTER TABLE "posts" RENAME CONSTRAINT "posts_users_posts" TO "posts_accounts_posts"`,
		`ALTER TYPE "users_role" RENAME TO "accounts_role"`,
	}; !slices.Equal(got, want) {
		t.Errorf("PostgreSQL:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got, want := sqls(caseScripts(t, c, velox.MySQL)), []string{
		"ALTER TABLE `users` DROP FOREIGN KEY `users_orgs_users`",
		"ALTER TABLE `posts` DROP FOREIGN KEY `posts_users_posts`",
		"RENAME TABLE `users` TO `accounts`",
		"ALTER TABLE `accounts` RENAME INDEX `users_orgs_users` TO `accounts_orgs_users`",
		"ALTER TABLE `posts` RENAME INDEX `posts_users_posts` TO `posts_accounts_posts`",
		"ALTER TABLE `accounts` ADD CONSTRAINT `accounts_orgs_users` FOREIGN KEY (`org_id`) REFERENCES `orgs` (`id`)",
		"ALTER TABLE `posts` ADD CONSTRAINT `posts_accounts_posts` FOREIGN KEY (`author_id`) REFERENCES `accounts` (`id`) ON DELETE CASCADE",
	}; !slices.Equal(got, want) {
		t.Errorf("MySQL:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	got := sqls(caseScripts(t, c, velox.SQLite))
	if len(got) < 2 || got[0] != `ALTER TABLE "users" RENAME TO "accounts"` ||
		slices.ContainsFunc(got[1:], func(s string) bool {
			return strings.HasPrefix(s, "ALTER TABLE") && !strings.HasPrefix(s, `ALTER TABLE "velox_new_`) // a rebuild's

		}) ||
		!slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, `CREATE TABLE "velox_new_accounts"`) }) ||
		!slices.Contains(got, `CREATE UNIQUE INDEX "accounts_email_key" ON "accounts" ("email")`) {
		t.Errorf("SQLite:\n%s", strings.Join(got, "\n"))
	}
}

// TestRenderCreateEnumAndIndex: an index on an enum column shares its name
// with the column's type, which PostgreSQL keeps apart; both are created.
func TestRenderCreateEnumAndIndex(t *testing.T) {
	from := &migrate.Schema{Dialect: velox.Postgres}
	to := mustTables(velox.Postgres, func(m *loadjson.Model) { addIndex(m, "User", index.Fields("role").StorageKey("users_role")) })
	got := sqls(render(t, from, to, diff(t, from, to, migrate.DiffOptions{}), migrate.RenderOptions{}))
	for _, w := range []string{`CREATE TYPE "users_role" AS ENUM ('admin', 'viewer')`, `CREATE INDEX "users_role" ON "users" ("role")`} {
		if !slices.Contains(got, w) {
			t.Errorf("no %s in\n%s", w, strings.Join(got, "\n"))
		}
	}
}

// TestRenderPostgresDetails: what the goldens show in passing, pinned.
func TestRenderPostgresDetails(t *testing.T) {
	ss := caseScripts(t, caseNamed("schema-qualified"), velox.Postgres)
	got := sqls(ss)
	if len(got) == 0 || got[0] != `CREATE SCHEMA IF NOT EXISTS "billing"` || strings.Count(strings.Join(got, "\n"), "CREATE SCHEMA") != 1 {
		t.Errorf("schema-qualified:\n%s", strings.Join(got, "\n"))
	}
	if !slices.Contains(got, `CREATE TYPE "billing"."invoices_state" AS ENUM ('open', 'paid')`) {
		t.Errorf("schema-qualified:\n%s", strings.Join(got, "\n"))
	}
	if ss[0].Statements[0].Review != "" {
		t.Errorf("CREATE SCHEMA has a review: %q", ss[0].Statements[0].Review)
	}

	got = sqls(caseScripts(t, caseNamed("create-tables"), velox.Postgres))
	if !slices.ContainsFunc(got, func(s string) bool {
		return strings.Contains(s, `"id" bigint GENERATED BY DEFAULT AS IDENTITY (SEQUENCE NAME "users_id_seq") NOT NULL`) &&
			strings.Contains(s, `CONSTRAINT "users_pkey" PRIMARY KEY ("id")`)
	}) || !slices.Contains(got, `COMMENT ON COLUMN "users"."name" IS 'the display name'`) {
		t.Errorf("create-tables:\n%s", strings.Join(got, "\n"))
	}

	for name, want := range map[string][]string{
		"change-comment": {`COMMENT ON COLUMN "users"."name" IS 'the name shown'`},
		"table-comment":  {`COMMENT ON TABLE "users" IS 'a person'`},
		"drop-not-null":  {`ALTER TABLE "users" ALTER COLUMN "name" DROP NOT NULL`},
		"change-default": {`ALTER TABLE "users" ALTER COLUMN "role" SET DEFAULT 'admin'`},
		"enum-opt-out": {
			`ALTER TABLE "users" ALTER COLUMN "role" DROP DEFAULT, ALTER COLUMN "role" TYPE text USING "role"::text, ALTER COLUMN "role" SET DEFAULT 'viewer'`,
			`DROP TYPE "users_role"`,
			`ALTER TABLE "users" DROP CONSTRAINT IF EXISTS "users_role_check"`,
			`ALTER TABLE "users" ADD CONSTRAINT "users_role_check" CHECK ("role" IN ('admin', 'viewer')) NOT VALID`,
			`ALTER TABLE "users" VALIDATE CONSTRAINT "users_role_check"`,
		},
	} {
		if got := sqls(caseScripts(t, caseNamed(name), velox.Postgres)); !slices.Equal(got, want) {
			t.Errorf("%s:\n%s\nwant\n%s", name, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}

	// A comment removed is NULL.
	from := mustTables(velox.Postgres, nil)
	to := mustTables(velox.Postgres, func(m *loadjson.Model) { fieldOf(m, "User", "name").Comment = "" })
	if got := sqls(render(t, from, to, diff(t, from, to, migrate.DiffOptions{}), migrate.RenderOptions{})); !slices.Equal(got, []string{`COMMENT ON COLUMN "users"."name" IS NULL`}) {
		t.Errorf("a removed comment: %q", got)
	}
	// A change's review line goes on its first statement, in the main script
	// before its online unit, but for set-not-null, which goes on the unit that
	// sets NOT NULL.
	to = mustTables(velox.Postgres, func(m *loadjson.Model) {
		f := fieldOf(m, "User", "bio")
		f.Optional, f.Nillable, f.Size = false, false, 50
		f.SchemaType = map[string]string{field.Postgres: "varchar(50)"}
	})
	ss = render(t, from, to, diff(t, from, to, migrate.DiffOptions{}), migrate.RenderOptions{})
	if main, on := scriptOf(ss, migrate.ScriptMain), scriptOf(ss, migrate.ScriptOnline); main == nil || on == nil ||
		!strings.Contains(main.Statements[0].Review, "alter-type: ") || strings.Contains(main.Statements[0].Review, "set-not-null: ") ||
		!strings.HasPrefix(on.Statements[0].Review, "set-not-null: ") || on.Statements[3].SQL != `ALTER TABLE "users" ALTER COLUMN "bio" SET NOT NULL` {
		t.Errorf("type and NOT NULL:\n%s", scriptsText(ss))
	}

	// A type change into an enum casts through text.
	from = mustTables(velox.Postgres, func(m *loadjson.Model) {
		fieldOf(m, "User", "role").SchemaType = map[string]string{field.Postgres: "text"}
	})
	to = mustTables(velox.Postgres, nil)
	got = sqls(render(t, from, to, diff(t, from, to, migrate.DiffOptions{}), migrate.RenderOptions{}))
	if !slices.Contains(got, `ALTER TABLE "users" ALTER COLUMN "role" DROP DEFAULT, ALTER COLUMN "role" TYPE "users_role" USING "role"::text::"users_role", ALTER COLUMN "role" SET DEFAULT 'viewer'`) {
		t.Errorf("into an enum:\n%s", strings.Join(got, "\n"))
	}
}

// TestRenderPostgresExecutes runs every PostgreSQL case on the server
// VELOX_TEST_POSTGRES names, as the runner will run its scripts: from is
// created by its own scripts in a new database, the case's scripts run, each
// unit of the enum and online scripts also cut short after each of its
// statements and run again, and the result is compared with a database made
// fresh from to.
func TestRenderPostgresExecutes(t *testing.T) {
	dsn := os.Getenv("VELOX_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("needs VELOX_TEST_POSTGRES")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	for _, c := range cases {
		if !slices.Contains(c.on(), velox.Postgres) {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			from, to := models(t, c, velox.Postgres)
			db := pgDatabase(t, admin, dsn)
			empty := &migrate.Schema{Dialect: velox.Postgres}
			runPostgres(t, db, render(t, empty, from, diff(t, empty, from, migrate.DiffOptions{}), migrate.RenderOptions{LockTimeout: 5 * time.Second}))
			runPostgresResuming(t, db, caseScripts(t, c, velox.Postgres), true)

			fresh := pgDatabase(t, admin, dsn)
			runPostgres(t, fresh, render(t, empty, to, diff(t, empty, to, migrate.DiffOptions{}), migrate.RenderOptions{}))
			if got, want := pgDescribe(t, db), pgDescribe(t, fresh); !slices.Equal(got, want) {
				t.Errorf("after the migration\n%s\nwant, as made fresh\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

// runPostgres runs ss as the runner will: a script in a transaction with
// SET LOCAL lock_timeout, a no-transaction script statement by statement.
func runPostgres(t *testing.T, db *sql.DB, ss []migrate.Script) {
	runPostgresResuming(t, db, ss, false)
}

// runPostgresResuming is runPostgres; with resume, each unit of a
// no-transaction script is run as a resume after a failure would run it:
// for every k, its first k statements, then the whole unit again.
func runPostgresResuming(t *testing.T, db *sql.DB, ss []migrate.Script, resume bool) {
	t.Helper()
	ctx := context.Background()
	exec := func(sts []migrate.Statement) {
		t.Helper()
		for _, st := range sts {
			if _, err := db.ExecContext(ctx, st.SQL); err != nil {
				t.Fatalf("%s: %v\n%s", st.SQL, err, scriptsText(ss))
			}
		}
	}
	for _, s := range ss {
		if s.Directives.NoTransaction {
			for i := 0; i < len(s.Statements); {
				j := i + 1
				for j < len(s.Statements) && s.Statements[j].Unit == s.Statements[i].Unit {
					j++
				}
				unit := s.Statements[i:j]
				if resume {
					for k := 1; k <= len(unit); k++ {
						exec(unit[:k])
					}
				}
				exec(unit)
				i = j
			}
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if s.Directives.LockTimeoutSet {
			if _, err := tx.Exec(fmt.Sprintf("SET LOCAL lock_timeout = '%dms'", s.Directives.LockTimeout.Milliseconds())); err != nil {
				t.Fatal(err)
			}
		}
		for _, st := range s.Statements {
			if _, err := tx.Exec(st.SQL); err != nil {
				_ = tx.Rollback()
				t.Fatalf("%s: %v\n%s", st.SQL, err, scriptsText(ss))
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
}

// pgDescribe lists what the database holds outside the system schemas,
// sorted: columns (type, nullability, default, identity, comment), indexes,
// constraints, enum types with their labels in order, sequences and table
// comments.
func pgDescribe(t *testing.T, db *sql.DB) []string {
	t.Helper()
	var out []string
	for _, q := range []string{
		`SELECT 'column ' || c.table_schema || '.' || c.table_name || '.' || c.column_name || ' ' || c.udt_schema || '.' || c.udt_name ||
			' ' || c.is_nullable || ' ' || coalesce(c.column_default, '-') || ' ' || c.is_identity || ' ' ||
			coalesce(col_description((quote_ident(c.table_schema) || '.' || quote_ident(c.table_name))::regclass, c.ordinal_position::int), '-') ||
			' ' || coalesce(c.character_maximum_length::text, '-')
		FROM information_schema.columns c WHERE c.table_schema NOT IN ('pg_catalog', 'information_schema')`,
		`SELECT 'index ' || schemaname || ' ' || indexdef FROM pg_indexes WHERE schemaname NOT IN ('pg_catalog', 'information_schema')`,
		// PostgreSQL 18 names NOT NULL constraints itself; velox models nullability, which the column query compares through is_nullable, and not those names.
		`SELECT 'constraint ' || n.nspname || '.' || r.relname || ' ' || c.conname || ' ' || pg_get_constraintdef(c.oid) || ' ' || c.convalidated
		FROM pg_constraint c JOIN pg_class r ON r.oid = c.conrelid JOIN pg_namespace n ON n.oid = r.relnamespace
		WHERE n.nspname NOT IN ('pg_catalog', 'information_schema') AND c.contype <> 'n'`,
		`SELECT 'enum ' || n.nspname || '.' || t.typname || ' ' || string_agg(e.enumlabel, ',' ORDER BY e.enumsortorder)
		FROM pg_type t JOIN pg_enum e ON e.enumtypid = t.oid JOIN pg_namespace n ON n.oid = t.typnamespace GROUP BY n.nspname, t.typname`,
		`SELECT 'sequence ' || schemaname || '.' || sequencename || ' ' || data_type FROM pg_sequences`,
		`SELECT 'comment ' || n.nspname || '.' || r.relname || ' ' || coalesce(obj_description(r.oid, 'pg_class'), '-')
		FROM pg_class r JOIN pg_namespace n ON n.oid = r.relnamespace
		WHERE r.relkind = 'r' AND n.nspname NOT IN ('pg_catalog', 'information_schema')`,
	} {
		rows, err := db.Query(q)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
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
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(out)
	return out
}

// TestRenderOwnedSequence: a serial's sequence is created before its table,
// as its column's type, and owned by the column after it, before the
// table's indexes; an identity's is the identity's own.
func TestRenderOwnedSequence(t *testing.T) {
	empty := &migrate.Schema{Dialect: velox.Postgres}
	serial := func(schema string) *migrate.Schema {
		def := "nextval('g_id_seq')"
		if schema != "" {
			def = "nextval('" + schema + ".g_id_seq')"
		}
		return &migrate.Schema{Dialect: velox.Postgres, Tables: []migrate.Table{{
			Schema: schema, Name: "g",
			Columns: []migrate.Column{
				{Name: "id", Type: "integer", Default: def, Sequence: "g_id_seq"},
				{Name: "n", Type: "bigint"},
			},
			Indexes: []migrate.Index{{Name: "g_n", Columns: []string{"n"}}},
		}}}
	}
	for schema, want := range map[string][]string{
		"": {
			`CREATE SEQUENCE "g_id_seq" AS integer`,
			"CREATE TABLE \"g\" (\n  \"id\" integer NOT NULL DEFAULT nextval('g_id_seq'),\n  \"n\" bigint NOT NULL\n)",
			`ALTER SEQUENCE "g_id_seq" OWNED BY "g"."id"`,
			`CREATE INDEX "g_n" ON "g" ("n")`,
		},
		"billing": {
			`CREATE SCHEMA IF NOT EXISTS "billing"`,
			`CREATE SEQUENCE "billing"."g_id_seq" AS integer`,
			"CREATE TABLE \"billing\".\"g\" (\n  \"id\" integer NOT NULL DEFAULT nextval('billing.g_id_seq'),\n  \"n\" bigint NOT NULL\n)",
			`ALTER SEQUENCE "billing"."g_id_seq" OWNED BY "billing"."g"."id"`,
			`CREATE INDEX "g_n" ON "billing"."g" ("n")`,
		},
	} {
		to := serial(schema)
		if got := sqls(render(t, empty, to, diff(t, empty, to, migrate.DiffOptions{}), migrate.RenderOptions{})); !slices.Equal(got, want) {
			t.Errorf("schema %q:\n%s\nwant\n%s", schema, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}

	// A type that is no integer gets no AS.
	to := serial("")
	to.Tables[0].Columns[0].Type = "numeric(20,0)"
	if got := sqls(render(t, empty, to, diff(t, empty, to, migrate.DiffOptions{}), migrate.RenderOptions{})); got[0] != `CREATE SEQUENCE "g_id_seq"` {
		t.Errorf("numeric: %s", got[0])
	}

	to = serial("")
	id := &to.Tables[0].Columns[0]
	id.Identity, id.Default = true, ""
	got := sqls(render(t, empty, to, diff(t, empty, to, migrate.DiffOptions{}), migrate.RenderOptions{}))
	for _, s := range got {
		if strings.Contains(s, "CREATE SEQUENCE") || strings.Contains(s, "OWNED BY") {
			t.Errorf("an identity: %s", strings.Join(got, "\n"))
			break
		}
	}
}

// constraintUsers is the PostgreSQL table name, with an identity id, email
// and name, its key, and ixs.
func constraintUsers(name string, ixs ...migrate.Index) migrate.Table {
	return migrate.Table{
		Name: name, PrimaryKey: []string{"id"}, PrimaryKeyName: name + "_pkey",
		Columns: []migrate.Column{
			{Name: "id", Type: "bigint", Identity: true, Sequence: name + "_id_seq"},
			{Name: "email", Type: "text"}, {Name: "name", Type: "text"},
		},
		Indexes: ixs,
	}
}

// emailKey is the unique index name over cols, email when none; a
// constraint's when constraint is set.
func emailKey(name string, constraint bool, cols ...string) migrate.Index {
	if len(cols) == 0 {
		cols = []string{"email"}
	}
	return migrate.Index{Name: name, Columns: cols, Unique: true, Constraint: constraint}
}

// TestRenderPostgresConstraintIndex: a unique constraint's index is dropped
// and renamed as the constraint, in the main script, and a flagged index is
// created and added as a constraint.
func TestRenderPostgresConstraintIndex(t *testing.T) {
	from := schemaOf(velox.Postgres, constraintUsers("users", emailKey("users_email_key", true)))
	addUnique := []migrate.Allow{{Code: "add-unique"}}
	for _, c := range []struct {
		name         string
		to           *migrate.Schema
		opts         migrate.PlanOptions
		main, online []string
	}{
		{"drop", schemaOf(velox.Postgres, constraintUsers("users")), migrate.PlanOptions{},
			[]string{`ALTER TABLE "users" DROP CONSTRAINT "users_email_key"`}, nil},
		{"rename", schemaOf(velox.Postgres, constraintUsers("users", emailKey("users_mail_key", false))), migrate.PlanOptions{},
			[]string{`ALTER TABLE "users" RENAME CONSTRAINT "users_email_key" TO "users_mail_key"`}, nil},
		{"redefine", schemaOf(velox.Postgres, constraintUsers("users", emailKey("users_email_key", false, "email", "name"))),
			migrate.PlanOptions{Allow: addUnique},
			[]string{`ALTER TABLE "users" DROP CONSTRAINT "users_email_key"`},
			[]string{
				`DROP INDEX CONCURRENTLY IF EXISTS "users_email_key"`,
				`CREATE UNIQUE INDEX CONCURRENTLY "users_email_key" ON "users" ("email", "name")`,
			}},
		{"table rename", schemaOf(velox.Postgres, constraintUsers("accounts", emailKey("accounts_email_key", false))),
			migrate.PlanOptions{Diff: migrate.DiffOptions{RenameTables: map[migrate.TableRef]migrate.TableRef{{Name: "users"}: {Name: "accounts"}}}},
			[]string{
				`ALTER TABLE "users" RENAME TO "accounts"`,
				`ALTER SEQUENCE "users_id_seq" RENAME TO "accounts_id_seq"`,
				`ALTER TABLE "accounts" RENAME CONSTRAINT "users_email_key" TO "accounts_email_key"`,
				`ALTER TABLE "accounts" RENAME CONSTRAINT "users_pkey" TO "accounts_pkey"`,
			}, nil},
		{"drop with its column", schemaOf(velox.Postgres, func() migrate.Table {
			u := constraintUsers("users")
			u.Columns = slices.DeleteFunc(u.Columns, func(c migrate.Column) bool { return c.Name == "email" })
			return u
		}()), migrate.PlanOptions{Allow: []migrate.Allow{{Code: "drop-column"}}},
			[]string{
				`ALTER TABLE "users" DROP CONSTRAINT "users_email_key"`,
				`ALTER TABLE "users" DROP COLUMN "email"`,
			}, nil},
		{"drop after a table rename", schemaOf(velox.Postgres, constraintUsers("accounts")),
			migrate.PlanOptions{Diff: migrate.DiffOptions{RenameTables: map[migrate.TableRef]migrate.TableRef{{Name: "users"}: {Name: "accounts"}}}},
			[]string{
				`ALTER TABLE "users" RENAME TO "accounts"`,
				`ALTER SEQUENCE "users_id_seq" RENAME TO "accounts_id_seq"`,
				`ALTER TABLE "accounts" RENAME CONSTRAINT "users_pkey" TO "accounts_pkey"`,
				`ALTER TABLE "accounts" DROP CONSTRAINT "users_email_key"`,
			}, nil},
	} {
		p, err := migrate.NewPlan(from, c.to, c.opts)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var main, online []string
		if s := scriptOf(p.Scripts, migrate.ScriptMain); s != nil {
			main = stmtSQL(s.Statements)
		}
		if s := scriptOf(p.Scripts, migrate.ScriptOnline); s != nil {
			online = stmtSQL(s.Statements)
		}
		if !slices.Equal(main, c.main) || !slices.Equal(online, c.online) {
			t.Errorf("%s:\n%s\nwant main %q, online %q", c.name, scriptsText(p.Scripts), c.main, c.online)
		}
	}

	// A flagged index is created as a constraint, as a baseline holds it.
	empty := &migrate.Schema{Dialect: velox.Postgres}
	got := sqls(render(t, empty, from, diff(t, empty, from, migrate.DiffOptions{}), migrate.RenderOptions{}))
	if want := []string{"CREATE TABLE \"users\" (\n" +
		"  \"id\" bigint GENERATED BY DEFAULT AS IDENTITY (SEQUENCE NAME \"users_id_seq\") NOT NULL,\n" +
		"  \"email\" text NOT NULL,\n" +
		"  \"name\" text NOT NULL,\n" +
		"  CONSTRAINT \"users_pkey\" PRIMARY KEY (\"id\"),\n" +
		"  CONSTRAINT \"users_email_key\" UNIQUE (\"email\")\n" +
		")"}; !slices.Equal(got, want) {
		t.Errorf("create:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// And added to an existing table as one, in the main script.
	plain := schemaOf(velox.Postgres, constraintUsers("users"))
	p, err := migrate.NewPlan(plain, from, migrate.PlanOptions{Allow: addUnique})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Scripts) != 1 || p.Scripts[0].Kind != migrate.ScriptMain ||
		!slices.Equal(stmtSQL(p.Scripts[0].Statements), []string{`ALTER TABLE "users" ADD CONSTRAINT "users_email_key" UNIQUE ("email")`}) {
		t.Errorf("add:\n%s", scriptsText(p.Scripts))
	}
}
