package engine_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/internal/fakeconn"
)

type inv struct{}

var (
	invInfo = &velox.EntityInfo{Name: "velox.inv", Table: "invoices", Columns: []velox.ColumnInfo{
		{Name: "id"}, {Name: "org_id"}, {Name: "number"}, {Name: "total"}, {Name: "paid_at"},
	}}
	hID     = velox.NumberCol[inv, int64]{I: 0}
	hOrg    = velox.NumberCol[inv, int64]{I: 1}
	hNumber = velox.StringCol[inv, string]{I: 2}
	hTotal  = velox.NumberCol[inv, float64]{I: 3}
	hPaidAt = velox.NullOrderedCol[inv, time.Time]{I: 4}
)

const (
	pgCols    = `"id", "org_id", "number", "total", "paid_at"`
	mysqlCols = "`id`, `org_id`, `number`, `total`, `paid_at`"
)

func stmt(where ...velox.Pred[inv]) *velox.Statement {
	st := &velox.Statement{Op: velox.OpSelect, Entity: invInfo}
	for _, p := range where {
		st.Where = append(st.Where, velox.Predicate(p))
	}
	return st
}

// The entities of the per-owner goldens: items keyed to invoices, and tags
// keyed to books through book_tags, a tag having notes.
var (
	itemInfo = &velox.EntityInfo{Name: "velox.item", Table: "items", PrimaryKey: []int{0},
		Columns: []velox.ColumnInfo{{Name: "id"}, {Name: "invoice_id"}, {Name: "sku"}}}
	tagInfo = &velox.EntityInfo{Name: "velox.tag", Table: "tags", PrimaryKey: []int{0},
		Columns: []velox.ColumnInfo{{Name: "id"}, {Name: "org_id"}, {Name: "name"}}}
	bookTagInfo = &velox.EntityInfo{Name: "velox.bookTag", Table: "book_tags", PrimaryKey: []int{0, 1},
		Columns: []velox.ColumnInfo{{Name: "book_id"}, {Name: "tag_id"}}}
	noteInfo = &velox.EntityInfo{Name: "velox.note", Table: "notes", PrimaryKey: []int{0},
		Columns: []velox.ColumnInfo{{Name: "id"}, {Name: "tag_id"}, {Name: "body"}}}
	// linkInfo is a join entity keyed by an id of its own, so that a link
	// between a book and a tag can repeat.
	linkInfo = &velox.EntityInfo{Name: "velox.link", Table: "links", PrimaryKey: []int{0},
		Columns: []velox.ColumnInfo{{Name: "id"}, {Name: "book_id"}, {Name: "tag_id"}}}
)

// eachItems is a per-owner read of the items of invoices 1 and 2, with sku
// <> "x", in sku order, keyed by column col.
func eachItems(col, limit int) *velox.Statement {
	return &velox.Statement{Op: velox.OpSelect, Entity: itemInfo, Where: []velox.Predicate{engine.NewPredicate(engine.Node{Op: engine.OpNEQ, Col: 2, V: "x"})},
		OrderBy: []velox.OrderTerm{{Column: 2, Desc: true}, {Column: 0}},
		Each:    &velox.EachClause{Keys: []any{int64(1), int64(2)}, Column: col, Limit: limit}}
}

// eachTags is a per-owner read of the tags of books 1 and 2 through
// book_tags, in name order, with the condition where.
func eachTags(limit int, where velox.Predicate) *velox.Statement {
	return &velox.Statement{Op: velox.OpSelect, Entity: tagInfo, Where: []velox.Predicate{where},
		OrderBy: []velox.OrderTerm{{Column: 2}, {Column: 0}},
		Each:    &velox.EachClause{Keys: []any{int64(1), int64(2)}, Join: bookTagInfo, JoinOwner: 0, JoinTarget: 1, Target: 0, Limit: limit}}
}

// countItems is CountEach's statement for the items of invoices 1 and 2,
// with sku <> "x".
func countItems() *velox.Statement {
	return &velox.Statement{Op: velox.OpSelect, Entity: itemInfo, Count: true, Where: []velox.Predicate{engine.NewPredicate(engine.Node{Op: engine.OpNEQ, Col: 2, V: "x"})},
		Each: &velox.EachClause{Keys: []any{int64(1), int64(2)}, Column: 1}}
}

// countTags is CountEach's statement for the tags of books 1 and 2 through
// join, whose owner and target columns are owner and target, with org_id 7.
func countTags(join *velox.EntityInfo, owner, target int) *velox.Statement {
	return &velox.Statement{Op: velox.OpSelect, Entity: tagInfo, Count: true, Where: []velox.Predicate{engine.NewPredicate(engine.Node{Op: engine.OpEQ, Col: 1, V: int64(7)})},
		Each: &velox.EachClause{Keys: []any{int64(1), int64(2)}, Join: join, JoinOwner: owner, JoinTarget: target, Target: 0}}
}

// inSchema returns a copy of in in schema s.
func inSchema(in *velox.EntityInfo, s string) *velox.EntityInfo {
	c := *in
	c.Schema = s
	return &c
}

func ins(cols []int, rows [][]any) *velox.Statement {
	return &velox.Statement{Op: velox.OpInsert, Entity: invInfo, Columns: cols, Rows: rows}
}

func TestRenderSchemaQualifiedTable(t *testing.T) {
	info := &velox.EntityInfo{Name: "velox.inv", Schema: "billing", Table: "invoices", Columns: []velox.ColumnInfo{{Name: "id"}}}
	for d, want := range map[velox.Dialect]string{
		velox.Postgres: `SELECT "id" FROM "billing"."invoices"`,
		velox.MySQL:    "SELECT `id` FROM `billing`.`invoices`",
		velox.SQLite:   `SELECT "id" FROM "billing"."invoices"`,
	} {
		if got, _ := render(d, &velox.Statement{Op: velox.OpSelect, Entity: info}); got != want {
			t.Errorf("%v: %s", d, got)
		}
	}
	del, _ := render(velox.Postgres, &velox.Statement{Op: velox.OpDelete, Entity: info})
	if del != `DELETE FROM "billing"."invoices"` {
		t.Errorf("delete: %s", del)
	}
}

