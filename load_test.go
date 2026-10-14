package velox_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// invoiceRow is declared in query_test.go; It is declared there too.

func itemRow(id, invoice int64, sku string, qty int64) []any {
	return []any{id, invoice, sku, qty}
}

// sampleRow is a full Sample row for the fake executor, in column order, with
// ParentID set to parentID (nil or an int64).
func sampleRow(id int64, parentID any) []any {
	return []any{id, "n", "s", nil, int64(0), nil, false, nil, t0, nil, []byte("b"), parentID, int64(0)}
}

// TestWithAttachesToOwners is half of Review Focus 2.
func TestWithAttachesToOwners(t *testing.T) {
	e := &velox.FakeExec{Results: [][][]any{
		{invoiceRow(1), invoiceRow(2), invoiceRow(3)},
		{itemRow(10, 1, "A", 1), itemRow(11, 2, "B", 1), itemRow(12, 1, "C", 1)},
	}}
	db := velox.OpenFake(velox.Postgres, e)
	invs, err := db.Query[testmodel.Invoice]().With(I.Items).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := `SELECT "id", "invoice_id", "sku", "qty" FROM "items" WHERE "invoice_id" IN ($1, $2, $3) ORDER BY "id"`; e.Log[1] != want {
		t.Errorf("\n got %s\nwant %s", e.Log[1], want)
	}
	skus := func(i int) []string {
		items, loaded := invs[i].Items.Get()
		if !loaded || items == nil {
			t.Fatalf("invoice %d: loaded %v, items %v", i, loaded, items)
		}
		var out []string
		for _, it := range items {
			out = append(out, it.SKU)
		}
		return out
	}
	if !reflect.DeepEqual(skus(0), []string{"A", "C"}) || !reflect.DeepEqual(skus(1), []string{"B"}) || len(skus(2)) != 0 {
		t.Errorf("%v %v %v", skus(0), skus(1), skus(2))
	}
}

func TestWithBelongsToSharesAndSkipsNil(t *testing.T) {
	e := &velox.FakeExec{Results: [][][]any{
		{itemRow(10, 1, "A", 1), itemRow(11, 1, "B", 1)},
		{invoiceRow(1)},
	}}
	db := velox.OpenFake(velox.Postgres, e)
	items, err := db.Query[testmodel.Item]().With(It.Invoice).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a, okA := items[0].Invoice.Get()
	b, okB := items[1].Invoice.Get()
	if !okA || !okB || a == nil || a != b || a.ID != 1 {
		t.Errorf("%v %v %v %v", a, b, okA, okB)
	}
	if !strings.Contains(e.Log[1], `WHERE "id" IN ($1)`) {
		t.Errorf("keys not deduplicated: %s", e.Log[1])
	}
}

func TestWithNullableKeyLoadsNothing(t *testing.T) {
	e := &velox.FakeExec{Results: [][][]any{
		{sampleRow(1, nil)},
	}}
	db := velox.OpenFake(velox.Postgres, e)
	ss, err := db.Query[testmodel.Sample]().With(testmodel.Samples.Parent).Limit(10).All(ctx)
	if err != nil || e.Calls != 1 {
		t.Fatalf("%v, calls %d (a nil key must send nothing)", err, e.Calls)
	}
	if p, loaded := ss[0].Parent.Get(); !loaded || p != nil {
		t.Errorf("%v %v", p, loaded)
	}
}

// TestWithNestedLoopsBack is half of Review Focus 1.
func TestWithNestedLoopsBack(t *testing.T) {
	e := &velox.FakeExec{Results: [][][]any{
		{invoiceRow(1)},
		{itemRow(10, 1, "A", 1)},
		{invoiceRow(1)},
	}}
	db := velox.OpenFake(velox.Postgres, e)
	invs, err := db.Query[testmodel.Invoice]().With(I.Items.With(It.Invoice)).Limit(1).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := invs[0].Items.Get()
	back, loaded := items[0].Invoice.Get()
	if !loaded || back == nil || back.ID != 1 {
		t.Errorf("%v %v", back, loaded)
	}
}

func TestWithFilterAndOrder(t *testing.T) {
	e := &velox.FakeExec{Results: [][][]any{{invoiceRow(1)}, {}}}
	db := velox.OpenFake(velox.MySQL, e)
	if _, err := db.Query[testmodel.Invoice]().With(I.Items.Where(It.Qty.GT(0)).OrderBy(It.SKU.Desc())).Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if want := "SELECT `id`, `invoice_id`, `sku`, `qty` FROM `items` WHERE `invoice_id` IN (?) AND `qty` > ? ORDER BY `sku` DESC"; e.Log[1] != want {
		t.Errorf("\n got %s\nwant %s", e.Log[1], want)
	}
}

// TestWithChunksUnderTheParameterLimit is Review Focus 4.
func TestWithChunksUnderTheParameterLimit(t *testing.T) {
	defer velox.SetMaxParams(velox.Postgres, 3)() // one parameter for the filter, two keys per chunk
	e := &velox.FakeExec{Results: [][][]any{
		{invoiceRow(1), invoiceRow(2), invoiceRow(3)},
		{itemRow(11, 1, "B", 1), itemRow(10, 1, "A", 1), itemRow(12, 2, "C", 1)},
		{itemRow(13, 3, "D", 1)},
	}}
	db := velox.OpenFake(velox.Postgres, e)
	invs, err := db.Query[testmodel.Invoice]().With(I.Items.Where(It.Qty.GT(0))).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Log) != 3 || !strings.Contains(e.Log[1], "IN ($1, $2)") || !strings.Contains(e.Log[2], "IN ($1)") {
		t.Fatalf("%q", e.Log)
	}
	var got []string
	for _, inv := range invs {
		items, _ := inv.Items.Get()
		for _, it := range items {
			got = append(got, it.SKU)
		}
	}
	if !reflect.DeepEqual(got, []string{"B", "A", "C", "D"}) {
		t.Errorf("%v: every row once, in the order the database returned within each owner", got)
	}
}

func TestIterRefusesWith(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	n := 0
	for _, err := range db.Query[testmodel.Invoice]().With(I.Items).Unbounded().Iter(ctx) {
		n++
		if err == nil || !strings.Contains(err.Error(), "Iter does not load relations") {
			t.Errorf("%v", err)
		}
	}
	if n != 1 {
		t.Errorf("yielded %d times, want the error once", n)
	}
}

func TestRelationsInJSON(t *testing.T) {
	type out struct {
		Items velox.Many[testmodel.Item] `json:",omitzero"`
	}
	b, _ := json.Marshal(out{})
	if string(b) != `{}` {
		t.Errorf("not loaded: %s", b)
	}
	e := &velox.FakeExec{Results: [][][]any{{invoiceRow(1)}, {itemRow(10, 1, "A", 2)}}}
	invs, err := velox.OpenFake(velox.Postgres, e).Query[testmodel.Invoice]().With(I.Items).Limit(1).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(out{Items: invs[0].Items})
	if string(b) != `{"Items":[{"ID":10,"InvoiceID":1,"SKU":"A","Qty":2,"Invoice":null}]}` {
		t.Errorf("loaded: %s", b)
	}
}
