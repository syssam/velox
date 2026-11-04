package migrate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// TestPostgresNotNullNotValid: PostgreSQL 18's NOT NULL NOT VALID sets attnotnull and
// leaves the rows from before it unchecked, so a column could hold NULL where Inspect
// read NOT NULL: Drift against a model that says NOT NULL found nothing, and a read
// into a field that cannot hold NULL failed. Inspect reads the column as nullable,
// with a note that names the constraint, and a plan that makes it NOT NULL checks the
// rows.
func TestPostgresNotNullNotValid(t *testing.T) {
	t.Parallel()
	d := velox.Postgres
	db, _ := freshDB(t, d)
	if oneValue(t, db, `SELECT (current_setting('server_version_num')::int >= 180000)::text`) != "true" {
		t.Skip("NOT NULL NOT VALID is PostgreSQL 18's")
	}
	nullable := oneTable(d, migrate.Column{Name: "c", Type: "text", Nullable: true})
	required := oneTable(d, migrate.Column{Name: "c", Type: "text"})
	fsys := dirOf(t, d, step{to: nullable, name: "base"})
	mustUp(t, db, fsys)
	mustExec(t, db, `INSERT INTO t (id, c) VALUES (1, NULL)`)
	mustExec(t, db, `ALTER TABLE t ADD CONSTRAINT t_c_nn NOT NULL c NOT VALID`)

	ctx := context.Background()
	r, err := migrate.Drift(ctx, db, required)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) == 0 || !strings.Contains(reportText(r), "t_c_nn") {
		t.Errorf("Drift against NOT NULL:\n%s", reportText(r))
	}
	r, err = migrate.Drift(ctx, db, nullable)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) > 0 {
		t.Errorf("Drift against the snapshot:\n%s", reportText(r))
	}

	fsys = extend(t, fsys, d, step{to: required, name: "required", opts: allowAll("set-not-null")})
	if _, err := migrate.Up(ctx, db, fsys); err == nil {
		t.Fatalf("Up made a column with a NULL NOT NULL\n%s", sqlFiles(fsys))
	}
	mustExec(t, db, `UPDATE t SET c = 'x'`)
	mustUp(t, db, fsys, migrate.WithResume())
	r, err = migrate.Drift(ctx, db, required)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) > 0 {
		t.Errorf("Drift after the plan:\n%s", reportText(r))
	}
}

// TestPostgresNotEnforced: PostgreSQL 18's NOT ENFORCED foreign key or check holds no
// row to it. Inspect read it as NOT VALID, whose note said VALIDATE CONSTRAINT checks
// the rows, which PostgreSQL refuses for it. It is a note of its own, as MySQL's NOT
// ENFORCED check is, and a statement that adds it again must write it.
func TestPostgresNotEnforced(t *testing.T) {
	t.Parallel()
	d := velox.Postgres
	db, _ := freshDB(t, d)
	if oneValue(t, db, `SELECT (current_setting('server_version_num')::int >= 180000)::text`) != "true" {
		t.Skip("NOT ENFORCED is PostgreSQL 18's")
	}
	s := oneTable(d, migrate.Column{Name: "p", Type: "integer", Nullable: true})
	s.Tables[0].ForeignKeys = []migrate.ForeignKey{{Name: "t_p_fkey", Columns: []string{"p"}, RefTable: migrate.TableRef{Name: "t"}, RefColumns: []string{"id"}}}
	s.Tables[0].Checks = []migrate.Check{{Name: "t_p_check", Expr: "(p > 0)"}}
	fsys := dirOf(t, d, step{to: s, name: "base"})
	mustUp(t, db, fsys)
	mustExec(t, db, `ALTER TABLE t DROP CONSTRAINT t_p_fkey, ADD CONSTRAINT t_p_fkey FOREIGN KEY (p) REFERENCES t (id) NOT ENFORCED`)
	mustExec(t, db, `ALTER TABLE t DROP CONSTRAINT t_p_check, ADD CONSTRAINT t_p_check CHECK (p > 0) NOT ENFORCED`)
	mustExec(t, db, `INSERT INTO t (id, p) VALUES (1, -5)`)
	r, err := migrate.Drift(context.Background(), db, s)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"t.t_p_fkey", "t.t_p_check"} {
		if !strings.Contains(reportText(r), name+": the constraint is NOT ENFORCED") {
			t.Errorf("Drift says nothing of %s:\n%s", name, reportText(r))
		}
	}
	refused(t, db, extend(t, fsys, d, files("0002_x.sql", "ALTER TABLE t DROP CONSTRAINT t_p_check, ADD CONSTRAINT t_p_check CHECK (p > 0);\n")),
		"t.t_p_check", "NOT ENFORCED")
}
