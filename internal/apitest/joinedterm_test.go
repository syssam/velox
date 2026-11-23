package apitest

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

type item = testmodel.Item

// byOrg is a child's aggregate grouped by its parent's column.
func byOrg(db *velox.DB) velox.Query[item] {
	org := velox.Joined(It.Invoice, I.OrgID)
	return db.Query[item]().
		Join(It.Invoice).
		Where(It.Invoice.Is(I.Paid.EQ(true))).
		Select(org, It.Qty.Sum().As("qty"), velox.Joined(It.Invoice, I.Total).Max().As("top")).
		GroupBy(org).
		OrderBy(org.Asc()).
		Limit(10)
}

// TestJoinedTermSQL: Rel.Joined is a term of the joined row (ADR 0143): a
// projection over a to-one join selects, groups, aggregates and orders by
// the parent's columns, every column qualified, the join's ON clause first.
func TestJoinedTermSQL(t *testing.T) {
	for _, tc := range []struct {
		d   velox.Dialect
		sql string
	}{
		{velox.Postgres, `SELECT "vxj1"."org_id", SUM("items"."qty") AS "qty", MAX("vxj1"."total") AS "top" FROM "items" ` +
			`JOIN "invoices" AS "vxj1" ON "vxj1"."id" = "items"."invoice_id" WHERE "vxj1"."paid" GROUP BY "vxj1"."org_id" ORDER BY "vxj1"."org_id" LIMIT 10`},
		{velox.MySQL, "SELECT `vxj1`.`org_id`, SUM(`items`.`qty`) AS `qty`, MAX(`vxj1`.`total`) AS `top` FROM `items` " +
			"JOIN `invoices` AS `vxj1` ON `vxj1`.`id` = `items`.`invoice_id` WHERE `vxj1`.`paid` = 1 GROUP BY `vxj1`.`org_id` ORDER BY ANY_VALUE(`vxj1`.`org_id`) IS NULL, `vxj1`.`org_id` LIMIT 10"},
		{velox.SQLite, `SELECT "vxj1"."org_id", SUM("items"."qty") AS "qty", MAX("vxj1"."total") AS "top" FROM "items" ` +
			`JOIN "invoices" AS "vxj1" ON "vxj1"."id" = "items"."invoice_id" WHERE "vxj1"."paid" = 1 GROUP BY "vxj1"."org_id" ORDER BY "vxj1"."org_id" IS NULL, "vxj1"."org_id" LIMIT 10`},
	} {
		text, _, err := byOrg(openFake(tc.d, &fakeconn.Exec{})).SQL(ctx)
		if err != nil || text != tc.sql {
			t.Errorf("%v:\n got %s %v\nwant %s", tc.d, text, err, tc.sql)
		}
	}
	// A Calc and a DateTrunc of the parent, and the parent's Read filter in ON.
	db := openFake(velox.Postgres, &fakeconn.Exec{}, velox.WithFilter(func(_ context.Context, _ *velox.DB, fe velox.FilterEntity, _ velox.Op) (velox.Predicate, error) {
		if fe.Name() == "Invoice" {
			return velox.FieldEQ("org_id", 7), nil
		}
		return velox.Allow(), nil
	}))
	month := velox.Joined(It.Invoice, velox.DateTrunc[inv](velox.UnitMonth, I.CreatedAt))
	text, args, err := db.Query[item]().LeftJoin(It.Invoice).
		Select(velox.Joined(It.Invoice, velox.Calc[inv]("LOWER(?)", I.Number)).As("n"), month.As("m")).
		GroupBy(velox.Joined(It.Invoice, velox.Calc[inv]("LOWER(?)", I.Number)), month).Limit(5).SQL(ctx)
	want := `SELECT (LOWER("vxj1"."number")) AS "n", (date_trunc('month', ("vxj1"."created_at" AT TIME ZONE 'UTC')) AT TIME ZONE 'UTC') AS "m" FROM "items" ` +
		`LEFT JOIN "invoices" AS "vxj1" ON "vxj1"."id" = "items"."invoice_id" AND "vxj1"."org_id" = $1 ` +
		`GROUP BY (LOWER("vxj1"."number")), (date_trunc('month', ("vxj1"."created_at" AT TIME ZONE 'UTC')) AT TIME ZONE 'UTC') LIMIT 5`
	if err != nil || text != want || !reflect.DeepEqual(args, []any{int64(7)}) {
		t.Errorf("filtered:\n got %s %v %v\nwant %s", text, args, err, want)
	}
}

