package velox_test

import (
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func TestAfter(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	last := &testmodel.Invoice{ID: 7, Total: 2.5}
	text, args := db.Query[testmodel.Invoice]().OrderBy(I.Total.Desc(), I.ID.Asc()).After(last).Limit(10).SQL()
	if !strings.HasSuffix(text, ` WHERE ("total" < $1 OR ("total" = $2 AND "id" > $3)) ORDER BY "total" DESC, "id" LIMIT $4`) {
		t.Errorf("%s", text)
	}
	if len(args) != 4 || args[0] != 2.5 || args[1] != 2.5 || args[2] != int64(7) {
		t.Errorf("%v", args)
	}
	text, _ = db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).OrderBy(I.ID.Asc()).After(last).Limit(10).SQL()
	if !strings.Contains(text, `WHERE "paid" = $1 AND ("id" > $2) ORDER BY "id"`) {
		t.Errorf("%s", text)
	}
}

// TestAfterRefusesUnsafeOrderings is Review Focus 5.
func TestAfterRefusesUnsafeOrderings(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	last := &testmodel.Invoice{ID: 1}
	for name, c := range map[string]struct {
		q    velox.Query[testmodel.Invoice]
		want string
	}{
		"no order":   {db.Query[testmodel.Invoice]().After(last), "After needs an OrderBy"},
		"not unique": {db.Query[testmodel.Invoice]().OrderBy(I.Total.Asc()).After(last), "After needs an OrderBy that includes the primary key or a unique index"},
		"nullable":   {db.Query[testmodel.Invoice]().OrderBy(I.PaidAt.Asc(), I.ID.Asc()).After(last), "After cannot page over the nullable column paid_at"},
	} {
		_, err := c.q.Limit(10).All(ctx)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A unique index other than the primary key will do.
	if _, err := db.Query[testmodel.Invoice]().OrderBy(I.OrgID.Asc(), I.Number.Asc()).After(last).Limit(10).All(ctx); err != nil {
		t.Errorf("unique index: %v", err)
	}
}

func TestParameterLimit(t *testing.T) {
	defer velox.SetMaxParams(velox.Postgres, 3)()
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	_, err := db.Query[testmodel.Invoice]().Where(I.ID.In(1, 2, 3, 4)).Limit(1).All(ctx)
	if err == nil || !strings.Contains(err.Error(), "the statement has 5 parameters; postgres allows 3") || e.Calls != 0 {
		t.Errorf("%v, calls %d", err, e.Calls)
	}
}
