package velox

import (
	"reflect"
	"testing"
	"time"
)

type inv struct{}

var (
	invInfo = &EntityInfo{Name: "velox.inv", Table: "invoices", Tenant: -1, Columns: []ColumnInfo{
		{Name: "id"}, {Name: "org_id"}, {Name: "number"}, {Name: "total"}, {Name: "paid_at"},
	}}
	hID     = NumberCol[inv, int64]{I: 0}
	hOrg    = NumberCol[inv, int64]{I: 1}
	hNumber = StringCol[inv, string]{I: 2}
	hTotal  = NumberCol[inv, float64]{I: 3}
	hPaidAt = NullOrderedCol[inv, time.Time]{I: 4}
)

const (
	pgCols    = `"id", "org_id", "number", "total", "paid_at"`
	mysqlCols = "`id`, `org_id`, `number`, `total`, `paid_at`"
)

func stmt(where ...Pred[inv]) *Statement {
	st := &Statement{Op: OpSelect, Entity: invInfo}
	for _, p := range where {
		st.Where = append(st.Where, Predicate(p))
	}
	return st
}

type renderCase struct {
	name string
	st   func() *Statement
	sql  map[Dialect]string
	args []any
}

func renderCases() []renderCase {
	return []renderCase{
		{"every row", func() *Statement { return stmt() }, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices"`,
			MySQL:    "SELECT " + mysqlCols + " FROM `invoices`",
			SQLite:   `SELECT ` + pgCols + ` FROM "invoices"`,
		}, nil},
		{"where, order, limit", func() *Statement {
			st := stmt(hOrg.EQ(7), hTotal.GT(10))
			st.OrderBy = []OrderTerm{{Column: 4, Desc: true}, {Column: 0}}
			st.Limit = 50
			return st
		}, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE "org_id" = $1 AND "total" > $2 ORDER BY "paid_at" DESC, "id" LIMIT $3`,
			MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE `org_id` = ? AND `total` > ? ORDER BY `paid_at` DESC, `id` LIMIT ?",
			SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE "org_id" = ? AND "total" > ? ORDER BY "paid_at" DESC, "id" LIMIT ?`,
		}, []any{int64(7), float64(10), 50}},
		{"in and not in", func() *Statement { return stmt(hID.In(1, 2, 3), hNumber.NotIn("x")) }, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE "id" IN ($1, $2, $3) AND "number" NOT IN ($4)`,
		}, []any{int64(1), int64(2), int64(3), "x"}},
		{"empty in and not in", func() *Statement { return stmt(hID.In(), hID.NotIn()) }, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE 1 = 0 AND 1 = 1`,
		}, nil},
		{"like escape clause", func() *Statement { return stmt(hNumber.HasPrefix("a")) }, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE "number" LIKE $1 ESCAPE '\'`,
			MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE `number` LIKE ? ESCAPE '\\\\'",
			SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE "number" LIKE ? ESCAPE '\'`,
		}, []any{"a%"}},
		{"and, or, not", func() *Statement {
			return stmt(Or(hID.EQ(1), And(hOrg.EQ(2), Not(hPaidAt.IsNull()))))
		}, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE ("id" = $1 OR ("org_id" = $2 AND NOT ("paid_at" IS NULL)))`,
		}, []any{int64(1), int64(2)}},
		{"empty and, empty or", func() *Statement { return stmt(And[inv](), Or[inv]()) }, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE 1 = 1 AND 1 = 0`,
		}, nil},
		{"between, equal fold, not null", func() *Statement {
			return stmt(hTotal.Between(1, 2), hNumber.EqualFold("Ab"), hPaidAt.NotNull())
		}, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE "total" BETWEEN $1 AND $2 AND LOWER("number") = LOWER($3) AND "paid_at" IS NOT NULL`,
		}, []any{float64(1), float64(2), "Ab"}},
		{"count", func() *Statement { st := stmt(hOrg.EQ(1)); st.Count = true; return st }, map[Dialect]string{
			Postgres: `SELECT COUNT(*) FROM "invoices" WHERE "org_id" = $1`,
		}, []any{int64(1)}},
		{"offset without limit", func() *Statement { st := stmt(); st.Offset = 20; return st }, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" OFFSET $1`,
			MySQL:    "SELECT " + mysqlCols + " FROM `invoices` LIMIT 18446744073709551615 OFFSET ?",
			SQLite:   `SELECT ` + pgCols + ` FROM "invoices" LIMIT -1 OFFSET ?`,
		}, []any{20}},
		{"some columns, limit and offset", func() *Statement {
			st := stmt()
			st.Columns, st.Limit, st.Offset = []int{2, 0}, 1, 5
			return st
		}, map[Dialect]string{
			Postgres: `SELECT "number", "id" FROM "invoices" LIMIT $1 OFFSET $2`,
		}, []any{1, 5}},
	}
}

func argsEqual(a, b []any) bool { return len(a) == len(b) && (len(a) == 0 || reflect.DeepEqual(a, b)) }

func TestRender(t *testing.T) {
	for _, c := range renderCases() {
		for d, want := range c.sql {
			got, args := render(d, c.st())
			if got != want {
				t.Errorf("%s, %v:\n got %s\nwant %s", c.name, d, got, want)
			}
			if !argsEqual(args, c.args) {
				t.Errorf("%s, %v: args %#v, want %#v", c.name, d, args, c.args)
			}
		}
	}
}

func TestRenderQuotesIdentifiers(t *testing.T) {
	info := &EntityInfo{Name: "velox.weird", Table: "user", Tenant: -1, Columns: []ColumnInfo{{Name: "order"}, {Name: "a`b\"c"}}}
	st := &Statement{Op: OpSelect, Entity: info}
	for d, want := range map[Dialect]string{
		Postgres: `SELECT "order", "a` + "`" + `b""c" FROM "user"`,
		MySQL:    "SELECT `order`, `a``b\"c` FROM `user`",
		SQLite:   `SELECT "order", "a` + "`" + `b""c" FROM "user"`,
	} {
		if got, _ := render(d, st); got != want {
			t.Errorf("%v:\n got %s\nwant %s", d, got, want)
		}
	}
}

func TestDialectString(t *testing.T) {
	if Postgres.String() != "postgres" || MySQL.String() != "mysql" || SQLite.String() != "sqlite" {
		t.Error(Postgres, MySQL, SQLite)
	}
}
