package apitest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// itemInvoiceRow is a row of Item joined to Invoice: the item's columns, then the
// invoice's.
func itemInvoiceRow(item []any, invoice []any) []any {
	return append(append([]any{}, item...), invoice...)
}

// nulls is n NULLs: the joined columns of a left join that found no row.
func nulls(n int) []any { return make([]any, n) }

func TestJoinToOneFillsTheRelationInOneStatement(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{
		itemInvoiceRow(itemRow(10, 1, "A", 1), invoiceRow(1)),
		itemInvoiceRow(itemRow(11, 2, "B", 2), invoiceRow(2)),
	}}
	db := openFake(velox.Postgres, e)
	items, err := db.Query[testmodel.Item]().
		Join(It.Invoice).
		Where(It.Qty.GT(0), It.Invoice.Is(I.Paid.EQ(true))).
		OrderBy(It.Invoice.By(I.Number.Desc())).
		Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Log) != 1 {
		t.Fatalf("a join is one statement, got %d: %v", len(e.Log), e.Log)
	}
	want := `SELECT "items"."id", "items"."invoice_id", "items"."sku", "items"."qty", ` +
		`"vxj1"."id", "vxj1"."created_at", "vxj1"."updated_at", "vxj1"."org_id", "vxj1"."number", "vxj1"."total", "vxj1"."paid", "vxj1"."paid_at", "vxj1"."note", "vxj1"."data" ` +
		`FROM "items" JOIN "invoices" AS "vxj1" ON "vxj1"."id" = "items"."invoice_id" ` +
		`WHERE "items"."qty" > $1 AND "vxj1"."paid" = $2 ORDER BY "vxj1"."number" DESC LIMIT $3`
	if e.Log[0] != want {
		t.Errorf("\n got %s\nwant %s", e.Log[0], want)
	}
	if len(items) != 2 || items[0].SKU != "A" || items[1].SKU != "B" {
		t.Fatalf("items: %+v", items)
	}
	for i, wantNumber := range []string{"INV-1", "INV-2"} {
		inv, loaded := items[i].Edges.Invoice.Get()
		if !loaded || inv == nil || inv.Number != wantNumber {
			t.Errorf("item %d: loaded %v, invoice %+v", i, loaded, inv)
		}
	}
}

func TestLeftJoinKeepsARowWithoutARelatedRow(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{
		itemInvoiceRow(itemRow(10, 1, "A", 1), invoiceRow(1)),
		itemInvoiceRow(itemRow(11, 0, "B", 2), nulls(10)),
	}}
	db := openFake(velox.Postgres, e)
	items, err := db.Query[testmodel.Item]().LeftJoin(It.Invoice).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.Log[0], ` LEFT JOIN "invoices" AS "vxj1" ON "vxj1"."id" = "items"."invoice_id"`) {
		t.Errorf("no left join in %s", e.Log[0])
	}
	if inv, loaded := items[0].Edges.Invoice.Get(); !loaded || inv == nil || inv.Number != "INV-1" {
		t.Errorf("item 0: loaded %v, invoice %+v", loaded, inv)
	}
	// Loaded, and nothing there: a nil, as With leaves one with no row.
	if inv, loaded := items[1].Edges.Invoice.Get(); !loaded || inv != nil {
		t.Errorf("item 1: loaded %v, invoice %+v", loaded, inv)
	}
}

func TestJoinedConditionsReachCountAndExists(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(3)}}}
	db := openFake(velox.Postgres, e)
	n, err := db.Query[testmodel.Item]().Join(It.Invoice).Where(It.Invoice.Is(I.Paid.EQ(true))).Count(ctx)
	if err != nil || n != 3 {
		t.Fatalf("count %d, %v", n, err)
	}
	want := `SELECT COUNT(*) FROM "items" JOIN "invoices" AS "vxj1" ON "vxj1"."id" = "items"."invoice_id" WHERE "vxj1"."paid" = $1`
	if e.Log[0] != want {
		t.Errorf("\n got %s\nwant %s", e.Log[0], want)
	}
}

