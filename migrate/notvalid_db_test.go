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
