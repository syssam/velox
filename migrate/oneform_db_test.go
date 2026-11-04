package migrate_test

import (
	"context"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// spellings are the types and defaults a schema may state otherwise than its server
// prints them.
var spellings = map[velox.Dialect][]migrate.Column{
	velox.Postgres: {
		{Name: "a", Type: "timestamp", Nullable: true},
		{Name: "b", Type: "time", Nullable: true},
		{Name: "c", Type: "timestamp(6)", Nullable: true},
		{Name: "d", Type: "timetz", Nullable: true},
		{Name: "e", Type: "text", Nullable: true, Default: "'x'::text"},
		{Name: "f", Type: "varchar(9)", Nullable: true, Default: "'x'::character varying"},
		{Name: "g", Type: "int2", Nullable: true, Default: "'5'::smallint"},
		{Name: "h", Type: "char", Nullable: true},
	},
	velox.MySQL: {
		{Name: "a", Type: "timestamp", Nullable: true},
		{Name: "b", Type: "time", Nullable: true},
		{Name: "c", Type: "timestamp(6)", Nullable: true},
		{Name: "d", Type: "integer", Nullable: true},
		{Name: "e", Type: "numeric(12,4)", Nullable: true},
		{Name: "f", Type: "bool", Nullable: true},
		{Name: "g", Type: "datetime(0)", Nullable: true},
	},
	velox.SQLite: {
		{Name: "a", Type: "TIMESTAMP", Nullable: true},
		{Name: "b", Type: "time", Nullable: true},
		{Name: "c", Type: "timestamp(6)", Nullable: true},
	},
}

// TestDriftReadsOneForm: a type or default as a schema spells it is the one its
// server prints, so a clean apply has no drift, and a baseline plans nothing.
func TestDriftReadsOneForm(t *testing.T) {
	t.Parallel()
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		t.Run(d.String(), func(t *testing.T) {
			ctx := context.Background()
			model := oneTable(d, spellings[d]...)

			db, _ := freshDB(t, d)
			mustUp(t, db, dirOf(t, d, step{to: model, name: "base"}))
			r, err := migrate.Drift(ctx, db, model)
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Changes) > 0 {
				t.Errorf("drift after a clean apply:\n%s", reportText(r))
			}

			snap, _, err := migrate.Baseline(ctx, db, model)
			if err != nil {
				t.Fatal(err)
			}
			changes, err := migrate.Diff(snap, model, migrate.DiffOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if len(changes) > 0 {
				t.Errorf("a plan after a baseline: %v", changes)
			}
			if _, err := migrate.NewPlan(snap, model, migrate.PlanOptions{}); err != nil {
				t.Errorf("a plan after a baseline is refused: %v", err)
			}
		})
	}
}
