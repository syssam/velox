package apitest

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
)

// reportDB is connectInvoices' database with each invoice's created_at set:
// 1 2025-12-31 23:30, 2 2026-02-15 10:00 (a Sunday), 3 2026-03-31 23:30,
// 4 2026-04-01 00:00, all UTC.
func reportDB(t *testing.T) *velox.DB {
	t.Helper()
	db := connectInvoices(t, ":memory:")
	if _, err := velox.Pool(db).Exec(`INSERT INTO invoices (id, org_id, number, total, paid, note, data) VALUES (4, 2, 'd', 5, true, '', x'');
		UPDATE invoices SET created_at = CASE id
			WHEN 1 THEN '2025-12-31 23:30:00.000000000+00:00'
			WHEN 2 THEN '2026-02-15 10:00:00.000000000+00:00'
			WHEN 3 THEN '2026-03-31 23:30:00.000000000+00:00'
			ELSE '2026-04-01 00:00:00.000000000+00:00' END,
			total = id * 10`); err != nil {
		t.Fatal(err)
	}
	return db
}

// truncs reads each invoice's DateTrunc, by id, as times in UTC formatted
// 2006-01-02 15:04.
func truncs(t *testing.T, db *velox.DB, d velox.Term[inv]) []string {
	t.Helper()
	var out []string
	for r, err := range db.Query[inv]().Select(d).OrderBy(I.ID.Asc()).Limit(10).Rows(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		var at time.Time
		if err := r.Scan(&at); err != nil {
			t.Fatal(err)
		}
		out = append(out, at.UTC().Format("2006-01-02 15:04"))
	}
	return out
}

