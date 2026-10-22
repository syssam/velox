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

// The entities of the per-owner goldens: items keyed to invoices, and tags
// keyed to books through book_tags, a tag having notes.
var (
	itemInfo = &EntityInfo{Name: "velox.item", Table: "items", Tenant: -1, PrimaryKey: []int{0},
		Columns: []ColumnInfo{{Name: "id"}, {Name: "invoice_id"}, {Name: "sku"}}}
	tagInfo = &EntityInfo{Name: "velox.tag", Table: "tags", Tenant: -1, PrimaryKey: []int{0},
		Columns: []ColumnInfo{{Name: "id"}, {Name: "org_id"}, {Name: "name"}}}
	bookTagInfo = &EntityInfo{Name: "velox.bookTag", Table: "book_tags", Tenant: -1, PrimaryKey: []int{0, 1},
		Columns: []ColumnInfo{{Name: "book_id"}, {Name: "tag_id"}}}
	noteInfo = &EntityInfo{Name: "velox.note", Table: "notes", Tenant: -1, PrimaryKey: []int{0},
		Columns: []ColumnInfo{{Name: "id"}, {Name: "tag_id"}, {Name: "body"}}}
)

// eachItems is a per-owner read of the items of invoices 1 and 2, with sku
// <> "x", in sku order, keyed by column col.
func eachItems(col, limit int) *Statement {
	return &Statement{Op: OpSelect, Entity: itemInfo, Where: []Predicate{{node{op: opNEQ, col: 2, v: "x"}}},
		OrderBy: []OrderTerm{{Column: 2, Desc: true}, {Column: 0}},
		Each:    &EachClause{Keys: []any{int64(1), int64(2)}, Column: col, Limit: limit}}
}

