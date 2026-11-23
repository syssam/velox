package apitest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

type inv = testmodel.Invoice

// report is a grouped report on invoices: the org, the sum and the count of
// the paid ones, over 100 in all, biggest first.
func report(db *velox.DB) velox.Query[inv] {
	return db.Query[inv]().Where(I.Paid.EQ(true)).
		Select(I.OrgID, I.Total.Sum(), velox.CountAll[inv]()).
		GroupBy(I.OrgID).
		Having(velox.Expr[inv]("? > ?", I.Total.Sum(), 100)).
		OrderBy(I.Total.Sum().Desc()).
		Limit(10)
}

// TestSelectSQL: the select list, GROUP BY, HAVING and ORDER BY of a
// projection, in each dialect, with the arguments in the order they are
// written. An ordering by an expression puts NULL last, ascending, as every
// ordering does: where the database sorts it first, a term of IS NULL goes
// before.
func TestSelectSQL(t *testing.T) {
	for _, tc := range []struct {
		d    velox.Dialect
		sql  string
		args []any
	}{
		{velox.Postgres,
			`SELECT "org_id", SUM("total"), COUNT(*) FROM "invoices" WHERE "paid" GROUP BY "org_id" HAVING (SUM("total") > $1) ORDER BY SUM("total") DESC LIMIT 10`,
			[]any{100}},
		{velox.MySQL,
			"SELECT `org_id`, SUM(`total`), COUNT(*) FROM `invoices` WHERE `paid` = 1 GROUP BY `org_id` HAVING (SUM(`total`) > ?) ORDER BY SUM(`total`) IS NULL DESC, SUM(`total`) DESC LIMIT 10",
			[]any{100}},
		{velox.SQLite,
			`SELECT "org_id", SUM("total"), COUNT(*) FROM "invoices" WHERE "paid" = 1 GROUP BY "org_id" HAVING (SUM("total") > ?) ORDER BY SUM("total") IS NULL DESC, SUM("total") DESC LIMIT 10`,
			[]any{100}},
	} {
		db := openFake(tc.d, &fakeconn.Exec{})
		text, args, err := report(db).SQL(ctx)
		if err != nil || text != tc.sql {
			t.Errorf("%v:\n got %s %v\nwant %s", tc.d, text, err, tc.sql)
		}
		if len(args) != len(tc.args) {
			t.Errorf("%v: args %v, want %v", tc.d, args, tc.args)
			continue
		}
		for i := range args {
			if args[i] != tc.args[i] {
				t.Errorf("%v: arg %d is %v, want %v", tc.d, i, args[i], tc.args[i])
			}
		}
	}
}

// TestSelectHavingByName: a condition by name in Having is resolved against
// the entity, as one in Where is, and renders as the typed one does.
func TestSelectHavingByName(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	q := db.Query[inv]().Select(I.OrgID).GroupBy(I.OrgID).Limit(5)
	byName, _, err := q.Having(velox.Pred[inv](velox.FieldEQ("org_id", int64(1)))).SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	typed, _, err := q.Having(I.OrgID.EQ(1)).SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if byName != typed || !strings.Contains(byName, `HAVING "org_id" = $1`) {
		t.Errorf("by name:\n%s\ntyped:\n%s", byName, typed)
	}
}

// TestSelectTerms: each kind of term renders, aggregates over expressions
// and DISTINCT too, and an Expr takes a term in place of a column.
func TestSelectTerms(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	month := velox.Calc[inv]("EXTRACT(MONTH FROM ?)", I.CreatedAt)
	net := velox.Calc[inv]("? * ?", I.Total, 0.9)
	text, args, err := db.Query[inv]().
		Select(month, net.Sum(), I.OrgID.CountDistinct(), I.Note.Count(), net.Avg(), I.Total.Min(), I.Total.Max()).
		GroupBy(month).
		Limit(5).SQL(ctx)
	want := `SELECT (EXTRACT(MONTH FROM "created_at")), SUM(("total" * $1)), COUNT(DISTINCT "org_id"), COUNT("note"), AVG(("total" * $2)), MIN("total"), MAX("total") ` +
		`FROM "invoices" GROUP BY (EXTRACT(MONTH FROM "created_at")) LIMIT 5`
	if err != nil || text != want {
		t.Errorf("\n got %s %v\nwant %s", text, err, want)
	}
	if len(args) != 2 || args[0] != 0.9 || args[1] != 0.9 {
		t.Errorf("args %v", args)
	}
}

