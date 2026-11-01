package velox_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
	_ "modernc.org/sqlite"
)

// exampleDDL is what velox-gen migrate sql -dialect sqlite prints for
// testmodel's invoices and items.
const exampleDDL = `CREATE TABLE "invoices" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "created_at" datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "updated_at" datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "org_id" integer NOT NULL,
  "number" text NOT NULL,
  "total" real NOT NULL,
  "paid" boolean NOT NULL,
  "paid_at" datetime,
  "note" text NOT NULL DEFAULT '',
  "data" blob NOT NULL
);
CREATE UNIQUE INDEX "invoice_org_id_number" ON "invoices" ("org_id", "number");
CREATE TABLE "items" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "invoice_id" integer NOT NULL,
  "sku" text NOT NULL,
  "qty" integer NOT NULL,
  CONSTRAINT "items_invoices_items" FOREIGN KEY ("invoice_id") REFERENCES "invoices" ("id")
);
CREATE INDEX "items_invoice_id" ON "items" ("invoice_id");`

// exampleDB opens an in-memory SQLite database with testmodel's invoices
// and items tables. An application's tables come from its migrations.
func exampleDB() *velox.DB {
	db, err := velox.Connect("sqlite", "file::memory:")
	if err != nil {
		panic(err)
	}
	if _, err := velox.Pool(db).Exec(exampleDDL); err != nil {
		panic(err)
	}
	return db
}

// Connect opens the database; Close closes it. velox.Pool is the *sql.DB under it,
// for pool settings and for what takes one, such as migrate.Up.
func ExampleConnect() {
	db, err := velox.Connect("sqlite", ":memory:")
	if err != nil {
		panic(err)
	}
	defer func() { _ = db.Close() }()
	fmt.Println(velox.Pool(db).Stats().MaxOpenConnections)
	// Output: 1
}

// A query is a value: build it with Where, OrderBy and Limit, then run it
// with All, One, Count or Iter.
func Example() {
	db := exampleDB()
	ctx := velox.WithTenant(context.Background(), 1)
	for _, n := range []string{"A-2", "A-1", "A-3"} {
		inv := testmodel.Invoice{Number: n, Total: 10, Data: []byte{}}
		if err := db.Insert(&inv).Exec(ctx); err != nil {
			fmt.Println(err)
			return
		}
	}
	invs, err := db.Query[testmodel.Invoice]().
		Where(testmodel.Invoices.Number.HasPrefix("A-")).
		OrderBy(testmodel.Invoices.Number.Asc()).
		Limit(2).
		All(ctx)
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, inv := range invs {
		fmt.Println(inv.ID, inv.Number)
	}
	n, err := db.Query[testmodel.Invoice]().Count(ctx)
	fmt.Println(n, err)
	_, err = db.Query[testmodel.Invoice]().Where(testmodel.Invoices.Number.EQ("Z-9")).One(ctx)
	fmt.Println(velox.IsNotFound(err))
	// Output:
	// 2 A-1
	// 1 A-2
	// 3 <nil>
	// true
}

// SQL renders the statement a query would send, and sends nothing, so it
// needs no database.
func ExampleQuery_SQL() {
	db := velox.Open(velox.Postgres, nil)
	ctx := velox.WithTenant(context.Background(), 7)
	text, args, err := db.Query[testmodel.Invoice]().
		Columns(testmodel.Invoices.ID, testmodel.Invoices.Number).
		Where(testmodel.Invoices.Total.GT(100), testmodel.Invoices.Paid.EQ(false)).
		OrderBy(testmodel.Invoices.Number.Asc()).
		Limit(10).
		SQL(ctx)
	fmt.Println(text)
	fmt.Println(args, err)
	// Output:
	// SELECT "id", "number" FROM "invoices" WHERE "total" > $1 AND "paid" = $2 AND "org_id" = $3 ORDER BY "number" LIMIT $4
	// [100 false 7 10] <nil>
}

// With loads a relation for every row the query returns, in one more query.
func ExampleQuery_With() {
	db := exampleDB()
	ctx := velox.WithTenant(context.Background(), 1)
	inv := testmodel.Invoice{Number: "A-1", Data: []byte{}}
	if err := db.Insert(&inv).Exec(ctx); err != nil {
		fmt.Println(err)
		return
	}
	err := db.InsertAll([]testmodel.Item{
		{InvoiceID: inv.ID, SKU: "pen", Qty: 2},
		{InvoiceID: inv.ID, SKU: "ink", Qty: 5},
	}).Exec(ctx)
	if err != nil {
		fmt.Println(err)
		return
	}
	got, err := db.Query[testmodel.Invoice]().Where(testmodel.Invoices.ID.EQ(inv.ID)).With(testmodel.Invoices.Items).One(ctx)
	if err != nil {
		fmt.Println(err)
		return
	}
	items, loaded := got.Items.Get()
	fmt.Println(loaded, len(items))
	for _, it := range items {
		fmt.Println(it.SKU, it.Qty)
	}
	// Output:
	// true 2
	// pen 2
	// ink 5
}