// TestRenderSchemaQualifiedWrites: INSERT, UPDATE and the upsert qualify the
// table with its schema, and the upsert's update-branch condition qualifies
// it again.
func TestRenderSchemaQualifiedWrites(t *testing.T) {
	info := &velox.EntityInfo{Name: "velox.inv", Schema: "billing", Table: "invoices", Columns: []velox.ColumnInfo{{Name: "id"}, {Name: "org_id"}, {Name: "total"}}}
	where := []velox.Predicate{engine.NewPredicate(engine.Node{Op: engine.OpEQ, Col: 1, V: int64(7)})}
	insert := func() *velox.Statement {
		return &velox.Statement{Op: velox.OpInsert, Entity: info, Columns: []int{1, 2}, Rows: [][]any{{int64(7), 1.5}}, Returning: []int{0}}
	}
	update := &velox.Statement{Op: velox.OpUpdate, Entity: info, Set: []velox.Assignment{{Column: 2, Op: velox.AssignAdd, Value: 1.5}}, Where: where}
	upsert := insert()
	upsert.Conflict = &velox.ConflictClause{Columns: []int{1}, Update: []int{2}, Where: where}
	for _, c := range []struct {
		d    velox.Dialect
		name string
		st   *velox.Statement
		want string
	}{
		{velox.Postgres, "insert", insert(), `INSERT INTO "billing"."invoices" ("org_id", "total") VALUES ($1, $2) RETURNING "id"`},
		{velox.Postgres, "update", update, `UPDATE "billing"."invoices" SET "total" = "total" + $1 WHERE "org_id" = $2`},
		{velox.Postgres, "upsert", upsert, `INSERT INTO "billing"."invoices" ("org_id", "total") VALUES ($1, $2) ON CONFLICT ("org_id") DO UPDATE SET "total" = EXCLUDED."total" WHERE "billing"."invoices"."org_id" = $3 RETURNING "id"`},
		{velox.MySQL, "insert", insert(), "INSERT INTO `billing`.`invoices` (`org_id`, `total`) VALUES (?, ?)"},
		{velox.MySQL, "update", update, "UPDATE `billing`.`invoices` SET `total` = `total` + ? WHERE `org_id` = ?"},
		{velox.SQLite, "insert", insert(), `INSERT INTO "billing"."invoices" ("org_id", "total") VALUES (?, ?) RETURNING "id"`},
		{velox.SQLite, "update", update, `UPDATE "billing"."invoices" SET "total" = "total" + ? WHERE "org_id" = ?`},
		{velox.SQLite, "upsert", upsert, `INSERT INTO "billing"."invoices" ("org_id", "total") VALUES (?, ?) ON CONFLICT ("org_id") DO UPDATE SET "total" = EXCLUDED."total" WHERE "billing"."invoices"."org_id" = ? RETURNING "id"`},
	} {
		if got, _ := render(c.d, c.st); got != c.want {
			t.Errorf("%v %s:\n got %s\nwant %s", c.d, c.name, got, c.want)
		}
	}
}

// TestRenderMySQLDoNothingQualifiesTheSchema: the no-op assignment's right side
// carries the whole qualified table name.
func TestRenderMySQLDoNothingQualifiesTheSchema(t *testing.T) {
	info := &velox.EntityInfo{Name: "velox.inv", Schema: "billing", Table: "invoices",
		Columns: []velox.ColumnInfo{{Name: "id"}, {Name: "org_id"}}}
	st := &velox.Statement{Op: velox.OpInsert, Entity: info, Columns: []int{1}, Rows: [][]any{{int64(7)}},
		Conflict: &velox.ConflictClause{Columns: []int{1}, DoNothing: true}}
	got, _ := render(velox.MySQL, st)
	want := "INSERT INTO `billing`.`invoices` (`org_id`) VALUES (?) AS new ON DUPLICATE KEY UPDATE `org_id` = `billing`.`invoices`.`org_id`"
	if got != want {
		t.Errorf("got %s", got)
	}
}

type renderCase struct {
	name string
	st   func() *velox.Statement
	sql  map[velox.Dialect]string
	args []any
}