// TestSelectRepeatedTermPlaceholders: on PostgreSQL a GROUP BY or ORDER BY
// term that repeats a selected or grouped term with equal values is written
// with its placeholders, so PostgreSQL sees one expression; a term of the
// same text with another value binds its own. MySQL and SQLite bind the
// value at each place. The render cache keeps the two shapes apart.
func TestSelectRepeatedTermPlaceholders(t *testing.T) {
	month := func(zone string) velox.Term[inv] {
		return velox.Calc[inv]("date_trunc('month', ? AT TIME ZONE ?)", I.CreatedAt, zone)
	}
	q := func(db *velox.DB, group string) velox.Query[inv] {
		return db.Query[inv]().Select(month("UTC").As("m"), velox.CountAll[inv]()).
			GroupBy(month(group)).OrderBy(month("UTC").Asc()).Limit(10)
	}
	for _, tc := range []struct {
		d     velox.Dialect
		group string
		sql   string
		args  []any
	}{
		{velox.Postgres, "UTC",
			`SELECT (date_trunc('month', "created_at" AT TIME ZONE $1)) AS "m", COUNT(*) FROM "invoices" ` +
				`GROUP BY (date_trunc('month', "created_at" AT TIME ZONE $1)) ORDER BY (date_trunc('month', "created_at" AT TIME ZONE $1)) LIMIT 10`,
			[]any{"UTC"}},
		{velox.Postgres, "Asia/Hong_Kong",
			`SELECT (date_trunc('month', "created_at" AT TIME ZONE $1)) AS "m", COUNT(*) FROM "invoices" ` +
				`GROUP BY (date_trunc('month', "created_at" AT TIME ZONE $2)) ORDER BY (date_trunc('month', "created_at" AT TIME ZONE $1)) LIMIT 10`,
			[]any{"UTC", "Asia/Hong_Kong"}},
		{velox.SQLite, "UTC",
			`SELECT (date_trunc('month', "created_at" AT TIME ZONE ?)) AS "m", COUNT(*) FROM "invoices" ` +
				`GROUP BY (date_trunc('month', "created_at" AT TIME ZONE ?)) ORDER BY (date_trunc('month', "created_at" AT TIME ZONE ?)) IS NULL, (date_trunc('month', "created_at" AT TIME ZONE ?)) LIMIT 10`,
			[]any{"UTC", "UTC", "UTC", "UTC"}},
	} {
		db := openFake(tc.d, &fakeconn.Exec{})
		// Twice on one DB: the second is a cache hit.
		for range 2 {
			text, args, err := q(db, tc.group).SQL(ctx)
			if err != nil || text != tc.sql {
				t.Errorf("%v %s:\n got %s %v\nwant %s", tc.d, tc.group, text, err, tc.sql)
			}
			if !reflect.DeepEqual(args, tc.args) {
				t.Errorf("%v %s: args %v, want %v", tc.d, tc.group, args, tc.args)
			}
		}
	}
	// One PostgreSQL DB renders both shapes, each from the cache the second time.
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	for range 2 {
		for _, g := range []string{"UTC", "Asia/Hong_Kong"} {
			_, args, err := q(db, g).SQL(ctx)
			if want := map[string]int{"UTC": 1, "Asia/Hong_Kong": 2}[g]; err != nil || len(args) != want {
				t.Errorf("shared cache, %s: %v %v", g, args, err)
			}
		}
	}
	// Grouped and ordered by, not selected; an aggregate ordering binds anew.
	text, args, err := db.Query[inv]().Select(velox.CountAll[inv]()).GroupBy(month("UTC")).
		OrderBy(month("UTC").Desc(), velox.Calc[inv]("? * ?", I.Total, 2).Sum().Asc()).Limit(5).SQL(ctx)
	want := `SELECT COUNT(*) FROM "invoices" GROUP BY (date_trunc('month', "created_at" AT TIME ZONE $1)) ` +
		`ORDER BY (date_trunc('month', "created_at" AT TIME ZONE $1)) DESC, SUM(("total" * $2)) LIMIT 5`
	if err != nil || text != want || len(args) != 2 {
		t.Errorf("unselected:\n got %s %v %v\nwant %s", text, args, err, want)
	}
	// A Rollup's keys and a DateTrunc of a bound term repeat too, on
	// PostgreSQL; MySQL's WITH ROLLUP and ANY_VALUE ordering bind anew.
	net := velox.Calc[inv]("? * ?", I.Total, 2)
	day := velox.DateTrunc[inv](velox.UnitDay, velox.Calc[inv]("? + ?", I.CreatedAt, 0))
	for _, tc := range []struct {
		d    velox.Dialect
		keys []velox.Selectable[inv]
		want string
		n    int
	}{
		{velox.Postgres, []velox.Selectable[inv]{velox.Rollup[inv](net, I.OrgID)},
			`SELECT ("total" * $1), COUNT(*) FROM "invoices" GROUP BY ROLLUP(("total" * $1), "org_id") ORDER BY ("total" * $1) LIMIT 5`, 1},
	} {
		text, args, err := openFake(tc.d, &fakeconn.Exec{}).Query[inv]().Select(net, velox.CountAll[inv]()).
			GroupBy(tc.keys...).OrderBy(net.Asc()).Limit(5).SQL(ctx)
		if err != nil || text != tc.want || len(args) != tc.n {
			t.Errorf("rollup %v:\n got %s %v %v\nwant %s", tc.d, text, args, err, tc.want)
		}
	}
	text, args, err = db.Query[inv]().Select(day, velox.CountAll[inv]()).GroupBy(day).OrderBy(day.Asc()).Limit(5).SQL(ctx)
	if err != nil || len(args) != 1 || strings.Count(text, "$1") != 3 || strings.Contains(text, "$2") {
		t.Errorf("DateTrunc: %s %v %v", text, args, err)
	}
}

