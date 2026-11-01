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

// mysqlColumn is t.name's EXTRA and collation.
func mysqlColumn(t *testing.T, db *sql.DB, name string) (extra, collation string) {
	t.Helper()
	var coll sql.NullString
	err := db.QueryRow(`SELECT EXTRA, COLLATION_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 't' AND COLUMN_NAME = ?`, name).Scan(&extra, &coll)
	if err != nil {
		t.Fatal(err)
	}
	return extra, coll.String
}

// mysqlGuarded is a database whose t.at has ON UPDATE and t.c a collation of
// its own, which the model cannot hold, and the plan that changes their
// comments, a MODIFY COLUMN of each.
func mysqlGuarded(t *testing.T, opts migrate.PlanOptions) (*sql.DB, fstest.MapFS) {
	t.Helper()
	d := velox.MySQL
	db, _ := freshDB(t, d)
	at := migrate.Column{Name: "at", Type: "timestamp", Default: "CURRENT_TIMESTAMP"}
	c := migrate.Column{Name: "c", Type: "varchar(10)", Nullable: true}
	fsys := dirOf(t, d, step{to: oneTable(d, at, c), name: "base"})
	mustUp(t, db, fsys)
	mustExec(t, db, "ALTER TABLE t MODIFY COLUMN at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP, MODIFY COLUMN c varchar(10) COLLATE utf8mb4_general_ci NULL")
	at.Comment, c.Comment = "when", "what"
	return db, extend(t, fsys, d, step{to: oneTable(d, at, c), name: "comment", opts: opts})
}

// TestMySQLCommentChangeRefused: a change of a column's comment is written as
// MODIFY COLUMN, which would lose ON UPDATE and the collation: Up refuses
// before it changes anything, naming them, and records no version.
func TestMySQLCommentChangeRefused(t *testing.T) {
	db, fsys := mysqlGuarded(t, migrate.PlanOptions{})
	_, err := migrate.Up(context.Background(), db, fsys)
	var e *migrate.Error
	if !errors.As(err, &e) || e.Code != "unmodeled" {
		a, b := mysqlColumn(t, db, "at")
		t.Fatalf("Up: %v; want an *Error of Code unmodeled; EXTRA %q, collation %q\n%s", err, a, b, sqlFiles(fsys))
	}
	for _, want := range []string{"t.at", "ON UPDATE", "t.c", "utf8mb4_general_ci", "velox:allow unmodeled-loss t.at", "migrate hash", "0002_comment.sql"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not say %q: %v", want, err)
		}
	}
	if extra, _ := mysqlColumn(t, db, "at"); !strings.Contains(strings.ToLower(extra), "on update") {
		t.Errorf("EXTRA %q after the refusal", extra)
	}
	if _, coll := mysqlColumn(t, db, "c"); coll != "utf8mb4_general_ci" {
		t.Errorf("collation %q after the refusal", coll)
	}
	if st := mustStatus(t, db, fsys); len(st.Pending) != 1 || st.Dirty != nil {
		t.Errorf("pending %v, dirty %v", st.Pending, st.Dirty)
	}
}

// TestMySQLModifyKeptByTheFile: a statement that writes what it would lose
// runs, and keeps it, with no directive.
func TestMySQLModifyKeptByTheFile(t *testing.T) {
	db, fsys := mysqlGuarded(t, migrate.PlanOptions{})
	fsys = rewrite(t, fsys, "0002_comment.sql", func(s string) string {
		s = strings.Replace(s, "DEFAULT CURRENT_TIMESTAMP COMMENT", "DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT", 1)
		return strings.Replace(s, "varchar(10) NULL COMMENT", "varchar(10) COLLATE utf8mb4_general_ci NULL COMMENT", 1)
	})
	if _, err := migrate.Up(context.Background(), db, fsys); err != nil {
		t.Fatalf("Up: %v\n%s", err, sqlFiles(fsys))
	}
	if extra, _ := mysqlColumn(t, db, "at"); !strings.Contains(strings.ToLower(extra), "on update") {
		t.Errorf("EXTRA %q", extra)
	}
	if _, coll := mysqlColumn(t, db, "c"); coll != "utf8mb4_general_ci" {
		t.Errorf("collation %q", coll)
	}
}

// TestMySQLModifyAllowed: the directive accepts the loss, for its target, and
// one for the other column alone leaves this one refused.
func TestMySQLModifyAllowed(t *testing.T) {
	db, fsys := mysqlGuarded(t, allowAll("unmodeled-loss"))
	if _, err := migrate.Up(context.Background(), db, fsys); err != nil {
		t.Fatalf("Up: %v\n%s", err, sqlFiles(fsys))
	}
	if extra, _ := mysqlColumn(t, db, "at"); strings.Contains(strings.ToLower(extra), "on update") {
		t.Errorf("EXTRA %q: the loss was accepted", extra)
	}

	db, fsys = mysqlGuarded(t, migrate.PlanOptions{Allow: []migrate.Allow{{Code: "unmodeled-loss", Target: "t.c"}}})
	_, err := migrate.Up(context.Background(), db, fsys)
	var e *migrate.Error
	if !errors.As(err, &e) || e.Code != "unmodeled" || !strings.Contains(err.Error(), "t.at") || strings.Contains(err.Error(), "t.c:") {
		t.Errorf("Up: %v", err)
	}
}

// TestMySQLModifyFollowsRenames: the guard reads a column under the name the
// database holds, before a rename the same file makes; a column it creates has
// nothing to lose.
func TestMySQLModifyFollowsRenames(t *testing.T) {
	d := velox.MySQL
	db, _ := freshDB(t, d)
	at := migrate.Column{Name: "at", Type: "timestamp", Default: "CURRENT_TIMESTAMP"}
	fsys := dirOf(t, d, step{to: oneTable(d, at), name: "base"})
	mustUp(t, db, fsys)
	mustExec(t, db, "ALTER TABLE t MODIFY COLUMN at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP")
	moved := extend(t, fsys, d, files("0002_rebuild.sql", "RENAME TABLE t TO t2;\nALTER TABLE t2 RENAME COLUMN at TO changed;\nALTER TABLE t2 MODIFY COLUMN changed timestamp NOT NULL COMMENT 'x';\n"))
	_, err := migrate.Up(context.Background(), db, moved)
	var e *migrate.Error
	if !errors.As(err, &e) || e.Code != "unmodeled" || !strings.Contains(err.Error(), "t2.changed") {
		t.Errorf("Up: %v", err)
	}

	created := extend(t, fsys, d, files("0002_rebuild.sql", "ALTER TABLE t ADD COLUMN n integer;\nALTER TABLE t MODIFY COLUMN n bigint;\nCREATE TABLE u (a integer);\nALTER TABLE u MODIFY a bigint;\n"))
	if _, err := migrate.Up(context.Background(), db, created); err != nil {
		t.Errorf("Up: %v", err)
	}
}