func renderCases() []renderCase {
	return []renderCase{
		{"eq nil", func() *velox.Statement {
			return stmt(velox.Pred[inv](engine.NewPredicate(engine.Node{Op: engine.OpEQ, Col: 4, V: nil})), velox.Pred[inv](engine.NewPredicate(engine.Node{Op: engine.OpNEQ, Col: 4, V: []byte(nil)})))
		}, map[velox.Dialect]string{
			velox.Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE "paid_at" IS NULL AND "paid_at" IS NOT NULL`,
			velox.MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE `paid_at` IS NULL AND `paid_at` IS NOT NULL",
			velox.SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE "paid_at" IS NULL AND "paid_at" IS NOT NULL`,
		}, nil},
		{"every row", func() *velox.Statement { return stmt() }, map[velox.Dialect]string{
			velox.Postgres: `SELECT ` + pgCols + ` FROM "invoices"`,
			velox.MySQL:    "SELECT " + mysqlCols + " FROM `invoices`",
			velox.SQLite:   `SELECT ` + pgCols + ` FROM "invoices"`,
		}, nil},
		{"where, order, limit", func() *velox.Statement {
			st := stmt(hOrg.EQ(7), hTotal.GT(10))
			st.OrderBy = []velox.OrderTerm{{Column: 4, Desc: true}, {Column: 0}}
			st.Limit = 50
			return st
		}, map[velox.Dialect]string{
			velox.Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE "org_id" = $1 AND "total" > $2 ORDER BY "paid_at" DESC, "id" LIMIT $3`,
			velox.MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE `org_id` = ? AND `total` > ? ORDER BY `paid_at` DESC, `id` LIMIT ?",
			velox.SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE "org_id" = ? AND "total" > ? ORDER BY "paid_at" DESC, "id" LIMIT 50`,
		}, []any{int64(7), float64(10), 50}},
		{"in and not in", func() *velox.Statement { return stmt(hID.In(1, 2, 3), hNumber.NotIn("x")) }, map[velox.Dialect]string{
			velox.Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE "id" IN ($1, $2, $3) AND "number" NOT IN ($4)`,
			velox.MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE `id` IN (?, ?, ?) AND `number` NOT IN (?)",
			velox.SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE "id" IN (?, ?, ?) AND "number" NOT IN (?)`,
		}, []any{int64(1), int64(2), int64(3), "x"}},
		{"empty in and not in", func() *velox.Statement { return stmt(hID.In(), hID.NotIn()) }, map[velox.Dialect]string{
			velox.Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE 1 = 0 AND 1 = 1`,
			velox.MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE 1 = 0 AND 1 = 1",
			velox.SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE 1 = 0 AND 1 = 1`,
		}, nil},
		{"like escape clause", func() *velox.Statement { return stmt(hNumber.HasPrefix("a")) }, map[velox.Dialect]string{
			velox.Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE "number" LIKE $1 ESCAPE '!'`,
			velox.MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE `number` LIKE ? ESCAPE '!'",
			velox.SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE "number" LIKE ? ESCAPE '!'`,
		}, []any{"a%"}},
		{"and, or, not", func() *velox.Statement {
			return stmt(velox.Or(hID.EQ(1), velox.And(hOrg.EQ(2), velox.Not(hPaidAt.IsNull()))))
		}, map[velox.Dialect]string{
			velox.Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE ("id" = $1 OR ("org_id" = $2 AND NOT ("paid_at" IS NULL)))`,
			velox.MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE (`id` = ? OR (`org_id` = ? AND NOT (`paid_at` IS NULL)))",
			velox.SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE ("id" = ? OR ("org_id" = ? AND NOT ("paid_at" IS NULL)))`,
		}, []any{int64(1), int64(2)}},
		{"or beside a sibling", func() *velox.Statement {
			return stmt(velox.Or(hID.EQ(1), hID.EQ(2)), hOrg.EQ(3))
		}, map[velox.Dialect]string{
			velox.Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE ("id" = $1 OR "id" = $2) AND "org_id" = $3`,
			velox.MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE (`id` = ? OR `id` = ?) AND `org_id` = ?",
			velox.SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE ("id" = ? OR "id" = ?) AND "org_id" = ?`,
		}, []any{int64(1), int64(2), int64(3)}},
		{"neq, gte, lt, lte", func() *velox.Statement {
			return stmt(hOrg.NEQ(1), hTotal.GTE(2), hTotal.LT(3), hID.LTE(4))
		}, map[velox.Dialect]string{
			velox.Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE "org_id" <> $1 AND "total" >= $2 AND "total" < $3 AND "id" <= $4`,
			velox.MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE `org_id` <> ? AND `total` >= ? AND `total` < ? AND `id` <= ?",
			velox.SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE "org_id" <> ? AND "total" >= ? AND "total" < ? AND "id" <= ?`,
		}, []any{int64(1), float64(2), float64(3), int64(4)}},
		{"empty and, empty or", func() *velox.Statement { return stmt(velox.And[inv](), velox.Or[inv]()) }, map[velox.Dialect]string{
			velox.Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE 1 = 1 AND 1 = 0`,
			velox.MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE 1 = 1 AND 1 = 0",
			velox.SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE 1 = 1 AND 1 = 0`,
		}, nil},
		{"between, equal fold, not null", func() *velox.Statement {
			return stmt(hTotal.Between(1, 2), hNumber.EqualFold("Ab"), hPaidAt.NotNull())
		}, map[velox.Dialect]string{
			velox.Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE "total" BETWEEN $1 AND $2 AND LOWER("number") = LOWER($3) AND "paid_at" IS NOT NULL`,
			velox.MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE `total` BETWEEN ? AND ? AND LOWER(`number`) = LOWER(?) AND `paid_at` IS NOT NULL",
			velox.SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE "total" BETWEEN ? AND ? AND LOWER("number") = LOWER(?) AND "paid_at" IS NOT NULL`,
		}, []any{float64(1), float64(2), "Ab"}},
		{"count", func() *velox.Statement { st := stmt(hOrg.EQ(1)); st.Count = true; return st }, map[velox.Dialect]string{
			velox.Postgres: `SELECT COUNT(*) FROM "invoices" WHERE "org_id" = $1`,
			velox.MySQL:    "SELECT COUNT(*) FROM `invoices` WHERE `org_id` = ?",
			velox.SQLite:   `SELECT COUNT(*) FROM "invoices" WHERE "org_id" = ?`,
		}, []any{int64(1)}},
		{"sum", func() *velox.Statement {
			st := stmt(hOrg.EQ(1))
			st.Aggregate, st.Columns = velox.AggregateSum, []int{3}
			return st
		}, map[velox.Dialect]string{
			velox.Postgres: `SELECT SUM("total") FROM "invoices" WHERE "org_id" = $1`,
			velox.MySQL:    "SELECT SUM(`total`) FROM `invoices` WHERE `org_id` = ?",
			velox.SQLite:   `SELECT SUM("total") FROM "invoices" WHERE "org_id" = ?`,
		}, []any{int64(1)}},
		{"count by", func() *velox.Statement {
			st := stmt(hOrg.EQ(1))
			st.Count, st.GroupBy, st.OrderBy = true, 3, []velox.OrderTerm{{Column: 2}}
			return st
		}, map[velox.Dialect]string{
			velox.Postgres: `SELECT "number", COUNT(*) FROM "invoices" WHERE "org_id" = $1 GROUP BY "number" ORDER BY "number"`,
			velox.MySQL:    "SELECT `number`, COUNT(*) FROM `invoices` WHERE `org_id` = ? GROUP BY `number` ORDER BY `number`",
			velox.SQLite:   `SELECT "number", COUNT(*) FROM "invoices" WHERE "org_id" = ? GROUP BY "number" ORDER BY "number"`,
		}, []any{int64(1)}},
		{"sum by, the first column", func() *velox.Statement {
			st := stmt(hOrg.EQ(1))
			st.Aggregate, st.Columns, st.GroupBy, st.OrderBy = velox.AggregateSum, []int{3}, 1, []velox.OrderTerm{{Column: 0}}
			return st
		}, map[velox.Dialect]string{
			velox.Postgres: `SELECT "id", SUM("total") FROM "invoices" WHERE "org_id" = $1 GROUP BY "id" ORDER BY "id"`,
			velox.MySQL:    "SELECT `id`, SUM(`total`) FROM `invoices` WHERE `org_id` = ? GROUP BY `id` ORDER BY `id`",
			velox.SQLite:   `SELECT "id", SUM("total") FROM "invoices" WHERE "org_id" = ? GROUP BY "id" ORDER BY "id"`,
		}, []any{int64(1)}},
		{"offset without limit", func() *velox.Statement { st := stmt(); st.Offset = 20; return st }, map[velox.Dialect]string{
			velox.Postgres: `SELECT ` + pgCols + ` FROM "invoices" OFFSET $1`,
			velox.MySQL:    "SELECT " + mysqlCols + " FROM `invoices` LIMIT 18446744073709551615 OFFSET ?",
			velox.SQLite:   `SELECT ` + pgCols + ` FROM "invoices" LIMIT -1 OFFSET ?`,
		}, []any{20}},
		{"some columns, limit and offset", func() *velox.Statement {
			st := stmt()
			st.Columns, st.Limit, st.Offset = []int{2, 0}, 1, 5
			return st
		}, map[velox.Dialect]string{
			velox.Postgres: `SELECT "number", "id" FROM "invoices" LIMIT $1 OFFSET $2`,
			velox.MySQL:    "SELECT `number`, `id` FROM `invoices` LIMIT ? OFFSET ?",
			velox.SQLite:   `SELECT "number", "id" FROM "invoices" LIMIT 1 OFFSET ?`,
		}, []any{1, 5}},
		{"insert one row", func() *velox.Statement { return ins([]int{1, 2}, [][]any{{int64(7), "A"}}) }, map[velox.Dialect]string{
			velox.Postgres: `INSERT INTO "invoices" ("org_id", "number") VALUES ($1, $2)`,
			velox.MySQL:    "INSERT INTO `invoices` (`org_id`, `number`) VALUES (?, ?)",
			velox.SQLite:   `INSERT INTO "invoices" ("org_id", "number") VALUES (?, ?)`,
		}, []any{int64(7), "A"}},
		{"insert two rows returning", func() *velox.Statement {
			st := ins([]int{1, 2}, [][]any{{int64(7), "A"}, {int64(8), "B"}})
			st.Returning = []int{0}
			return st
		}, map[velox.Dialect]string{
			velox.Postgres: `INSERT INTO "invoices" ("org_id", "number") VALUES ($1, $2), ($3, $4) RETURNING "id"`,
			velox.MySQL:    "INSERT INTO `invoices` (`org_id`, `number`) VALUES (?, ?), (?, ?)",
			velox.SQLite:   `INSERT INTO "invoices" ("org_id", "number") VALUES (?, ?), (?, ?) RETURNING "id"`,
		}, []any{int64(7), "A", int64(8), "B"}},
		{"upsert update", func() *velox.Statement {
			st := ins([]int{1, 2, 3}, [][]any{{int64(7), "A", 1.5}})
			st.Conflict = &velox.ConflictClause{Columns: []int{1, 2}, Update: []int{3}}
			return st
		}, map[velox.Dialect]string{
			velox.Postgres: `INSERT INTO "invoices" ("org_id", "number", "total") VALUES ($1, $2, $3) ON CONFLICT ("org_id", "number") DO UPDATE SET "total" = EXCLUDED."total"`,
			velox.MySQL:    "INSERT INTO `invoices` (`org_id`, `number`, `total`) VALUES (?, ?, ?) AS new ON DUPLICATE KEY UPDATE `total` = new.`total`",
			velox.SQLite:   `INSERT INTO "invoices" ("org_id", "number", "total") VALUES (?, ?, ?) ON CONFLICT ("org_id", "number") DO UPDATE SET "total" = EXCLUDED."total"`,
		}, []any{int64(7), "A", 1.5}},
		{"upsert do nothing", func() *velox.Statement {
			st := ins([]int{1, 2}, [][]any{{int64(7), "A"}})
			st.Conflict = &velox.ConflictClause{Columns: []int{1, 2}, DoNothing: true}
			return st
		}, map[velox.Dialect]string{
			velox.Postgres: `INSERT INTO "invoices" ("org_id", "number") VALUES ($1, $2) ON CONFLICT ("org_id", "number") DO NOTHING`,
			velox.MySQL:    "INSERT INTO `invoices` (`org_id`, `number`) VALUES (?, ?) AS new ON DUPLICATE KEY UPDATE `org_id` = `invoices`.`org_id`",
			velox.SQLite:   `INSERT INTO "invoices" ("org_id", "number") VALUES (?, ?) ON CONFLICT ("org_id", "number") DO NOTHING`,
		}, []any{int64(7), "A"}},
		{"update", func() *velox.Statement {
			st := stmt(hOrg.EQ(7))
			st.Op = velox.OpUpdate
			st.Set = []velox.Assignment{{Column: 3, Op: velox.AssignAdd, Value: 2.0}, {Column: 4, Op: velox.AssignSet}, {Column: 2, Op: velox.AssignSub, Value: "x"}}
			return st
		}, map[velox.Dialect]string{
			velox.Postgres: `UPDATE "invoices" SET "total" = "total" + $1, "paid_at" = $2, "number" = "number" - $3 WHERE "org_id" = $4`,
			velox.MySQL:    "UPDATE `invoices` SET `total` = `total` + ?, `paid_at` = ?, `number` = `number` - ? WHERE `org_id` = ?",
			velox.SQLite:   `UPDATE "invoices" SET "total" = "total" + ?, "paid_at" = ?, "number" = "number" - ? WHERE "org_id" = ?`,
		}, []any{2.0, nil, "x", int64(7)}},
		{"each: window", func() *velox.Statement { return eachItems(1, 2) }, map[velox.Dialect]string{
			velox.Postgres: `SELECT "id", "invoice_id", "sku" FROM (SELECT "id", "invoice_id", "sku", ROW_NUMBER() OVER (PARTITION BY "invoice_id" ORDER BY "sku" DESC, "id") AS "vx_rn" FROM "items" WHERE "invoice_id" IN ($1, $2) AND "sku" <> $3) AS "vx" WHERE "vx_rn" <= $4 ORDER BY "vx_rn"`,
			velox.MySQL:    "SELECT `id`, `invoice_id`, `sku` FROM (SELECT `id`, `invoice_id`, `sku`, ROW_NUMBER() OVER (PARTITION BY `invoice_id` ORDER BY `sku` DESC, `id`) AS `vx_rn` FROM `items` WHERE `invoice_id` IN (?, ?) AND `sku` <> ?) AS `vx` WHERE `vx_rn` <= ? ORDER BY `vx_rn`",
			velox.SQLite:   `SELECT "id", "invoice_id", "sku" FROM (SELECT "id", "invoice_id", "sku", ROW_NUMBER() OVER (PARTITION BY "invoice_id" ORDER BY "sku" DESC, "id") AS "vx_rn" FROM "items" WHERE "invoice_id" IN (?, ?) AND "sku" <> ?) AS "vx" WHERE "vx_rn" <= ? ORDER BY "vx_rn"`,
		}, []any{int64(1), int64(2), "x", 2}},
		{"each: keyed by the target key", func() *velox.Statement { return eachItems(0, 1) }, map[velox.Dialect]string{
			velox.Postgres: `SELECT "id", "invoice_id", "sku" FROM "items" WHERE "id" IN ($1, $2) AND "sku" <> $3 ORDER BY "sku" DESC, "id"`,
			velox.MySQL:    "SELECT `id`, `invoice_id`, `sku` FROM `items` WHERE `id` IN (?, ?) AND `sku` <> ? ORDER BY `sku` DESC, `id`",
			velox.SQLite:   `SELECT "id", "invoice_id", "sku" FROM "items" WHERE "id" IN (?, ?) AND "sku" <> ? ORDER BY "sku" DESC, "id"`,
		}, []any{int64(1), int64(2), "x"}},
		{"each: through", func() *velox.Statement {
			return eachTags(1, engine.NewPredicate(engine.Node{Op: engine.OpEQ, Col: 1, V: int64(7)}))
		}, map[velox.Dialect]string{
			velox.Postgres: `SELECT "id", "org_id", "name", "vx_owner" FROM (SELECT "tags"."id", "tags"."org_id", "tags"."name", "vx_j"."book_id" AS "vx_owner", ROW_NUMBER() OVER (PARTITION BY "vx_j"."book_id" ORDER BY "tags"."name", "tags"."id") AS "vx_rn" FROM "tags" JOIN "book_tags" AS "vx_j" ON "vx_j"."tag_id" = "tags"."id" WHERE "vx_j"."book_id" IN ($1, $2) AND "tags"."org_id" = $3) AS "vx" WHERE "vx_rn" <= $4 ORDER BY "vx_rn"`,
			velox.MySQL:    "SELECT `id`, `org_id`, `name`, `vx_owner` FROM (SELECT `tags`.`id`, `tags`.`org_id`, `tags`.`name`, `vx_j`.`book_id` AS `vx_owner`, ROW_NUMBER() OVER (PARTITION BY `vx_j`.`book_id` ORDER BY `tags`.`name`, `tags`.`id`) AS `vx_rn` FROM `tags` JOIN `book_tags` AS `vx_j` ON `vx_j`.`tag_id` = `tags`.`id` WHERE `vx_j`.`book_id` IN (?, ?) AND `tags`.`org_id` = ?) AS `vx` WHERE `vx_rn` <= ? ORDER BY `vx_rn`",
			velox.SQLite:   `SELECT "id", "org_id", "name", "vx_owner" FROM (SELECT "tags"."id", "tags"."org_id", "tags"."name", "vx_j"."book_id" AS "vx_owner", ROW_NUMBER() OVER (PARTITION BY "vx_j"."book_id" ORDER BY "tags"."name", "tags"."id") AS "vx_rn" FROM "tags" JOIN "book_tags" AS "vx_j" ON "vx_j"."tag_id" = "tags"."id" WHERE "vx_j"."book_id" IN (?, ?) AND "tags"."org_id" = ?) AS "vx" WHERE "vx_rn" <= ? ORDER BY "vx_rn"`,
		}, []any{int64(1), int64(2), int64(7), 1}},
		{"each: through, no limit", func() *velox.Statement {
			return eachTags(0, engine.NewPredicate(engine.Node{Op: engine.OpEQ, Col: 1, V: int64(7)}))
		}, map[velox.Dialect]string{
			velox.Postgres: `SELECT "tags"."id", "tags"."org_id", "tags"."name", "vx_j"."book_id" AS "vx_owner" FROM "tags" JOIN "book_tags" AS "vx_j" ON "vx_j"."tag_id" = "tags"."id" WHERE "vx_j"."book_id" IN ($1, $2) AND "tags"."org_id" = $3 ORDER BY "tags"."name", "tags"."id"`,
			velox.MySQL:    "SELECT `tags`.`id`, `tags`.`org_id`, `tags`.`name`, `vx_j`.`book_id` AS `vx_owner` FROM `tags` JOIN `book_tags` AS `vx_j` ON `vx_j`.`tag_id` = `tags`.`id` WHERE `vx_j`.`book_id` IN (?, ?) AND `tags`.`org_id` = ? ORDER BY `tags`.`name`, `tags`.`id`",
			velox.SQLite:   `SELECT "tags"."id", "tags"."org_id", "tags"."name", "vx_j"."book_id" AS "vx_owner" FROM "tags" JOIN "book_tags" AS "vx_j" ON "vx_j"."tag_id" = "tags"."id" WHERE "vx_j"."book_id" IN (?, ?) AND "tags"."org_id" = ? ORDER BY "tags"."name", "tags"."id"`,
		}, []any{int64(1), int64(2), int64(7)}},
		{"each: through, a condition with EXISTS", func() *velox.Statement {
			notes := engine.Link{Target: noteInfo, OwnerCol: 0, TargetCol: 1}
			return eachTags(2, engine.NewPredicate(engine.Node{Op: engine.OpExists, V: notes, Kids: []engine.Node{{Op: engine.OpEQ, Col: 2, V: "hi"}}}))
		}, map[velox.Dialect]string{
			velox.Postgres: `SELECT "id", "org_id", "name", "vx_owner" FROM (SELECT "tags"."id", "tags"."org_id", "tags"."name", "vx_j"."book_id" AS "vx_owner", ROW_NUMBER() OVER (PARTITION BY "vx_j"."book_id" ORDER BY "tags"."name", "tags"."id") AS "vx_rn" FROM "tags" JOIN "book_tags" AS "vx_j" ON "vx_j"."tag_id" = "tags"."id" WHERE "vx_j"."book_id" IN ($1, $2) AND EXISTS (SELECT 1 FROM "notes" AS "vx1" WHERE "vx1"."tag_id" = "tags"."id" AND "body" = $3)) AS "vx" WHERE "vx_rn" <= $4 ORDER BY "vx_rn"`,
			velox.MySQL:    "SELECT `id`, `org_id`, `name`, `vx_owner` FROM (SELECT `tags`.`id`, `tags`.`org_id`, `tags`.`name`, `vx_j`.`book_id` AS `vx_owner`, ROW_NUMBER() OVER (PARTITION BY `vx_j`.`book_id` ORDER BY `tags`.`name`, `tags`.`id`) AS `vx_rn` FROM `tags` JOIN `book_tags` AS `vx_j` ON `vx_j`.`tag_id` = `tags`.`id` WHERE `vx_j`.`book_id` IN (?, ?) AND EXISTS (SELECT 1 FROM `notes` AS `vx1` WHERE `vx1`.`tag_id` = `tags`.`id` AND `body` = ?)) AS `vx` WHERE `vx_rn` <= ? ORDER BY `vx_rn`",
			velox.SQLite:   `SELECT "id", "org_id", "name", "vx_owner" FROM (SELECT "tags"."id", "tags"."org_id", "tags"."name", "vx_j"."book_id" AS "vx_owner", ROW_NUMBER() OVER (PARTITION BY "vx_j"."book_id" ORDER BY "tags"."name", "tags"."id") AS "vx_rn" FROM "tags" JOIN "book_tags" AS "vx_j" ON "vx_j"."tag_id" = "tags"."id" WHERE "vx_j"."book_id" IN (?, ?) AND EXISTS (SELECT 1 FROM "notes" AS "vx1" WHERE "vx1"."tag_id" = "tags"."id" AND "body" = ?)) AS "vx" WHERE "vx_rn" <= ? ORDER BY "vx_rn"`,
		}, []any{int64(1), int64(2), "hi", 2}},
		{"each: through, schema-qualified", func() *velox.Statement {
			st := eachTags(1, engine.NewPredicate(engine.Node{Op: engine.OpEQ, Col: 1, V: int64(7)}))
			st.Entity, st.Each.Join = inSchema(tagInfo, "billing"), inSchema(bookTagInfo, "billing")
			return st
		}, map[velox.Dialect]string{
			velox.Postgres: `SELECT "id", "org_id", "name", "vx_owner" FROM (SELECT "billing"."tags"."id", "billing"."tags"."org_id", "billing"."tags"."name", "vx_j"."book_id" AS "vx_owner", ROW_NUMBER() OVER (PARTITION BY "vx_j"."book_id" ORDER BY "billing"."tags"."name", "billing"."tags"."id") AS "vx_rn" FROM "billing"."tags" JOIN "billing"."book_tags" AS "vx_j" ON "vx_j"."tag_id" = "billing"."tags"."id" WHERE "vx_j"."book_id" IN ($1, $2) AND "billing"."tags"."org_id" = $3) AS "vx" WHERE "vx_rn" <= $4 ORDER BY "vx_rn"`,
			velox.MySQL:    "SELECT `id`, `org_id`, `name`, `vx_owner` FROM (SELECT `billing`.`tags`.`id`, `billing`.`tags`.`org_id`, `billing`.`tags`.`name`, `vx_j`.`book_id` AS `vx_owner`, ROW_NUMBER() OVER (PARTITION BY `vx_j`.`book_id` ORDER BY `billing`.`tags`.`name`, `billing`.`tags`.`id`) AS `vx_rn` FROM `billing`.`tags` JOIN `billing`.`book_tags` AS `vx_j` ON `vx_j`.`tag_id` = `billing`.`tags`.`id` WHERE `vx_j`.`book_id` IN (?, ?) AND `billing`.`tags`.`org_id` = ?) AS `vx` WHERE `vx_rn` <= ? ORDER BY `vx_rn`",
			velox.SQLite:   `SELECT "id", "org_id", "name", "vx_owner" FROM (SELECT "billing"."tags"."id", "billing"."tags"."org_id", "billing"."tags"."name", "vx_j"."book_id" AS "vx_owner", ROW_NUMBER() OVER (PARTITION BY "vx_j"."book_id" ORDER BY "billing"."tags"."name", "billing"."tags"."id") AS "vx_rn" FROM "billing"."tags" JOIN "billing"."book_tags" AS "vx_j" ON "vx_j"."tag_id" = "billing"."tags"."id" WHERE "vx_j"."book_id" IN (?, ?) AND "billing"."tags"."org_id" = ?) AS "vx" WHERE "vx_rn" <= ? ORDER BY "vx_rn"`,
		}, []any{int64(1), int64(2), int64(7), 1}},
		{"each: schema-qualified", func() *velox.Statement {
			st := eachItems(1, 2)
			st.Entity = inSchema(itemInfo, "billing")
			return st
		}, map[velox.Dialect]string{
			velox.Postgres: `SELECT "id", "invoice_id", "sku" FROM (SELECT "id", "invoice_id", "sku", ROW_NUMBER() OVER (PARTITION BY "invoice_id" ORDER BY "sku" DESC, "id") AS "vx_rn" FROM "billing"."items" WHERE "invoice_id" IN ($1, $2) AND "sku" <> $3) AS "vx" WHERE "vx_rn" <= $4 ORDER BY "vx_rn"`,
			velox.MySQL:    "SELECT `id`, `invoice_id`, `sku` FROM (SELECT `id`, `invoice_id`, `sku`, ROW_NUMBER() OVER (PARTITION BY `invoice_id` ORDER BY `sku` DESC, `id`) AS `vx_rn` FROM `billing`.`items` WHERE `invoice_id` IN (?, ?) AND `sku` <> ?) AS `vx` WHERE `vx_rn` <= ? ORDER BY `vx_rn`",
			velox.SQLite:   `SELECT "id", "invoice_id", "sku" FROM (SELECT "id", "invoice_id", "sku", ROW_NUMBER() OVER (PARTITION BY "invoice_id" ORDER BY "sku" DESC, "id") AS "vx_rn" FROM "billing"."items" WHERE "invoice_id" IN (?, ?) AND "sku" <> ?) AS "vx" WHERE "vx_rn" <= ? ORDER BY "vx_rn"`,
		}, []any{int64(1), int64(2), "x", 2}},
		{"each: through links that may repeat", func() *velox.Statement {
			st := eachTags(1, engine.NewPredicate(engine.Node{Op: engine.OpEQ, Col: 1, V: int64(7)}))
			st.Each.Join, st.Each.JoinOwner, st.Each.JoinTarget = linkInfo, 1, 2
			return st
		}, map[velox.Dialect]string{
			velox.Postgres: `SELECT "id", "org_id", "name", "vx_owner" FROM (SELECT "tags"."id", "tags"."org_id", "tags"."name", "vx_j"."book_id" AS "vx_owner", ROW_NUMBER() OVER (PARTITION BY "vx_j"."book_id" ORDER BY "tags"."name", "tags"."id") AS "vx_rn" FROM "tags" JOIN (SELECT DISTINCT "book_id", "tag_id" FROM "links" WHERE "book_id" IN ($1, $2)) AS "vx_j" ON "vx_j"."tag_id" = "tags"."id" WHERE "tags"."org_id" = $3) AS "vx" WHERE "vx_rn" <= $4 ORDER BY "vx_rn"`,
			velox.MySQL:    "SELECT `id`, `org_id`, `name`, `vx_owner` FROM (SELECT `tags`.`id`, `tags`.`org_id`, `tags`.`name`, `vx_j`.`book_id` AS `vx_owner`, ROW_NUMBER() OVER (PARTITION BY `vx_j`.`book_id` ORDER BY `tags`.`name`, `tags`.`id`) AS `vx_rn` FROM `tags` JOIN (SELECT DISTINCT `book_id`, `tag_id` FROM `links` WHERE `book_id` IN (?, ?)) AS `vx_j` ON `vx_j`.`tag_id` = `tags`.`id` WHERE `tags`.`org_id` = ?) AS `vx` WHERE `vx_rn` <= ? ORDER BY `vx_rn`",
			velox.SQLite:   `SELECT "id", "org_id", "name", "vx_owner" FROM (SELECT "tags"."id", "tags"."org_id", "tags"."name", "vx_j"."book_id" AS "vx_owner", ROW_NUMBER() OVER (PARTITION BY "vx_j"."book_id" ORDER BY "tags"."name", "tags"."id") AS "vx_rn" FROM "tags" JOIN (SELECT DISTINCT "book_id", "tag_id" FROM "links" WHERE "book_id" IN (?, ?)) AS "vx_j" ON "vx_j"."tag_id" = "tags"."id" WHERE "tags"."org_id" = ?) AS "vx" WHERE "vx_rn" <= ? ORDER BY "vx_rn"`,
		}, []any{int64(1), int64(2), int64(7), 1}},
		{"count each", countItems, map[velox.Dialect]string{
			velox.Postgres: `SELECT "invoice_id", COUNT(*) FROM "items" WHERE "invoice_id" IN ($1, $2) AND "sku" <> $3 GROUP BY "invoice_id"`,
			velox.MySQL:    "SELECT `invoice_id`, COUNT(*) FROM `items` WHERE `invoice_id` IN (?, ?) AND `sku` <> ? GROUP BY `invoice_id`",
			velox.SQLite:   `SELECT "invoice_id", COUNT(*) FROM "items" WHERE "invoice_id" IN (?, ?) AND "sku" <> ? GROUP BY "invoice_id"`,
		}, []any{int64(1), int64(2), "x"}},
		{"count each: through", func() *velox.Statement { return countTags(bookTagInfo, 0, 1) }, map[velox.Dialect]string{
			velox.Postgres: `SELECT "vx_j"."book_id", COUNT(*) FROM "tags" JOIN "book_tags" AS "vx_j" ON "vx_j"."tag_id" = "tags"."id" WHERE "vx_j"."book_id" IN ($1, $2) AND "tags"."org_id" = $3 GROUP BY "vx_j"."book_id"`,
			velox.MySQL:    "SELECT `vx_j`.`book_id`, COUNT(*) FROM `tags` JOIN `book_tags` AS `vx_j` ON `vx_j`.`tag_id` = `tags`.`id` WHERE `vx_j`.`book_id` IN (?, ?) AND `tags`.`org_id` = ? GROUP BY `vx_j`.`book_id`",
			velox.SQLite:   `SELECT "vx_j"."book_id", COUNT(*) FROM "tags" JOIN "book_tags" AS "vx_j" ON "vx_j"."tag_id" = "tags"."id" WHERE "vx_j"."book_id" IN (?, ?) AND "tags"."org_id" = ? GROUP BY "vx_j"."book_id"`,
		}, []any{int64(1), int64(2), int64(7)}},
		{"count each: through links that may repeat", func() *velox.Statement { return countTags(linkInfo, 1, 2) }, map[velox.Dialect]string{
			velox.Postgres: `SELECT "vx_j"."book_id", COUNT(*) FROM "tags" JOIN (SELECT DISTINCT "book_id", "tag_id" FROM "links" WHERE "book_id" IN ($1, $2)) AS "vx_j" ON "vx_j"."tag_id" = "tags"."id" WHERE "tags"."org_id" = $3 GROUP BY "vx_j"."book_id"`,
			velox.MySQL:    "SELECT `vx_j`.`book_id`, COUNT(*) FROM `tags` JOIN (SELECT DISTINCT `book_id`, `tag_id` FROM `links` WHERE `book_id` IN (?, ?)) AS `vx_j` ON `vx_j`.`tag_id` = `tags`.`id` WHERE `tags`.`org_id` = ? GROUP BY `vx_j`.`book_id`",
			velox.SQLite:   `SELECT "vx_j"."book_id", COUNT(*) FROM "tags" JOIN (SELECT DISTINCT "book_id", "tag_id" FROM "links" WHERE "book_id" IN (?, ?)) AS "vx_j" ON "vx_j"."tag_id" = "tags"."id" WHERE "tags"."org_id" = ? GROUP BY "vx_j"."book_id"`,
		}, []any{int64(1), int64(2), int64(7)}},
		{"count each: through links that may repeat, no condition", func() *velox.Statement {
			st := countTags(linkInfo, 1, 2)
			st.Where = nil
			return st
		}, map[velox.Dialect]string{
			velox.Postgres: `SELECT "vx_j"."book_id", COUNT(*) FROM "tags" JOIN (SELECT DISTINCT "book_id", "tag_id" FROM "links" WHERE "book_id" IN ($1, $2)) AS "vx_j" ON "vx_j"."tag_id" = "tags"."id" GROUP BY "vx_j"."book_id"`,
			velox.MySQL:    "SELECT `vx_j`.`book_id`, COUNT(*) FROM `tags` JOIN (SELECT DISTINCT `book_id`, `tag_id` FROM `links` WHERE `book_id` IN (?, ?)) AS `vx_j` ON `vx_j`.`tag_id` = `tags`.`id` GROUP BY `vx_j`.`book_id`",
			velox.SQLite:   `SELECT "vx_j"."book_id", COUNT(*) FROM "tags" JOIN (SELECT DISTINCT "book_id", "tag_id" FROM "links" WHERE "book_id" IN (?, ?)) AS "vx_j" ON "vx_j"."tag_id" = "tags"."id" GROUP BY "vx_j"."book_id"`,
		}, []any{int64(1), int64(2)}},
		{"delete", func() *velox.Statement {
			st := stmt(hID.In(1, 2))
			st.Op = velox.OpDelete
			return st
		}, map[velox.Dialect]string{
			velox.Postgres: `DELETE FROM "invoices" WHERE "id" IN ($1, $2)`,
			velox.MySQL:    "DELETE FROM `invoices` WHERE `id` IN (?, ?)",
			velox.SQLite:   `DELETE FROM "invoices" WHERE "id" IN (?, ?)`,
		}, []any{int64(1), int64(2)}},
	}
}

