package velox_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

var (
	ctx = velox.AllTenants(context.Background(), "velox tests")
	t0  = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	I   = testmodel.Invoices
	It  = testmodel.Items
)

const allCols = `"created_at", "updated_at", "id", "org_id", "number", "total", "paid", "paid_at", "note", "data"`

func invoiceRow(id int64) []any {
	return []any{t0, t0, id, int64(1), fmt.Sprintf("INV-%d", id), 12.5, true, t0, nil, []byte("d")}
}

func TestAllScansEveryColumn(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1), invoiceRow(2)}}
	db := velox.OpenFake(velox.Postgres, e)
	got, err := db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1)).OrderBy(I.ID.Asc()).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].ID != 2 || got[1].Number != "INV-2" || got[1].PaidAt == nil ||
		!got[1].PaidAt.Equal(t0) || got[1].Note != nil || string(got[1].Data) != "d" || !got[1].CreatedAt.Equal(t0) {
		t.Fatalf("%+v", got)
	}
	want := `SELECT ` + allCols + ` FROM "invoices" WHERE "org_id" = $1 ORDER BY "id" LIMIT $2`
	if e.SQL != want {
		t.Errorf("SQL\n got %s\nwant %s", e.SQL, want)
	}
	if !reflect.DeepEqual(e.Args, []any{int64(1), 10}) {
		t.Errorf("args %#v", e.Args)
	}
}

func TestOne(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(3)}}
	db := velox.OpenFake(velox.Postgres, e)
	got, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(3)).One(ctx)
	if err != nil || got.ID != 3 {
		t.Fatalf("%+v, %v", got, err)
	}
	if !strings.HasSuffix(e.SQL, `WHERE "id" = $1 LIMIT $2`) || e.Args[1] != 2 {
		t.Errorf("One must ask for 2 rows: %s %v", e.SQL, e.Args)
	}
	e.Rows = nil
	if _, err := db.Query[testmodel.Invoice]().One(ctx); !errors.Is(err, velox.ErrNotFound) || !strings.Contains(err.Error(), "testmodel.Invoice") {
		t.Errorf("no rows: %v", err)
	}
	e.Rows = [][]any{invoiceRow(1), invoiceRow(2)}
	if _, err := db.Query[testmodel.Invoice]().One(ctx); !errors.Is(err, velox.ErrNotSingular) {
		t.Errorf("two rows: %v", err)
	}
}

func TestColumnsAndOmit(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(5), "INV-5"}}}
	db := velox.OpenFake(velox.Postgres, e)
	got, err := db.Query[testmodel.Invoice]().Columns(I.ID, I.Number).Unbounded().All(ctx)
	if err != nil || len(got) != 1 || got[0].ID != 5 || got[0].Number != "INV-5" || got[0].Total != 0 {
		t.Fatalf("%+v, %v", got, err)
	}
	if want := `SELECT "id", "number" FROM "invoices"`; e.SQL != want {
		t.Errorf("Columns SQL %s", e.SQL)
	}
	row := invoiceRow(6)
	e.Rows = [][]any{append(row[:8:8], row[9])}
	got, err = db.Query[testmodel.Invoice]().Omit(I.Note).Unbounded().All(ctx)
	if err != nil || got[0].ID != 6 || got[0].Note != nil {
		t.Fatalf("%+v, %v", got, err)
	}
	if want := `SELECT "created_at", "updated_at", "id", "org_id", "number", "total", "paid", "paid_at", "data" FROM "invoices"`; e.SQL != want {
		t.Errorf("Omit SQL %s", e.SQL)
	}
}

func TestCount(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(3)}}}
	db := velox.OpenFake(velox.Postgres, e)
	n, err := db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).OrderBy(I.ID.Asc()).Limit(5).Count(ctx)
	if err != nil || n != 3 {
		t.Fatalf("%d, %v", n, err)
	}
	if want := `SELECT COUNT(*) FROM "invoices" WHERE "paid" = $1`; e.SQL != want {
		t.Errorf("SQL %s", e.SQL)
	}
}

func TestIterStopsEarly(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1), invoiceRow(2), invoiceRow(3)}}
	db := velox.OpenFake(velox.Postgres, e)
	var seen []int64
	for inv, err := range db.Query[testmodel.Invoice]().Unbounded().Iter(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, inv.ID)
		if len(seen) == 2 {
			break
		}
	}
	if !reflect.DeepEqual(seen, []int64{1, 2}) {
		t.Errorf("%v", seen)
	}
}