// eachTags is a per-owner read of the tags of books 1 and 2 through
// book_tags, in name order, with the condition where.
func eachTags(limit int, where Predicate) *Statement {
	return &Statement{Op: OpSelect, Entity: tagInfo, Where: []Predicate{where},
		OrderBy: []OrderTerm{{Column: 2}, {Column: 0}},
		Each:    &EachClause{Keys: []any{int64(1), int64(2)}, Join: bookTagInfo, JoinOwner: 0, JoinTarget: 1, Target: 0, Limit: limit}}
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
			MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE `id` IN (?, ?, ?) AND `number` NOT IN (?)",
			SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE "id" IN (?, ?, ?) AND "number" NOT IN (?)`,
		}, []any{int64(1), int64(2), int64(3), "x"}},
		{"empty in and not in", func() *Statement { return stmt(hID.In(), hID.NotIn()) }, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE 1 = 0 AND 1 = 1`,
			MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE 1 = 0 AND 1 = 1",
			SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE 1 = 0 AND 1 = 1`,
		}, nil},
		{"like escape clause", func() *Statement { return stmt(hNumber.HasPrefix("a")) }, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE "number" LIKE $1 ESCAPE '!'`,
			MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE `number` LIKE ? ESCAPE '!'",
			SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE "number" LIKE ? ESCAPE '!'`,
		}, []any{"a%"}},
		{"and, or, not", func() *Statement {
			return stmt(Or(hID.EQ(1), And(hOrg.EQ(2), Not(hPaidAt.IsNull()))))
		}, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE ("id" = $1 OR ("org_id" = $2 AND NOT ("paid_at" IS NULL)))`,
			MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE (`id` = ? OR (`org_id` = ? AND NOT (`paid_at` IS NULL)))",
			SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE ("id" = ? OR ("org_id" = ? AND NOT ("paid_at" IS NULL)))`,
		}, []any{int64(1), int64(2)}},
		{"or beside a sibling", func() *Statement {
			return stmt(Or(hID.EQ(1), hID.EQ(2)), hOrg.EQ(3))
		}, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE ("id" = $1 OR "id" = $2) AND "org_id" = $3`,
			MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE (`id` = ? OR `id` = ?) AND `org_id` = ?",
			SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE ("id" = ? OR "id" = ?) AND "org_id" = ?`,
		}, []any{int64(1), int64(2), int64(3)}},
		{"neq, gte, lt, lte", func() *Statement {
			return stmt(hOrg.NEQ(1), hTotal.GTE(2), hTotal.LT(3), hID.LTE(4))
		}, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE "org_id" <> $1 AND "total" >= $2 AND "total" < $3 AND "id" <= $4`,
			MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE `org_id` <> ? AND `total` >= ? AND `total` < ? AND `id` <= ?",
			SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE "org_id" <> ? AND "total" >= ? AND "total" < ? AND "id" <= ?`,
		}, []any{int64(1), float64(2), float64(3), int64(4)}},
		{"empty and, empty or", func() *Statement { return stmt(And[inv](), Or[inv]()) }, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE 1 = 1 AND 1 = 0`,
			MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE 1 = 1 AND 1 = 0",
			SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE 1 = 1 AND 1 = 0`,
		}, nil},
		{"between, equal fold, not null", func() *Statement {
			return stmt(hTotal.Between(1, 2), hNumber.EqualFold("Ab"), hPaidAt.NotNull())
		}, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE "total" BETWEEN $1 AND $2 AND LOWER("number") = LOWER($3) AND "paid_at" IS NOT NULL`,
			MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE `total` BETWEEN ? AND ? AND LOWER(`number`) = LOWER(?) AND `paid_at` IS NOT NULL",
			SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE "total" BETWEEN ? AND ? AND LOWER("number") = LOWER(?) AND "paid_at" IS NOT NULL`,
		}, []any{float64(1), float64(2), "Ab"}},
		{"count", func() *Statement { st := stmt(hOrg.EQ(1)); st.Count = true; return st }, map[Dialect]string{
			Postgres: `SELECT COUNT(*) FROM "invoices" WHERE "org_id" = $1`,
			MySQL:    "SELECT COUNT(*) FROM `invoices` WHERE `org_id` = ?",
			SQLite:   `SELECT COUNT(*) FROM "invoices" WHERE "org_id" = ?`,
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
			MySQL:    "SELECT `number`, `id` FROM `invoices` LIMIT ? OFFSET ?",
			SQLite:   `SELECT "number", "id" FROM "invoices" LIMIT ? OFFSET ?`,
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
		{"each: window", func() *Statement { return eachItems(1, 2) }, map[Dialect]string{
			Postgres: `SELECT "id", "invoice_id", "sku" FROM (SELECT "id", "invoice_id", "sku", ROW_NUMBER() OVER (PARTITION BY "invoice_id" ORDER BY "sku" DESC, "id") AS "vx_rn" FROM "items" WHERE "invoice_id" IN ($1, $2) AND "sku" <> $3) AS "vx" WHERE "vx_rn" <= $4 ORDER BY "vx_rn"`,
			MySQL:    "SELECT `id`, `invoice_id`, `sku` FROM (SELECT `id`, `invoice_id`, `sku`, ROW_NUMBER() OVER (PARTITION BY `invoice_id` ORDER BY `sku` DESC, `id`) AS `vx_rn` FROM `items` WHERE `invoice_id` IN (?, ?) AND `sku` <> ?) AS `vx` WHERE `vx_rn` <= ? ORDER BY `vx_rn`",
			SQLite:   `SELECT "id", "invoice_id", "sku" FROM (SELECT "id", "invoice_id", "sku", ROW_NUMBER() OVER (PARTITION BY "invoice_id" ORDER BY "sku" DESC, "id") AS "vx_rn" FROM "items" WHERE "invoice_id" IN (?, ?) AND "sku" <> ?) AS "vx" WHERE "vx_rn" <= ? ORDER BY "vx_rn"`,
		}, []any{int64(1), int64(2), "x", 2}},
		{"each: keyed by the target key", func() *Statement { return eachItems(0, 1) }, map[Dialect]string{
			Postgres: `SELECT "id", "invoice_id", "sku" FROM "items" WHERE "id" IN ($1, $2) AND "sku" <> $3 ORDER BY "sku" DESC, "id"`,
			MySQL:    "SELECT `id`, `invoice_id`, `sku` FROM `items` WHERE `id` IN (?, ?) AND `sku` <> ? ORDER BY `sku` DESC, `id`",
			SQLite:   `SELECT "id", "invoice_id", "sku" FROM "items" WHERE "id" IN (?, ?) AND "sku" <> ? ORDER BY "sku" DESC, "id"`,
		}, []any{int64(1), int64(2), "x"}},
		{"each: through", func() *Statement { return eachTags(1, Predicate{node{op: opEQ, col: 1, v: int64(7)}}) }, map[Dialect]string{
			Postgres: `SELECT "id", "org_id", "name", "vx_owner" FROM (SELECT "tags"."id", "tags"."org_id", "tags"."name", "vx_j"."book_id" AS "vx_owner", ROW_NUMBER() OVER (PARTITION BY "vx_j"."book_id" ORDER BY "tags"."name", "tags"."id") AS "vx_rn" FROM "tags" JOIN "book_tags" AS "vx_j" ON "vx_j"."tag_id" = "tags"."id" WHERE "vx_j"."book_id" IN ($1, $2) AND "tags"."org_id" = $3) AS "vx" WHERE "vx_rn" <= $4 ORDER BY "vx_rn"`,
			MySQL:    "SELECT `id`, `org_id`, `name`, `vx_owner` FROM (SELECT `tags`.`id`, `tags`.`org_id`, `tags`.`name`, `vx_j`.`book_id` AS `vx_owner`, ROW_NUMBER() OVER (PARTITION BY `vx_j`.`book_id` ORDER BY `tags`.`name`, `tags`.`id`) AS `vx_rn` FROM `tags` JOIN `book_tags` AS `vx_j` ON `vx_j`.`tag_id` = `tags`.`id` WHERE `vx_j`.`book_id` IN (?, ?) AND `tags`.`org_id` = ?) AS `vx` WHERE `vx_rn` <= ? ORDER BY `vx_rn`",
			SQLite:   `SELECT "id", "org_id", "name", "vx_owner" FROM (SELECT "tags"."id", "tags"."org_id", "tags"."name", "vx_j"."book_id" AS "vx_owner", ROW_NUMBER() OVER (PARTITION BY "vx_j"."book_id" ORDER BY "tags"."name", "tags"."id") AS "vx_rn" FROM "tags" JOIN "book_tags" AS "vx_j" ON "vx_j"."tag_id" = "tags"."id" WHERE "vx_j"."book_id" IN (?, ?) AND "tags"."org_id" = ?) AS "vx" WHERE "vx_rn" <= ? ORDER BY "vx_rn"`,
		}, []any{int64(1), int64(2), int64(7), 1}},
		{"each: through, no limit", func() *Statement { return eachTags(0, Predicate{node{op: opEQ, col: 1, v: int64(7)}}) }, map[Dialect]string{
			Postgres: `SELECT "tags"."id", "tags"."org_id", "tags"."name", "vx_j"."book_id" AS "vx_owner" FROM "tags" JOIN "book_tags" AS "vx_j" ON "vx_j"."tag_id" = "tags"."id" WHERE "vx_j"."book_id" IN ($1, $2) AND "tags"."org_id" = $3 ORDER BY "tags"."name", "tags"."id"`,
			MySQL:    "SELECT `tags`.`id`, `tags`.`org_id`, `tags`.`name`, `vx_j`.`book_id` AS `vx_owner` FROM `tags` JOIN `book_tags` AS `vx_j` ON `vx_j`.`tag_id` = `tags`.`id` WHERE `vx_j`.`book_id` IN (?, ?) AND `tags`.`org_id` = ? ORDER BY `tags`.`name`, `tags`.`id`",
			SQLite:   `SELECT "tags"."id", "tags"."org_id", "tags"."name", "vx_j"."book_id" AS "vx_owner" FROM "tags" JOIN "book_tags" AS "vx_j" ON "vx_j"."tag_id" = "tags"."id" WHERE "vx_j"."book_id" IN (?, ?) AND "tags"."org_id" = ? ORDER BY "tags"."name", "tags"."id"`,
		}, []any{int64(1), int64(2), int64(7)}},
		{"each: through, a condition with EXISTS", func() *Statement {
			notes := link{target: noteInfo, ownerCol: 0, targetCol: 1}
			return eachTags(2, Predicate{node{op: opExists, v: notes, kids: []node{{op: opEQ, col: 2, v: "hi"}}}})
		}, map[Dialect]string{
			Postgres: `SELECT "id", "org_id", "name", "vx_owner" FROM (SELECT "tags"."id", "tags"."org_id", "tags"."name", "vx_j"."book_id" AS "vx_owner", ROW_NUMBER() OVER (PARTITION BY "vx_j"."book_id" ORDER BY "tags"."name", "tags"."id") AS "vx_rn" FROM "tags" JOIN "book_tags" AS "vx_j" ON "vx_j"."tag_id" = "tags"."id" WHERE "vx_j"."book_id" IN ($1, $2) AND EXISTS (SELECT 1 FROM "notes" AS "vx1" WHERE "vx1"."tag_id" = "tags"."id" AND "body" = $3)) AS "vx" WHERE "vx_rn" <= $4 ORDER BY "vx_rn"`,
			MySQL:    "SELECT `id`, `org_id`, `name`, `vx_owner` FROM (SELECT `tags`.`id`, `tags`.`org_id`, `tags`.`name`, `vx_j`.`book_id` AS `vx_owner`, ROW_NUMBER() OVER (PARTITION BY `vx_j`.`book_id` ORDER BY `tags`.`name`, `tags`.`id`) AS `vx_rn` FROM `tags` JOIN `book_tags` AS `vx_j` ON `vx_j`.`tag_id` = `tags`.`id` WHERE `vx_j`.`book_id` IN (?, ?) AND EXISTS (SELECT 1 FROM `notes` AS `vx1` WHERE `vx1`.`tag_id` = `tags`.`id` AND `body` = ?)) AS `vx` WHERE `vx_rn` <= ? ORDER BY `vx_rn`",
			SQLite:   `SELECT "id", "org_id", "name", "vx_owner" FROM (SELECT "tags"."id", "tags"."org_id", "tags"."name", "vx_j"."book_id" AS "vx_owner", ROW_NUMBER() OVER (PARTITION BY "vx_j"."book_id" ORDER BY "tags"."name", "tags"."id") AS "vx_rn" FROM "tags" JOIN "book_tags" AS "vx_j" ON "vx_j"."tag_id" = "tags"."id" WHERE "vx_j"."book_id" IN (?, ?) AND EXISTS (SELECT 1 FROM "notes" AS "vx1" WHERE "vx1"."tag_id" = "tags"."id" AND "body" = ?)) AS "vx" WHERE "vx_rn" <= ? ORDER BY "vx_rn"`,
		}, []any{int64(1), int64(2), "hi", 2}},
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
		if len(c.sql) != 3 {
			t.Errorf("%s: goldens for %d dialects, want all 3", c.name, len(c.sql))
		}
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

// TestNewClausesOnEveryDialect: Exists, Sum, Of through a join entity and a
// locking read with SKIP LOCKED, side by side on each dialect. SQLite has no
// locking reads, and the guard refuses one there.
func TestNewClausesOnEveryDialect(t *testing.T) {
	join := &EntityInfo{Name: "velox.join", Table: "joins", Tenant: -1, Columns: []ColumnInfo{{Name: "inv_id"}, {Name: "tag_id"}}}
	cases := []struct {
		name string
		st   func() *Statement
		sql  map[Dialect]string
		args []any
	}{
		{"exists", func() *Statement {
			st := stmt(hOrg.EQ(1))
			st.Exists, st.Limit = true, 1
			return st
		}, map[Dialect]string{
			Postgres: `SELECT 1 FROM "invoices" WHERE "org_id" = $1 LIMIT $2`,
			MySQL:    "SELECT 1 FROM `invoices` WHERE `org_id` = ? LIMIT ?",
			SQLite:   `SELECT 1 FROM "invoices" WHERE "org_id" = ? LIMIT ?`,
		}, []any{int64(1), 1}},
		{"sum", func() *Statement {
			st := stmt(hOrg.EQ(1))
			st.Aggregate, st.Columns = AggregateSum, []int{3}
			return st
		}, map[Dialect]string{
			Postgres: `SELECT SUM("total") FROM "invoices" WHERE "org_id" = $1`,
			MySQL:    "SELECT SUM(`total`) FROM `invoices` WHERE `org_id` = ?",
			SQLite:   `SELECT SUM("total") FROM "invoices" WHERE "org_id" = ?`,
		}, []any{int64(1)}},
		{"of through a join", func() *Statement {
			st := stmt(hOrg.EQ(1))
			st.Where = append(st.Where, Predicate{node{op: opThrough, col: 0, v: join, w: 0,
				kids: []node{{op: opIn, col: 1, v: []any{int64(7), int64(8)}}}}})
			st.Limit = 5
			return st
		}, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE "org_id" = $1 AND EXISTS (SELECT 1 FROM "joins" AS "vx1" WHERE "vx1"."inv_id" = "invoices"."id" AND "tag_id" IN ($2, $3)) LIMIT $4`,
			MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE `org_id` = ? AND EXISTS (SELECT 1 FROM `joins` AS `vx1` WHERE `vx1`.`inv_id` = `invoices`.`id` AND `tag_id` IN (?, ?)) LIMIT ?",
			SQLite:   `SELECT ` + pgCols + ` FROM "invoices" WHERE "org_id" = ? AND EXISTS (SELECT 1 FROM "joins" AS "vx1" WHERE "vx1"."inv_id" = "invoices"."id" AND "tag_id" IN (?, ?)) LIMIT ?`,
		}, []any{int64(1), int64(7), int64(8), 5}},
		{"for update skip locked", func() *Statement {
			st := stmt(hID.EQ(1))
			st.Lock, st.LockWait, st.Limit, st.InTx = LockForUpdate, LockSkipLocked, 1, true
			return st
		}, map[Dialect]string{
			Postgres: `SELECT ` + pgCols + ` FROM "invoices" WHERE "id" = $1 LIMIT $2 FOR UPDATE SKIP LOCKED`,
			MySQL:    "SELECT " + mysqlCols + " FROM `invoices` WHERE `id` = ? LIMIT ? FOR UPDATE SKIP LOCKED",
		}, []any{int64(1), 1}},
	}
	for _, c := range cases {
		for _, d := range []Dialect{Postgres, MySQL, SQLite} {
			want, ok := c.sql[d]
			err := OpenFake(d, &FakeExec{}).guard(c.st())
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
			if got != want || !argsEqual(args, c.args) {
				t.Errorf("%s, %v:\n got %s %#v\nwant %s %#v", c.name, d, got, args, want, c.args)
			}
		}
	}
}
