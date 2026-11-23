package apitest

import (
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// TestDecimalAddSQL: on MySQL, Add and Sub on a decimal column cast their
// argument to the column's type, so that an argument the driver interpolates
// as text ('0.0001') does not make the sum a DOUBLE. PostgreSQL and SQLite
// send what they always sent.
func TestDecimalAddSQL(t *testing.T) {
	L := testmodel.Ledgers
	for _, c := range []struct {
		d    velox.Dialect
		want string
	}{
		{velox.MySQL, "UPDATE `ledgers` SET `amount` = `amount` + CAST(? AS DECIMAL(19,4)), `fee` = `fee` - CAST(? AS DECIMAL(19,4)) WHERE `id` = ?"},
		{velox.Postgres, `UPDATE "ledgers" SET "amount" = "amount" + $1, "fee" = "fee" - $2 WHERE "id" = $3`},
		{velox.SQLite, `UPDATE "ledgers" SET "amount" = "amount" + ?, "fee" = "fee" - ? WHERE "id" = ?`},
	} {
		t.Run(c.d.String(), func(t *testing.T) {
			e := &fakeconn.Exec{Affected: 1}
			db := openFake(c.d, e)
			if _, err := db.Update[testmodel.Ledger]().Where(L.ID.EQ(7)).Set(L.Amount.Add("0.0001"), L.Fee.Sub("0.05")).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			wantLog(t, e, []string{c.want})
			if !reflect.DeepEqual(e.Args, []any{testmodel.Dec("0.0001"), testmodel.Dec("0.05"), int64(7)}) {
				t.Errorf("args %v", e.Args)
			}
		})
	}
}

// TestDecimalTermSQL: on SQLite the SUM and AVG of an expression marked
// Decimal are rounded as a decimal column's are; MIN, an unmarked
// expression and the other dialects are not. Decimal on a term that is not
// an expression fails the statement. The mark is in the render cache's key
// and in Equal.
func TestDecimalTermSQL(t *testing.T) {
	L := testmodel.Ledgers
	type led = testmodel.Ledger
	net := func() velox.Term[led] { return velox.Calc[led]("? * ?", L.Amount, 3) }
	const x = `("amount" * ?)`
	for _, c := range []struct {
		d    velox.Dialect
		term velox.Term[led]
		want string
	}{
		{velox.SQLite, net().Decimal().Sum().As("s"), `SELECT CASE WHEN typeof(SUM(` + x + `)) = 'real' THEN CAST(printf('%!.15g', SUM(` + x + `)) AS REAL) ELSE SUM(` + x + `) END AS "s" FROM "ledgers" LIMIT 1`},
		{velox.SQLite, net().Decimal().As("n").Avg(), `SELECT CASE WHEN typeof(AVG(` + x + `)) = 'real' THEN CAST(printf('%!.15g', AVG(` + x + `)) AS REAL) ELSE AVG(` + x + `) END FROM "ledgers" LIMIT 1`},
		{velox.SQLite, net().Sum(), `SELECT SUM(` + x + `) FROM "ledgers" LIMIT 1`},
		{velox.SQLite, net().Decimal().Min(), `SELECT MIN(` + x + `) FROM "ledgers" LIMIT 1`},
		{velox.Postgres, net().Decimal().Sum(), `SELECT SUM(("amount" * $1)) FROM "ledgers" LIMIT 1`},
	} {
		db := openFake(c.d, &fakeconn.Exec{})
		// The marked and unmarked sums share a DB: the mark is in the key.
		for range 2 {
			for _, term := range []velox.Term[led]{net().Sum(), c.term} {
				text, _, err := db.Query[led]().Select(term).Limit(1).SQL(ctx)
				if term == c.term && (err != nil || text != c.want) {
					t.Errorf("%v:\n got %s %v\nwant %s", c.d, text, err, c.want)
				}
			}
		}
	}
	db := openFake(velox.SQLite, &fakeconn.Exec{})
	for _, bad := range []velox.Term[led]{L.Amount.Sum().Decimal(), velox.CountAll[led]().Decimal()} {
		if _, _, err := db.Query[led]().Select(bad).Limit(1).SQL(ctx); err == nil || !strings.Contains(err.Error(), "Decimal") {
			t.Errorf("Decimal of a non-expression: %v", err)
		}
	}
	// An invalid expression keeps its own error.
	if _, _, err := db.Query[led]().Select(velox.Calc[led]("? * ?", L.Amount).Decimal().Sum()).Limit(1).SQL(ctx); err == nil || strings.Contains(err.Error(), "Decimal") {
		t.Errorf("Decimal of an invalid Calc: %v", err)
	}
	if net().Decimal().Equal(net()) || !net().Decimal().Equal(net().Decimal()) {
		t.Error("Equal ignores Decimal")
	}
}

// TestDecimalSQLiteSQL: on SQLite the SUM and AVG of a decimal column are
// rounded to 15 significant digits when they are a REAL, in Sum, SumBy and a
// term, and a value of the column's Go type bound in an Expr is the number
// SQLite's NUMERIC affinity would store. PostgreSQL and MySQL send what they
// always sent.
func TestDecimalSQLiteSQL(t *testing.T) {
	L := testmodel.Ledgers
	const sum = `CASE WHEN typeof(SUM("amount")) = 'real' THEN CAST(printf('%!.15g', SUM("amount")) AS REAL) ELSE SUM("amount") END`
	for _, c := range []struct {
		d    velox.Dialect
		want []string
		args []any
	}{
		{velox.SQLite, []string{
			`SELECT ` + sum + ` FROM "ledgers"`,
			`SELECT CASE WHEN typeof(AVG("amount")) = 'real' THEN CAST(printf('%!.15g', AVG("amount")) AS REAL) ELSE AVG("amount") END FROM "ledgers"`,
			`SELECT "id", ` + sum + ` FROM "ledgers" GROUP BY "id" ORDER BY "id"`,
			`SELECT ` + sum + `, SUM("id"), MIN("amount") FROM "ledgers" GROUP BY "fee" HAVING (` + sum + ` > ?) AND ("amount" * 2 > ?) AND ("fee" = ?) LIMIT 5`,
		}, []any{int64(10), 1.5, "x"}},
		{velox.Postgres, []string{
			`SELECT SUM("amount") FROM "ledgers"`,
			`SELECT AVG("amount") FROM "ledgers"`,
			`SELECT "id", SUM("amount") FROM "ledgers" GROUP BY "id" ORDER BY "id"`,
			`SELECT SUM("amount"), SUM("id"), MIN("amount") FROM "ledgers" GROUP BY "fee" HAVING (SUM("amount") > $1) AND ("amount" * 2 > $2) AND ("fee" = $3) LIMIT 5`,
		}, []any{testmodel.Dec("10"), testmodel.Dec("1.5"), "x"}},
	} {
		t.Run(c.d.String(), func(t *testing.T) {
			e := &fakeconn.Exec{}
			db := openFake(c.d, e)
			q := db.Query[testmodel.Ledger]()
			if _, err := velox.Sum(ctx, q, L.Amount); err != nil {
				t.Fatal(err)
			}
			if _, _, err := velox.Avg(ctx, q, L.Amount); err != nil {
				t.Fatal(err)
			}
			if _, err := velox.SumBy(ctx, q.AllowUnbounded(), L.ID.Key(), L.Amount); err != nil {
				t.Fatal(err)
			}
			for _, err := range q.Select(L.Amount.Sum(), L.ID.Sum(), L.Amount.Min()).GroupBy(L.Fee).Having(
				velox.Expr[testmodel.Ledger]("? > ?", L.Amount.Sum(), testmodel.Dec("10")),
				velox.Expr[testmodel.Ledger]("? * 2 > ?", L.Amount, testmodel.Dec("1.5")),
				// A string is no decimal column's type: bound as it is.
				velox.Expr[testmodel.Ledger]("? = ?", L.Fee, "x"),
			).Limit(5).Rows(ctx) {
				if err != nil {
					t.Fatal(err)
				}
			}
			wantLog(t, e, c.want)
			if !reflect.DeepEqual(e.Args, c.args) {
				t.Errorf("args %#v, want %#v", e.Args, c.args)
			}
		})
	}
}

// TestSQLiteNumber: the decimal text SQLite would store as an INTEGER is an
// int64, other numbers' text a float64, and anything else is left as it is.
func TestSQLiteNumber(t *testing.T) {
	for _, c := range []struct {
		in   testmodel.Dec
		want any
	}{
		{"10", int64(10)}, {"-3", int64(-3)}, {"1.5", 1.5}, {"-0.25", -0.25}, {"1e3", 1000.0}, {".5", 0.5},
		{"12345678901234567890", 12345678901234567890.0},
		{"abc", testmodel.Dec("abc")}, {"0x10", testmodel.Dec("0x10")}, {"Inf", testmodel.Dec("Inf")},
		{"NaN", testmodel.Dec("NaN")}, {"1e", testmodel.Dec("1e")}, {"1_0", testmodel.Dec("1_0")}, {"", testmodel.Dec("")},
	} {
		e := &fakeconn.Exec{}
		db := openFake(velox.SQLite, e)
		if _, err := db.Query[testmodel.Ledger]().Where(velox.Expr[testmodel.Ledger]("? = ?", testmodel.Ledgers.Amount, c.in)).Count(ctx); err != nil {
			t.Fatal(err)
		}
		if len(e.Args) != 1 || e.Args[0] != c.want {
			t.Errorf("%q: %#v, want %#v", c.in, e.Args, c.want)
		}
	}
}

// A Decimal MySQL's decimal cannot hold is refused with the descriptor.
func TestDecimalDescriptorRefused(t *testing.T) {
	for _, dec := range [][2]uint8{{0, 2}, {66, 2}, {40, 31}, {4, 5}} {
		decCol = dec
		decMeta = new(velox.MetaOnce[decRow])
		err := velox.CheckEntity[decRow]()
		if err == nil || !strings.Contains(err.Error(), "has Decimal") {
			t.Errorf("Decimal %v: %v", dec, err)
		}
	}
}

type decRow struct {
	ID     int64
	Amount testmodel.Dec
}

var (
	decMeta = new(velox.MetaOnce[decRow])
	decCol  [2]uint8
)

func (*decRow) VeloxMeta() *velox.Meta[decRow] {
	return decMeta.Load(func() velox.Descriptor[decRow] {
		return velox.Descriptor[decRow]{
			Table:      "dec_rows",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Amount", Name: "amount", Decimal: decCol}},
			PrimaryKey: []int{0},
			FieldPtr: func(x *decRow, i int) any {
				if i == 0 {
					return &x.ID
				}
				return &x.Amount
			},
		}
	})
}