// TestIterBreakClosesTheRows: leaving an Iter loop early must close the rows,
// or the statement's connection is never returned to the pool. It covers both
// execution paths (no hooks, and an Around hook that wraps the loop body).
func TestIterBreakClosesTheRows(t *testing.T) {
	for name, opts := range map[string][]velox.Option{
		"no hooks": nil,
		"with Around": {velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
			return next(ctx)
		})},
	} {
		e := &velox.FakeExec{Rows: [][]any{invoiceRow(1), invoiceRow(2), invoiceRow(3)}}
		db := velox.OpenFake(velox.Postgres, e, opts...)
		seen := 0
		for _, err := range db.Query[testmodel.Invoice]().Unbounded().Iter(ctx) {
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			seen++
			break // on the first row, with two more unread
		}
		if seen != 1 || e.Calls != 1 || e.Closed != 1 {
			t.Errorf("%s: saw %d rows, %d queries, %d closed; want 1, 1, 1", name, seen, e.Calls, e.Closed)
		}
	}
}

func TestSQLNeedsNoDatabase(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.MySQL, e)
	text, args := sqlOK(t)(db.Query[testmodel.Invoice]().Where(I.Number.HasPrefix("A")).Limit(3).SQL(ctx))
	want := "SELECT `created_at`, `updated_at`, `id`, `org_id`, `number`, `total`, `paid`, `paid_at`, `note`, `data` FROM `invoices` WHERE `number` LIKE ? ESCAPE '\\\\' LIMIT ?"
	if text != want || !reflect.DeepEqual(args, []any{"A%", 3}) || e.Calls != 0 {
		t.Errorf("%s %v calls %d", text, args, e.Calls)
	}
}

// TestQueryCanRunAgain is Review Focus 4.
func TestQueryCanRunAgain(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
	tenant := func(_ context.Context, st *velox.Statement) error {
		st.Where = append(st.Where, velox.ColumnEQ(st.Entity.Tenant, int64(7)))
		return nil
	}
	db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(tenant))
	q := db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).Limit(5)
	want := `SELECT ` + allCols + ` FROM "invoices" WHERE "paid" = $1 AND "org_id" = $2 LIMIT $3`
	for run := range 3 {
		if _, err := q.All(ctx); err != nil {
			t.Fatal(err)
		}
		if e.SQL != want || !reflect.DeepEqual(e.Args, []any{true, int64(7), 5}) {
			t.Fatalf("run %d:\n got %s %v\nwant %s", run, e.SQL, e.Args, want)
		}
	}
	// SQL(ctx) runs the hook too, and shows its condition once.
	if text, _ := sqlOK(t)(q.SQL(ctx)); text != want {
		t.Errorf("SQL(ctx):\n got %s\nwant %s", text, want)
	}
}

type staleEntity struct {
	ID   int64
	Name string
}

func (*staleEntity) Schema(*velox.Schema) {}

var staleMeta velox.MetaOnce[staleEntity]

func (*staleEntity) VeloxMeta() *velox.Meta[staleEntity] {
	return staleMeta.Get(func() velox.Shape[staleEntity] {
		return velox.Shape[staleEntity]{Columns: []string{"ID"}}
	})
}

func TestStaleGeneratedCodeIsReportedOnUse(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	_, err := db.Query[staleEntity]().All(ctx)
	if !errors.Is(err, velox.ErrStale) || !strings.Contains(err.Error(), "velox_test.staleEntity") || e.Calls != 0 {
		t.Fatalf("%v, calls %d", err, e.Calls)
	}
	if text, _, err := db.Query[staleEntity]().SQL(ctx); text != "" || !errors.Is(err, velox.ErrStale) {
		t.Errorf("SQL of an invalid entity: %q %v", text, err)
	}
}

