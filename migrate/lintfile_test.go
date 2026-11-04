package migrate_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// lintFile parses src as the file 0002_x.sql of d and lints it.
func lintFile(t *testing.T, d velox.Dialect, src string) ([]migrate.Finding, error) {
	t.Helper()
	f, err := migrate.ParseFile(d, "0002_x.sql", []byte(src))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	return migrate.LintFile(d, f)
}

// found is a finding as the test compares it.
type found struct{ Code, Target, Pos string }

func TestLintFile(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		d    velox.Dialect
		src  string
		want []found
	}{
		{velox.Postgres, "SELECT 1;\nDROP TABLE users;", []found{{"drop-table", "users", "0002_x.sql:2"}}},
		{velox.Postgres, `DROP TABLE IF EXISTS "Users", billing.invoices CASCADE;`, []found{{"drop-table", "Users", "0002_x.sql:1"}, {"drop-table", "billing.invoices", "0002_x.sql:1"}}},
		{velox.Postgres, "drop table Users;", []found{{"drop-table", "users", "0002_x.sql:1"}}},
		{velox.MySQL, "DROP TABLE `Users`;", []found{{"drop-table", "Users", "0002_x.sql:1"}}},
		{velox.Postgres, "ALTER TABLE users DROP COLUMN name;", []found{{"drop-column", "users.name", "0002_x.sql:1"}}},
		{velox.SQLite, "ALTER TABLE users DROP COLUMN name;", []found{{"drop-column", "users.name", "0002_x.sql:1"}}},
		{velox.MySQL, "ALTER TABLE users DROP name, DROP INDEX users_name, DROP FOREIGN KEY fk;", []found{{"drop-column", "users.name", "0002_x.sql:1"}}},
		{velox.Postgres, "ALTER TABLE ONLY billing.invoices DROP COLUMN IF EXISTS total, DROP CONSTRAINT c;", []found{{"drop-column", "billing.invoices.total", "0002_x.sql:1"}}},
		{velox.Postgres, "ALTER TABLE users ALTER COLUMN age TYPE bigint;", []found{{"alter-type", "users.age", "0002_x.sql:1"}}},
		{velox.Postgres, "ALTER TABLE users ALTER age SET DATA TYPE bigint USING age::bigint;", []found{{"alter-type", "users.age", "0002_x.sql:1"}}},
		{velox.MySQL, "ALTER TABLE users MODIFY COLUMN age bigint NOT NULL;", []found{{"alter-type", "users.age", "0002_x.sql:1"}}},
		{velox.MySQL, "ALTER TABLE users MODIFY age bigint;", []found{{"alter-type", "users.age", "0002_x.sql:1"}}},
		{velox.Postgres, "ALTER TABLE users ALTER COLUMN name SET NOT NULL, ALTER COLUMN name SET DEFAULT 'x';", []found{{"set-not-null", "users.name", "0002_x.sql:1"}}},
		{velox.Postgres, "TRUNCATE users, posts;", []found{{"delete-rows", "users", "0002_x.sql:1"}, {"delete-rows", "posts", "0002_x.sql:1"}}},
		{velox.MySQL, "TRUNCATE TABLE users;", []found{{"delete-rows", "users", "0002_x.sql:1"}}},
		{velox.SQLite, "\n\nDELETE FROM users;", []found{{"delete-rows", "users", "0002_x.sql:3"}}},
		{velox.Postgres, "DELETE FROM users WHERE id = 1;", nil},
		{velox.MySQL, "UPDATE users SET name = 'x';", []found{{"update-rows", "users", "0002_x.sql:1"}}},
		{velox.SQLite, "UPDATE users SET name = 'x' WHERE id = 1;", nil},
		{velox.Postgres, "SELECT 'DROP TABLE users';\n-- DROP TABLE users;\n/* DROP TABLE users; */", nil},
		{velox.MySQL, "SELECT \"DROP TABLE users\"; # DROP TABLE users;", nil},
		{velox.Postgres, "DO $$ BEGIN DROP TABLE users; END $$;", nil},
		{velox.Postgres, "ALTER TABLE users ADD COLUMN email text, ALTER COLUMN name DROP NOT NULL;", nil},
		// Only a WHERE of the statement itself counts.
		{velox.Postgres, "UPDATE users SET x = (SELECT y FROM z WHERE z.id = users.zid);", []found{{"update-rows", "users", "0002_x.sql:1"}}},
		{velox.Postgres, "DELETE FROM users WHERE id IN (SELECT id FROM gone);", nil},
		{velox.MySQL, "DELETE FROM users WHERE (id = 1);", nil},
		// A WITH clause comes before the statement.
		{velox.Postgres, "WITH old AS (SELECT id FROM users WHERE age > 9) DELETE FROM users;", []found{{"delete-rows", "users", "0002_x.sql:1"}}},
		{velox.Postgres, "WITH RECURSIVE a(x) AS (SELECT 1), b AS MATERIALIZED (SELECT 2) UPDATE users SET n = 1;", []found{{"update-rows", "users", "0002_x.sql:1"}}},
		{velox.Postgres, "WITH a AS (DELETE FROM logs) DELETE FROM users WHERE id = 1;", []found{{"delete-rows", "logs", "0002_x.sql:1"}}},
		{velox.Postgres, "WITH a AS (SELECT 1) SELECT * FROM a;", nil},
		// MySQL's multi-table forms name their tables before FROM, or before SET.
		{velox.MySQL, "DELETE u FROM users u JOIN orgs o ON o.id = u.org_id;", []found{{"delete-rows", "users", "0002_x.sql:1"}}},
		{velox.MySQL, "DELETE LOW_PRIORITY users, orgs FROM users JOIN orgs ON orgs.id = users.org_id;", []found{{"delete-rows", "users", "0002_x.sql:1"}, {"delete-rows", "orgs", "0002_x.sql:1"}}},
		{velox.MySQL, "DELETE u FROM users AS u JOIN orgs o ON o.id = u.org_id WHERE o.gone;", nil},
		{velox.MySQL, "DELETE FROM users, orgs USING users JOIN orgs ON orgs.id = users.org_id;", []found{{"delete-rows", "users", "0002_x.sql:1"}, {"delete-rows", "orgs", "0002_x.sql:1"}}},
		{velox.MySQL, "UPDATE users u JOIN orgs o ON o.id = u.org_id SET u.name = o.name;", []found{{"update-rows", "users", "0002_x.sql:1"}}},
		{velox.MySQL, "UPDATE users, orgs SET users.name = orgs.name;", []found{{"update-rows", "users", "0002_x.sql:1"}, {"update-rows", "orgs", "0002_x.sql:1"}}},
		// What destroys more than a table.
		{velox.Postgres, "DROP SCHEMA app CASCADE;", []found{{"drop-schema", "app", "0002_x.sql:1"}}},
		{velox.Postgres, `DROP SCHEMA IF EXISTS app, "Other";`, []found{{"drop-schema", "app", "0002_x.sql:1"}, {"drop-schema", "Other", "0002_x.sql:1"}}},
		{velox.MySQL, "DROP SCHEMA app;", []found{{"drop-schema", "app", "0002_x.sql:1"}}},
		{velox.MySQL, "DROP DATABASE IF EXISTS app;", []found{{"drop-database", "app", "0002_x.sql:1"}}},
		{velox.Postgres, "DROP DATABASE app;", []found{{"drop-database", "app", "0002_x.sql:1"}}},
		// MySQL's other way to retype a column.
		{velox.MySQL, "ALTER TABLE users CHANGE COLUMN age years bigint;", []found{{"alter-type", "users.age", "0002_x.sql:1"}}},
		{velox.MySQL, "ALTER TABLE users CHANGE age age bigint, DROP COLUMN x;", []found{{"alter-type", "users.age", "0002_x.sql:1"}, {"drop-column", "users.x", "0002_x.sql:1"}}},
		// A WHERE that leaves no row out is none.
		{velox.Postgres, "DELETE FROM users WHERE true;", []found{{"delete-rows", "users", "0002_x.sql:1"}}},
		{velox.MySQL, "DELETE FROM users WHERE 1;", []found{{"delete-rows", "users", "0002_x.sql:1"}}},
		{velox.SQLite, "DELETE FROM users WHERE 1 = 1;", []found{{"delete-rows", "users", "0002_x.sql:1"}}},
		{velox.SQLite, "DELETE FROM users WHERE 'a' = 'a';", []found{{"delete-rows", "users", "0002_x.sql:1"}}},
		{velox.Postgres, "DELETE FROM users WHERE (TRUE) RETURNING id;", []found{{"delete-rows", "users", "0002_x.sql:1"}}},
		{velox.Postgres, "DELETE FROM users WHERE id = id;", []found{{"delete-rows", "users", "0002_x.sql:1"}}},
		{velox.Postgres, "DELETE FROM users WHERE NOT false;", []found{{"delete-rows", "users", "0002_x.sql:1"}}},
		{velox.Postgres, "DELETE FROM users WHERE x = 1 OR 1 = 1;", []found{{"delete-rows", "users", "0002_x.sql:1"}}},
		{velox.MySQL, "DELETE FROM users WHERE 1 = 1 ORDER BY id;", []found{{"delete-rows", "users", "0002_x.sql:1"}}},
		{velox.Postgres, "UPDATE users SET a = 1 WHERE true AND 1 = 1;", []found{{"update-rows", "users", "0002_x.sql:1"}}},
		{velox.Postgres, "DELETE FROM users WHERE true AND x = 1;", nil},
		{velox.Postgres, "DELETE FROM users WHERE false;", nil},
		{velox.MySQL, "DELETE FROM users WHERE 0;", nil},
		{velox.Postgres, "DELETE FROM users WHERE 1 = 2;", nil},
		{velox.Postgres, "DELETE FROM users WHERE a = b;", nil},
		{velox.Postgres, "DELETE FROM users WHERE x = 'true';", nil},
		{velox.Postgres, "UPDATE users SET a = 1 WHERE 1 = 1 AND EXISTS (SELECT 1 FROM z);", nil},
		// A data-modifying WITH clause, at any depth; a read and a bounded write are none.
		{velox.Postgres, "WITH d AS (DELETE FROM users RETURNING id) SELECT 1;", []found{{"delete-rows", "users", "0002_x.sql:1"}}},
		{velox.Postgres, "WITH d AS NOT MATERIALIZED (UPDATE users SET a = 1 RETURNING id) SELECT * FROM d;", []found{{"update-rows", "users", "0002_x.sql:1"}}},
		{velox.Postgres, "WITH a AS (SELECT 1), d AS (DELETE FROM users RETURNING id), e AS (DELETE FROM logs RETURNING id) INSERT INTO z SELECT id FROM d;", []found{{"delete-rows", "users", "0002_x.sql:1"}, {"delete-rows", "logs", "0002_x.sql:1"}}},
		{velox.Postgres, "WITH d AS (WITH e AS (DELETE FROM users RETURNING id) SELECT id FROM e) SELECT 1;", []found{{"delete-rows", "users", "0002_x.sql:1"}}},
		{velox.Postgres, "WITH d AS (DELETE FROM users WHERE id = 1 RETURNING id) SELECT 1;", nil},
		{velox.Postgres, "WITH d AS (SELECT 1) SELECT * FROM d;", nil},
		{velox.Postgres, "WITH d AS (DELETE FROM a RETURNING id) DELETE FROM b;", []found{{"delete-rows", "a", "0002_x.sql:1"}, {"delete-rows", "b", "0002_x.sql:1"}}},
		// MERGE says what it can do to its target.
		{velox.Postgres, "MERGE INTO users u USING s ON u.id = s.id WHEN MATCHED THEN DELETE;", []found{{"delete-rows", "users", "0002_x.sql:1"}}},
		{velox.Postgres, "MERGE INTO ONLY users u USING s ON u.id = s.id WHEN MATCHED AND s.x THEN UPDATE SET a = 1 WHEN NOT MATCHED THEN INSERT VALUES (1);", []found{{"update-rows", "users", "0002_x.sql:1"}}},
		{velox.Postgres, "MERGE INTO users u USING s ON u.id = s.id WHEN NOT MATCHED THEN INSERT VALUES (1);", nil},
		{velox.Postgres, "WITH x AS (SELECT 1) MERGE INTO users u USING x ON true WHEN MATCHED THEN DELETE WHEN NOT MATCHED BY SOURCE THEN UPDATE SET a = 1;", []found{{"delete-rows", "users", "0002_x.sql:1"}, {"update-rows", "users", "0002_x.sql:1"}}},
		// SQLite's conflict clause is not the table.
		{velox.SQLite, "UPDATE OR REPLACE users SET a = 1;", []found{{"update-rows", "users", "0002_x.sql:1"}}},
		{velox.SQLite, "UPDATE OR IGNORE users SET a = 1 WHERE id = 2;", nil},
	} {
		got, err := lintFile(t, c.d, c.src)
		if err != nil {
			t.Errorf("%s %q: %v", c.d, c.src, err)
			continue
		}
		var have []found
		for _, f := range got {
			if f.Class != migrate.Destructive || f.Message == "" || f.Change < 0 {
				t.Errorf("%s %q: %+v", c.d, c.src, f)
			}
			have = append(have, found{f.Code, f.Target, f.Pos})
		}
		if !reflect.DeepEqual(have, c.want) {
			t.Errorf("%s %q:\n got %v\nwant %v", c.d, c.src, have, c.want)
		}
	}
	// The statement's index is the finding's Change.
	got, _ := lintFile(t, velox.Postgres, "SELECT 1;\nSELECT 2;\nTRUNCATE users;")
	if len(got) != 1 || got[0].Change != 2 || got[0].Message != "deletes every row of users" {
		t.Errorf("change: %+v", got)
	}
	got, _ = lintFile(t, velox.Postgres, "UPDATE users SET a = 1;")
	if len(got) != 1 || got[0].Message != "updates every row of users" {
		t.Errorf("message: %+v", got)
	}

	// A header is no pass: what is added to a generated file is classified,
	// all but what Render itself writes uncovered, a rebuild's drop and a
	// change of a column's type or comment.
	const generated = "-- Generated by velox-gen v1.0.0 migrate diff. Review before applying; velox.sum checks it.\n"
	got, err := lintFile(t, velox.Postgres, generated+"DROP TABLE users;")
	if err != nil || len(got) != 1 || got[0].Code != "drop-table" || got[0].Target != "users" {
		t.Errorf("generated: %v %v", got, err)
	}
	got, err = lintFile(t, velox.SQLite, generated+`-- velox:foreign-keys off
CREATE TABLE "velox_new_t" (x integer);
DROP TABLE "t";
ALTER TABLE "velox_new_t" RENAME TO "t";
DROP TABLE "other";
`)
	if err != nil || len(got) != 1 || got[0].Target != "other" {
		t.Errorf("generated rebuild: %v %v", got, err)
	}
	got, err = lintFile(t, velox.SQLite, `-- velox:foreign-keys off
CREATE TABLE "velox_new_t" (x integer);
DROP TABLE "t";
ALTER TABLE "velox_new_t" RENAME TO "t";
`)
	if err != nil || len(got) != 1 || got[0].Target != "t" {
		t.Errorf("a rebuild by hand: %v %v", got, err)
	}
	if got, err = lintFile(t, velox.MySQL, generated+"ALTER TABLE `t` MODIFY COLUMN `c` varchar(10) NULL COMMENT 'x';"); err != nil || len(got) != 0 {
		t.Errorf("generated MODIFY: %v %v", got, err)
	}

	// What cannot run in a transaction is an error.
	for _, c := range []struct {
		d        velox.Dialect
		src, pos string
	}{
		{velox.Postgres, "SELECT 1;\nCREATE INDEX CONCURRENTLY users_name ON users (name);", "0002_x.sql:2"},
		{velox.Postgres, "ALTER TYPE mood ADD VALUE 'ok';", "0002_x.sql:1"},
		{velox.Postgres, "BEGIN;\nSELECT 1;", "0002_x.sql:1"},
		{velox.Postgres, "SELECT 1;\nCOMMIT;", "0002_x.sql:2"},
		{velox.SQLite, "BEGIN TRANSACTION;", "0002_x.sql:1"},
		{velox.SQLite, "START TRANSACTION;", "0002_x.sql:1"},
		{velox.SQLite, "SELECT 1;\nEND;", "0002_x.sql:2"},
		{velox.SQLite, "ROLLBACK;", "0002_x.sql:1"},
		{velox.Postgres, "SELECT 1;\nABORT;", "0002_x.sql:2"},
	} {
		_, err := lintFile(t, c.d, c.src)
		var e *migrate.Error
		if !errors.As(err, &e) || e.Code != "file" || e.Pos != c.pos || !strings.HasPrefix(err.Error(), "migrate: "+c.pos+": ") {
			t.Errorf("%s %q: %v", c.d, c.src, err)
		}
	}
	// Two errors are joined.
	_, err = lintFile(t, velox.Postgres, "BEGIN;\nCOMMIT;")
	if j, ok := err.(interface{ Unwrap() []error }); !ok || len(j.Unwrap()) != 2 {
		t.Errorf("joined: %v", err)
	}
	// Outside a transaction, and on MySQL, they are not.
	for _, c := range []struct {
		d   velox.Dialect
		src string
	}{
		{velox.Postgres, "-- velox:no-transaction\nCREATE INDEX CONCURRENTLY users_name ON users (name);\nALTER TYPE mood ADD VALUE 'ok';\nCOMMIT;"},
		{velox.MySQL, "BEGIN;\nCOMMIT;"},
		{velox.Postgres, "SELECT 'BEGIN';\nCREATE TABLE concurrently (id int);"},
	} {
		if _, err := lintFile(t, c.d, c.src); err != nil {
			t.Errorf("%s %q: %v", c.d, c.src, err)
		}
	}

	// The codes that only LintFile gives can be allowed.
	if as, err := migrate.ParseAllow("delete-rows,update-rows,drop-schema,drop-database"); err != nil || len(as) != 4 {
		t.Errorf("ParseAllow: %v %v", as, err)
	}
}

