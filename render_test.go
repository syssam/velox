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

func ins(cols []int, rows [][]any) *Statement {
	return &Statement{Op: OpInsert, Entity: invInfo, Columns: cols, Rows: rows}
}

func TestRenderSchemaQualifiedTable(t *testing.T) {
	info := &EntityInfo{Name: "velox.inv", Schema: "billing", Table: "invoices", Tenant: -1, Columns: []ColumnInfo{{Name: "id"}}}
	for d, want := range map[Dialect]string{
		Postgres: `SELECT "id" FROM "billing"."invoices"`,
		MySQL:    "SELECT `id` FROM `billing`.`invoices`",
		SQLite:   `SELECT "id" FROM "billing"."invoices"`,
	} {
		if got, _ := render(d, &Statement{Op: OpSelect, Entity: info}); got != want {
			t.Errorf("%v: %s", d, got)
		}
	}
	del, _ := render(Postgres, &Statement{Op: OpDelete, Entity: info})
	if del != `DELETE FROM "billing"."invoices"` {
		t.Errorf("delete: %s", del)
	}
}

// TestRenderMySQLDoNothingQualifiesTheSchema: the no-op assignment's right side
// carries the whole qualified table name.
func TestRenderMySQLDoNothingQualifiesTheSchema(t *testing.T) {
	info := &EntityInfo{Name: "velox.inv", Schema: "billing", Table: "invoices", Tenant: -1,
		Columns: []ColumnInfo{{Name: "id"}, {Name: "org_id"}}}
	st := &Statement{Op: OpInsert, Entity: info, Columns: []int{1}, Rows: [][]any{{int64(7)}},
		Conflict: &ConflictClause{Columns: []int{1}, DoNothing: true}}
	got, _ := render(MySQL, st)
	want := "INSERT INTO `billing`.`invoices` (`org_id`) VALUES (?) AS new ON DUPLICATE KEY UPDATE `org_id` = `billing`.`invoices`.`org_id`"
	if got != want {
		t.Errorf("got %s", got)
	}
}

type renderCase struct {
	name string
	st   func() *Statement
	sql  map[Dialect]string
	args []any
}

