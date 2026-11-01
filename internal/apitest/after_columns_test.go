package apitest

import (
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// TestAfterRefusesAnOrderColumnNotSelected: the row a query with Columns
// returned has its zero value in a column it did not select, and After would
// compare against that zero.
func TestAfterRefusesAnOrderColumnNotSelected(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	last := &testmodel.Invoice{ID: 1}
	const want = "After needs the order column created_at selected"
	base := db.Query[testmodel.Invoice]().OrderBy(I.CreatedAt.Desc(), I.ID.Desc()).After(last).Limit(2)
	for name, q := range map[string]velox.Query[testmodel.Invoice]{
		"Columns": base.Columns(I.ID, I.Number),
		"Omit":    base.Omit(I.CreatedAt),
	} {
		if _, err := q.All(ctx); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: All: %v", name, err)
		}
		if _, _, err := q.SQL(ctx); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: SQL: %v", name, err)
		}
	}
	if _, err := base.Columns(I.ID, I.CreatedAt).All(ctx); err != nil {
		t.Errorf("every order column selected: %v", err)
	}
	if _, err := base.All(ctx); err != nil {
		t.Errorf("no Columns: %v", err)
	}
}