// TestEachRefusesReservedNames: the window's rank and owner aliases share the
// outer query's names with the target's columns, so a target column of
// either name cannot be read with a per-owner limit.
func TestEachRefusesReservedNames(t *testing.T) {
	for _, name := range []string{"vx_rn", "vx_owner"} {
		in := *tagInfo
		in.Columns = []velox.ColumnInfo{{Name: "id"}, {Name: "org_id"}, {Name: name}}
		st := eachTags(1, engine.NewPredicate(engine.Node{Op: engine.OpEQ, Col: 1, V: int64(7)}))
		st.Entity = &in
		_, _, err := engine.RenderStmt(velox.Postgres, st)
		if err == nil || !strings.Contains(err.Error(), "velox.tag") || !strings.Contains(err.Error(), `"`+name+`"`) {
			t.Errorf("%s: %v, want an error naming the entity and the column", name, err)
		}
		// Without a limit there is no window, and no clash.
		st.Each.Limit = 0
		if _, _, err := engine.RenderStmt(velox.Postgres, st); err != nil {
			t.Errorf("%s, no limit: %v", name, err)
		}
	}
}

func argsEqual(a, b []any) bool { return len(a) == len(b) && (len(a) == 0 || reflect.DeepEqual(a, b)) }

// argsOf is the arguments args, which hold st's Limit as a parameter, as d
// renders them: SQLite writes the Limit in the text, so it has no argument.
func argsOf(d velox.Dialect, st *velox.Statement, args []any) []any {
	if d != velox.SQLite || st.Limit == 0 {
		return args
	}
	i := len(args) - 1
	if st.Offset > 0 {
		i--
	}
	return append(append([]any{}, args[:i]...), args[i+1:]...)
}

