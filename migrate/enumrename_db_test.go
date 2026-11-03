package migrate_test

import (
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// TestPostgresEnumSwapAfterColumnRename: a plan that renames an enum column and
// removes one of its values swaps the type, dropping the partial index over the
// column and making it again. The guard compared the index the database had,
// over the old name, with the statement over the new one, and refused the plan
// for its columns and predicate. It compares under the names after the rename.
func TestPostgresEnumSwapAfterColumnRename(t *testing.T) {
	d := velox.Postgres
	model := func(col string, values ...string) *migrate.Schema {
		return &migrate.Schema{Dialect: d,
			Enums: []migrate.Enum{{Name: "t_role", Values: values}},
			Tables: []migrate.Table{{
				Name:           "t",
				Columns:        []migrate.Column{{Name: "id", Type: "integer"}, {Name: col, Type: `"t_role"`}},
				PrimaryKey:     []string{"id"},
				PrimaryKeyName: "t_pkey",
				Indexes:        []migrate.Index{{Name: "t_p", Columns: []string{col}, Where: col + ` = 'a'::"t_role"`}},
			}},
		}
	}
	opts := migrate.PlanOptions{
		Diff:  migrate.DiffOptions{RenameColumns: map[migrate.ColumnRef]string{{Table: migrate.TableRef{Name: "t"}, Column: "role"}: "kind"}},
		Allow: []migrate.Allow{{Code: "drop-enum-value"}},
	}
	migrateSchemas(t, d, model("role", "a", "b", "c"), model("kind", "a", "b"), opts)
}