// A transaction commits when fn returns nil, and rolls back when it
// returns an error, which Tx returns.
func ExampleDB_Tx() {
	db := exampleDB()
	ctx := velox.WithTenant(context.Background(), 1)
	inv := testmodel.Invoice{Number: "A-1", Data: []byte{}}
	if err := db.Insert(&inv).Exec(ctx); err != nil {
		fmt.Println(err)
		return
	}
	errDeclined := errors.New("card declined")
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		if _, err := tx.Update[testmodel.Invoice]().Set(testmodel.Invoices.Paid.Set(true)).Where(testmodel.Invoices.ID.EQ(inv.ID)).Exec(ctx); err != nil {
			return err
		}
		return errDeclined
	})
	fmt.Println(errors.Is(err, errDeclined))
	got, err := db.Query[testmodel.Invoice]().Where(testmodel.Invoices.ID.EQ(inv.ID)).One(ctx)
	fmt.Println(got.Paid, err)
	// Output:
	// true
	// false <nil>
}

// WithTenant scopes every statement to one tenant. An untyped constant
// converts to the tenant column's integer type.
func ExampleWithTenant() {
	db := exampleDB()
	org1 := velox.WithTenant(context.Background(), 1)
	org2 := velox.WithTenant(context.Background(), 2)
	for _, w := range []struct {
		ctx    context.Context
		number string
	}{{org1, "A-1"}, {org1, "A-2"}, {org2, "B-1"}} {
		inv := testmodel.Invoice{Number: w.number, Data: []byte{}}
		if err := db.Insert(&inv).Exec(w.ctx); err != nil {
			fmt.Println(err)
			return
		}
	}
	n1, _ := db.Query[testmodel.Invoice]().Count(org1)
	n2, _ := db.Query[testmodel.Invoice]().Count(org2)
	fmt.Println(n1, n2)
	_, err := db.Query[testmodel.Invoice]().Count(context.Background())
	fmt.Println(err)
	// Output:
	// 2 1
	// velox: testmodel.Invoice: no tenant in the context
}

// SumBy adds up a column per value of a key column. The tenant scope applies:
// with WithAllTenants, the key's values are the tenants.
func ExampleSumBy() {
	db := exampleDB()
	for _, w := range []struct {
		org   int64
		total float64
	}{{1, 10}, {1, 20}, {2, 5}} {
		c := velox.WithTenant(context.Background(), w.org)
		inv := testmodel.Invoice{Number: fmt.Sprint(w.total), Total: w.total, Data: []byte{}}
		if err := db.Insert(&inv).Exec(c); err != nil {
			fmt.Println(err)
			return
		}
	}
	all := velox.WithAllTenants(context.Background(), "the example's report")
	groups, err := velox.SumBy(all, db.Query[testmodel.Invoice]().Unbounded(), testmodel.Invoices.OrgID.Key(), testmodel.Invoices.Total)
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, g := range groups {
		fmt.Println(g.Key, g.Value)
	}
	// Output:
	// 1 30
	// 2 5
}

// An upsert's update branch writes only the columns Update names: an
// UpdateDefault, such as mixin.Time's updated_at, does not fire.
func ExampleInsertQuery_OnConflict() {
	db := exampleDB()
	ctx := velox.WithTenant(context.Background(), 1)
	first := testmodel.Invoice{Number: "A-1", Total: 10, Data: []byte{}}
	if err := db.Insert(&first).Exec(ctx); err != nil {
		fmt.Println(err)
		return
	}
	again := testmodel.Invoice{Number: "A-1", Total: 25, Data: []byte{}}
	err := db.Insert(&again).OnConflict(testmodel.Invoices.OrgID, testmodel.Invoices.Number).Update(testmodel.Invoices.Total).Exec(ctx)
	if err != nil {
		fmt.Println(err)
		return
	}
	got, err := db.Query[testmodel.Invoice]().Where(testmodel.Invoices.Number.EQ("A-1")).One(ctx)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(got.ID, got.Total, got.UpdatedAt.Equal(first.UpdatedAt))
	// Output:
	// 1 25 true
}