func TestJoinRefusesWhatItCannotRead(t *testing.T) {
	cases := []struct {
		name string
		run  func(db *velox.DB) error
		want string
	}{
		{"a to-many relation", func(db *velox.DB) error {
			_, err := db.Query[testmodel.Invoice]().Join(I.Items).Limit(1).All(ctx)
			return err
		}, "to-many"},
		{"a joined condition without the join", func(db *velox.DB) error {
			_, err := db.Query[testmodel.Item]().Where(It.Invoice.Is(I.Paid.EQ(true))).Limit(1).All(ctx)
			return err
		}, "Join"},
		{"a joined order without the join", func(db *velox.DB) error {
			_, err := db.Query[testmodel.Item]().OrderBy(It.Invoice.By(I.Number.Asc())).Limit(1).All(ctx)
			return err
		}, "Join"},
		{"the same relation twice", func(db *velox.DB) error {
			_, err := db.Query[testmodel.Item]().Join(It.Invoice).LeftJoin(It.Invoice).Limit(1).All(ctx)
			return err
		}, "twice"},
		{"After a row that was not read with the join", func(db *velox.DB) error {
			_, err := db.Query[testmodel.Item]().Join(It.Invoice).OrderBy(It.Invoice.By(I.Number.Asc()), It.ID.Asc()).
				After(&testmodel.Item{ID: 1}).Limit(1).All(ctx)
			return err
		}, "After"},
		{"a lock", func(db *velox.DB) error {
			return db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
				_, err := tx.Query[testmodel.Item]().Join(It.Invoice).ForUpdate().Limit(1).All(ctx)
				return err
			})
		}, "lock"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := &fakeconn.Exec{}
			err := c.run(openFake(velox.Postgres, e))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error with %q", err, c.want)
			}
			for _, s := range e.Log {
				if strings.HasPrefix(s, "SELECT") {
					t.Errorf("a refused query was sent: %s", s)
				}
			}
		})
	}
}

func TestJoinIterYieldsJoinedRows(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{
		itemInvoiceRow(itemRow(10, 1, "A", 1), invoiceRow(1)),
		itemInvoiceRow(itemRow(11, 2, "B", 2), invoiceRow(2)),
	}}
	db := openFake(velox.Postgres, e)
	var numbers []string
	for it, err := range db.Query[testmodel.Item]().Join(It.Invoice).Limit(10).Iter(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		inv, loaded := it.Edges.Invoice.Get()
		if !loaded || inv == nil {
			t.Fatalf("item %d: invoice not loaded", it.ID)
		}
		numbers = append(numbers, inv.Number)
	}
	if len(numbers) != 2 || numbers[0] != "INV-1" || numbers[1] != "INV-2" {
		t.Errorf("numbers %v", numbers)
	}
}

// Columns narrows the entity's own columns; the target's are all read.
func TestJoinColumnsNarrowTheEntityOnly(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{append([]any{int64(10), "A"}, invoiceRow(1)...)}}
	db := openFake(velox.Postgres, e)
	items, err := db.Query[testmodel.Item]().Columns(It.ID, It.SKU).Join(It.Invoice).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(e.Log[0], `SELECT "items"."id", "items"."sku", "vxj1"."id", `) {
		t.Errorf("SQL %s", e.Log[0])
	}
	if inv, _ := items[0].Edges.Invoice.Get(); items[0].SKU != "A" || inv == nil || inv.Number != "INV-1" {
		t.Errorf("item %+v, invoice %+v", items[0], inv)
	}
}

