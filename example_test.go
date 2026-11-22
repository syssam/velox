package velox_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/scope"
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
	ctx := testmodel.Org.With(context.Background(), 1)
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
	ctx := testmodel.Org.With(context.Background(), 7)
	text, args, err := db.Query[testmodel.Invoice]().
		Columns(testmodel.Invoices.ID, testmodel.Invoices.Number).
		Where(testmodel.Invoices.Total.GT(100), testmodel.Invoices.Paid.EQ(false)).
		OrderBy(testmodel.Invoices.Number.Asc()).
		Limit(10).
		SQL(ctx)
	fmt.Println(text)
	fmt.Println(args, err)
	// Output:
	// SELECT "id", "number" FROM "invoices" WHERE "total" > $1 AND NOT "paid" AND "org_id" = $2 ORDER BY "number" LIMIT $3
	// [100 7 10] <nil>
}

// Select makes a subquery of one column, and InSelect tests another column
// of the same type against it. The subquery's entity gets its own Read
// policy and interceptors, as a query on it would.
func ExampleNumberCol_InSelect() {
	db := velox.Open(velox.Postgres, nil)
	ctx := testmodel.Org.With(context.Background(), 7)
	I, It := testmodel.Invoices, testmodel.Items
	bigOrders := It.InvoiceID.Select(It.Qty.GTE(100))
	text, args, err := db.Query[testmodel.Invoice]().
		Columns(I.ID).
		Where(I.ID.InSelect(bigOrders)).
		Limit(10).
		SQL(ctx)
	fmt.Println(text)
	fmt.Println(args, err)
	// Output:
	// SELECT "id" FROM "invoices" WHERE EXISTS (SELECT 1 FROM "items" AS "vx1" WHERE "vx1"."invoice_id" = "invoices"."id" AND "qty" >= $1) AND "org_id" = $2 LIMIT $3
	// [100 7 10] <nil>
}

// Text, Number and HasKey test a value inside a JSON column, at a path of
// object keys. A value of another kind is NULL, so the same rows match on
// every dialect; the keys are arguments.
func ExampleJSONCol_Text() {
	db := velox.Open(velox.SQLite, nil)
	D := fixture.JDocs
	text, args, err := db.Query[fixture.JDoc]().
		Columns(D.ID).
		Where(D.Attrs.Text("plan", "tier").EQ("pro")).
		Limit(10).
		SQL(context.Background())
	fmt.Println(text)
	fmt.Println(args, err)
	// Output:
	// SELECT "id" FROM "j_docs" WHERE CASE WHEN json_type("attrs", ?) = 'text' THEN json_extract("attrs", ?) END = ? LIMIT 10
	// [$."plan"."tier" $."plan"."tier" pro] <nil>
}

// With loads a relation for every row the query returns, in one more query.
func ExampleQuery_With() {
	db := exampleDB()
	ctx := testmodel.Org.With(context.Background(), 1)
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
	items, loaded := got.Edges.Items.Get()
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
	ctx := testmodel.Org.With(context.Background(), 1)
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

// A scope confines an entity's rows to a value the context carries (see the
// scope package); the DB's WithoutPolicy lifts it, with a reason the hooks
// see. The bypass is the DB's: the same context on db is still scoped.
func ExampleDB_WithoutPolicy() {
	db := exampleDB()
	org1 := testmodel.Org.With(context.Background(), 1)
	org2 := testmodel.Org.With(context.Background(), 2)
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
	all, _ := db.WithoutPolicy("the example's report").Query[testmodel.Invoice]().Count(context.Background())
	fmt.Println(n1, n2, all)
	_, err := db.Query[testmodel.Invoice]().Count(context.Background())
	fmt.Println(errors.Is(err, scope.ErrNoScope))
	// Output:
	// 2 1 3
	// true
}

// SumBy adds up a column per value of a key column. The scope applies:
// with WithoutPolicy, the key's values are the orgs.
func ExampleSumBy() {
	db := exampleDB()
	for _, w := range []struct {
		org   int64
		total float64
	}{{1, 10}, {1, 20}, {2, 5}} {
		c := testmodel.Org.With(context.Background(), w.org)
		inv := testmodel.Invoice{Number: fmt.Sprint(w.total), Total: w.total, Data: []byte{}}
		if err := db.Insert(&inv).Exec(c); err != nil {
			fmt.Println(err)
			return
		}
	}
	all := db.WithoutPolicy("the example's report")
	groups, err := velox.SumBy(context.Background(), all.Query[testmodel.Invoice]().AllowUnbounded(), testmodel.Invoices.OrgID.Key(), testmodel.Invoices.Total)
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
	ctx := testmodel.Org.With(context.Background(), 1)
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

// SetExpr assigns a column an SQL expression over the row's own columns,
// for what Set, Add and Sub do not say. The update keeps the scope: only
// the context's organization's rows change.
func ExampleSetExpr() {
	db := exampleDB()
	ctx := testmodel.Org.With(context.Background(), 1)
	I := testmodel.Invoices
	inv := testmodel.Invoice{Number: "a-1", Total: 10, Data: []byte{}}
	if err := db.Insert(&inv).Exec(ctx); err != nil {
		fmt.Println(err)
		return
	}
	got, err := db.Update[testmodel.Invoice]().
		Where(I.ID.EQ(inv.ID)).
		Set(
			velox.SetExpr(I.Total, "ROUND(? * ?, 2)", I.Total, 1.075),
			velox.SetExpr(I.Number, "UPPER(?)", I.Number),
		).
		One(ctx)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(got.Number, got.Total)
	text, _, err := velox.Open(velox.Postgres, nil).Update[testmodel.Invoice]().
		Where(I.ID.EQ(inv.ID)).
		Set(velox.SetExpr(I.Total, "? * 2", I.Total)).
		SQL(ctx)
	fmt.Println(text, err)
	// Output:
	// A-1 10.75
	// UPDATE "invoices" SET "total" = ("total" * 2), "updated_at" = $1 WHERE "id" = $2 AND "org_id" = $3 <nil>
}

// Calc is an SQL expression as a Term: an ordering with Asc or Desc, a
// result column of Select, a key of GroupBy. Its ? are the entity's column
// handles, Terms, or bound values, and the query keeps its policies.
func ExampleCalc() {
	db := velox.Open(velox.Postgres, nil)
	ctx := testmodel.Org.With(context.Background(), 7)
	I := testmodel.Invoices
	text, args, err := db.Query[testmodel.Invoice]().
		Columns(I.ID, I.Number).
		Where(I.Paid.EQ(false)).
		OrderBy(velox.Calc[testmodel.Invoice]("similarity(?, ?)", I.Number, "INV-42").Desc()).
		Limit(5).
		SQL(ctx)
	fmt.Println(text)
	fmt.Println(args, err)
	// Output:
	// SELECT "id", "number" FROM "invoices" WHERE NOT "paid" AND "org_id" = $1 ORDER BY (similarity("number", $2)) DESC LIMIT $3
	// [7 INV-42 5] <nil>
}