func TestRender(t *testing.T) {
	for _, c := range renderCases() {
		if len(c.sql) != 3 {
			t.Errorf("%s: goldens for %d dialects, want all 3", c.name, len(c.sql))
		}
		for d, want := range c.sql {
			got, args := render(d, c.st())
			if got != want {
				t.Errorf("%s, %v:\n got %s\nwant %s", c.name, d, got, want)
			}
			if wantArgs := argsOf(d, c.st(), c.args); !argsEqual(args, wantArgs) {
				t.Errorf("%s, %v: args %#v, want %#v", c.name, d, args, wantArgs)
			}
		}
	}
}

func TestRenderQuotesIdentifiers(t *testing.T) {
	info := &velox.EntityInfo{Name: "velox.weird", Table: "user", Columns: []velox.ColumnInfo{{Name: "order"}, {Name: "a`b\"c"}}}
	st := &velox.Statement{Op: velox.OpSelect, Entity: info}
	for d, want := range map[velox.Dialect]string{
		velox.Postgres: `SELECT "order", "a` + "`" + `b""c" FROM "user"`,
		velox.MySQL:    "SELECT `order`, `a``b\"c` FROM `user`",
		velox.SQLite:   `SELECT "order", "a` + "`" + `b""c" FROM "user"`,
	} {
		if got, _ := render(d, st); got != want {
			t.Errorf("%v:\n got %s\nwant %s", d, got, want)
		}
	}
}