// TestChainingCopiesTheQuery: extending a query returns a new one and leaves
// the query extended as it was, so one base query can be extended many ways.
func TestChainingCopiesTheQuery(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	base := db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1)).OrderBy(I.ID.Asc()).Unbounded()
	baseSQL, baseArgs := sqlOK(t)(base.SQL(ctx))

	paid := base.Where(I.Paid.EQ(true))
	big := base.Where(I.Total.GT(100))
	if text, args := sqlOK(t)(paid.SQL(ctx)); !strings.HasSuffix(text, `WHERE "org_id" = $1 AND "paid" = $2 ORDER BY "id"`) || !reflect.DeepEqual(args, []any{int64(1), true}) {
		t.Errorf("first branch: %s %v", text, args)
	}
	if text, args := sqlOK(t)(big.SQL(ctx)); !strings.HasSuffix(text, `WHERE "org_id" = $1 AND "total" > $2 ORDER BY "id"`) || !reflect.DeepEqual(args, []any{int64(1), float64(100)}) {
		t.Errorf("second branch: %s %v", text, args)
	}

	byTotal := base.OrderBy(I.Total.Desc())
	byNumber := base.OrderBy(I.Number.Asc())
	if text, _ := sqlOK(t)(byTotal.SQL(ctx)); !strings.HasSuffix(text, `ORDER BY "id", "total" DESC`) {
		t.Errorf("first ordering: %s", text)
	}
	if text, _ := sqlOK(t)(byNumber.SQL(ctx)); !strings.HasSuffix(text, `ORDER BY "id", "number"`) {
		t.Errorf("second ordering: %s", text)
	}

	// Three conditions and three orderings leave the base's slices with room
	// for a fourth, which the two branches must not both write to.
	roomy := db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1), I.Paid.EQ(true), I.Total.GT(1)).
		OrderBy(I.ID.Asc(), I.Total.Desc(), I.Number.Asc()).Unbounded()
	low, high := roomy.Where(I.ID.LT(10)), roomy.Where(I.ID.GT(20))
	if text, _ := sqlOK(t)(low.SQL(ctx)); !strings.Contains(text, `AND "id" < $4 ORDER`) {
		t.Errorf("first branch of a base with room: %s", text)
	}
	if text, _ := sqlOK(t)(high.SQL(ctx)); !strings.Contains(text, `AND "id" > $4 ORDER`) {
		t.Errorf("second branch of a base with room: %s", text)
	}
	byPaid, byOrg := roomy.OrderBy(I.Paid.Asc()), roomy.OrderBy(I.OrgID.Asc())
	if text, _ := sqlOK(t)(byPaid.SQL(ctx)); !strings.HasSuffix(text, `ORDER BY "id", "total" DESC, "number", "paid"`) {
		t.Errorf("first ordering of a base with room: %s", text)
	}
	if text, _ := sqlOK(t)(byOrg.SQL(ctx)); !strings.HasSuffix(text, `ORDER BY "id", "total" DESC, "number", "org_id"`) {
		t.Errorf("second ordering of a base with room: %s", text)
	}

	base.Limit(5).Offset(2).Unbounded().Columns(I.ID).Omit(I.Note)
	if text, args := sqlOK(t)(base.SQL(ctx)); text != baseSQL || !reflect.DeepEqual(args, baseArgs) {
		t.Errorf("the base query changed:\n got %s %v\nwant %s %v", text, args, baseSQL, baseArgs)
	}
}

// TestBaseQueryIsSafeToExtendConcurrently; run it with -race.
func TestBaseQueryIsSafeToExtendConcurrently(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	base := db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1))
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 100 {
				id := int64(g*1000 + i)
				q := base.Where(I.ID.EQ(id)).OrderBy(I.ID.Desc())
				text, args, err := q.Unbounded().SQL(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				if want := `WHERE "org_id" = $1 AND "id" = $2 ORDER BY "id" DESC`; !strings.HasSuffix(text, want) || !reflect.DeepEqual(args, []any{int64(1), id}) {
					t.Errorf("goroutine %d: %s %v", g, text, args)
					return
				}
				if _, err := q.Unbounded().All(ctx); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
}

type panickyEntity struct{ ID int64 }

func (*panickyEntity) Schema(*velox.Schema) { panic("no schema today") }

var panickyMeta velox.MetaOnce[panickyEntity]

func (*panickyEntity) VeloxMeta() *velox.Meta[panickyEntity] {
	return panickyMeta.Get(func() velox.Shape[panickyEntity] {
		return velox.Shape[panickyEntity]{Columns: []string{"ID"}, Addr: func(x *panickyEntity, _ int) any { return &x.ID }}
	})
}

type addrlessEntity struct{ ID int64 }

func (*addrlessEntity) Schema(*velox.Schema) {}

var addrlessMeta velox.MetaOnce[addrlessEntity]

func (*addrlessEntity) VeloxMeta() *velox.Meta[addrlessEntity] {
	return addrlessMeta.Get(func() velox.Shape[addrlessEntity] {
		return velox.Shape[addrlessEntity]{Columns: []string{"ID"}}
	})
}

// TestBrokenMetadataIsAnErrorOnEveryUse: a Schema method that panics, or
// generated code with no scan plan, makes every query on the entity return a
// *SchemaError naming it, not just the first, and none panics.
func TestBrokenMetadataIsAnErrorOnEveryUse(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(1)}}}
	db := velox.OpenFake(velox.Postgres, e)
	check := func(name, entity, reason string, run func() error) {
		t.Helper()
		for i := range 2 {
			var err error
			panicked := true
			func() {
				defer func() {
					if p := recover(); p != nil {
						t.Errorf("%s, query %d panicked: %v", name, i+1, p)
					}
				}()
				err = run()
				panicked = false
			}()
			if panicked {
				continue
			}
			var se *velox.SchemaError
			if !errors.As(err, &se) || se.Entity != entity || !strings.Contains(se.Reason, reason) || !strings.HasPrefix(err.Error(), "velox: ") {
				t.Errorf("%s, query %d: %v", name, i+1, err)
			}
		}
	}
	check("Schema panics", "velox_test.panickyEntity", "Schema panicked: no schema today", func() error {
		_, err := db.Query[panickyEntity]().All(ctx)
		return err
	})
	check("no Addr", "velox_test.addrlessEntity", "Addr", func() error {
		_, err := db.Query[addrlessEntity]().All(ctx)
		return err
	})
	if e.Calls != 0 {
		t.Errorf("%d queries reached the database", e.Calls)
	}
}

