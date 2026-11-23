package benchschema_test

import (
	"testing"

	"github.com/syssam/velox/internal/benchschema"
	"github.com/syssam/velox/migrate"
)

// TestTables: the tables plan, and the wide ones have 33 columns and six
// indexes but for a group's first head, which refers to no table.
func TestTables(t *testing.T) {
	for _, wide := range []bool{false, true} {
		s := benchschema.Tables(20, wide)
		if len(s.Tables) != 20 {
			t.Fatalf("%d tables", len(s.Tables))
		}
		if _, err := migrate.NewPlan(&migrate.Schema{Dialect: s.Dialect}, s, migrate.PlanOptions{}); err != nil {
			t.Fatalf("wide %v: %v", wide, err)
		}
		e1 := s.Tables[1]
		if len(e1.ForeignKeys) != 1 || e1.ForeignKeys[0].RefTable.Name != "e0s" {
			t.Errorf("e1s refers to %v", e1.ForeignKeys)
		}
		if s.Tables[10].ForeignKeys[0].Columns[0] != "up_id" {
			t.Errorf("e10s refers by %v", s.Tables[10].ForeignKeys)
		}
		if wide && (len(e1.Columns) != 33 || len(e1.Indexes) != 6) {
			t.Errorf("wide e1s: %d columns, %d indexes", len(e1.Columns), len(e1.Indexes))
		}
	}
}