// TestSelectRepeatedTermMySQL: on MySQL a selected or ordered term with a
// bound value that repeats a GROUP BY key is written as ANY_VALUE of it
// (decision 0129). MySQL's ? cannot be repeated, so the copies are other
// expressions to only_full_group_by; ANY_VALUE of a key is the group's
// value. A term that repeats no key, or binds nothing, is written as it is.
func TestSelectRepeatedTermMySQL(t *testing.T) {
	bucket := func(n int) velox.Term[inv] { return velox.Calc[inv]("? * ?", I.OrgID, n) }
	const b = "(`org_id` * ?)"
	for _, tc := range []struct {
		name string
		q    func(velox.Query[inv]) velox.Query[inv]
		sql  string
		n    int
	}{
		{"selected", func(q velox.Query[inv]) velox.Query[inv] {
			return q.Select(bucket(10).As("b"), velox.CountAll[inv]()).GroupBy(bucket(10)).OrderBy(bucket(10).Desc())
		}, "SELECT ANY_VALUE(" + b + ") AS `b`, COUNT(*) FROM `invoices` GROUP BY " + b +
			" ORDER BY ANY_VALUE(" + b + ") IS NULL DESC, ANY_VALUE(" + b + ") DESC LIMIT 10", 4},
		{"unselected", func(q velox.Query[inv]) velox.Query[inv] {
			return q.Select(velox.CountAll[inv]()).GroupBy(bucket(10)).OrderBy(bucket(10).Asc())
		}, "SELECT COUNT(*) FROM `invoices` GROUP BY " + b +
			" ORDER BY ANY_VALUE(" + b + ") IS NULL, ANY_VALUE(" + b + ") LIMIT 10", 3},
		{"second key", func(q velox.Query[inv]) velox.Query[inv] {
			return q.Select(bucket(10), velox.CountAll[inv]()).GroupBy(bucket(100), bucket(10)).OrderBy(bucket(10).Asc())
		}, "SELECT ANY_VALUE(" + b + "), COUNT(*) FROM `invoices` GROUP BY " + b + ", " + b +
			" ORDER BY ANY_VALUE(" + b + ") IS NULL, ANY_VALUE(" + b + ") LIMIT 10", 5},
		{"other value", func(q velox.Query[inv]) velox.Query[inv] {
			return q.Select(bucket(10), velox.CountAll[inv]()).GroupBy(bucket(100)).OrderBy(bucket(10).Asc())
		}, "SELECT " + b + ", COUNT(*) FROM `invoices` GROUP BY " + b +
			" ORDER BY ANY_VALUE(" + b + ") IS NULL, " + b + " LIMIT 10", 4},
		{"no value", func(q velox.Query[inv]) velox.Query[inv] {
			lower := velox.Calc[inv]("LOWER(?)", I.Number)
			return q.Select(lower, I.OrgID.Sum()).GroupBy(lower).OrderBy(lower.Asc())
		}, "SELECT (LOWER(`number`)), SUM(`org_id`) FROM `invoices` GROUP BY (LOWER(`number`))" +
			" ORDER BY ANY_VALUE((LOWER(`number`))) IS NULL, (LOWER(`number`)) LIMIT 10", 0},
		{"ungrouped", func(q velox.Query[inv]) velox.Query[inv] {
			return q.Select(bucket(10)).OrderBy(bucket(10).Asc())
		}, "SELECT " + b + " FROM `invoices` ORDER BY " + b + " IS NULL, " + b + " LIMIT 10", 3},
	} {
		// Each twice on one DB: the second is a cache hit.
		db := openFake(velox.MySQL, &fakeconn.Exec{})
		for range 2 {
			text, args, err := tc.q(db.Query[inv]().Limit(10)).SQL(ctx)
			if err != nil || text != tc.sql || len(args) != tc.n {
				t.Errorf("%s:\n got %s %v %v\nwant %s", tc.name, text, args, err, tc.sql)
			}
		}
	}
	// One DB renders both shapes of one text key, each from the cache the
	// second time.
	db := openFake(velox.MySQL, &fakeconn.Exec{})
	for range 2 {
		for _, n := range []int{10, 100} {
			text, _, err := db.Query[inv]().Select(bucket(10), velox.CountAll[inv]()).GroupBy(bucket(n)).Limit(10).SQL(ctx)
			if wrapped := strings.HasPrefix(text, "SELECT ANY_VALUE("); err != nil || wrapped != (n == 10) {
				t.Errorf("shared cache, %d: %s %v", n, text, err)
			}
		}
	}
	// WITH ROLLUP makes a rolled-up key NULL only where a term is the key
	// as GROUP BY writes it; ANY_VALUE of a copy could show a row's value in
	// a subtotal row, so the statement fails.
	net := velox.Calc[inv]("? * ?", I.Total, 2)
	for _, q := range []velox.Query[inv]{
		db.Query[inv]().Select(net, velox.CountAll[inv]()).GroupBy(velox.Rollup[inv](net, I.OrgID)),
		db.Query[inv]().Select(velox.CountAll[inv]()).GroupBy(velox.Rollup[inv](net)).OrderBy(net.Asc()),
	} {
		if _, _, err := q.Limit(5).SQL(ctx); err == nil || !strings.Contains(err.Error(), "WITH ROLLUP") {
			t.Errorf("rollup: %v", err)
		}
	}
	// A Rollup of keys that bind nothing is as before.
	text, _, err := db.Query[inv]().Select(I.OrgID, velox.CountAll[inv]()).GroupBy(velox.Rollup[inv](I.OrgID)).Limit(5).SQL(ctx)
	if want := "SELECT `org_id`, COUNT(*) FROM `invoices` GROUP BY `org_id` WITH ROLLUP LIMIT 5"; err != nil || text != want {
		t.Errorf("plain rollup:\n got %s %v\nwant %s", text, err, want)
	}
}