func TestHasThroughTheAPI(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	text, args := sqlOK(t)(db.Query[testmodel.Invoice]().Where(I.Items.HasWhere(It.Qty.GT(2)), velox.Not(I.Items.Has())).Limit(5).SQL(ctx))
	want := `SELECT ` + allCols + ` FROM "invoices" WHERE EXISTS (SELECT 1 FROM "items" AS "vx1" WHERE "vx1"."invoice_id" = "invoices"."id" AND "qty" > $1) AND NOT (EXISTS (SELECT 1 FROM "items" AS "vx1" WHERE "vx1"."invoice_id" = "invoices"."id")) LIMIT $2`
	if text != want || len(args) != 2 {
		t.Errorf("\n got %s\nwant %s", text, want)
	}
	if _, err := db.Delete[testmodel.Invoice]().Where(I.Items.Has()).Exec(ctx); err != nil || !strings.Contains(e.SQL, `DELETE FROM "invoices" WHERE EXISTS`) {
		t.Errorf("%v %s", err, e.SQL)
	}
}

func TestABrokenRelationIsAnError(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	bad := velox.Rel[testmodel.Invoice, testmodel.Item]{I: 9}
	badItem := velox.Rel[testmodel.Item, testmodel.Invoice]{I: 9}
	const msg = ": has no relation 9"
	check := func(name string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s: %v, want an error containing %q", name, err, msg)
		}
	}
	_, err := db.Query[testmodel.Invoice]().With(bad).Limit(1).All(ctx)
	if want := "velox: testmodel.Invoice: has no relation 9; run go tool velox-gen"; err == nil || err.Error() != want {
		t.Errorf("With: %v, want %q", err, want)
	}
	for name, p := range map[string]velox.Pred[testmodel.Invoice]{
		"direct":            bad.Has(),
		"inside Not":        velox.Not(bad.Has()),
		"inside HasWhere":   I.Items.HasWhere(badItem.Has()),
		"inside And and Or": velox.Or(I.Paid.EQ(true), velox.And(bad.Has())),
	} {
		q := db.Query[testmodel.Invoice]().Where(p).Limit(1)
		_, err := q.All(ctx)
		check(name+" All", err)
		_, err = q.One(ctx)
		check(name+" One", err)
		_, err = q.Count(ctx)
		check(name+" Count", err)
		for _, err := range q.Iter(ctx) {
			check(name+" Iter", err)
		}
		text, args, err := q.SQL(ctx)
		check(name+" Query.SQL", err)
		if text != "" || args != nil {
			t.Errorf("%s Query.SQL: %q %v", name, text, args)
		}
		u := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(p)
		_, err = u.Exec(ctx)
		check(name+" Update", err)
		text, _, err = u.SQL(ctx)
		check(name+" Update.SQL", err)
		if text != "" {
			t.Errorf("%s Update.SQL: %q", name, text)
		}
		d := db.Delete[testmodel.Invoice]().Where(p)
		_, err = d.Exec(ctx)
		check(name+" Delete", err)
		text, _, err = d.SQL(ctx)
		check(name+" Delete.SQL", err)
		if text != "" {
			t.Errorf("%s Delete.SQL: %q", name, text)
		}
	}
}
