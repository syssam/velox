package untyped_test

import (
	"strings"
	"testing"

	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/untyped"
)

// TestTableOfNoEntity: the Table of a type that is not an entity holds the
// error that says so, and views no rows; an entity's Table is valid and the
// same each time.
func TestTableOfNoEntity(t *testing.T) {
	type plain struct{ ID int64 }
	if err := untyped.TableOf[plain]().Err(); err == nil || !strings.Contains(err.Error(), "is not an entity") {
		t.Errorf("TableOf of a plain struct: %v", err)
	}
	if untyped.RowsOf[plain](untyped.TableOf[testmodel.Ticket]().NewRows(2)) != nil {
		t.Error("RowsOf of a plain struct: not nil")
	}
	tk := untyped.TableOf[testmodel.Ticket]()
	if tk.Err() != nil || tk.Info() != untyped.TableOf[testmodel.Ticket]().Info() {
		t.Errorf("TableOf of an entity: %v", tk.Err())
	}
	if rows := untyped.RowsOf[testmodel.Ticket](tk.NewRows(2)); len(rows) != 2 {
		t.Errorf("RowsOf: %d rows", len(rows))
	}
}