// TestHavingRepeatedTerm: a HAVING term that repeats a GROUP BY key,
// outside any aggregate, is written as the key is (decision 0138): on
// PostgreSQL with the key's placeholders, binding nothing; on MySQL as MIN
// of it, whose HAVING names no column outside an aggregate unless it is a
// key column. A term inside an aggregate, or of another value, is written
// as it is. The render cache keeps the shapes apart.
func TestHavingRepeatedTerm(t *testing.T) {
	bucket := func(n int) velox.Term[inv] { return velox.Calc[inv]("? * ?", I.OrgID, n) }
	having := func(q velox.Query[inv], n int) velox.Query[inv] {
		return q.Select(bucket(10), velox.CountAll[inv]()).GroupBy(bucket(10)).
			Having(velox.Expr[inv]("? > ?", bucket(n), 15), velox.Expr[inv]("? > ?", bucket(n).Sum(), 5)).Limit(10)
	}
	for _, tc := range []struct {
		d    velox.Dialect
		n    int
		sql  string
		args []any
	}{
		{velox.Postgres, 10, `SELECT ("org_id" * $1), COUNT(*) FROM "invoices" GROUP BY ("org_id" * $1) ` +
			`HAVING (("org_id" * $1) > $2) AND (SUM(("org_id" * $3)) > $4) LIMIT 10`, []any{10, 15, 10, 5}},
		{velox.Postgres, 100, `SELECT ("org_id" * $1), COUNT(*) FROM "invoices" GROUP BY ("org_id" * $1) ` +
			`HAVING (("org_id" * $2) > $3) AND (SUM(("org_id" * $4)) > $5) LIMIT 10`, []any{10, 100, 15, 100, 5}},
		{velox.MySQL, 10, "SELECT ANY_VALUE((`org_id` * ?)), COUNT(*) FROM `invoices` GROUP BY (`org_id` * ?) " +
			"HAVING (MIN((`org_id` * ?)) > ?) AND (SUM((`org_id` * ?)) > ?) LIMIT 10", []any{10, 10, 10, 15, 10, 5}},
		{velox.MySQL, 100, "SELECT ANY_VALUE((`org_id` * ?)), COUNT(*) FROM `invoices` GROUP BY (`org_id` * ?) " +
			"HAVING ((`org_id` * ?) > ?) AND (SUM((`org_id` * ?)) > ?) LIMIT 10", []any{10, 10, 100, 15, 100, 5}},
		{velox.SQLite, 10, `SELECT ("org_id" * ?), COUNT(*) FROM "invoices" GROUP BY ("org_id" * ?) ` +
			`HAVING (("org_id" * ?) > ?) AND (SUM(("org_id" * ?)) > ?) LIMIT 10`, []any{10, 10, 10, 15, 10, 5}},
	} {
		db := openFake(tc.d, &fakeconn.Exec{})
		// The other value's shape first, on the same DB: the two share the
		// text's cache, and the second run of each is a hit.
		_, _, _ = having(db.Query[inv](), 110-tc.n).SQL(ctx)
		for range 2 {
			text, args, err := having(db.Query[inv](), tc.n).SQL(ctx)
			if err != nil || text != tc.sql || !reflect.DeepEqual(args, tc.args) {
				t.Errorf("%v %d:\n got %s %v %v\nwant %s %v", tc.d, tc.n, text, args, err, tc.sql, tc.args)
			}
		}
	}
	// A key that binds nothing: PostgreSQL matches it by its text; MySQL's
	// HAVING needs MIN of it as well. A key column is written as it is.
	next := velox.Calc[inv]("? + 1", I.OrgID)
	for _, tc := range []struct {
		d   velox.Dialect
		sql string
	}{
		{velox.Postgres, `SELECT COUNT(*) FROM "invoices" GROUP BY ("org_id" + 1), "paid" HAVING (("org_id" + 1) > 2) AND ("paid") LIMIT 5`},
		{velox.MySQL, "SELECT COUNT(*) FROM `invoices` GROUP BY (`org_id` + 1), `paid` HAVING (MIN((`org_id` + 1)) > 2) AND (`paid`) LIMIT 5"},
	} {
		text, _, err := openFake(tc.d, &fakeconn.Exec{}).Query[inv]().Select(velox.CountAll[inv]()).GroupBy(next, I.Paid).
			Having(velox.Expr[inv]("? > 2", next), velox.Expr[inv]("?", I.Paid)).Limit(5).SQL(ctx)
		if err != nil || text != tc.sql {
			t.Errorf("no value %v:\n got %s %v\nwant %s", tc.d, text, err, tc.sql)
		}
	}
	// Under MySQL's WITH ROLLUP MIN of a copy is not a subtotal row's NULL
	// key, so the statement fails; PostgreSQL reuses the placeholders.
	q := func(d velox.Dialect) (string, error) {
		text, _, err := openFake(d, &fakeconn.Exec{}).Query[inv]().Select(velox.CountAll[inv]()).
			GroupBy(velox.Rollup[inv](bucket(10))).Having(velox.Expr[inv]("? > 15", bucket(10))).Limit(5).SQL(ctx)
		return text, err
	}
	if _, err := q(velox.MySQL); err == nil || !strings.Contains(err.Error(), "WITH ROLLUP") {
		t.Errorf("MySQL rollup: %v", err)
	}
	if text, err := q(velox.Postgres); err != nil || strings.Contains(text, "$2") {
		t.Errorf("PostgreSQL rollup: %s %v", text, err)
	}
}

