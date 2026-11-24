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

// TestNullsOrderSQL: NullsFirst and NullsLast write NULLS FIRST or LAST on
// PostgreSQL and SQLite, and an IS NULL term before the column on MySQL,
// whose DESC puts NULL first; the column's own direction is kept.
func TestNullsOrderSQL(t *testing.T) {
	for _, c := range []struct {
		o                velox.Order[testmodel.Invoice]
		pg, mysql, sqlit string
	}{
		{I.PaidAt.Asc().NullsFirst(),
			`"paid_at" NULLS FIRST`, "`paid_at` IS NULL DESC, `paid_at`", `"paid_at" NULLS FIRST`},
		{I.PaidAt.Asc().NullsLast(),
			`"paid_at" NULLS LAST`, "`paid_at` IS NULL, `paid_at`", `"paid_at" NULLS LAST`},
		{I.PaidAt.Desc().NullsFirst(),
			`"paid_at" DESC NULLS FIRST`, "`paid_at` IS NULL DESC, `paid_at` DESC", `"paid_at" DESC NULLS FIRST`},
		{I.PaidAt.Desc().NullsLast(),
			`"paid_at" DESC NULLS LAST`, "`paid_at` IS NULL, `paid_at` DESC", `"paid_at" DESC NULLS LAST`},
		// The later call wins.
		{I.PaidAt.Desc().NullsFirst().NullsLast(),
			`"paid_at" DESC NULLS LAST`, "`paid_at` IS NULL, `paid_at` DESC", `"paid_at" DESC NULLS LAST`},
		// A column that cannot be NULL is ordered as without it.
		{I.Total.Desc().NullsLast(), `"total" DESC`, "`total` DESC", `"total" DESC`},
	} {
		for d, want := range map[velox.Dialect]string{velox.Postgres: c.pg, velox.MySQL: c.mysql, velox.SQLite: c.sqlit} {
			key := "`id`"
			if d != velox.MySQL {
				key = `"id"`
			}
			text, _ := sqlOK(t)(openFake(d, &fakeconn.Exec{}).Query[testmodel.Invoice]().OrderBy(c.o, I.ID.Asc()).Limit(10).SQL(ctx))
			if want := " ORDER BY " + want + ", " + key + " LIMIT 10"; !strings.HasSuffix(text, want) {
				t.Errorf("%v:\n got %s\nwant …%s", d, text, want)
			}
		}
	}
}

// TestNullsOrderUnsetIsUnchanged: an OrderTerm whose Nulls is not set
// writes what it wrote before Nulls was added.
func TestNullsOrderUnsetIsUnchanged(t *testing.T) {
	for d, want := range map[velox.Dialect]string{
		velox.Postgres: ` ORDER BY "paid_at" DESC, "id"`,
		velox.MySQL:    " ORDER BY `paid_at` IS NULL DESC, `paid_at` DESC, `id`",
		velox.SQLite:   ` ORDER BY "paid_at" IS NULL DESC, "paid_at" DESC, "id"`,
	} {
		text, _ := sqlOK(t)(openFake(d, &fakeconn.Exec{}).Query[testmodel.Invoice]().OrderBy(I.PaidAt.Desc(), I.ID.Asc()).Limit(10).SQL(ctx))
		if !strings.HasSuffix(text, want+" LIMIT 10") {
			t.Errorf("%v: %s", d, text)
		}
	}
}

// TestNullsOrderJoined: a joined column's Nulls is written on the joined
// alias, and a left join's column, NULL where the join found no row, takes
// it even when the column cannot be NULL.
func TestNullsOrderJoined(t *testing.T) {
	for d, want := range map[velox.Dialect]string{
		velox.Postgres: ` ORDER BY "vxj1"."paid_at" NULLS FIRST, "vxj1"."total" DESC NULLS LAST`,
		velox.MySQL:    " ORDER BY `vxj1`.`paid_at` IS NULL DESC, `vxj1`.`paid_at`, `vxj1`.`total` IS NULL, `vxj1`.`total` DESC",
		velox.SQLite:   ` ORDER BY "vxj1"."paid_at" NULLS FIRST, "vxj1"."total" DESC NULLS LAST`,
	} {
		q := openFake(d, &fakeconn.Exec{}).Query[testmodel.Item]().LeftJoin(It.Invoice).
			OrderBy(It.Invoice.By(I.PaidAt.Asc().NullsFirst()), It.Invoice.By(I.Total.Desc().NullsLast()))
		text, _ := sqlOK(t)(q.Limit(10).SQL(ctx))
		if !strings.HasSuffix(text, want+" LIMIT 10") {
			t.Errorf("%v:\n got %s\nwant …%s", d, text, want)
		}
	}
}