// An inner and a left join of one relation are two shapes of the render cache.
func TestJoinShapesDoNotShareACacheEntry(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	for range 2 {
		for _, q := range []velox.Query[testmodel.Item]{
			db.Query[testmodel.Item]().Join(It.Invoice),
			db.Query[testmodel.Item]().LeftJoin(It.Invoice),
			db.Query[testmodel.Item]().Limit(1),
		} {
			if _, err := q.Limit(1).All(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(e.Log) != 6 {
		t.Fatalf("%d statements", len(e.Log))
	}
	for i := range 3 {
		if e.Log[i] != e.Log[i+3] {
			t.Errorf("run %d changed between rounds:\n%s\n%s", i, e.Log[i], e.Log[i+3])
		}
	}
	if !strings.Contains(e.Log[0], ` JOIN "invoices"`) || strings.Contains(e.Log[0], "LEFT") ||
		!strings.Contains(e.Log[1], ` LEFT JOIN "invoices"`) || strings.Contains(e.Log[2], "JOIN") {
		t.Errorf("shapes shared an entry:\n%s\n%s\n%s", e.Log[0], e.Log[1], e.Log[2])
	}
}

// A Rewrite hook may not take the join away: the rows are scanned by it.
func TestJoinRewriteMayNotChangeJoins(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		st.Joins = nil
		return nil
	}))
	_, err := db.Query[testmodel.Item]().Join(It.Invoice).Limit(1).All(ctx)
	if err == nil || !strings.Contains(err.Error(), "Joins") {
		t.Fatalf("got %v", err)
	}
	if len(e.Log) != 0 {
		t.Errorf("a refused statement was sent: %v", e.Log)
	}
	// A hook that leaves them alone sees the join, and it still reads.
	e = &fakeconn.Exec{Rows: [][]any{itemInvoiceRow(itemRow(10, 1, "A", 1), invoiceRow(1))}}
	seen := 0
	db = openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		seen = len(st.Joins)
		return nil
	}))
	items, err := db.Query[testmodel.Item]().Join(It.Invoice).Limit(1).All(ctx)
	if err != nil || seen != 1 {
		t.Fatalf("seen %d, %v", seen, err)
	}
	if inv, _ := items[0].Edges.Invoice.Get(); inv == nil || inv.ID != 1 {
		t.Errorf("invoice %+v", inv)
	}
}

// The joined entity's own relations load as With loads them: one more statement
// for the joined rows of every owner, not one for each.
func TestJoinLoadsTheJoinedEntitiesRelations(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{
			itemInvoiceRow(itemRow(10, 1, "A", 1), invoiceRow(1)),
			itemInvoiceRow(itemRow(11, 2, "B", 2), invoiceRow(2)),
			itemInvoiceRow(itemRow(12, 1, "C", 3), invoiceRow(1)),
		},
		{itemRow(10, 1, "A", 1), itemRow(12, 1, "C", 3), itemRow(13, 2, "D", 4)},
	}}
	db := openFake(velox.Postgres, e)
	items, err := db.Query[testmodel.Item]().Join(It.Invoice.With(I.Items)).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Log) != 2 {
		t.Fatalf("%d statements, want 2: %v", len(e.Log), e.Log)
	}
	// Invoice 1 is joined to two items: it is read twice and loaded once.
	if want := `SELECT "id", "invoice_id", "sku", "qty" FROM "items" WHERE "invoice_id" IN ($1, $2) ORDER BY "id"`; e.Log[1] != want {
		t.Errorf("\n got %s\nwant %s", e.Log[1], want)
	}
	skus := func(i int) (out []string) {
		inv, _ := items[i].Edges.Invoice.Get()
		xs, loaded := inv.Edges.Items.Get()
		if !loaded {
			t.Fatalf("item %d: its invoice's items are not loaded", i)
		}
		for _, x := range xs {
			out = append(out, x.SKU)
		}
		return out
	}
	if got := fmt.Sprint(skus(0), skus(1), skus(2)); got != "[A C] [D] [A C]" {
		t.Errorf("skus %s", got)
	}
}

