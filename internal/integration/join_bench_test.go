package integration

import (
	"testing"

	"github.com/syssam/velox/internal/testmodel"
)

// BenchmarkJoin compares ways to read 100 items of one org's invoices, one item for
// each invoice as a feed holds one row for each post, each with its invoice, ordered as a feed is: by the related row's column and then by the item's.
// Join is one statement. With is two: the items, then their invoices by IN; its
// ordering is by the item's own column, as it cannot order by the invoice's. HasWith
// tests the invoice without reading it, as an EXISTS.
func BenchmarkJoin(b *testing.B) {
	for _, tg := range targets(b, nil) {
		b.Run(tg.name, func(b *testing.B) {
			setup(b, tg)
			seedScan(b, tg)
			check := func(b *testing.B, rows []testmodel.Item, wantInvoice bool) {
				if len(rows) != 100 {
					b.Fatalf("got %d rows, want 100", len(rows))
				}
				if inv, _ := rows[0].Edges.Invoice.Get(); wantInvoice && inv == nil {
					b.Fatal("no invoice")
				}
			}
			b.Run("Join100", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					rows, err := tg.db.Query[testmodel.Item]().Join(It.Invoice).
						Where(It.SKU.EQ("SKU-0"), It.Invoice.Is(I.OrgID.EQ(7))).
						OrderBy(It.Invoice.By(I.ID.Desc()), It.ID.Asc()).Limit(100).All(ctx)
					if err != nil {
						b.Fatal(err)
					}
					check(b, rows, true)
				}
			})
			b.Run("JoinSameOrder100", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					rows, err := tg.db.Query[testmodel.Item]().Join(It.Invoice).
						Where(It.SKU.EQ("SKU-0"), It.Invoice.Is(I.OrgID.EQ(7))).
						OrderBy(It.ID.Asc()).Limit(100).All(ctx)
					if err != nil {
						b.Fatal(err)
					}
					check(b, rows, true)
				}
			})
			b.Run("With100", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					rows, err := tg.db.Query[testmodel.Item]().With(It.Invoice).
						Where(It.SKU.EQ("SKU-0"), It.Invoice.HasWith(I.OrgID.EQ(7))).
						OrderBy(It.ID.Asc()).Limit(100).All(ctx)
					if err != nil {
						b.Fatal(err)
					}
					check(b, rows, true)
				}
			})
			b.Run("HasWith100", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					rows, err := tg.db.Query[testmodel.Item]().
						Where(It.SKU.EQ("SKU-0"), It.Invoice.HasWith(I.OrgID.EQ(7))).
						OrderBy(It.ID.Asc()).Limit(100).All(ctx)
					if err != nil {
						b.Fatal(err)
					}
					check(b, rows, false)
				}
			})
		})
	}
}