// TestNullsOrderExprArgs: an expression with bound values is written twice
// on MySQL, its IS NULL term and itself, each binding its own values.
func TestNullsOrderExprArgs(t *testing.T) {
	calc := velox.Calc[testmodel.Invoice]("NULLIF(? % ?, ?)", I.OrgID, int64(3), int64(0))
	for _, c := range []struct {
		d    velox.Dialect
		want string
		args []any
	}{
		{velox.Postgres, ` ORDER BY (NULLIF("org_id" % $1, $2)) DESC NULLS LAST LIMIT 10`, []any{int64(3), int64(0)}},
		{velox.SQLite, ` ORDER BY (NULLIF("org_id" % ?, ?)) DESC NULLS LAST LIMIT 10`, []any{int64(3), int64(0)}},
		{velox.MySQL, " ORDER BY (NULLIF(`org_id` % ?, ?)) IS NULL, (NULLIF(`org_id` % ?, ?)) DESC LIMIT 10", []any{int64(3), int64(0), int64(3), int64(0)}},
	} {
		text, args := sqlOK(t)(openFake(c.d, &fakeconn.Exec{}).Query[testmodel.Invoice]().OrderBy(calc.Desc().NullsLast()).Limit(10).SQL(ctx))
		if !strings.HasSuffix(text, c.want) || !reflect.DeepEqual(args, c.args) {
			t.Errorf("%v:\n got %s %v\nwant …%s %v", c.d, text, args, c.want, c.args)
		}
	}
}

// TestNullsOrderAfter: After's condition places NULL where the ordering
// does: past a value of a NULLs-first term is only the greater values, past
// NULL is every value; past a value of a NULLs-last term takes NULL too,
// and nothing is past NULL.
func TestNullsOrderAfter(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	at := t0
	for name, c := range map[string]struct {
		o    velox.Order[testmodel.Invoice]
		row  testmodel.Invoice
		want string
	}{
		"asc first, from a value": {I.PaidAt.Asc().NullsFirst(), testmodel.Invoice{ID: 7, PaidAt: &at},
			` WHERE ("paid_at" > $1 OR ("paid_at" = $2 AND "id" > $3)) ORDER BY "paid_at" NULLS FIRST, "id" LIMIT 10`},
		"asc first, from NULL": {I.PaidAt.Asc().NullsFirst(), testmodel.Invoice{ID: 7},
			` WHERE ("paid_at" IS NOT NULL OR ("paid_at" IS NULL AND "id" > $1)) ORDER BY "paid_at" NULLS FIRST, "id" LIMIT 10`},
		"desc last, from a value": {I.PaidAt.Desc().NullsLast(), testmodel.Invoice{ID: 7, PaidAt: &at},
			` WHERE ("paid_at" < $1 OR "paid_at" IS NULL OR ("paid_at" = $2 AND "id" > $3)) ORDER BY "paid_at" DESC NULLS LAST, "id" LIMIT 10`},
		"desc last, from NULL": {I.PaidAt.Desc().NullsLast(), testmodel.Invoice{ID: 7},
			` WHERE (("paid_at" IS NULL AND "id" > $1)) ORDER BY "paid_at" DESC NULLS LAST, "id" LIMIT 10`},
	} {
		text, _ := sqlOK(t)(db.Query[testmodel.Invoice]().OrderBy(c.o, I.ID.Asc()).After(&c.row).Limit(10).SQL(ctx))
		if !strings.HasSuffix(text, c.want) {
			t.Errorf("%s:\n got %s\nwant …%s", name, text, c.want)
		}
	}
}

// TestNullsOrderCacheKey: the render cache keys on Nulls: one DB renders
// each placement as its own SQL.
func TestNullsOrderCacheKey(t *testing.T) {
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		db := openFake(d, &fakeconn.Exec{})
		seen := map[string]int{}
		os := []velox.Order[testmodel.Invoice]{I.PaidAt.Asc(), I.PaidAt.Asc().NullsFirst(), I.PaidAt.Asc().NullsLast()}
		if d == velox.MySQL {
			os = os[:2] // ascending NULLS LAST is the default's SQL there
		}
		for i, o := range os {
			for range 2 {
				text, _ := sqlOK(t)(db.Query[testmodel.Invoice]().OrderBy(o).Limit(10).SQL(ctx))
				if j, ok := seen[text]; ok && j != i {
					t.Errorf("%v: placements %d and %d share %s", d, j, i, text)
				}
				seen[text] = i
			}
		}
		if len(seen) != len(os) {
			t.Errorf("%v: %d texts: %v", d, len(seen), seen)
		}
	}
}

// TestNullsOrderUntyped: an untyped query's OrderTerm places NULL by its
// Nulls, and one that is neither NullsFirst nor NullsLast fails the
// statement.
func TestNullsOrderUntyped(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	tbl := untyped.TableOf[testmodel.Invoice]()
	o := velox.OrderTerm{Column: col(I.PaidAt), Desc: true, Nulls: velox.NullsLast}
	text, _ := sqlOK(t)(untyped.Query(db, tbl).OrderBy(o).Limit(1).SQL(ctx))
	if !strings.HasSuffix(text, ` ORDER BY "paid_at" DESC NULLS LAST LIMIT 1`) {
		t.Errorf("%s", text)
	}
	o.Nulls = 9
	_, err := untyped.Query(db, tbl).OrderBy(o).Limit(1).All(ctx)
	if err == nil || !strings.Contains(err.Error(), "Nulls is 9") {
		t.Errorf("err = %v", err)
	}
}
