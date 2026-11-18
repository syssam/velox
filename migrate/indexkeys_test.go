package migrate_test

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// An index's Desc and OpClass render on the column they name, the class first
// and then DESC, as CREATE INDEX writes a key; a change to either is a drop and
// a create, built beside the old index.
func TestIndexKeysRender(t *testing.T) {
	t.Parallel()
	ix := migrate.Index{
		Name: "docs_keys", Columns: []string{"name", "attrs", "id"},
		Desc: []string{"name", "id"}, OpClass: map[string]string{"name": "text_pattern_ops", "attrs": "jsonb_path_ops"},
	}
	got := renderSQL(t, methodSchema(velox.Postgres), methodSchema(velox.Postgres, ix))
	want := `CREATE INDEX CONCURRENTLY "docs_keys" ON "docs" ("name" text_pattern_ops DESC, "attrs" jsonb_path_ops, "id" DESC)`
	if !slices.Contains(got, want) {
		t.Errorf("add: %q, want %q", got, want)
	}
	// With the table, and with a method: a GIN index may name a class.
	gin := migrate.Index{Name: "docs_attrs", Columns: []string{"attrs"}, Method: "gin", OpClass: map[string]string{"attrs": "jsonb_path_ops"}}
	got = renderSQL(t, &migrate.Schema{Dialect: velox.Postgres}, methodSchema(velox.Postgres, gin))
	if want := `CREATE INDEX "docs_attrs" ON "docs" USING gin ("attrs" jsonb_path_ops)`; !slices.Contains(got, want) {
		t.Errorf("create table: %q, want %q", got, want)
	}
	// The same keys are no change; a column's direction or class is one.
	if got := renderSQL(t, methodSchema(velox.Postgres, ix), methodSchema(velox.Postgres, ix)); len(got) != 0 {
		t.Errorf("same: %q", got)
	}
	asc := ix
	asc.Desc = []string{"name"}
	got = renderSQL(t, methodSchema(velox.Postgres, ix), methodSchema(velox.Postgres, asc))
	if all := strings.Join(got, "\n"); !strings.Contains(all, `("name" text_pattern_ops DESC, "attrs" jsonb_path_ops, "id")`) ||
		!strings.Contains(all, `DROP INDEX CONCURRENTLY IF EXISTS "docs_keys"`) || !strings.Contains(all, `RENAME TO "docs_keys"`) {
		t.Errorf("a direction changes: %q", got)
	}
	other := ix
	other.OpClass = map[string]string{"name": "text_pattern_ops"}
	got = renderSQL(t, methodSchema(velox.Postgres, ix), methodSchema(velox.Postgres, other))
	if all := strings.Join(got, "\n"); strings.Contains(all, "jsonb_path_ops") || !strings.Contains(all, `RENAME TO "docs_keys"`) {
		t.Errorf("a class goes: %q", got)
	}
}

// A schema refuses a Desc or an OpClass outside PostgreSQL, one on a column the
// index does not cover, DESC on a method with no order, and a class that is not
// a name.
func TestIndexKeysRefused(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		d      velox.Dialect
		ix     migrate.Index
		reason string
	}{
		{"desc mysql", velox.MySQL, migrate.Index{Desc: []string{"name"}}, `gives the index "k" a Desc or an OpClass, which only PostgreSQL has`},
		{"class sqlite", velox.SQLite, migrate.Index{OpClass: map[string]string{"name": "x"}}, `a Desc or an OpClass, which only PostgreSQL has`},
		{"desc elsewhere", velox.Postgres, migrate.Index{Desc: []string{"id"}}, `Desc on the column "id", which it does not cover`},
		{"class elsewhere", velox.Postgres, migrate.Index{OpClass: map[string]string{"id": "x"}}, `an OpClass on the column "id", which it does not cover`},
		{"desc hash", velox.Postgres, migrate.Index{Method: "hash", Desc: []string{"name"}}, `Desc with the method "hash", and only btree has an order`},
		{"class name", velox.Postgres, migrate.Index{OpClass: map[string]string{"name": "x); DROP TABLE docs"}}, `which is not a name`},
		{"class empty", velox.Postgres, migrate.Index{OpClass: map[string]string{"name": ""}}, `which is not a name`},
		{"class dotted", velox.Postgres, migrate.Index{OpClass: map[string]string{"name": "a.b.c"}}, `which is not a name`},
	} {
		c.ix.Name, c.ix.Columns = "k", []string{"name"}
		_, err := migrate.Diff(&migrate.Schema{Dialect: c.d}, methodSchema(c.d, c.ix), migrate.DiffOptions{})
		e, ok := err.(*migrate.Error)
		if !ok || e.Code != "schema" || !strings.Contains(e.Reason, c.reason) {
			t.Errorf("%s: %v, want a schema error with %q", c.name, err, c.reason)
		}
	}
}

// TestIndexKeysSwapLive: a plan that adds DESC, names an operator class, and
// takes them away again applies on a table with rows, and the database then
// holds what the schema says under the same name, with no drift. Each prefix of
// the chain is applied to a fresh database.
func TestIndexKeysSwapLive(t *testing.T) {
	t.Parallel()
	needPostgres(t)
	type stage struct {
		name string
		ix   migrate.Index
	}
	base := migrate.Index{Name: "docs_name_ix", Columns: []string{"name", "id"}}
	desc, ops := base, base
	desc.Desc = []string{"id"}
	ops.Desc = []string{"id"}
	ops.OpClass = map[string]string{"name": "text_pattern_ops"}
	stages := []stage{{"stepa", base}, {"stepb", desc}, {"stepc", ops}, {"stepd", base}}
	for n := 1; n <= len(stages); n++ {
		steps := make([]step, n)
		for i := range steps {
			steps[i] = step{to: methodSchema(velox.Postgres, stages[i].ix), name: stages[i].name}
		}
		last := stages[n-1]
		t.Run(last.name, func(t *testing.T) {
			db, _ := freshDB(t, velox.Postgres)
			mustUp(t, db, dirOf(t, velox.Postgres, steps[:1]...))
			mustExec(t, db, `INSERT INTO docs (id, attrs, name) VALUES (1, '{"a": 1}', 'x'), (2, '{}', 'y')`)
			mustUp(t, db, dirOf(t, velox.Postgres, steps...))
			ixs := mustInspect(t, db).Tables[0].Indexes
			if len(ixs) != 1 || ixs[0].Name != last.ix.Name || !slices.Equal(ixs[0].Desc, last.ix.Desc) || !maps.Equal(ixs[0].OpClass, last.ix.OpClass) {
				t.Errorf("indexes %+v, want %+v", ixs, last.ix)
			}
			r, err := migrate.Drift(context.Background(), db, methodSchema(velox.Postgres, last.ix))
			if err != nil || len(r.Changes) > 0 || len(r.Warnings) > 0 {
				t.Errorf("drift: %v, changes %v, warnings %v", err, r.Changes, r.Warnings)
			}
		})
	}
}