func renderCases() []renderCase {
	return []renderCase{
		{"eq nil", func() *Statement {
			return stmt(Pred[inv]{node{op: opEQ, col: 4, v: nil}}, Pred[inv]{node{op: opNEQ, col: 4, v: []byte(nil)}})
		}, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE "paid_at" IS NULL AND "paid_at" IS NOT NULL`,
			MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE `paid_at` IS NULL AND `paid_at` IS NOT NULL",
			SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE "paid_at" IS NULL AND "paid_at" IS NOT NULL`,
		}, nil},
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
		{"sum", func() *Statement {
			st := stmt(hOrg.EQ(1))
			st.Aggregate, st.Columns = AggregateSum, []int{3}
			return st
		}, map[Dialect]string{
			Postgres: `SELECT SUM("total") FROM "invoices" WHERE "org_id" = $1`,
			MySQL:    "SELECT SUM(`total`) FROM `invoices` WHERE `org_id` = ?",
			SQLite:   `SELECT SUM("total") FROM "invoices" WHERE "org_id" = ?`,
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
		{"insert one row", func() *Statement { return ins([]int{1, 2}, [][]any{{int64(7), "A"}}) }, map[Dialect]string{
			Postgres: `INSERT INTO "invoices" ("org_id", "number") VALUES ($1, $2)`,
			MySQL:    "INSERT INTO `invoices` (`org_id`, `number`) VALUES (?, ?)",
			SQLite:   `INSERT INTO "invoices" ("org_id", "number") VALUES (?, ?)`,
		}, []any{int64(7), "A"}},
		{"insert two rows returning", func() *Statement {
			st := ins([]int{1, 2}, [][]any{{int64(7), "A"}, {int64(8), "B"}})
			st.Returning = []int{0}
			return st
		}, map[Dialect]string{
			Postgres: `INSERT INTO "invoices" ("org_id", "number") VALUES ($1, $2), ($3, $4) RETURNING "id"`,
			MySQL:    "INSERT INTO `invoices` (`org_id`, `number`) VALUES (?, ?), (?, ?)",
			SQLite:   `INSERT INTO "invoices" ("org_id", "number") VALUES (?, ?), (?, ?) RETURNING "id"`,
		}, []any{int64(7), "A", int64(8), "B"}},
		{"upsert update", func() *Statement {
			st := ins([]int{1, 2, 3}, [][]any{{int64(7), "A", 1.5}})
			st.Conflict = &ConflictClause{Columns: []int{1, 2}, Update: []int{3}}
			return st
		}, map[Dialect]string{
			Postgres: `INSERT INTO "invoices" ("org_id", "number", "total") VALUES ($1, $2, $3) ON CONFLICT ("org_id", "number") DO UPDATE SET "total" = EXCLUDED."total"`,
			MySQL:    "INSERT INTO `invoices` (`org_id`, `number`, `total`) VALUES (?, ?, ?) AS new ON DUPLICATE KEY UPDATE `total` = new.`total`",
			SQLite:   `INSERT INTO "invoices" ("org_id", "number", "total") VALUES (?, ?, ?) ON CONFLICT ("org_id", "number") DO UPDATE SET "total" = EXCLUDED."total"`,
		}, []any{int64(7), "A", 1.5}},
		{"upsert do nothing", func() *Statement {
			st := ins([]int{1, 2}, [][]any{{int64(7), "A"}})
			st.Conflict = &ConflictClause{Columns: []int{1, 2}, DoNothing: true}
			return st
		}, map[Dialect]string{
			Postgres: `INSERT INTO "invoices" ("org_id", "number") VALUES ($1, $2) ON CONFLICT ("org_id", "number") DO NOTHING`,
			MySQL:    "INSERT INTO `invoices` (`org_id`, `number`) VALUES (?, ?) AS new ON DUPLICATE KEY UPDATE `org_id` = `invoices`.`org_id`",
			SQLite:   `INSERT INTO "invoices" ("org_id", "number") VALUES (?, ?) ON CONFLICT ("org_id", "number") DO NOTHING`,
		}, []any{int64(7), "A"}},
		{"update", func() *Statement {
			st := stmt(hOrg.EQ(7))
			st.Op = OpUpdate
			st.Set = []Assignment{{Column: 3, Op: AssignAdd, Value: 2.0}, {Column: 4, Op: AssignSet}, {Column: 2, Op: AssignSub, Value: "x"}}
			return st
		}, map[Dialect]string{
			Postgres: `UPDATE "invoices" SET "total" = "total" + $1, "paid_at" = $2, "number" = "number" - $3 WHERE "org_id" = $4`,
			MySQL:    "UPDATE `invoices` SET `total` = `total` + ?, `paid_at` = ?, `number` = `number` - ? WHERE `org_id` = ?",
			SQLite:   `UPDATE "invoices" SET "total" = "total" + ?, "paid_at" = ?, "number" = "number" - ? WHERE "org_id" = ?`,
		}, []any{2.0, nil, "x", int64(7)}},
		{"delete", func() *Statement {
			st := stmt(hID.In(1, 2))
			st.Op = OpDelete
			return st
		}, map[Dialect]string{
			Postgres: `DELETE FROM "invoices" WHERE "id" IN ($1, $2)`,
			MySQL:    "DELETE FROM `invoices` WHERE `id` IN (?, ?)",
			SQLite:   `DELETE FROM "invoices" WHERE "id" IN (?, ?)`,
		}, []any{int64(1), int64(2)}},
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

func TestExistsRenders(t *testing.T) {
	for d, want := range map[Dialect]string{
		Postgres: `SELECT 1 FROM "invoices" WHERE "org_id" = $1 LIMIT $2`,
		MySQL:    "SELECT 1 FROM `invoices` WHERE `org_id` = ? LIMIT ?",
		SQLite:   `SELECT 1 FROM "invoices" WHERE "org_id" = ? LIMIT ?`,
	} {
		st := stmt(hOrg.EQ(1))
		st.Exists, st.Limit = true, 1
		got, args := render(d, st)
		if got != want || !argsEqual(args, []any{int64(1), 1}) {
			t.Errorf("%v: %s %v, want %s", d, got, args, want)
		}
	}
}

// The lock clause follows LIMIT and OFFSET, with the same text on PostgreSQL
// and MySQL.
func TestLockRenders(t *testing.T) {
	for d, base := range map[Dialect][2]string{
		Postgres: {`SELECT ` + pgCols + ` FROM "invoices" WHERE "id" = $1`, `$2`},
		MySQL:    {"SELECT " + mysqlCols + " FROM `invoices` WHERE `id` = ?", "?"},
	} {
		where, p2 := base[0], base[1]
		p3 := "$3"
		if d == MySQL {
			p3 = "?"
		}
		for _, c := range []struct {
			lock          Lock
			wait          LockWait
			limit, offset int
			want          string
		}{
			{LockForUpdate, 0, 1, 0, where + " LIMIT " + p2 + " FOR UPDATE"},
			{LockForShare, LockSkipLocked, 1, 0, where + " LIMIT " + p2 + " FOR SHARE SKIP LOCKED"},
			{LockForUpdate, LockNoWait, 1, 0, where + " LIMIT " + p2 + " FOR UPDATE NOWAIT"},
			{LockForUpdate, LockSkipLocked, 1, 5, where + " LIMIT " + p2 + " OFFSET " + p3 + " FOR UPDATE SKIP LOCKED"},
			{LockForShare, 0, 0, 5, map[Dialect]string{Postgres: where + " OFFSET $2 FOR SHARE", MySQL: where + " LIMIT 18446744073709551615 OFFSET ? FOR SHARE"}[d]},
			{0, 0, 1, 0, where + " LIMIT " + p2},
		} {
			st := stmt(hID.EQ(1))
			st.Lock, st.LockWait, st.Limit, st.Offset = c.lock, c.wait, c.limit, c.offset
			if got, _ := render(d, st); got != c.want {
				t.Errorf("%v %d %d:\n got %s\nwant %s", d, c.lock, c.wait, got, c.want)
			}
		}
	}
}
