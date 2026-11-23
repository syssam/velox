package apitest

import (
	"reflect"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
)

// The nextapp port's projection questions (ADR 0142), answered on the
// invoice model: an aggregate of a column known by name, an aggregate in
// Calc's text, COUNT(DISTINCT CASE …) and SELECT DISTINCT keys.

// TestAggregateOfAColumnByName: ColumnByName's Term aggregates as the
// handle does (its Sum is the handle's Sum, written the same), and a Calc
// marked Agg is an aggregate beside GroupBy, in Select and in Having; both
// run on SQLite and agree.
func TestAggregateOfAColumnByName(t *testing.T) {
	org, _ := velox.ColumnByName[inv]("org_id")
	total, ok := velox.ColumnByName[inv]("total")
	if !ok {
		t.Fatal("total not found")
	}
	q := func(db *velox.DB) velox.Query[inv] {
		return db.Query[inv]().
			Select(org, total.Sum().As("sum"), velox.Calc[inv]("SUM(?)", total).Agg().As("calc")).
			GroupBy(org).
			Having(velox.Expr[inv]("? > ?", velox.Calc[inv]("SUM(?)", total).Agg(), 5)).
			OrderBy(org.Asc()).
			Limit(10)
	}
	for _, tc := range []struct {
		d   velox.Dialect
		sql string
	}{
		{velox.Postgres, `SELECT "org_id", SUM("total") AS "sum", (SUM("total")) AS "calc" FROM "invoices" GROUP BY "org_id" HAVING ((SUM("total")) > $1) ORDER BY "org_id" LIMIT 10`},
		{velox.MySQL, "SELECT `org_id`, SUM(`total`) AS `sum`, (SUM(`total`)) AS `calc` FROM `invoices` GROUP BY `org_id` HAVING ((SUM(`total`)) > ?) ORDER BY `org_id` LIMIT 10"},
		{velox.SQLite, `SELECT "org_id", SUM("total") AS "sum", (SUM("total")) AS "calc" FROM "invoices" GROUP BY "org_id" HAVING ((SUM("total")) > ?) ORDER BY "org_id" LIMIT 10`},
	} {
		db := openFake(tc.d, &fakeconn.Exec{})
		text, args, err := q(db).SQL(ctx)
		if err != nil || text != tc.sql || !reflect.DeepEqual(args, []any{5}) {
			t.Errorf("%v:\n got %s %v %v\nwant %s", tc.d, text, args, err, tc.sql)
		}
		typed, _, _ := db.Query[inv]().Select(I.OrgID, I.Total.Sum().As("sum")).GroupBy(I.OrgID).Limit(10).SQL(ctx)
		byName, _, _ := db.Query[inv]().Select(org, total.Sum().As("sum")).GroupBy(org).Limit(10).SQL(ctx)
		if typed != byName {
			t.Errorf("%v: by name is not the handle's:\n%s\n%s", tc.d, byName, typed)
		}
	}

	type row struct {
		OrgID     int64
		Sum, Calc float64
	}
	got, err := velox.Scan[row](ctx, q(reportDB(t)))
	want := []row{{1, 60, 60}, {2, 40, 40}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("SQLite: %v %v, want %v", got, err, want)
	}
}