// TestDistinctOrderedByTerm: on MySQL a DISTINCT projection's ordering by
// one of its result terms, not a column, is ANY_VALUE of it, in its IS
// NULL and in itself (decision 0138): MySQL finds neither a copy with its
// own ? nor x IS NULL of an expression in the select list. An ordering by
// a column is as before; so is PostgreSQL, which reuses the placeholders.
// An ordering by a term it does not select is refused before (guard).
func TestDistinctOrderedByTerm(t *testing.T) {
	bucket := func(n int) velox.Term[inv] { return velox.Calc[inv]("? * ?", I.OrgID, n) }
	const b = "(`org_id` * ?)"
	for _, tc := range []struct {
		name string
		d    velox.Dialect
		q    func(velox.Query[inv]) velox.Query[inv]
		sql  string
	}{
		{"bound", velox.MySQL, func(q velox.Query[inv]) velox.Query[inv] {
			return q.Select(bucket(10), I.OrgID).Distinct().OrderBy(bucket(10).Desc(), I.OrgID.Asc())
		}, "SELECT DISTINCT " + b + ", `org_id` FROM `invoices` ORDER BY ANY_VALUE(" + b + ") IS NULL DESC, ANY_VALUE(" + b + ") DESC, `org_id` LIMIT 10"},
		{"no value", velox.MySQL, func(q velox.Query[inv]) velox.Query[inv] {
			lower := velox.Calc[inv]("LOWER(?)", I.Number)
			return q.Select(lower).Distinct().OrderBy(lower.Asc())
		}, "SELECT DISTINCT (LOWER(`number`)) FROM `invoices` ORDER BY ANY_VALUE((LOWER(`number`))) IS NULL, ANY_VALUE((LOWER(`number`))) LIMIT 10"},
		{"not distinct", velox.MySQL, func(q velox.Query[inv]) velox.Query[inv] {
			return q.Select(bucket(10)).OrderBy(bucket(10).Asc())
		}, "SELECT " + b + " FROM `invoices` ORDER BY " + b + " IS NULL, " + b + " LIMIT 10"},
		{"PostgreSQL", velox.Postgres, func(q velox.Query[inv]) velox.Query[inv] {
			return q.Select(bucket(10)).Distinct().OrderBy(bucket(10).Desc())
		}, `SELECT DISTINCT ("org_id" * $1) FROM "invoices" ORDER BY ("org_id" * $1) DESC LIMIT 10`},
	} {
		db := openFake(tc.d, &fakeconn.Exec{})
		for range 2 {
			text, _, err := tc.q(db.Query[inv]().Limit(10)).SQL(ctx)
			if err != nil || text != tc.sql {
				t.Errorf("%s:\n got %s %v\nwant %s", tc.name, text, err, tc.sql)
			}
		}
	}
	// One DB renders both values of one text alike, from the cache the
	// second time, with the values bound.
	db := openFake(velox.MySQL, &fakeconn.Exec{})
	for range 2 {
		for _, n := range []int{10, 100} {
			text, args, err := db.Query[inv]().Select(bucket(n)).Distinct().OrderBy(bucket(n).Asc()).Limit(10).SQL(ctx)
			if err != nil || strings.Count(text, "ANY_VALUE(") != 2 || len(args) != 3 || args[2] != n {
				t.Errorf("shared cache, %d: %s %v %v", n, text, args, err)
			}
		}
	}
}

