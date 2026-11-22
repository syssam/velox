package migrate_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// TestUpDryRunMySQL: a dry run refuses the MODIFY that would lose ON UPDATE
// and a collation, as Up does, and changes nothing; once the file writes
// them, it reports the file and still runs nothing.
func TestUpDryRunMySQL(t *testing.T) {
	t.Parallel()
	db, fsys := mysqlGuarded(t, migrate.PlanOptions{})
	_, err := migrate.Up(context.Background(), db, fsys, migrate.WithDryRun())
	if e, ok := errors.AsType[*migrate.Error](err); !ok || e.Code != "unmodeled" || !strings.Contains(err.Error(), "ON UPDATE") {
		t.Fatalf("dry run: %v", err)
	}
	fsys = rewrite(t, fsys, "0002_comment.sql", func(s string) string {
		s = strings.Replace(s, "DEFAULT CURRENT_TIMESTAMP COMMENT", "DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT", 1)
		return strings.Replace(s, "varchar(10) NULL COMMENT", "varchar(10) COLLATE utf8mb4_general_ci NULL COMMENT", 1)
	})
	r, err := migrate.Up(context.Background(), db, fsys, migrate.WithDryRun())
	if err != nil || !slices.Equal(versions(r.Applied), []uint64{2}) {
		t.Fatalf("dry run: %+v, %v", r, err)
	}
	if st := mustStatus(t, db, fsys); len(st.Pending) != 1 || st.Dirty != nil {
		t.Errorf("pending %v, dirty %v after the dry run", st.Pending, st.Dirty)
	}
}

// TestUpDryRunPostgres: a dry run refuses the retype that would lose a
// collation, as Up does, and on a new database reports every file and makes
// no table, the history's neither.
func TestUpDryRunPostgres(t *testing.T) {
	t.Parallel()
	d := velox.Postgres
	db, _ := freshDB(t, d)
	col := func(typ string) migrate.Column { return migrate.Column{Name: "c", Type: typ, Nullable: true} }
	base := dirOf(t, d, step{to: oneTable(d, col("character varying(10)")), name: "base"})
	fsys := extend(t, base, d, step{to: oneTable(d, col("character varying(20)")), name: "widen"})
	r, err := migrate.Up(context.Background(), db, fsys, migrate.WithDryRun())
	if err != nil || !slices.Equal(versions(r.Applied), []uint64{1, 2}) {
		t.Fatalf("dry run on a new database: %+v, %v", r, err)
	}
	var tables int
	if err := db.QueryRow(`SELECT count(*) FROM pg_tables WHERE schemaname = current_schema()`).Scan(&tables); err != nil || tables != 0 {
		t.Errorf("%d tables after the dry run, %v", tables, err)
	}
	mustUp(t, db, base)
	mustExec(t, db, `ALTER TABLE t ALTER COLUMN c TYPE varchar(10) COLLATE "C"`)
	_, err = migrate.Up(context.Background(), db, fsys, migrate.WithDryRun())
	if e, ok := errors.AsType[*migrate.Error](err); !ok || e.Code != "unmodeled" || !strings.Contains(err.Error(), "COLLATE") {
		t.Fatalf("dry run: %v", err)
	}
	if coll := pgCollation(t, db); coll != "C" {
		t.Errorf("the collation after the dry run: %q", coll)
	}
}