// TestLintFileGeneratedClean: what Dir.Add writes, with its directives, lints
// clean on every case and dialect.
func TestLintFileGeneratedClean(t *testing.T) {
	t.Parallel()
	for _, c := range cases {
		for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
			if !slices.Contains(c.on(), d) {
				continue
			}
			dir, err := migrate.ReadDir(caseDir(t, c, d))
			if err != nil {
				t.Fatalf("%s %v: %v", c.name, d, err)
			}
			for _, f := range dir.Files {
				findings, err := migrate.LintFile(d, f)
				if err != nil {
					t.Errorf("%s %v %s: %v", c.name, d, f.Path, err)
				}
				for _, b := range migrate.Blocked(findings, f.Directives.Allow) {
					t.Errorf("%s %v: %s: %s: %s", c.name, d, b.Pos, b.Code, b.Message)
				}
			}
		}
	}
}

func TestWords(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		d    velox.Dialect
		sql  string
		want []string
	}{
		{velox.Postgres, `alter table "Users" drop column x -- c`, []string{"ALTER", "TABLE", "Users", "DROP", "COLUMN", "X"}},
		{velox.Postgres, `SELECT 'a b', E'c\'d', $t$e f$t$ /* g */ FROM t`, []string{"SELECT", ",", ",", "FROM", "T"}},
		{velox.MySQL, "SELECT `a``b`, \"lit\" # x\nFROM t", []string{"SELECT", "a`b", ",", "FROM", "T"}},
		{velox.SQLite, `SELECT [a b] FROM "x""y".t`, []string{"SELECT", "a b", "FROM", `x"y`, ".", "T"}},
	} {
		if got := migrate.Words(c.d, c.sql); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %q:\n got %q\nwant %q", c.d, c.sql, got, c.want)
		}
	}
}