// TestDateTruncSQLite: every unit on a real SQLite database, read into a
// time.Time; an offset moves the buckets, and a fiscal year moves years and
// quarters.
func TestDateTruncSQLite(t *testing.T) {
	db := reportDB(t)
	at := I.CreatedAt
	for _, tc := range []struct {
		name string
		d    velox.Term[inv]
		want []string
	}{
		{"year", velox.DateTrunc[inv](velox.UnitYear, at), []string{"2025-01-01 00:00", "2026-01-01 00:00", "2026-01-01 00:00", "2026-01-01 00:00"}},
		{"quarter", velox.DateTrunc[inv](velox.UnitQuarter, at), []string{"2025-10-01 00:00", "2026-01-01 00:00", "2026-01-01 00:00", "2026-04-01 00:00"}},
		{"month", velox.DateTrunc[inv](velox.UnitMonth, at), []string{"2025-12-01 00:00", "2026-02-01 00:00", "2026-03-01 00:00", "2026-04-01 00:00"}},
		{"week", velox.DateTrunc[inv](velox.UnitWeek, at), []string{"2025-12-29 00:00", "2026-02-09 00:00", "2026-03-30 00:00", "2026-03-30 00:00"}},
		{"day", velox.DateTrunc[inv](velox.UnitDay, at), []string{"2025-12-31 00:00", "2026-02-15 00:00", "2026-03-31 00:00", "2026-04-01 00:00"}},
		{"hour", velox.DateTrunc[inv](velox.UnitHour, at), []string{"2025-12-31 23:00", "2026-02-15 10:00", "2026-03-31 23:00", "2026-04-01 00:00"}},
		{"month +08:00", velox.DateTrunc[inv](velox.UnitMonth, at, velox.InZone("+08:00")), []string{"2025-12-31 16:00", "2026-01-31 16:00", "2026-03-31 16:00", "2026-03-31 16:00"}},
		{"day -05:30", velox.DateTrunc[inv](velox.UnitDay, at, velox.InZone("-05:30")), []string{"2025-12-31 05:30", "2026-02-15 05:30", "2026-03-31 05:30", "2026-03-31 05:30"}},
		{"fiscal year April", velox.DateTrunc[inv](velox.UnitYear, at, velox.FiscalYear(time.April)), []string{"2025-04-01 00:00", "2025-04-01 00:00", "2025-04-01 00:00", "2026-04-01 00:00"}},
		{"fiscal quarter February", velox.DateTrunc[inv](velox.UnitQuarter, at, velox.FiscalYear(time.February)), []string{"2025-11-01 00:00", "2026-02-01 00:00", "2026-02-01 00:00", "2026-02-01 00:00"}},
		{"by name", velox.DateTrunc[inv](velox.UnitMonth, must(velox.ColumnByName[inv]("created_at"))), []string{"2025-12-01 00:00", "2026-02-01 00:00", "2026-03-01 00:00", "2026-04-01 00:00"}},
	} {
		if got := truncs(t, db, tc.d); !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// must is ColumnByName's term, which the test knows is there.
func must(t velox.Term[inv], ok bool) velox.Term[inv] {
	if !ok {
		panic("no such column")
	}
	return t
}

// TestDateTruncGroupBy: one DateTrunc is a result column, the key of GroupBy
// and the ordering, and its rows are the months' sums, on SQLite.
func TestDateTruncGroupBy(t *testing.T) {
	db := reportDB(t)
	month := velox.DateTrunc[inv](velox.UnitQuarter, I.CreatedAt)
	var got []string
	for r, err := range db.Query[inv]().Select(month, I.Total.Sum()).GroupBy(month).OrderBy(month.Desc()).Limit(10).Rows(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		var at time.Time
		var sum float64
		if err := r.Scan(&at, &sum); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%s %v", at.Format("2006-01"), sum))
	}
	if want := []string{"2026-04 40", "2026-01 50", "2025-10 10"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestDateTruncSQL: the text of a DateTrunc in Select, GroupBy and ORDER BY
// is the same, and holds no argument, on each dialect.
func TestDateTruncSQL(t *testing.T) {
	month := velox.DateTrunc[inv](velox.UnitMonth, I.CreatedAt, velox.InZone("+08:00"))
	for _, tc := range []struct {
		d    velox.Dialect
		want string
	}{
		{velox.Postgres, `SELECT (date_trunc('month', ("created_at" AT TIME ZONE INTERVAL '+08:00')) AT TIME ZONE INTERVAL '+08:00'), COUNT(*) FROM "invoices" ` +
			`GROUP BY (date_trunc('month', ("created_at" AT TIME ZONE INTERVAL '+08:00')) AT TIME ZONE INTERVAL '+08:00') ` +
			`ORDER BY (date_trunc('month', ("created_at" AT TIME ZONE INTERVAL '+08:00')) AT TIME ZONE INTERVAL '+08:00') LIMIT 12`},
		{velox.MySQL, "SELECT CONVERT_TZ(CAST(DATE_FORMAT(CONVERT_TZ(`created_at`, '+00:00', '+08:00'), '%Y-%m-01') AS DATETIME), '+08:00', '+00:00'), COUNT(*) FROM `invoices` " +
			"GROUP BY CONVERT_TZ(CAST(DATE_FORMAT(CONVERT_TZ(`created_at`, '+00:00', '+08:00'), '%Y-%m-01') AS DATETIME), '+08:00', '+00:00') " +
			"ORDER BY ANY_VALUE(CONVERT_TZ(CAST(DATE_FORMAT(CONVERT_TZ(`created_at`, '+00:00', '+08:00'), '%Y-%m-01') AS DATETIME), '+08:00', '+00:00')) IS NULL, " +
			"CONVERT_TZ(CAST(DATE_FORMAT(CONVERT_TZ(`created_at`, '+00:00', '+08:00'), '%Y-%m-01') AS DATETIME), '+08:00', '+00:00') LIMIT 12"},
		{velox.SQLite, `SELECT datetime(datetime("created_at", '+480 minutes', 'start of month'), '-480 minutes'), COUNT(*) FROM "invoices" ` +
			`GROUP BY datetime(datetime("created_at", '+480 minutes', 'start of month'), '-480 minutes') ` +
			`ORDER BY datetime(datetime("created_at", '+480 minutes', 'start of month'), '-480 minutes') IS NULL, datetime(datetime("created_at", '+480 minutes', 'start of month'), '-480 minutes') LIMIT 12`},
	} {
		db := openFake(tc.d, &fakeconn.Exec{})
		text, args, err := db.Query[inv]().Select(month, velox.CountAll[inv]()).GroupBy(month).OrderBy(month.Asc()).Limit(12).SQL(ctx)
		if err != nil || text != tc.want || len(args) != 0 {
			t.Errorf("%v:\n got %s %v %v\nwant %s", tc.d, text, args, err, tc.want)
		}
	}
}

// TestDateTruncFails: a zone's name on SQLite, a column that holds no time,
// and a bad option fail the statement, before anything is sent.
func TestDateTruncFails(t *testing.T) {
	for _, tc := range []struct {
		d    velox.Dialect
		t    velox.Term[inv]
		want string
	}{
		{velox.SQLite, velox.DateTrunc[inv](velox.UnitDay, I.CreatedAt, velox.InZone("Europe/London")), "SQLite has no time zones"},
		{velox.Postgres, velox.DateTrunc[inv](velox.UnitDay, I.Number), "holds a string, not a time"},
		{velox.Postgres, velox.DateTrunc[inv](velox.UnitDay, I.CreatedAt, velox.InZone("x'y")), "not a time zone's name"},
		{velox.Postgres, velox.DateTrunc[inv](velox.UnitDay, I.CreatedAt, velox.FiscalYear(time.April)), "not days"},
		{velox.Postgres, velox.DateTrunc[inv](velox.UnitDay, "created_at"), "not a column handle"},
	} {
		db := openFake(tc.d, &fakeconn.Exec{})
		_, _, err := db.Query[inv]().Select(tc.t).Limit(1).SQL(ctx)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: got %v, want %q", tc.d, err, tc.want)
		}
	}
	// A nullable time column is a time too.
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	if _, _, err := db.Query[inv]().Select(velox.DateTrunc[inv](velox.UnitDay, I.PaidAt)).Limit(1).SQL(ctx); err != nil {
		t.Errorf("paid_at: %v", err)
	}
}

// TestRollupSQL: Rollup is ROLLUP on PostgreSQL, beside other keys too, and
// WITH ROLLUP on MySQL; Cube is CUBE on PostgreSQL.
func TestRollupSQL(t *testing.T) {
	q := func(d velox.Dialect, keys ...velox.Selectable[inv]) (string, error) {
		db := openFake(d, &fakeconn.Exec{})
		text, _, err := db.Query[inv]().Select(I.OrgID, I.Paid, I.Total.Sum()).GroupBy(keys...).Limit(10).SQL(ctx)
		return text, err
	}
	for _, tc := range []struct {
		d    velox.Dialect
		keys []velox.Selectable[inv]
		want string
	}{
		{velox.Postgres, []velox.Selectable[inv]{velox.Rollup[inv](I.OrgID, I.Paid)},
			`SELECT "org_id", "paid", SUM("total") FROM "invoices" GROUP BY ROLLUP("org_id", "paid") LIMIT 10`},
		{velox.Postgres, []velox.Selectable[inv]{I.OrgID, velox.Rollup[inv](I.Paid)},
			`SELECT "org_id", "paid", SUM("total") FROM "invoices" GROUP BY "org_id", ROLLUP("paid") LIMIT 10`},
		{velox.Postgres, []velox.Selectable[inv]{velox.Cube[inv](I.OrgID, I.Paid)},
			`SELECT "org_id", "paid", SUM("total") FROM "invoices" GROUP BY CUBE("org_id", "paid") LIMIT 10`},
		{velox.MySQL, []velox.Selectable[inv]{velox.Rollup[inv](I.OrgID, I.Paid)},
			"SELECT `org_id`, `paid`, SUM(`total`) FROM `invoices` GROUP BY `org_id`, `paid` WITH ROLLUP LIMIT 10"},
	} {
		if got, err := q(tc.d, tc.keys...); err != nil || got != tc.want {
			t.Errorf("%v:\n got %s %v\nwant %s", tc.d, got, err, tc.want)
		}
	}
	for _, tc := range []struct {
		d    velox.Dialect
		keys []velox.Selectable[inv]
		want string
	}{
		{velox.SQLite, []velox.Selectable[inv]{velox.Rollup[inv](I.OrgID)}, "SQLite has no ROLLUP or CUBE"},
		{velox.MySQL, []velox.Selectable[inv]{velox.Cube[inv](I.OrgID)}, "MySQL has no CUBE"},
		{velox.MySQL, []velox.Selectable[inv]{I.OrgID, velox.Rollup[inv](I.Paid)}, "only key"},
		{velox.Postgres, []velox.Selectable[inv]{velox.Rollup[inv]()}, "Rollup of no keys"},
		{velox.Postgres, []velox.Selectable[inv]{velox.Rollup[inv](I.Total.Sum())}, "Rollup of an aggregate"},
	} {
		if _, err := q(tc.d, tc.keys...); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: got %v, want %q", tc.d, err, tc.want)
		}
	}
	// A Rollup is nothing but a key of GroupBy.
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	r := velox.Rollup[inv](I.OrgID)
	if _, _, err := db.Query[inv]().Select(r).Limit(1).SQL(ctx); err == nil || !strings.Contains(err.Error(), "key of GroupBy") {
		t.Errorf("Select: %v", err)
	}
	if _, _, err := db.Query[inv]().Select(velox.Calc[inv]("? + 1", r)).Limit(1).SQL(ctx); err == nil || !strings.Contains(err.Error(), "key of GroupBy") {
		t.Errorf("Calc: %v", err)
	}
	// GROUPING tells a subtotal's NULL from a key's own.
	text, _, err := db.Query[inv]().Select(I.OrgID, velox.Calc[inv]("GROUPING(?)", I.OrgID)).GroupBy(r).Limit(1).SQL(ctx)
	if err != nil || !strings.Contains(text, `(GROUPING("org_id"))`) {
		t.Errorf("GROUPING: %s %v", text, err)
	}
}

// TestColumnByName: a column's name is its term, which selects, groups and
// orders as its handle does: the ordering is the column's, so After follows
// it. An unknown name is false, and its term fails the statement.
func TestColumnByName(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	org, ok := velox.ColumnByName[inv]("org_id")
	if !ok {
		t.Fatal("org_id not found")
	}
	byName, _, err := db.Query[inv]().Select(org, I.Total.Sum()).GroupBy(org).OrderBy(org.Desc()).Limit(5).SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	typed, _, _ := db.Query[inv]().Select(I.OrgID, I.Total.Sum()).GroupBy(I.OrgID).OrderBy(I.OrgID.Desc()).Limit(5).SQL(ctx)
	if byName != typed {
		t.Errorf("by name:\n%s\ntyped:\n%s", byName, typed)
	}
	if org.Desc() != I.OrgID.Desc() || org.Asc() != I.OrgID.Asc() {
		t.Error("the ordering by name is not the handle's")
	}
	text, args, err := db.Query[inv]().Where(velox.Expr[inv]("? > ?", org, 3)).Limit(1).SQL(ctx)
	if err != nil || !strings.Contains(text, `WHERE ("org_id" > $1)`) || len(args) != 1 {
		t.Errorf("in an Expr: %s %v %v", text, args, err)
	}
	id, _ := velox.ColumnByName[inv]("id")
	if _, err := db.Query[inv]().OrderBy(org.Asc(), id.Asc()).After(&inv{OrgID: 2}).Limit(1).All(ctx); err != nil {
		t.Errorf("After an ordering by name: %v", err)
	}

	bad, ok := velox.ColumnByName[inv]("OrgID")
	if ok {
		t.Error("a Go field name is a column name")
	}
	if _, _, err := db.Query[inv]().Select(bad).Limit(1).SQL(ctx); err == nil || !strings.Contains(err.Error(), `no column "OrgID"`) {
		t.Errorf("unknown name: %v", err)
	}
	if _, ok := velox.ColumnByName[struct{}]("id"); ok {
		t.Error("a non-entity has a column")
	}
}

// TestOrderReadable: an ordering says what it orders by and which way, and
// a term says which column it is.
func TestOrderReadable(t *testing.T) {
	if n, ok := I.Total.Desc().Term().Column(); !ok || n != "total" || !I.Total.Desc().Descending() || I.Total.Asc().Descending() {
		t.Errorf("handle: %q %v", n, ok)
	}
	sum := I.Total.Sum()
	o := sum.Asc()
	if _, ok := o.Term().Column(); ok || o.Descending() {
		t.Error("an aggregate's ordering is a column, or descending")
	}
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	a, _, _ := db.Query[inv]().Select(I.OrgID).GroupBy(I.OrgID).OrderBy(o.Term().Desc()).Limit(1).SQL(ctx)
	b, _, _ := db.Query[inv]().Select(I.OrgID).GroupBy(I.OrgID).OrderBy(sum.Desc()).Limit(1).SQL(ctx)
	if a != b {
		t.Errorf("an ordering's term reordered:\n%s\n%s", a, b)
	}
	if n, ok := I.OrgID.Sum().As("x").Column(); ok {
		t.Errorf("an aggregate is column %q", n)
	}
	named, _ := velox.ColumnByName[inv]("note")
	if n, ok := named.As("memo").Column(); !ok || n != "note" {
		t.Errorf("a named column: %q %v", n, ok)
	}
}

// TestRawRowColumns: a row of Rows names its columns: a term named with As
// by its name, a column by its own, on SQLite; and so does a row of DB.Rows.
func TestRawRowColumns(t *testing.T) {
	db := reportDB(t)
	for r, err := range db.Query[inv]().Select(I.OrgID, I.Total.Sum().As("total_sum")).GroupBy(I.OrgID).Limit(1).Rows(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		cols, err := r.Columns()
		if err != nil || !slices.Equal(cols, []string{"org_id", "total_sum"}) {
			t.Errorf("got %q %v", cols, err)
		}
	}
	for r, err := range db.Rows(ctx, "test", "SELECT 1 AS one, 2 AS two") {
		if err != nil {
			t.Fatal(err)
		}
		if cols, err := r.Columns(); err != nil || !slices.Equal(cols, []string{"one", "two"}) {
			t.Errorf("DB.Rows: got %q %v", cols, err)
		}
	}
}

// TestGroupedOrderMySQL: on MySQL an ordering by a grouped expression puts
// its NULL test in ANY_VALUE, as ONLY_FULL_GROUP_BY refuses x IS NULL where
// the key is x; an aggregate's ordering and an ungrouped one are as before.
func TestGroupedOrderMySQL(t *testing.T) {
	db := openFake(velox.MySQL, &fakeconn.Exec{})
	k := velox.Calc[inv]("? * 2", I.Total)
	text, _, err := db.Query[inv]().Select(k, I.Total.Sum()).GroupBy(k).OrderBy(k.Desc(), I.Total.Sum().Asc()).Limit(5).SQL(ctx)
	want := "SELECT (`total` * 2), SUM(`total`) FROM `invoices` GROUP BY (`total` * 2) " +
		"ORDER BY ANY_VALUE((`total` * 2)) IS NULL DESC, (`total` * 2) DESC, SUM(`total`) IS NULL, SUM(`total`) LIMIT 5"
	if err != nil || text != want {
		t.Errorf("grouped:\n got %s %v\nwant %s", text, err, want)
	}
	text, _, err = db.Query[inv]().Select(I.ID).OrderBy(k.Asc()).Limit(5).SQL(ctx)
	want = "SELECT `id` FROM `invoices` ORDER BY (`total` * 2) IS NULL, (`total` * 2) LIMIT 5"
	if err != nil || text != want {
		t.Errorf("ungrouped:\n got %s %v\nwant %s", text, err, want)
	}
}
