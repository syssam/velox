package velox_test

import (
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// TestOneRowAllocationBudget pins design §8: a one-row query makes at most 8
// more allocations than hand-written code scanning the same rows.
func TestOneRowAllocationBudget(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
	db := velox.OpenFake(velox.Postgres, e)
	run := func() {
		if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx); err != nil {
			t.Fatal(err)
		}
	}
	run() // fill the render cache
	vx := testing.AllocsPerRun(200, run)
	text, args := e.SQL, e.Args
	raw := testing.AllocsPerRun(200, func() {
		r, err := velox.RawRows(db, text, args)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]testmodel.Invoice, 0, 1)
		for r.Next() {
			out = append(out, testmodel.Invoice{})
			x := &out[len(out)-1]
			if err := r.Scan(&x.CreatedAt, &x.UpdatedAt, &x.ID, &x.OrgID, &x.Number, &x.Total, &x.Paid, &x.PaidAt, &x.Note, &x.Data); err != nil {
				t.Fatal(err)
			}
		}
		r.Close()
	})
	t.Logf("velox %.0f allocations, hand-written %.0f", vx, raw)
	if vx > raw+8 {
		t.Errorf("velox makes %.0f allocations for a one-row query and hand-written code %.0f: over the budget of +8 (design §8)", vx, raw)
	}
}

func BenchmarkOneRow(b *testing.B) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{invoiceRow(1)}})
	b.ReportAllocs()
	for b.Loop() {
		if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