// TestOrderByExpression: an entity query orders by an expression, and the
// same shape with other values renders alike.
func TestOrderByExpression(t *testing.T) {
	for _, tc := range []struct {
		d   velox.Dialect
		sql string
	}{
		{velox.Postgres, `SELECT "id" FROM "invoices" ORDER BY (LOWER("number")), "id" DESC LIMIT 5`},
		{velox.MySQL, "SELECT `id` FROM `invoices` ORDER BY (LOWER(`number`)) IS NULL, (LOWER(`number`)), `id` DESC LIMIT 5"},
		{velox.SQLite, `SELECT "id" FROM "invoices" ORDER BY (LOWER("number")) IS NULL, (LOWER("number")), "id" DESC LIMIT 5`},
	} {
		db := openFake(tc.d, &fakeconn.Exec{})
		text, _, err := db.Query[inv]().Columns(I.ID).
			OrderBy(velox.Calc[inv]("LOWER(?)", I.Number).Asc(), I.ID.Desc()).Limit(5).SQL(ctx)
		if err != nil || text != tc.sql {
			t.Errorf("%v:\n got %s %v\nwant %s", tc.d, text, err, tc.sql)
		}
	}
	// After reads the ordering's columns from the last row: not an expression.
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	_, err := db.Query[inv]().Columns(I.ID).After(&inv{ID: 1}).
		OrderBy(velox.Calc[inv]("LOWER(?)", I.Number).Asc(), I.ID.Asc()).Limit(5).All(ctx)
	if err == nil || !strings.Contains(err.Error(), "expression") {
		t.Errorf("After with an expression order: %v", err)
	}
}

// TestSelectKeepsPolicy: a projection reads under the Read policy, the
// scope and the interceptors, as every query does, and a refused caller
// sends nothing.
func TestSelectKeepsPolicy(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openPolicied(velox.Postgres, e)
	q := db.Query[testmodel.Ticket]().
		Select(Tk.Owner, velox.CountAll[testmodel.Ticket]()).
		GroupBy(Tk.Owner).Limit(10)
	for row, err := range q.Rows(asUser("ann")) {
		_, _ = row, err
	}
	wantSent(t, e, `SELECT "owner", COUNT(*) FROM "tickets" WHERE "org_id" = $1 AND "owner" = $2 GROUP BY "owner" LIMIT 10`, int64(1), "ann")

	calls := e.Calls
	var got error
	for _, err := range q.Rows(asUser("")) {
		got = err
	}
	if !errors.Is(got, velox.ErrDenied) || e.Calls != calls {
		t.Errorf("no user: %v, %d statements sent", got, e.Calls-calls)
	}
}

// TestSelectRows: the rows come back by position, as the database reports
// them, on a real SQLite database.
func TestSelectRows(t *testing.T) {
	db := connectInvoices(t, ":memory:")
	type row struct {
		org     int64
		n       int64
		longest string
	}
	var got []row
	q := db.Query[inv]().
		Select(I.OrgID, velox.CountAll[inv](), velox.Calc[inv]("MAX(?)", I.Number)).
		GroupBy(I.OrgID).Limit(10)
	for r, err := range q.Rows(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		var x row
		if err := r.Scan(&x.org, &x.n, &x.longest); err != nil {
			t.Fatal(err)
		}
		got = append(got, x)
	}
	if len(got) != 1 || got[0] != (row{1, 3, "c"}) {
		t.Errorf("got %v, want [{1 3 c}]", got)
	}
	// A break ends the read and frees the connection.
	for range q.Rows(ctx) {
		break
	}
	if n, err := db.Query[inv]().Count(ctx); err != nil || n != 3 {
		t.Errorf("after a break: %d, %v", n, err)
	}
}

