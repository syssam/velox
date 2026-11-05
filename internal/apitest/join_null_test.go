package apitest

import (
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// An order by a nullable joined column puts NULL last ascending on every
// dialect, as an order by the row's own column does. A left join's missing
// target is NULL too, in a column that holds none.
func TestJoinOrderByANullableColumn(t *testing.T) {
	for _, c := range []struct {
		d    velox.Dialect
		left bool
		o    velox.Order[testmodel.Invoice]
		want string
	}{
		{velox.Postgres, false, I.PaidAt.Asc(), ` ORDER BY "vxj1"."paid_at", "items"."id" LIMIT `},
		{velox.MySQL, false, I.PaidAt.Asc(), " ORDER BY `vxj1`.`paid_at` IS NULL, `vxj1`.`paid_at`, `items`.`id` LIMIT "},
		{velox.SQLite, false, I.PaidAt.Desc(), ` ORDER BY "vxj1"."paid_at" IS NULL DESC, "vxj1"."paid_at" DESC, "items"."id" LIMIT `},
		{velox.SQLite, true, I.Number.Asc(), ` ORDER BY "vxj1"."number" IS NULL, "vxj1"."number", "items"."id" LIMIT `},
		{velox.Postgres, true, I.Number.Asc(), ` ORDER BY "vxj1"."number", "items"."id" LIMIT `},
		{velox.MySQL, false, I.Number.Asc(), " ORDER BY `vxj1`.`number`, `items`.`id` LIMIT "},
	} {
		q := openFake(c.d, &fakeconn.Exec{}).Query[testmodel.Item]()
		if c.left {
			q = q.LeftJoin(It.Invoice)
		} else {
			q = q.Join(It.Invoice)
		}
		text, _ := sqlOK(t)(q.OrderBy(It.Invoice.By(c.o), It.ID.Asc()).Limit(10).SQL(ctx))
		if !strings.Contains(text, c.want) {
			t.Errorf("%v, left %v:\n got %s\nwant …%s", c.d, c.left, text, c.want)
		}
	}
}

// After pages past a nullable joined column with the conditions it uses on
// the row's own column, on the joined row: past a value is > v OR IS NULL,
// and past NULL ascending is the tie on the key alone.
func TestJoinAfterANullableColumn(t *testing.T) {
	at := t0
	q := openFake(velox.Postgres, &fakeconn.Exec{}).Query[testmodel.Item]().
		Join(It.Invoice).OrderBy(It.Invoice.By(I.PaidAt.Asc()), It.ID.Asc()).Limit(10)
	for name, c := range map[string]struct {
		paidAt *time.Time
		want   string
	}{
		"from a value": {&at, ` WHERE (("vxj1"."paid_at" > $1 OR "vxj1"."paid_at" IS NULL) OR ("vxj1"."paid_at" = $2 AND "items"."id" > $3)) `},
		"from NULL":    {nil, ` WHERE (("vxj1"."paid_at" IS NULL AND "items"."id" > $1)) `},
	} {
		after := &testmodel.Item{ID: 7}
		if err := It.Invoice.Fill(after, &testmodel.Invoice{PaidAt: c.paidAt}); err != nil {
			t.Fatal(err)
		}
		text, _ := sqlOK(t)(q.After(after).SQL(ctx))
		if !strings.Contains(text, c.want) {
			t.Errorf("%s:\n got %s\nwant …%s", name, text, c.want)
		}
	}
}
