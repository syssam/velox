package integration

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// TestDistinct reads distinct values with Values and a projection on every
// database: the invoices' orgs are 1 (four rows) and 2 (one).
func TestDistinct(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		q := tg.db.Query[testmodel.Invoice]().Distinct().OrderBy(I.OrgID.Asc()).Limit(10)
		orgs, err := velox.Values(ctx, q, I.OrgID.Key())
		if err != nil || !slices.Equal(orgs, []int64{1, 2}) {
			t.Errorf("Values: %v, %v", orgs, err)
		}
		wantRows(t, "projection", rowsOf(t, ctx, q.Select(I.OrgID), 1), []string{"1", "2"})
	})
}

// TestExplain runs each database's EXPLAIN on a query, its parameters
// bound, and reads a plan that names the table. It sends no write: the
// rows stay.
func TestExplain(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		plan, err := velox.Explain(ctx, tg.db.Query[testmodel.Invoice]().Where(I.Number.EQ("INV-1"), I.Paid.EQ(true)).Limit(5))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(plan, "invoices") || len(strings.TrimSpace(plan)) == 0 {
			t.Errorf("plan:\n%s", plan)
		}
		plan, err = velox.Explain(ctx, tg.db.Query[testmodel.Invoice]().Distinct().Select(I.OrgID).Limit(5))
		if err != nil || !strings.Contains(plan, "invoices") {
			t.Errorf("projection: %v\n%s", err, plan)
		}
		if n, err := tg.db.Query[testmodel.Invoice]().Count(ctx); err != nil || n != 5 {
			t.Errorf("count after: %d, %v", n, err)
		}
	})
}

// TestUpsertUpdateAll overwrites the conflicting row's columns with the
// inserted row's, but the key, the conflict columns and the Immutable
// created_at; updated_at takes the inserted row's Default, its
// UpdateDefault does not fire. The existing row's key comes back.
func TestUpsertUpdateAll(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		over := newInvoice(1, "INV-1")
		over.Total, over.Note, over.Paid = 99, "changed", false
		before := time.Now().Add(-time.Minute)
		if err := tg.db.Insert(&over).OnConflict(I.OrgID, I.Number).UpdateAll().Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if over.ID != 1 {
			t.Errorf("ID %d, want 1", over.ID)
		}
		got, err := tg.db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got.Total != 99 || got.Note != "changed" || got.Paid || got.PaidAt != nil || got.Number != "INV-1" || got.OrgID != 1 {
			t.Errorf("row 1: %+v", got)
		}
		if !got.CreatedAt.Equal(t0) || got.UpdatedAt.Before(before) {
			t.Errorf("created_at %v (want %v), updated_at %v (want the insert's)", got.CreatedAt, t0, got.UpdatedAt)
		}
		if n, err := tg.db.Query[testmodel.Invoice]().Count(ctx); err != nil || n != 5 {
			t.Errorf("count: %d, %v", n, err)
		}
	})
}
