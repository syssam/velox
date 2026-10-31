package apitest

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

const itemCols = `"id", "invoice_id", "sku", "qty"`

// TestOfDirect: Of on a direct relation is an IN over the owners' keys, read
// when Of is called.
func TestOfDirect(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	inv1, inv2 := testmodel.Invoice{ID: 1}, testmodel.Invoice{ID: 2}
	p := I.Items.Of(&inv1, &inv2)
	inv1.ID = 99 // after Of: the condition keeps the key it read
	if _, err := db.Query[testmodel.Item]().Where(p).Limit(10).All(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `SELECT ` + itemCols + ` FROM "items" WHERE "invoice_id" IN ($1, $2) LIMIT $3`; e.SQL != want {
		t.Errorf("\n got %s\nwant %s", e.SQL, want)
	}
	if want := []any{int64(1), int64(2), 10}; !reflect.DeepEqual(e.Args, want) {
		t.Errorf("args %v, want %v", e.Args, want)
	}

	item := testmodel.Item{ID: 3, InvoiceID: 2}
	if _, err := db.Query[testmodel.Invoice]().Where(It.Invoice.Of(&item)).Limit(10).All(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `SELECT ` + allCols + ` FROM "invoices" WHERE "id" IN ($1) LIMIT $2`; e.SQL != want {
		t.Errorf("\n got %s\nwant %s", e.SQL, want)
	}
	if want := []any{int64(2), 10}; !reflect.DeepEqual(e.Args, want) {
		t.Errorf("args %v, want %v", e.Args, want)
	}
}

// TestOfEdgeCases: a nil owner fails the statement; an owner whose key is
// NULL relates to nothing; no owners match no row, and are not taken for a
// condition that matches every row; a repeated owner is one more parameter.
func TestOfEdgeCases(t *testing.T) {
	e := &fakeconn.Exec{Affected: 0}
	db := openFake(velox.Postgres, e)

	_, err := db.Query[testmodel.Item]().Where(I.Items.Of(nil)).Limit(10).All(ctx)
	if err == nil || err.Error() != "velox: testmodel.Invoice: Of was given a nil owner" {
		t.Errorf("nil owner: %v", err)
	}
	inv1 := testmodel.Invoice{ID: 1}
	if _, err := db.Query[testmodel.Item]().Where(I.Items.Of(&inv1, nil)).Limit(10).All(ctx); err == nil {
		t.Error("a nil owner among others was accepted")
	}

	admin := testmodel.WithUser(ctx, "admin")
	ticket := testmodel.Ticket{ID: 1} // ParentID nil
	got, err := db.Query[testmodel.Ticket]().Where(testmodel.Tickets.Parent.Of(&ticket)).Limit(10).All(admin)
	if err != nil || len(got) != 0 {
		t.Fatalf("NULL key: %v, %v", got, err)
	}
	if !strings.HasSuffix(e.SQL, ` FROM "tickets" WHERE 1 = 0 LIMIT $1`) || !reflect.DeepEqual(e.Args, []any{10}) {
		t.Errorf("NULL key: %s %v", e.SQL, e.Args)
	}

	items, err := db.Query[testmodel.Item]().Where(I.Items.Of()).Limit(10).All(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("no owners: %v, %v", items, err)
	}
	if want := `SELECT ` + itemCols + ` FROM "items" WHERE 1 = 0 LIMIT $1`; e.SQL != want {
		t.Errorf("no owners: %s", e.SQL)
	}
	n, err := db.Update[testmodel.Item]().Set(It.Qty.Set(1)).Where(I.Items.Of()).Exec(ctx)
	if err != nil || n != 0 {
		t.Errorf("Update on no owners: %d, %v", n, err)
	}
	if want := `UPDATE "items" SET "qty" = $1 WHERE 1 = 0`; e.SQL != want {
		t.Errorf("Update on no owners: %s", e.SQL)
	}

	if _, err := db.Query[testmodel.Item]().Where(I.Items.Of(&inv1, &inv1)).Limit(10).All(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `SELECT ` + itemCols + ` FROM "items" WHERE "invoice_id" IN ($1, $2) LIMIT $3`; e.SQL != want || !reflect.DeepEqual(e.Args, []any{int64(1), int64(1), 10}) {
		t.Errorf("repeated owner: %s %v", e.SQL, e.Args)
	}
}

// TestOfWhere: the relation's Where conditions apply too.
func TestOfWhere(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	inv1 := testmodel.Invoice{ID: 1}
	if _, err := db.Query[testmodel.Item]().Where(I.Items.Where(It.Qty.GT(0)).OrderBy(It.SKU.Desc()).Of(&inv1)).Limit(10).All(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `SELECT ` + itemCols + ` FROM "items" WHERE ("invoice_id" IN ($1) AND "qty" > $2) LIMIT $3`; e.SQL != want {
		t.Errorf("\n got %s\nwant %s", e.SQL, want)
	}
	if want := []any{int64(1), int64(0), 10}; !reflect.DeepEqual(e.Args, want) {
		t.Errorf("args %v, want %v", e.Args, want)
	}
}

// TestOfInTheGuard: Of with no owners matches no row, so Not of it matches
// every row, and an Update or Delete on it alone is refused; through a join
// entity as directly.
func TestOfInTheGuard(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	if _, err := db.Update[fixture.DTag]().Set(fixture.DTags.Name.Set("x")).Where(fixture.DBooks.Tags.Of()).Exec(ctx); err != nil {
		t.Errorf("Of(): %v", err)
	}
	for name, run := range map[string]func() error{
		"direct": func() error {
			_, err := db.Delete[testmodel.Item]().Where(velox.Not(I.Items.Of())).Exec(ctx)
			return err
		},
		"through": func() error {
			_, err := db.Update[fixture.DTag]().Set(fixture.DTags.Name.Set("x")).Where(velox.Not(fixture.DBooks.Tags.Of())).Exec(ctx)
			return err
		},
	} {
		if err := run(); !errors.Is(err, velox.ErrNoWhere) {
			t.Errorf("%s: Not(Of()): %v", name, err)
		}
	}
	b := fixture.DBook{ID: 1}
	if _, err := db.Delete[fixture.DTag]().Where(velox.Not(fixture.DBooks.Tags.Of(&b))).Exec(ctx); err != nil {
		t.Errorf("Not(Of(&b)): %v", err)
	}
}
