package apitest

import (
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

func TestAfter(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	last := &testmodel.Invoice{ID: 7, Total: 2.5}
	text, args := sqlOK(t)(db.Query[testmodel.Invoice]().OrderBy(I.Total.Desc(), I.ID.Asc()).After(last).Limit(10).SQL(ctx))
	if !strings.HasSuffix(text, ` WHERE ("total" < $1 OR ("total" = $2 AND "id" > $3)) ORDER BY "total" DESC, "id" LIMIT 10`) {
		t.Errorf("%s", text)
	}
	if len(args) != 3 || args[0] != 2.5 || args[1] != 2.5 || args[2] != int64(7) {
		t.Errorf("%v", args)
	}
	text, _ = sqlOK(t)(db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).OrderBy(I.ID.Asc()).After(last).Limit(10).SQL(ctx))
	if !strings.Contains(text, `WHERE "paid" AND ("id" > $1) ORDER BY "id"`) {
		t.Errorf("%s", text)
	}
}

// TestAfterRefusesUnsafeOrderings is Review Focus 5.
func TestAfterRefusesUnsafeOrderings(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	last := &testmodel.Invoice{ID: 1}
	for name, c := range map[string]struct {
		q    velox.Query[testmodel.Invoice]
		want string
	}{
		"no order":   {db.Query[testmodel.Invoice]().After(last), "After needs an OrderBy"},
		"not unique": {db.Query[testmodel.Invoice]().OrderBy(I.Total.Asc()).After(last), "After needs an OrderBy that includes the primary key or a unique index"},
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

// TestAfterNullable: an order by a nullable column puts NULL after every
// value, ascending, and before them, descending, on every dialect, and After
// pages over it: from a value, the rows after it include the NULLs ascending;
// from a NULL, only the NULLs ascending, and every value descending.
func TestAfterNullable(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	at := t0
	for name, c := range map[string]struct {
		q    velox.Query[testmodel.Invoice]
		want string
	}{
		"asc from a value": {db.Query[testmodel.Invoice]().OrderBy(I.PaidAt.Asc(), I.ID.Asc()).After(&testmodel.Invoice{ID: 7, PaidAt: &at}),
			` WHERE ("paid_at" > $1 OR "paid_at" IS NULL OR ("paid_at" = $2 AND "id" > $3)) ORDER BY "paid_at", "id" LIMIT 10`},
		"asc from NULL": {db.Query[testmodel.Invoice]().OrderBy(I.PaidAt.Asc(), I.ID.Asc()).After(&testmodel.Invoice{ID: 7}),
			` WHERE (("paid_at" IS NULL AND "id" > $1)) ORDER BY "paid_at", "id" LIMIT 10`},
		"desc from a value": {db.Query[testmodel.Invoice]().OrderBy(I.PaidAt.Desc(), I.ID.Desc()).After(&testmodel.Invoice{ID: 7, PaidAt: &at}),
			` WHERE ("paid_at" < $1 OR ("paid_at" = $2 AND "id" < $3)) ORDER BY "paid_at" DESC, "id" DESC LIMIT 10`},
		"desc from NULL": {db.Query[testmodel.Invoice]().OrderBy(I.PaidAt.Desc(), I.ID.Asc()).After(&testmodel.Invoice{ID: 7}),
			` WHERE ("paid_at" IS NOT NULL OR ("paid_at" IS NULL AND "id" > $1)) ORDER BY "paid_at" DESC, "id" LIMIT 10`},
		"second term": {db.Query[testmodel.Invoice]().OrderBy(I.OrgID.Asc(), I.PaidAt.Asc(), I.ID.Asc()).After(&testmodel.Invoice{ID: 7, OrgID: 2, PaidAt: &at}),
			` WHERE ("org_id" > $1 OR ("org_id" = $2 AND ("paid_at" > $3 OR "paid_at" IS NULL)) OR ("org_id" = $4 AND "paid_at" = $5 AND "id" > $6)) ORDER BY "org_id", "paid_at", "id" LIMIT 10`},
	} {
		text, _ := sqlOK(t)(c.q.Limit(10).SQL(ctx))
		if !strings.HasSuffix(text, c.want) {
			t.Errorf("%s:\n got %s\nwant …%s", name, text, c.want)
		}
	}
	// MySQL and SQLite put NULL first, ascending: an IS NULL term puts it last.
	for _, c := range []struct {
		d          velox.Dialect
		desc       bool
		want, asPG string
	}{
		{velox.MySQL, false, " ORDER BY `paid_at` IS NULL, `paid_at`, `id`", ` ORDER BY "paid_at", "id"`},
		{velox.SQLite, true, ` ORDER BY "paid_at" IS NULL DESC, "paid_at" DESC, "id"`, ` ORDER BY "paid_at" DESC, "id"`},
	} {
		for d, want := range map[velox.Dialect]string{c.d: c.want, velox.Postgres: c.asPG} {
			o := I.PaidAt.Asc()
			if c.desc {
				o = I.PaidAt.Desc()
			}
			text, _ := sqlOK(t)(openFake(d, &fakeconn.Exec{}).Query[testmodel.Invoice]().OrderBy(o, I.ID.Asc()).Limit(10).SQL(ctx))
			if !strings.Contains(text, want+" LIMIT ") {
				t.Errorf("%v:\n got %s\nwant …%s", d, text, want)
			}
		}
	}
}

func TestAfterNilRow(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	q := db.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).After(nil)
	if _, err := q.All(ctx); err == nil || !strings.HasPrefix(err.Error(), "velox: ") || !strings.HasSuffix(err.Error(), ": After was given a nil row") { //velox:unbounded-ok
		t.Errorf("%v", err)
	}
	if text, _, err := q.SQL(ctx); text != "" || err == nil || !strings.HasSuffix(err.Error(), ": After was given a nil row") { //velox:unbounded-ok
		t.Errorf("SQL(ctx): %q %v", text, err)
	}
}

func TestAfterRefusesSliceColumns(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	_, err := db.Query[testmodel.Invoice]().OrderBy(I.Data.Asc(), I.ID.Asc()).After(&testmodel.Invoice{ID: 1}).Limit(10).All(ctx)
	if err == nil || !strings.Contains(err.Error(), "After cannot page over the column data, which can be NULL") {
		t.Errorf("%v", err)
	}
}

func TestAfterMixedDirections(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	last := &testmodel.Invoice{ID: 7, OrgID: 3, Total: 2.5}
	text, args := sqlOK(t)(db.Query[testmodel.Invoice]().OrderBy(I.OrgID.Asc(), I.Total.Desc(), I.ID.Asc()).After(last).Limit(10).SQL(ctx))
	want := ` WHERE ("org_id" > $1 OR ("org_id" = $2 AND "total" < $3) OR ("org_id" = $4 AND "total" = $5 AND "id" > $6)) ORDER BY "org_id", "total" DESC, "id" LIMIT 10`
	if !strings.HasSuffix(text, want) {
		t.Errorf("%s", text)
	}
	wantArgs := []any{int64(3), int64(3), 2.5, int64(3), 2.5, int64(7)}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("%#v", args)
	}
}