// TestSelectRefused: a projection does not mix with what reads entities, and
// a malformed one fails before anything is sent.
func TestSelectRefused(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	base := func() velox.Query[inv] {
		return db.Query[inv]().Select(I.OrgID).Limit(5)
	}
	for _, tc := range []struct {
		name string
		run  func() error
		want string
	}{
		{"All", func() error { _, err := base().All(ctx); return err }, "Rows"},
		{"One", func() error { _, err := base().One(ctx); return err }, "Rows"},
		{"First", func() error { _, err := base().First(ctx); return err }, "Rows"},
		{"Iter", func() error {
			for _, err := range base().Iter(ctx) {
				return err
			}
			return nil
		}, "Rows"},
		{"Count", func() error { _, err := base().Count(ctx); return err }, "Rows"},
		{"Exists", func() error { _, err := base().Exists(ctx); return err }, "Rows"},
		{"Sum", func() error { _, err := velox.Sum(ctx, base(), I.Total); return err }, "Rows"},
		{"CountBy", func() error { _, err := velox.CountBy(ctx, base().AllowUnbounded(), I.OrgID.Key()); return err }, "Rows"},
		{"Columns", func() error { return rowsErr(base().Columns(I.ID)) }, "Columns"},
		{"Omit", func() error { return rowsErr(base().Omit(I.Note)) }, "Omit"},
		{"With", func() error { return rowsErr(base().With(I.Items)) }, "With"},
		{"Join", func() error {
			return rowsErr(db.Query[testmodel.Ticket]().Select(Tk.Owner).Join(Tk.Invoice).Limit(5))
		}, "Join"},
		{"After", func() error { return rowsErr(base().After(&inv{ID: 1})) }, "After"},
		{"ForUpdate", func() error { return rowsErr(base().ForUpdate()) }, "lock"},
		{"no terms", func() error { return rowsErr(db.Query[inv]().Select().Limit(5)) }, "no terms"},
		{"GroupBy without Select", func() error { return rowsErr(db.Query[inv]().GroupBy(I.OrgID).Limit(5)) }, "Select"},
		{"Having without Select", func() error {
			return rowsErr(db.Query[inv]().Having(velox.Expr[inv]("? > 1", I.Total)).Limit(5))
		}, "Select"},
		{"a relation in Having", func() error {
			return rowsErr(base().GroupBy(I.OrgID).Having(I.Items.Has()))
		}, "Having"},
		{"a relation by name in Having", func() error {
			return rowsErr(base().GroupBy(I.OrgID).Having(velox.Pred[inv](velox.HasEdge("items"))))
		}, "Having"},
		{"a relation inside Not in Having", func() error {
			return rowsErr(base().GroupBy(I.OrgID).Having(velox.Not(I.Items.Has())))
		}, "Having"},
		{"a column subquery in Having", func() error {
			return rowsErr(base().GroupBy(I.OrgID).Having(velox.Pred[inv](velox.FieldInSelect("id", "Item", "invoice_id"))))
		}, "Having"},
		{"an unknown column by name in Having", func() error {
			return rowsErr(base().GroupBy(I.OrgID).Having(velox.Pred[inv](velox.FieldEQ("nope", 1))))
		}, "nope"},
		{"no Limit", func() error { return rowsErr(db.Query[inv]().Select(I.OrgID)) }, "Limit"},
		{"aggregate of an aggregate", func() error {
			return rowsErr(db.Query[inv]().Select(I.Total.Sum().Max()).Limit(5))
		}, "aggregate"},
		{"a bad expression", func() error {
			return rowsErr(db.Query[inv]().Select(velox.Calc[inv]("(SELECT 1)")).Limit(5))
		}, "SELECT"},
		{"a column of another entity", func() error {
			return rowsErr(db.Query[inv]().Select(velox.Calc[inv]("?", testmodel.Tickets.ID)).Limit(5))
		}, "another entity"},
		{"a term of another entity", func() error {
			return rowsErr(db.Query[inv]().Select(velox.Calc[inv]("?", Tk.ID)).Limit(5))
		}, "another entity"},
		{"an encoded column", func() error {
			return rowsErr(db.Query[fixture.JDoc]().Select(velox.Calc[fixture.JDoc]("?", fixture.JDocs.Attrs)).Limit(5))
		}, "encoded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := e.Calls
			err := tc.run()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error naming %q", err, tc.want)
			}
			if e.Calls != before {
				t.Errorf("%d statements sent", e.Calls-before)
			}
		})
	}
}

// rowsErr runs q's Rows and returns the error it yields.
func rowsErr[E any](q velox.Query[E]) error {
	for _, err := range q.Rows(ctx) {
		return err
	}
	return nil
}