// TestLintFileSavepoint: ROLLBACK TO a savepoint never ends the transaction,
// so it is not refused; ROLLBACK itself is.
func TestLintFileSavepoint(t *testing.T) {
	t.Parallel()
	for _, d := range []velox.Dialect{velox.Postgres, velox.SQLite} {
		for _, q := range []string{"ROLLBACK TO SAVEPOINT s", "ROLLBACK TO s", "rollback work to savepoint s", "ROLLBACK TRANSACTION TO s"} {
			if _, err := lintFile(t, d, "SAVEPOINT s;\nCREATE TABLE a (x integer);\n"+q+";\nRELEASE s;\n"); err != nil {
				t.Errorf("%v %s: %v", d, q, err)
			}
		}
		if _, err := lintFile(t, d, "ROLLBACK WORK;\n"); err == nil {
			t.Errorf("%v: ROLLBACK WORK not refused", d)
		}
	}
}

// TestLintFileRebuildWithoutForeignKeysOff: a rebuild in a file that does not
// turn foreign keys off, whose DROP TABLE would delete the children's rows, is
// LintFile's refusal; generated files have the directive.
func TestLintFileRebuildWithoutForeignKeysOff(t *testing.T) {
	t.Parallel()
	rebuild := `CREATE TABLE "velox_new_t" (a integer);
INSERT INTO "velox_new_t" (a) SELECT a FROM t;
DROP TABLE t;
ALTER TABLE "velox_new_t" RENAME TO t;
`
	_, err := lintFile(t, velox.SQLite, rebuild)
	var e *migrate.Error
	if !errors.As(err, &e) || e.Code != "file" || !strings.Contains(err.Error(), "foreign-keys off") || !strings.Contains(e.Pos, "0002_x.sql:1") {
		t.Errorf("no directive: %v", err)
	}
	if _, err := lintFile(t, velox.SQLite, "-- velox:foreign-keys off\n"+rebuild); err != nil {
		t.Errorf("with the directive: %v", err)
	}
	// Under another temporary name too.
	if _, err := lintFile(t, velox.SQLite, "CREATE TABLE tmp (a integer);\nINSERT INTO tmp SELECT * FROM t;\nDROP TABLE t;\nALTER TABLE tmp RENAME TO t;\n"); err == nil {
		t.Error("another name: no refusal")
	}
	if _, err := lintFile(t, velox.SQLite, "ALTER TABLE a RENAME TO b;\n"); err != nil {
		t.Errorf("a plain rename: %v", err)
	}
}