// TestJoinedTermRuns: on SQLite, the items' quantities grouped by their
// invoices' org; an inner join leaves out the items of an unpaid invoice,
// a left join keeps the item with no invoice, in a group of NULL.
func TestJoinedTermRuns(t *testing.T) {
	db := connectInvoices(t, ":memory:")
	if _, err := velox.Pool(db).Exec(`UPDATE invoices SET org_id = id, paid = id <> 3, total = id * 10;
		INSERT INTO invoices (id, org_id, number, total, paid, note, data) VALUES (4, 1, 'd', 5, true, '', x'');
		CREATE TABLE items (id INTEGER PRIMARY KEY, invoice_id INTEGER NOT NULL, sku TEXT NOT NULL, qty INTEGER NOT NULL);
		INSERT INTO items (id, invoice_id, sku, qty) VALUES (1, 1, 'a', 1), (2, 1, 'b', 2), (3, 2, 'c', 4), (4, 3, 'd', 8), (5, 4, 'e', 16), (6, 99, 'f', 32)`); err != nil {
		t.Fatal(err)
	}
	type row struct {
		OrgID int64
		Qty   int64
		Top   float64
	}
	got, err := velox.Scan[row](ctx, byOrg(db))
	if want := []row{{1, 19, 10}, {2, 4, 20}}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("inner: %v %v, want %v", got, err, want)
	}
	org := velox.Joined(It.Invoice, I.OrgID)
	type left struct {
		OrgID *int64
		Qty   int64
	}
	all, err := velox.Scan[left](ctx, db.Query[item]().LeftJoin(It.Invoice).
		Select(org, It.Qty.Sum().As("qty")).GroupBy(org).OrderBy(org.Asc()).Limit(10))
	if err != nil || len(all) != 4 || all[3].OrgID != nil || all[3].Qty != 32 || *all[2].OrgID != 3 {
		t.Errorf("left: %+v %v", all, err)
	}
}

// TestJoinedTermRefused: a joined term needs the join, reads a to-one
// relation, holds no aggregate, relation's aggregate or other joined term,
// and a projection over a join takes no With.
func TestJoinedTermRefused(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	q := db.Query[item]()
	for name, c := range map[string]struct {
		q    velox.Query[item]
		want string
	}{
		"no join":   {q.Select(velox.Joined(It.Invoice, I.OrgID)).Limit(1), "needs Join or LeftJoin"},
		"aggregate": {q.Join(It.Invoice).Select(velox.Joined(It.Invoice, I.Total.Sum())).Limit(1), "aggregate the joined term instead"},
		"calc aggregate": {q.Join(It.Invoice).Select(velox.Joined(It.Invoice, velox.Calc[inv]("SUM(?)", I.Total).Agg())).Limit(1),
			"aggregate the joined term instead"},
		"relation's aggregate": {q.Join(It.Invoice).Select(velox.Joined(It.Invoice, I.Items.Count())).Limit(1), "relation's aggregate"},
		"invalid inner":        {q.Join(It.Invoice).Select(velox.Joined(It.Invoice, velox.Calc[inv]("SELECT 1"))).Limit(1), "SELECT"},
	} {
		if _, _, err := c.q.SQL(ctx); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", name, err, c.want)
		}
	}
	if _, err := velox.Scan[struct{ OrgID int64 }](ctx, q.Join(It.Invoice.With(I.Items)).Select(velox.Joined(It.Invoice, I.OrgID)).Limit(1)); err == nil || !strings.Contains(err.Error(), "cannot be read with With") {
		t.Errorf("with: %v", err)
	}
	// A to-many relation: Joined of Invoices.Items, on a query of Invoice.
	if _, _, err := db.Query[inv]().Select(velox.Joined(I.Items, It.Qty)).Limit(1).SQL(ctx); err == nil || !strings.Contains(err.Error(), "to-many") {
		t.Errorf("to-many: %v", err)
	}
}