func TestExistsRenders(t *testing.T) {
	for d, want := range map[velox.Dialect]string{
		velox.Postgres: `SELECT 1 FROM "invoices" WHERE "org_id" = $1 LIMIT $2`,
		velox.MySQL:    "SELECT 1 FROM `invoices` WHERE `org_id` = ? LIMIT ?",
		velox.SQLite:   `SELECT 1 FROM "invoices" WHERE "org_id" = ? LIMIT 1`,
	} {
		st := stmt(hOrg.EQ(1))
		st.Exists, st.Limit = true, 1
		got, args := render(d, st)
		if got != want || !argsEqual(args, argsOf(d, st, []any{int64(1), 1})) {
			t.Errorf("%v: %s %v, want %s", d, got, args, want)
		}
	}
}

// The lock clause follows LIMIT and OFFSET, with the same text on PostgreSQL
// and MySQL.
func TestLockRenders(t *testing.T) {
	for d, base := range map[velox.Dialect][2]string{
		velox.Postgres: {`SELECT ` + pgCols + ` FROM "invoices" WHERE "id" = $1`, `$2`},
		velox.MySQL:    {"SELECT " + mysqlCols + " FROM `invoices` WHERE `id` = ?", "?"},
	} {
		where, p2 := base[0], base[1]
		p3 := "$3"
		if d == velox.MySQL {
			p3 = "?"
		}
		for _, c := range []struct {
			lock          velox.Lock
			wait          velox.LockWait
			limit, offset int
			want          string
		}{
			{velox.LockForUpdate, 0, 1, 0, where + " LIMIT " + p2 + " FOR UPDATE"},
			{velox.LockForShare, velox.LockSkipLocked, 1, 0, where + " LIMIT " + p2 + " FOR SHARE SKIP LOCKED"},
			{velox.LockForUpdate, velox.LockNoWait, 1, 0, where + " LIMIT " + p2 + " FOR UPDATE NOWAIT"},
			{velox.LockForUpdate, velox.LockSkipLocked, 1, 5, where + " LIMIT " + p2 + " OFFSET " + p3 + " FOR UPDATE SKIP LOCKED"},
			{velox.LockForShare, 0, 0, 5, map[velox.Dialect]string{velox.Postgres: where + " OFFSET $2 FOR SHARE", velox.MySQL: where + " LIMIT 18446744073709551615 OFFSET ? FOR SHARE"}[d]},
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

// TestNewClausesOnEveryDialect: Exists, Sum, Of through a join entity and a
// locking read with SKIP LOCKED, side by side on each dialect. SQLite has no
// locking reads, and the guard refuses one there.
func TestNewClausesOnEveryDialect(t *testing.T) {
	join := &velox.EntityInfo{Name: "velox.join", Table: "joins", Columns: []velox.ColumnInfo{{Name: "inv_id"}, {Name: "tag_id"}}}
	cases := []struct {
		name string
		st   func() *velox.Statement
		sql  map[velox.Dialect]string
		args []any
	}{
		{"exists", func() *velox.Statement {
			st := stmt(hOrg.EQ(1))
			st.Exists, st.Limit = true, 1
			return st
		}, map[velox.Dialect]string{
			velox.Postgres: `SELECT 1 FROM "invoices" WHERE "org_id" = $1 LIMIT $2`,
			velox.MySQL:    "SELECT 1 FROM `invoices` WHERE `org_id` = ? LIMIT ?",
			velox.SQLite:   `SELECT 1 FROM "invoices" WHERE "org_id" = ? LIMIT 1`,
		}, []any{int64(1), 1}},
		{"sum", func() *velox.Statement {
			st := stmt(hOrg.EQ(1))
			st.Aggregate, st.Columns = velox.AggregateSum, []int{3}
			return st
		}, map[velox.Dialect]string{
			velox.Postgres: `SELECT SUM("total") FROM "invoices" WHERE "org_id" = $1`,
			velox.MySQL:    "SELECT SUM(`total`) FROM `invoices` WHERE `org_id` = ?",
			velox.SQLite:   `SELECT SUM("total") FROM "invoices" WHERE "org_id" = ?`,
		}, []any{int64(1)}},
		{"of through a join", func() *velox.Statement {
			st := stmt(hOrg.EQ(1))
			st.Where = append(st.Where, engine.NewPredicate(engine.Node{Op: engine.OpThrough, Col: 0, V: join, W: 0,
				Kids: []engine.Node{{Op: engine.OpIn, Col: 1, V: []any{int64(7), int64(8)}}}}))
			st.Limit = 5
			return st
		}, map[velox.Dialect]string{
			velox.Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE "org_id" = $1 AND EXISTS (SELECT 1 FROM "joins" AS "vx1" WHERE "vx1"."inv_id" = "invoices"."id" AND "tag_id" IN ($2, $3)) LIMIT $4`,
			velox.MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE `org_id` = ? AND EXISTS (SELECT 1 FROM `joins` AS `vx1` WHERE `vx1`.`inv_id` = `invoices`.`id` AND `tag_id` IN (?, ?)) LIMIT ?",
			velox.SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE "org_id" = ? AND EXISTS (SELECT 1 FROM "joins" AS "vx1" WHERE "vx1"."inv_id" = "invoices"."id" AND "tag_id" IN (?, ?)) LIMIT 5`,
		}, []any{int64(1), int64(7), int64(8), 5}},
		{"for update skip locked", func() *velox.Statement {
			st := stmt(hID.EQ(1))
			st.Lock, st.LockWait, st.Limit, st.InTx = velox.LockForUpdate, velox.LockSkipLocked, 1, true
			return st
		}, map[velox.Dialect]string{
			velox.Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE "id" = $1 LIMIT $2 FOR UPDATE SKIP LOCKED`,
			velox.MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE `id` = ? LIMIT ? FOR UPDATE SKIP LOCKED",
		}, []any{int64(1), 1}},
	}
	for _, c := range cases {
		for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
			want, ok := c.sql[d]
			_, _, err := engineOf(openFake(d, &fakeconn.Exec{})).DryRun(context.Background(), c.st())
			if !ok {
				const refusal = "velox: velox.inv: SQLite has no locking reads; its transactions lock the whole database"
				if err == nil || err.Error() != refusal {
					t.Errorf("%s, %v: the guard gives %v, want %s", c.name, d, err, refusal)
				}
				continue
			}
			if err != nil {
				t.Errorf("%s, %v: the guard refuses it: %v", c.name, d, err)
			}
			got, args := render(d, c.st())
			if wantArgs := argsOf(d, c.st(), c.args); got != want || !argsEqual(args, wantArgs) {
				t.Errorf("%s, %v:\n got %s %#v\nwant %s %#v", c.name, d, got, args, want, wantArgs)
			}
		}
	}
}

// TestUnknownOpIsRefused: a condition of a kind the renderer does not know
// fails rendering with an error instead of rendering nothing.
func TestUnknownOpIsRefused(t *testing.T) {
	_, _, err := engine.RenderStmt(velox.Postgres, &velox.Statement{Op: velox.OpSelect, Entity: invInfo, Where: []velox.Predicate{engine.NewPredicate(engine.Node{Op: 200})}})
	if want := "velox: velox.inv: a condition of unknown kind 200"; err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
}

var nullInfo = &velox.EntityInfo{Name: "velox.memo", Table: "memos", PrimaryKey: []int{0}, Columns: []velox.ColumnInfo{
	{Name: "id"},
	{Name: "title"},
	{Name: "note", Nullable: true},
	{Name: "n", Nullable: true},
	{Name: "blob", Nullable: true},
}}

func nullIns(rows ...[]any) *velox.Statement {
	return &velox.Statement{Op: velox.OpInsert, Entity: nullInfo, Columns: []int{0, 1, 2, 3, 4}, Rows: rows}
}

// TestRenderInsertNullLiteral: a nullable column that holds NULL in every row
// of an INSERT is written as the literal NULL, not as a parameter; a column
// that holds a value in any row keeps a parameter in every row; a column that
// is not nullable keeps its parameter whatever it holds.
func TestRenderInsertNullLiteral(t *testing.T) {
	seven := int64(7)
	var none *string
	var noN *int64
	var noBlob []byte
	st := nullIns(
		[]any{int64(1), "a", none, noN, noBlob},
		[]any{int64(2), "b", none, &seven, noBlob},
	)
	text, args := render(velox.Postgres, st)
	const want = `INSERT INTO "memos" ("id", "title", "note", "n", "blob") VALUES ($1, $2, NULL, $3, NULL), ($4, $5, NULL, $6, NULL)`
	if text != want {
		t.Errorf("text:\n got %s\nwant %s", text, want)
	}
	if len(args) != 6 {
		t.Errorf("%d args, want 6: %v", len(args), args)
	}
	for d, w := range map[velox.Dialect]string{
		velox.MySQL:  "INSERT INTO `memos` (`id`, `title`, `note`, `n`, `blob`) VALUES (?, ?, NULL, ?, NULL), (?, ?, NULL, ?, NULL)",
		velox.SQLite: `INSERT INTO "memos" ("id", "title", "note", "n", "blob") VALUES (?, ?, NULL, ?, NULL), (?, ?, NULL, ?, NULL)`,
	} {
		if got, _ := render(d, st); got != w {
			t.Errorf("%v:\n got %s\nwant %s", d, got, w)
		}
	}

	// A value in one row only: the column is a parameter in every row.
	x := "x"
	text, args = render(velox.Postgres, nullIns(
		[]any{int64(1), "a", none, noN, noBlob},
		[]any{int64(2), "b", &x, noN, noBlob},
	))
	if want := `INSERT INTO "memos" ("id", "title", "note", "n", "blob") VALUES ($1, $2, $3, NULL, NULL), ($4, $5, $6, NULL, NULL)`; text != want {
		t.Errorf("mixed:\n got %s\nwant %s", text, want)
	}
	if len(args) != 6 || args[2] != any(none) {
		t.Errorf("mixed args: %v", args)
	}

	// A nil in a column that is not nullable is no NULL to write.
	text, args = render(velox.Postgres, nullIns([]any{int64(1), nil, none, noN, noBlob}))
	if want := `INSERT INTO "memos" ("id", "title", "note", "n", "blob") VALUES ($1, $2, NULL, NULL, NULL)`; text != want || len(args) != 2 {
		t.Errorf("not nullable: %s %v", text, args)
	}
}

// TestRenderCacheInsertNullShape: two INSERTs that differ only in which
// columns are all NULL are different shapes, each cached under its own text,
// and a cached shape collects as many arguments as its text has parameters.
func TestRenderCacheInsertNullShape(t *testing.T) {
	var none *string
	var noN *int64
	var noBlob []byte
	x, seven := "x", int64(7)
	allNull := func() *velox.Statement { return nullIns([]any{int64(1), "a", none, noN, noBlob}) }
	withNote := func() *velox.Statement { return nullIns([]any{int64(1), "a", &x, noN, noBlob}) }
	withN := func() *velox.Statement { return nullIns([]any{int64(1), "a", none, &seven, noBlob}) }
	c := &engine.RenderCache{}
	for pass := range 2 { // the second pass reads every shape from the cache
		for name, c2 := range map[string]struct {
			st   func() *velox.Statement
			want string
			args int
		}{
			"all null": {allNull, `INSERT INTO "memos" ("id", "title", "note", "n", "blob") VALUES ($1, $2, NULL, NULL, NULL)`, 2},
			"note":     {withNote, `INSERT INTO "memos" ("id", "title", "note", "n", "blob") VALUES ($1, $2, $3, NULL, NULL)`, 3},
			"n":        {withN, `INSERT INTO "memos" ("id", "title", "note", "n", "blob") VALUES ($1, $2, NULL, $3, NULL)`, 3},
		} {
			text, args := cachedRender(c, velox.Postgres, c2.st())
			if text != c2.want || len(args) != c2.args {
				t.Errorf("pass %d, %s: %s, %d args; want %s, %d", pass, name, text, len(args), c2.want, c2.args)
			}
		}
	}
}