// TestCountDistinctOfACase: COUNT(DISTINCT CASE WHEN x <> 0 THEN x END) is
// Calc's CASE with CountDistinct; the CASE's NULL for a zero is not counted.
func TestCountDistinctOfACase(t *testing.T) {
	nonZero := velox.Calc[inv]("CASE WHEN ? <> 0 THEN ? END", I.Total, I.Total).CountDistinct().As("n")
	q := func(db *velox.DB) velox.Query[inv] {
		return db.Query[inv]().Select(I.OrgID, nonZero).GroupBy(I.OrgID).OrderBy(I.OrgID.Asc()).Limit(10)
	}
	for _, tc := range []struct {
		d   velox.Dialect
		sql string
	}{
		{velox.Postgres, `SELECT "org_id", COUNT(DISTINCT (CASE WHEN "total" <> 0 THEN "total" END)) AS "n" FROM "invoices" GROUP BY "org_id" ORDER BY "org_id" LIMIT 10`},
		{velox.MySQL, "SELECT `org_id`, COUNT(DISTINCT (CASE WHEN `total` <> 0 THEN `total` END)) AS `n` FROM `invoices` GROUP BY `org_id` ORDER BY `org_id` LIMIT 10"},
		{velox.SQLite, `SELECT "org_id", COUNT(DISTINCT (CASE WHEN "total" <> 0 THEN "total" END)) AS "n" FROM "invoices" GROUP BY "org_id" ORDER BY "org_id" LIMIT 10`},
	} {
		text, args, err := q(openFake(tc.d, &fakeconn.Exec{})).SQL(ctx)
		if err != nil || text != tc.sql || len(args) != 0 {
			t.Errorf("%v:\n got %s %v %v\nwant %s", tc.d, text, args, err, tc.sql)
		}
	}
	db := reportDB(t)
	// org 1: totals 0, 20, 20; org 2: 40.
	if _, err := velox.Pool(db).Exec(`UPDATE invoices SET total = CASE id WHEN 1 THEN 0 WHEN 3 THEN 20 ELSE total END`); err != nil {
		t.Fatal(err)
	}
	type row struct{ OrgID, N int64 }
	got, err := velox.Scan[row](ctx, q(db))
	if want := []row{{1, 1}, {2, 1}}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("SQLite: %v %v, want %v", got, err, want)
	}
}

// TestDistinctKeys: ent's GroupBy(keys...).Scan with no aggregate reads each
// distinct pair once. velox says it as Select(keys).Distinct(), or as
// Select(keys).GroupBy(keys); both read the same rows.
func TestDistinctKeys(t *testing.T) {
	distinct := func(db *velox.DB) velox.Query[inv] {
		return db.Query[inv]().Where(I.Total.GT(0)).Select(I.OrgID, I.Paid).Distinct().OrderBy(I.OrgID.Asc()).AllowUnbounded()
	}
	grouped := func(db *velox.DB) velox.Query[inv] {
		return db.Query[inv]().Where(I.Total.GT(0)).Select(I.OrgID, I.Paid).GroupBy(I.OrgID, I.Paid).OrderBy(I.OrgID.Asc()).AllowUnbounded()
	}
	for _, tc := range []struct {
		d               velox.Dialect
		distinct, group string
	}{
		{velox.Postgres,
			`SELECT DISTINCT "org_id", "paid" FROM "invoices" WHERE "total" > $1 ORDER BY "org_id"`,
			`SELECT "org_id", "paid" FROM "invoices" WHERE "total" > $1 GROUP BY "org_id", "paid" ORDER BY "org_id"`},
		{velox.MySQL,
			"SELECT DISTINCT `org_id`, `paid` FROM `invoices` WHERE `total` > ? ORDER BY `org_id`",
			"SELECT `org_id`, `paid` FROM `invoices` WHERE `total` > ? GROUP BY `org_id`, `paid` ORDER BY `org_id`"},
		{velox.SQLite,
			`SELECT DISTINCT "org_id", "paid" FROM "invoices" WHERE "total" > ? ORDER BY "org_id"`,
			`SELECT "org_id", "paid" FROM "invoices" WHERE "total" > ? GROUP BY "org_id", "paid" ORDER BY "org_id"`},
	} {
		db := openFake(tc.d, &fakeconn.Exec{})
		if text, _, err := distinct(db).SQL(ctx); err != nil || text != tc.distinct {
			t.Errorf("%v Distinct:\n got %s %v\nwant %s", tc.d, text, err, tc.distinct)
		}
		if text, _, err := grouped(db).SQL(ctx); err != nil || text != tc.group {
			t.Errorf("%v GroupBy:\n got %s %v\nwant %s", tc.d, text, err, tc.group)
		}
	}
	db := reportDB(t)
	type pair struct {
		OrgID int64
		Paid  bool
	}
	want := []pair{{1, false}, {2, true}}
	for name, q := range map[string]velox.Query[inv]{"Distinct": distinct(db), "GroupBy": grouped(db)} {
		if got, err := velox.Scan[pair](ctx, q); err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("SQLite %s: %v %v, want %v", name, got, err, want)
		}
	}
}
