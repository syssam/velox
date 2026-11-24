package apitest

import (
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/untyped"
)

// TestOrderValuesSQL: OrderValues selects each expression of the order
// after the columns, aliased velox_order_<n>, and AfterValues compares the
// same expression with the value it read, each copy binding its own values,
// NULL placed as the ordering places it; the key breaks ties.
func TestOrderValuesSQL(t *testing.T) {
	calc := velox.Calc[testmodel.Invoice]("NULLIF(? % ?, ?)", I.OrgID, int64(3), int64(0))
	row := &testmodel.Invoice{ID: 7}
	for _, c := range []struct {
		name string
		d    velox.Dialect
		o    velox.Order[testmodel.Invoice]
		v    any
		want string
		args []any
	}{
		{"pg desc nulls last, from a value", velox.Postgres, calc.Desc().NullsLast(), int64(2),
			`, (NULLIF("org_id" % $1, $2)) AS "velox_order_0" FROM "invoices" WHERE ((((NULLIF("org_id" % $3, $4)) < $5 OR (NULLIF("org_id" % $6, $7)) IS NULL)) OR (((NULLIF("org_id" % $8, $9)) = $10) AND "id" > $11)) ORDER BY (NULLIF("org_id" % $12, $13)) DESC NULLS LAST, "id" LIMIT 10`,
			[]any{int64(3), int64(0), int64(3), int64(0), int64(2), int64(3), int64(0), int64(3), int64(0), int64(2), int64(7), int64(3), int64(0)}},
		{"pg asc default, from NULL", velox.Postgres, calc.Asc(), nil,
			` WHERE ((((NULLIF("org_id" % $3, $4)) IS NULL) AND "id" > $5)) ORDER BY (NULLIF("org_id" % $6, $7)), "id" LIMIT 10`,
			[]any{int64(3), int64(0), int64(3), int64(0), int64(7), int64(3), int64(0)}},
		{"sqlite asc nulls first, from NULL", velox.SQLite, calc.Asc().NullsFirst(), nil,
			` WHERE (((NULLIF("org_id" % ?, ?)) IS NOT NULL) OR (((NULLIF("org_id" % ?, ?)) IS NULL) AND "id" > ?)) ORDER BY (NULLIF("org_id" % ?, ?)) NULLS FIRST, "id" LIMIT 10`,
			nil},
		{"mysql desc default, from a value", velox.MySQL, calc.Desc(), "2",
			" AS `velox_order_0` FROM `invoices` WHERE (((NULLIF(`org_id` % ?, ?)) < ?) OR (((NULLIF(`org_id` % ?, ?)) = ?) AND `id` > ?)) ORDER BY (NULLIF(`org_id` % ?, ?)) IS NULL DESC, (NULLIF(`org_id` % ?, ?)) DESC, `id` LIMIT 10",
			[]any{int64(3), int64(0), int64(3), int64(0), "2", int64(3), int64(0), "2", int64(7), int64(3), int64(0), int64(3), int64(0)}},
	} {
		q := untyped.FromQuery(openFake(c.d, &fakeconn.Exec{}).Query[testmodel.Invoice]().OrderBy(c.o, I.ID.Asc()).Limit(10))
		text, args := sqlOK(t)(q.OrderValues().AfterValues(row, []any{c.v}).SQL(ctx))
		if !strings.Contains(text, c.want) || (c.args != nil && !reflect.DeepEqual(args, c.args)) {
			t.Errorf("%s:\n got %s %v\nwant …%s %v", c.name, text, args, c.want, c.args)
		}
	}
}

// TestOrderValuesDecimalSQL: a decimal the driver read as text is compared
// cast to a decimal, on MySQL DECIMAL(65,30), so that it is not compared as
// a double; a time read as text is cast to DATETIME(6) on MySQL.
func TestOrderValuesDecimalSQL(t *testing.T) {
	calc := velox.Calc[testmodel.Invoice]("? * ?", I.Total, int64(2))
	row := &testmodel.Invoice{ID: 7}
	for _, c := range []struct {
		d    velox.Dialect
		v    any
		want string
		arg  string
	}{
		{velox.MySQL, untyped.DecimalText("2.50"), "(`total` * ?) = CAST(? AS DECIMAL(65,30))", "2.50"},
		{velox.MySQL, untyped.TimeText("2026-03-01 10:00:00"), "(`total` * ?) = CAST(? AS DATETIME(6))", "2026-03-01 10:00:00"},
		{velox.Postgres, untyped.DecimalText("2.50"), `("total" * $3) = CAST($4 AS NUMERIC)`, "2.50"},
	} {
		q := untyped.FromQuery(openFake(c.d, &fakeconn.Exec{}).Query[testmodel.Invoice]().OrderBy(calc.Asc().NullsFirst(), I.ID.Asc()).Limit(10))
		text, args := sqlOK(t)(q.AfterValues(row, []any{c.v}).SQL(ctx))
		if !strings.Contains(text, c.want) || len(args) < 4 || args[3] != c.arg {
			t.Errorf("%v:\n got %s %#v\nwant …%s… with %q", c.d, text, args, c.want, c.arg)
		}
	}
}

// TestOrderValuesCacheKey: one DB renders a read with and without its
// order values as two texts, in either order.
func TestOrderValuesCacheKey(t *testing.T) {
	db := openFake(velox.SQLite, &fakeconn.Exec{})
	calc := velox.Calc[testmodel.Invoice]("? + ?", I.OrgID, int64(1))
	q := untyped.FromQuery(db.Query[testmodel.Invoice]().OrderBy(calc.Asc(), I.ID.Asc()).Limit(10))
	plain, _ := sqlOK(t)(q.SQL(ctx))
	vals, _ := sqlOK(t)(q.OrderValues().SQL(ctx))
	again, _ := sqlOK(t)(q.SQL(ctx))
	if plain != again || strings.Contains(plain, "velox_order_0") || !strings.Contains(vals, `AS "velox_order_0"`) {
		t.Errorf("plain %s\nvalues %s\nagain %s", plain, vals, again)
	}
}

// TestOrderSig: an ordering's signature changes with its text, a bound
// value, its direction and its NULL placement, and not otherwise.
func TestOrderSig(t *testing.T) {
	db := openFake(velox.SQLite, &fakeconn.Exec{})
	tbl := untyped.TableOf[testmodel.Invoice]()
	sig := func(o velox.Order[testmodel.Invoice]) string {
		return tbl.OrderSig(untyped.FromQuery(db.Query[testmodel.Invoice]().OrderBy(o)).Order()[0])
	}
	c := func(v int64) velox.Term[testmodel.Invoice] { return velox.Calc[testmodel.Invoice]("? + ?", I.OrgID, v) }
	base := sig(c(1).Asc())
	if base != sig(c(1).Asc()) {
		t.Error("the same ordering has two signatures")
	}
	for name, o := range map[string]velox.Order[testmodel.Invoice]{
		"value": c(2).Asc(), "direction": c(1).Desc(), "nulls": c(1).Asc().NullsFirst(),
		"text": velox.Calc[testmodel.Invoice]("? - ?", I.OrgID, int64(1)).Asc(),
	} {
		if sig(o) == base {
			t.Errorf("another %s, the same signature", name)
		}
	}
}