// TestSelectRenderCache: the same shape with other values renders alike, and
// another aggregate or column renders apart.
func TestSelectRenderCache(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	sqlOf := func(term velox.Term[inv], v any) (string, []any) {
		t.Helper()
		text, args, err := db.Query[inv]().Select(term).
			Having(velox.Expr[inv]("? > ?", term, v)).Limit(1).SQL(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return text, args
	}
	a, argsA := sqlOf(I.Total.Sum(), 1)
	b, argsB := sqlOf(I.Total.Sum(), 2)
	if a != b || argsA[0] != 1 || argsB[0] != 2 {
		t.Errorf("same shape: %s %v / %s %v", a, argsA, b, argsB)
	}
	seen := map[string]bool{a: true}
	for _, other := range []velox.Term[inv]{I.Total.Avg(), I.OrgID.Sum(), I.Total.Max(), velox.CountAll[inv]()} {
		o, _ := sqlOf(other, 1)
		if seen[o] {
			t.Errorf("two shapes render alike: %s", o)
		}
		seen[o] = true
	}
}

// TestRewriteHookCannotChangeSelect: the scan reads the terms' columns by
// position, so a Rewrite hook that changes them is refused.
func TestRewriteHookCannotChangeSelect(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		if st.Project != nil {
			st.Project.Terms = st.Project.Terms[:0]
		}
		return nil
	}))
	var got error
	for _, err := range report(db).Rows(ctx) {
		got = err
	}
	if got == nil || !strings.Contains(got.Error(), "Project") || e.Calls != 0 {
		t.Errorf("a hook that empties the terms: %v, %d statements sent", got, e.Calls)
	}
}

// TestSelectHandleAggregates: a column handle is a term as it is, and its
// aggregates are the ones its type allows: Count on every scalar handle, Min
// and Max on an ordered, number or text one, Sum and Avg on a number one.
func TestSelectHandleAggregates(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	text, _, err := db.Query[inv]().Limit(1).Select(
		I.ID, I.Total.Sum(), I.Total.Avg(), I.Total.Min(), I.Number.Max(),
		I.Paid.Count(), I.Number.CountDistinct(), I.PaidAt.Max(), I.PaidAt.Count(),
	).SQL(ctx)
	want := `SELECT "id", SUM("total"), AVG("total"), MIN("total"), MAX("number"), COUNT("paid"), COUNT(DISTINCT "number"), MAX("paid_at"), COUNT("paid_at") FROM "invoices" LIMIT 1`
	if err != nil || text != want {
		t.Errorf("got %s %v\nwant %s", text, err, want)
	}
}

// TestSelectRaw: SelectRaw is Select of one Calc, and keeps the policy
// like it: the text is guarded, and the statement's conditions are kept.
func TestSelectRaw(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	got, _, err := db.Query[inv]().Where(I.Paid.EQ(true)).Limit(3).
		SelectRaw("LOWER(?) || ?", I.Number, "!").SQL(ctx)
	want := `SELECT (LOWER("number") || $1) FROM "invoices" WHERE "paid" LIMIT 3`
	if err != nil || got != want {
		t.Errorf("got %s %v\nwant %s", got, err, want)
	}
	// With Select before it, it adds a term.
	got, _, err = db.Query[inv]().Limit(3).Select(I.ID).SelectRaw("? * 2", I.Total).SQL(ctx)
	want = `SELECT "id", ("total" * 2) FROM "invoices" LIMIT 3`
	if err != nil || got != want {
		t.Errorf("got %s %v\nwant %s", got, err, want)
	}
	// A subquery in the text is refused, as Expr refuses it.
	_, _, err = db.Query[inv]().Limit(3).SelectRaw("(SELECT 1)").SQL(ctx)
	if err == nil || !strings.Contains(err.Error(), "SELECT") {
		t.Errorf("a subquery in SelectRaw: %v", err)
	}
}

// TestSelectRefusesWhatIsNoTerm: Select and GroupBy take a handle or a Term
// of the query's entity; anything else fails the statement before it is
// sent.
func TestSelectRefusesWhatIsNoTerm(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	for name, q := range map[string]velox.Query[inv]{
		"a string":          db.Query[inv]().Limit(1).Select("org_id"),
		"a nil":             db.Query[inv]().Limit(1).Select(nil),
		"a foreign handle":  db.Query[inv]().Limit(1).Select(Tk.Owner),
		"a foreign term":    db.Query[inv]().Limit(1).Select(Tk.ID.Sum()),
		"a foreign GroupBy": db.Query[inv]().Limit(1).Select(I.ID).GroupBy(Tk.Owner),
	} {
		if _, _, err := q.SQL(ctx); err == nil {
			t.Errorf("%s: no error", name)
		}
		if err := rowsErr(q); err == nil || e.Calls != 0 {
			t.Errorf("%s: Rows: %v after %d statements", name, err, e.Calls)
		}
	}
}