func TestAfterSecondCaseArgs(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	_, args := sqlOK(t)(db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).OrderBy(I.ID.Asc()).After(&testmodel.Invoice{ID: 7}).Limit(10).SQL(ctx))
	if !reflect.DeepEqual(args, []any{int64(7)}) {
		t.Errorf("%#v", args)
	}
}

// TestAfterReachesTheDatabase: All, Iter and Count send After's condition, and
// Count returns the refusal for an ordering After cannot page over.
func TestAfterReachesTheDatabase(t *testing.T) {
	cond := `("id" > $1)`
	last := &testmodel.Invoice{ID: 7}
	e := &fakeconn.Exec{Rows: [][]any{invoiceRow(8)}}
	db := openFake(velox.Postgres, e)
	q := db.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).After(last).Limit(10)
	if _, err := q.All(ctx); err != nil || !strings.Contains(e.SQL, cond) || e.Args[0] != int64(7) {
		t.Errorf("All: %v, %s %v", err, e.SQL, e.Args)
	}
	e.SQL, e.Args = "", nil
	for _, err := range q.Iter(ctx) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(e.SQL, cond) || e.Args[0] != int64(7) {
		t.Errorf("Iter: %s %v", e.SQL, e.Args)
	}
	e.SQL, e.Args = "", nil
	e.Rows = [][]any{{int64(1)}}
	if n, err := q.Count(ctx); err != nil || n != 1 || !strings.Contains(e.SQL, "COUNT(*)") || !strings.Contains(e.SQL, cond) || strings.Contains(e.SQL, "ORDER BY") {
		t.Errorf("Count: %d %v, %s %v", n, err, e.SQL, e.Args)
	}
	calls := e.Calls
	if _, err := db.Query[testmodel.Invoice]().After(last).Count(ctx); err == nil || !strings.Contains(err.Error(), "After needs an OrderBy") || e.Calls != calls {
		t.Errorf("Count refusal: %v", err)
	}
}

// TestAfterWithRelations: After's condition is on the owner query only.
func TestAfterWithRelations(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{{invoiceRow(8)}, {itemRow(10, 8, "A", 1)}}}
	db := openFake(velox.Postgres, e)
	_, err := db.Query[testmodel.Invoice]().With(I.Items).OrderBy(I.ID.Asc()).After(&testmodel.Invoice{ID: 7}).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Log) != 2 || !strings.Contains(e.Log[0], ` WHERE ("id" > $1) ORDER BY "id"`) {
		t.Fatalf("%q", e.Log)
	}
	if want := `SELECT "id", "invoice_id", "sku", "qty" FROM "items" WHERE "invoice_id" = ANY($1) ORDER BY "id"`; e.Log[1] != want {
		t.Errorf("\n got %s\nwant %s", e.Log[1], want)
	}
}
