package migrate_test

import (
	"context"
	"slices"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// TestIndexMethodSwapLive: a plan that changes an index's method applies on
// a table with rows, and the database then holds the new method under the
// name the schema gave it, with no drift. The chain goes gin, hash, btree and
// back to gin; each prefix of it is applied to a fresh database.
func TestIndexMethodSwapLive(t *testing.T) {
	t.Parallel()
	needPostgres(t)
	schemas := []struct {
		name, method string
		s            *migrate.Schema
	}{}
	for _, m := range []string{"gin", "hash", "", "gin"} {
		ix := migrate.Index{Name: "docs_attrs_ix", Columns: []string{"attrs"}, Method: m}
		schemas = append(schemas, struct {
			name, method string
			s            *migrate.Schema
		}{"step" + string(rune('a'+len(schemas))), m, methodSchema(velox.Postgres, ix)})
	}
	for n := 1; n <= len(schemas); n++ {
		steps := make([]step, n)
		for i := range steps {
			steps[i] = step{to: schemas[i].s, name: schemas[i].name}
		}
		last := schemas[n-1]
		t.Run(last.name, func(t *testing.T) {
			db, _ := freshDB(t, velox.Postgres)
			mustUp(t, db, dirOf(t, velox.Postgres, steps[:1]...))
			mustExec(t, db, `INSERT INTO docs (id, attrs, name) VALUES (1, '{"a": 1}', 'x')`)
			mustUp(t, db, dirOf(t, velox.Postgres, steps...))
			var got []string
			for _, i := range mustInspect(t, db).Tables[0].Indexes {
				got = append(got, i.Name+":"+i.Method)
			}
			if want := []string{"docs_attrs_ix:" + last.method}; !slices.Equal(got, want) {
				t.Errorf("indexes %q, want %q", got, want)
			}
			r, err := migrate.Drift(context.Background(), db, last.s)
			if err != nil || len(r.Changes) > 0 || len(r.Warnings) > 0 {
				t.Errorf("drift: %v, changes %v, warnings %v", err, r.Changes, r.Warnings)
			}
		})
	}
}
