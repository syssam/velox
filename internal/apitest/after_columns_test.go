package apitest

import (
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// TestAfterWithColumnsOnWhatIgnoresThem: Count and the rest return no entity row, so
// what Columns selects cannot leave a zero for After to page from. After's other
// rules still apply.
func TestAfterWithColumnsOnWhatIgnoresThem(t *testing.T) {
	last := &testmodel.Invoice{ID: 1}
	for name, c := range map[string]struct {
		rows [][]any
		run  func(q velox.Query[testmodel.Invoice]) error
	}{
		"Count":  {[][]any{{int64(3)}}, func(q velox.Query[testmodel.Invoice]) error { _, err := q.Count(ctx); return err }},
		"Exists": {[][]any{{int64(1)}}, func(q velox.Query[testmodel.Invoice]) error { _, err := q.Exists(ctx); return err }},
		"Sum":    {[][]any{{float64(3)}}, func(q velox.Query[testmodel.Invoice]) error { _, err := velox.Sum(ctx, q, I.Total); return err }},
		"CountBy": {nil, func(q velox.Query[testmodel.Invoice]) error {
			_, err := velox.CountBy(ctx, q, I.OrgID.Key())
			return err
		}},
		"SumBy": {nil, func(q velox.Query[testmodel.Invoice]) error {
			_, err := velox.SumBy(ctx, q, I.OrgID.Key(), I.Total)
			return err
		}},
	} {
		t.Run(name, func(t *testing.T) {
			e := &fakeconn.Exec{Rows: c.rows}
			base := openFake(velox.Postgres, e).Query[testmodel.Invoice]().Unbounded()
			if err := c.run(base.OrderBy(I.CreatedAt.Desc(), I.ID.Desc()).Columns(I.Number).After(last)); err != nil {
				t.Errorf("Columns then After: %v", err)
			}
			if !strings.Contains(e.SQL, "created_at") {
				t.Errorf("After's condition is gone: %s", e.SQL)
			}
			if err := c.run(base.Columns(I.Number).After(last)); err == nil || !strings.Contains(err.Error(), "After needs an OrderBy") {
				t.Errorf("no OrderBy: %v", err)
			}
		})
	}
}

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
