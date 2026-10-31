package migrate_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/codegen"
	"github.com/syssam/velox2/migrate"
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/field"
)

// ChkOrder declares checks of the forms each database rewrites when it stores
// them: parentheses, a cast on a literal, a charset on a string, a function's
// case, IN as ANY, BETWEEN as two comparisons.
type ChkOrder struct{ schema.Schema }

func (ChkOrder) Fields() []schema.Field {
	return []schema.Field{
		field.Int("total"),
		field.Int("qty"),
		field.String("code").MaxLen(16),
		field.String("state").MaxLen(16),
	}
}

func (ChkOrder) Annotations() []schema.Annotation {
	return []schema.Annotation{
		schema.Check("chk_total_positive", "total > 0"),
		schema.Check("chk_qty_range", "qty BETWEEN 1 AND 100"),
		schema.Check("chk_code_lower", "code = lower(code)"),
		schema.Check("chk_state_known", "state IN ('new', 'paid')"),
		schema.Check("chk_both", "total <> 7 AND (qty <> 7 OR state <> 'new')"),
		schema.Check("chk_code_set", "code <> ''"),
	}
}

// TestCheckOnRealDatabases: schema.Check goes from a schema through codegen.Tables
// and a plan to a fresh database, which enforces it, names it in a
// *velox.ConstraintError and reads it back as no drift. A row that breaks no
// check goes in.
func TestCheckOnRealDatabases(t *testing.T) {
	for _, d := range []velox.Dialect{velox.SQLite, velox.Postgres, velox.MySQL} {
		t.Run(d.String(), func(t *testing.T) {
			g, err := codegen.Build(marshal(ChkOrder{}))
			if err != nil {
				t.Fatal(err)
			}
			model, err := codegen.Tables(g, d)
			if err != nil {
				t.Fatal(err)
			}
			db, _ := freshDB(t, d)
			fsys := fromDir(t, d, model)
			mustUp(t, db, fsys)
			invariants(t, db, d, fsys, model)
			r, err := migrate.Drift(context.Background(), db, model)
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Changes)+len(r.Warnings) > 0 {
				t.Errorf("drift:\n%s", reportText(r))
			}

			// The control: a check that differs is seen, on every dialect, so
			// the empty report above is a comparison, not a blind spot.
			other, err := codegen.Tables(g, d)
			if err != nil {
				t.Fatal(err)
			}
			changed := false
			for i := range other.Tables {
				for j, c := range other.Tables[i].Checks {
					if c.Name == "chk_total_positive" {
						other.Tables[i].Checks[j].Expr, changed = "total > 1", true
					}
				}
			}
			if !changed {
				t.Fatal("the model has no chk_total_positive")
			}
			r, err = migrate.Drift(context.Background(), db, other)
			if err != nil {
				t.Fatal(err)
			}
			if text := reportText(r); !strings.Contains(text, "chk_total_positive") {
				t.Errorf("a changed check is not reported:\n%s", text)
			}

			vdb := velox.Open(d, db)
			ctx := context.Background()
			q := "INSERT INTO chk_orders (total, qty, code, state) VALUES (?, ?, ?, ?)"
			if d == velox.Postgres {
				q = "INSERT INTO chk_orders (total, qty, code, state) VALUES ($1, $2, $3, $4)"
			}
			if _, err := vdb.Exec(ctx, "test", q, 1, 1, "a", "new"); err != nil {
				t.Fatalf("a good row: %v", err)
			}
			for _, c := range []struct {
				name        string
				total, qty  int
				code, state string
			}{
				{"chk_total_positive", 0, 1, "a", "paid"},
				{"chk_qty_range", 1, 101, "a", "new"},
				{"chk_code_lower", 1, 1, "A", "new"},
				{"chk_state_known", 1, 1, "a", "lost"},
				{"chk_both", 7, 7, "a", "new"},
				{"chk_code_set", 1, 1, "", "new"},
			} {
				_, err := vdb.Exec(ctx, "test", q, c.total, c.qty, c.code, c.state)
				var ce *velox.ConstraintError
				if !errors.As(err, &ce) {
					t.Errorf("%s: %v, not a ConstraintError", c.name, err)
					continue
				}
				if ce.Kind != velox.ConstraintCheck || ce.Constraint != c.name {
					t.Errorf("kind %v, constraint %q, want a check named %q: %v", ce.Kind, ce.Constraint, c.name, err)
				}
			}
		})
	}
}
