package itest

import (
	"fmt"
	"testing"

	"github.com/syssam/velox2/internal/testmodel"
)

// BenchmarkScan measures the read path on every database: 100 invoices (two
// time.Time columns, a *time.Time, bytes, a float, a bool and strings), the
// same 100 with their 500 items loaded, and 100 items, which carry only scalar
// columns and so serve as the control for the first two.
func BenchmarkScan(b *testing.B) {
	for _, tg := range targets(b, nil) {
		b.Run(tg.name, func(b *testing.B) {
			setup(b, tg)
			seedScan(b, tg)
			b.Run("Query100", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					rows, err := tg.db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(7)).OrderBy(I.ID.Asc()).Limit(100).All(ctx)
					if err != nil {
						b.Fatal(err)
					}
					if len(rows) != 100 {
						b.Fatalf("got %d rows, want 100", len(rows))
					}
				}
			})
			b.Run("EagerO2M100", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					rows, err := tg.db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(7)).OrderBy(I.ID.Asc()).With(I.Items).Limit(100).All(ctx)
					if err != nil {
						b.Fatal(err)
					}
					if len(rows) != 100 {
						b.Fatalf("got %d parents, want 100", len(rows))
					}
					items := 0
					for i := range rows {
						xs, _ := rows[i].Items.Get()
						items += len(xs)
					}
					if items != 500 {
						b.Fatalf("got %d items, want 500", items)
					}
				}
			})
			b.Run("Items100", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					rows, err := tg.db.Query[testmodel.Item]().Where(It.InvoiceID.GTE(1000)).OrderBy(It.ID.Asc()).Limit(100).All(ctx)
					if err != nil {
						b.Fatal(err)
					}
					if len(rows) != 100 {
						b.Fatalf("got %d rows, want 100", len(rows))
					}
				}
			})
		})
	}
}

// seedScan adds to setup's rows 100 invoices, ids 1000 to 1099 in org 7, every
// third one paid, and 5 items for each, ids 10000 + 5i + j.
func seedScan(tb testing.TB, tg target) {
	tb.Helper()
	for i := range 100 {
		id := int64(1000 + i)
		var paidAt any
		if i%3 == 0 {
			paidAt = t0
		}
		tg.exec(tb, "INSERT INTO invoices (created_at, updated_at, id, org_id, number, total, paid, paid_at, note, data) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			t0, t0, id, 7, fmt.Sprintf("SCAN-%d", i), float64(i)+0.5, i%3 == 0, paidAt, fmt.Sprintf("note for invoice %d", i), []byte("scan benchmark payload"))
		for j := range 5 {
			tg.exec(tb, "INSERT INTO items (id, invoice_id, sku, qty) VALUES (?, ?, ?, ?)", int64(10000+5*i+j), id, fmt.Sprintf("SKU-%d", j), int64(j))
		}
	}
}