func TestJoinTakesOnlyTheRelationsWith(t *testing.T) {
	for name, r := range map[string]velox.Rel[testmodel.Item, testmodel.Invoice]{
		"Where":     It.Invoice.Where(I.Paid.EQ(true)),
		"OrderBy":   It.Invoice.OrderBy(I.ID.Asc()),
		"LimitEach": It.Invoice.LimitEach(1),
		"MaxRows":   It.Invoice.MaxRows(5),
	} {
		_, err := openFake(velox.Postgres, &fakeconn.Exec{}).Query[testmodel.Item]().Join(r).Limit(1).All(ctx)
		if err == nil || !strings.Contains(err.Error(), "With") {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

// After pages over a joined column from the joined row of the row it is given.
func TestJoinAfterPagesOverAJoinedColumn(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{itemInvoiceRow(itemRow(10, 1, "A", 1), invoiceRow(1)), itemInvoiceRow(itemRow(11, 2, "B", 2), invoiceRow(2))},
		{itemInvoiceRow(itemRow(12, 3, "C", 3), invoiceRow(3))},
	}}
	db := openFake(velox.Postgres, e)
	q := db.Query[testmodel.Item]().Join(It.Invoice).OrderBy(It.Invoice.By(I.Number.Desc()), It.ID.Asc()).Limit(2)
	page, err := q.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	next, err := q.After(&page[1]).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(next) != 1 || next[0].SKU != "C" {
		t.Errorf("next page: %+v", next)
	}
	want := `SELECT "items"."id", "items"."invoice_id", "items"."sku", "items"."qty", ` +
		`"vxj1"."id", "vxj1"."created_at", "vxj1"."updated_at", "vxj1"."org_id", "vxj1"."number", "vxj1"."total", "vxj1"."paid", "vxj1"."paid_at", "vxj1"."note", "vxj1"."data" ` +
		`FROM "items" JOIN "invoices" AS "vxj1" ON "vxj1"."id" = "items"."invoice_id" ` +
		`WHERE ("vxj1"."number" < $1 OR ("vxj1"."number" = $2 AND "items"."id" > $3)) ` +
		`ORDER BY "vxj1"."number" DESC, "items"."id" LIMIT $4`
	if e.Log[1] != want {
		t.Errorf("\n got %s\nwant %s", e.Log[1], want)
	}
	if got := fmt.Sprint(e.ArgLog[1]); got != "[INV-2 INV-2 11 2]" {
		t.Errorf("args %s", got)
	}
}

// A row that a left join found nothing for pages from NULL, which sorts
// after every value ascending: past it are the rows of NULL with a greater
// key. Past a value, the rows of NULL follow too, though the column holds
// none.
func TestJoinAfterARowWithoutAJoinedRow(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{itemInvoiceRow(itemRow(11, 0, "B", 2), nulls(10))},
		{},
		{itemInvoiceRow(itemRow(10, 1, "A", 1), invoiceRow(1))},
		{},
	}}
	db := openFake(velox.Postgres, e)
	q := db.Query[testmodel.Item]().LeftJoin(It.Invoice).OrderBy(It.Invoice.By(I.Number.Asc()), It.ID.Asc()).Limit(1)
	page, err := q.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = q.After(&page[0]).All(ctx); err != nil {
		t.Fatal(err)
	}
	if want := ` WHERE (("vxj1"."number" IS NULL AND "items"."id" > $1)) `; !strings.Contains(e.Log[1], want) {
		t.Errorf("past NULL:\n got %s\nwant …%s", e.Log[1], want)
	}
	if page, err = q.All(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = q.After(&page[0]).All(ctx); err != nil {
		t.Fatal(err)
	}
	if want := ` WHERE (("vxj1"."number" > $1 OR "vxj1"."number" IS NULL) OR ("vxj1"."number" = $2 AND "items"."id" > $3)) `; !strings.Contains(e.Log[3], want) {
		t.Errorf("past a value:\n got %s\nwant …%s", e.Log[3], want)
	}
}

// Fill gives a hand-built row the joined entity After pages from.
func TestJoinFillBuildsTheRowAfterPagesFrom(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{{itemInvoiceRow(itemRow(12, 3, "C", 3), invoiceRow(3))}}}
	db := openFake(velox.Postgres, e)
	after := &testmodel.Item{ID: 11}
	if err := It.Invoice.Fill(after, &testmodel.Invoice{Number: "INV-2"}); err != nil {
		t.Fatal(err)
	}
	q := db.Query[testmodel.Item]().Join(It.Invoice).OrderBy(It.Invoice.By(I.Number.Desc()), It.ID.Asc()).Limit(2)
	if _, err := q.After(after).All(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(e.ArgLog[0]); got != "[INV-2 INV-2 11 2]" {
		t.Errorf("args %s", got)
	}
	if v, loaded := after.Edges.Invoice.Get(); !loaded || v.Number != "INV-2" {
		t.Errorf("the field holds %v, %v", v, loaded)
	}
	if v, ok := It.Invoice.Filled(after); !ok || v.Number != "INV-2" {
		t.Errorf("Filled: %v, %v", v, ok)
	}
	if v, ok := It.Invoice.Filled(&testmodel.Item{}); ok || v != nil {
		t.Errorf("Filled of a row with none: %v, %v", v, ok)
	}
}
