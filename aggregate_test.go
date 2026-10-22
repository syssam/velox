package velox_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
	"github.com/syssam/velox2/internal/testmodel/kind"
)

func TestAggregateSQL(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{30.0}}}
	db := velox.OpenFake(velox.Postgres, e)
	q := db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).OrderBy(I.ID.Asc()).Limit(5).Offset(2).Columns(I.Number)
	check := func(fn string) {
		t.Helper()
		want := `SELECT ` + fn + `("total") FROM "invoices" WHERE "paid" = $1`
		if e.SQL != want || !reflect.DeepEqual(e.Args, []any{true}) {
			t.Errorf("%s sent %s %v, want %s", fn, e.SQL, e.Args, want)
		}
	}
	if v, err := velox.Sum(ctx, q, I.Total); err != nil || v != 30 {
		t.Fatalf("Sum: %v, %v", v, err)
	}
	check("SUM")
	if v, ok, err := velox.Avg(ctx, q, I.Total); err != nil || !ok || v != 30 {
		t.Fatalf("Avg: %v, %v, %v", v, ok, err)
	}
	check("AVG")
	if v, ok, err := velox.Min(ctx, q, I.Total); err != nil || !ok || v != 30 {
		t.Fatalf("Min: %v, %v, %v", v, ok, err)
	}
	check("MIN")
	if v, ok, err := velox.Max(ctx, q, I.Total); err != nil || !ok || v != 30 {
		t.Fatalf("Max: %v, %v, %v", v, ok, err)
	}
	check("MAX")
}

// A NULL aggregate (no row matched, or only NULLs) is 0 for Sum and false for
// the others.
func TestAggregateNull(t *testing.T) {
	for name, rows := range map[string][][]any{"NULL": {{nil}}, "no row": nil} {
		e := &velox.FakeExec{Rows: rows}
		db := velox.OpenFake(velox.Postgres, e)
		q := db.Query[testmodel.Invoice]()
		if v, err := velox.Sum(ctx, q, I.Total); err != nil || v != 0 {
			t.Errorf("%s: Sum %v, %v", name, v, err)
		}
		if v, ok, err := velox.Avg(ctx, q, I.Total); err != nil || ok || v != 0 {
			t.Errorf("%s: Avg %v, %v, %v", name, v, ok, err)
		}
		if v, ok, err := velox.Min(ctx, q, I.Total); err != nil || ok || v != 0 {
			t.Errorf("%s: Min %v, %v, %v", name, v, ok, err)
		}
		if v, ok, err := velox.Max(ctx, q, I.Total); err != nil || ok || v != 0 {
			t.Errorf("%s: Max %v, %v, %v", name, v, ok, err)
		}
	}
	e := &velox.FakeExec{Rows: [][]any{{int64(350)}}}
	db := velox.OpenFake(velox.Postgres, e)
	if v, ok, err := velox.Max(ctx, db.Query[testmodel.Sample](), testmodel.Samples.Price); err != nil || !ok || v != kind.Cents(350) {
		t.Errorf("a named type: %v, %v, %v", v, ok, err)
	}
}

func TestAggregateRefusals(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{1.0}}}
	db := velox.OpenFake(velox.Postgres, e)
	after := db.Query[testmodel.Invoice]().After(&testmodel.Invoice{ID: 3})
	if _, err := velox.Sum(ctx, after, I.Total); err == nil || !strings.Contains(err.Error(), "After needs an OrderBy") {
		t.Errorf("Sum: %v", err)
	}
	if _, _, err := velox.Avg(ctx, after, I.Total); err == nil {
		t.Error("Avg: no error")
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
	for _, opts := range [][]velox.Option{nil, {velox.WithAround(func(c context.Context, _ *velox.Statement, next func(context.Context) error) error { return next(c) })}} {
		db := velox.OpenFake(velox.Postgres, e, opts...)
		if _, err := velox.Sum(context.Background(), db.Query[testmodel.Invoice](), I.Total); !errors.Is(err, velox.ErrNoTenant) {
			t.Errorf("no tenant (hooks %d): %v", len(opts), err)
		}
		if _, _, err := velox.Max(context.Background(), db.Query[testmodel.Invoice](), I.Total); !errors.Is(err, velox.ErrNoTenant) {
			t.Errorf("no tenant, Max (hooks %d): %v", len(opts), err)
		}
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
	// The tenant scope applies.
	if _, err := velox.Sum(tenant(int64(7)), velox.OpenFake(velox.Postgres, e).Query[testmodel.Invoice](), I.Total); err != nil {
		t.Fatal(err)
	}
	if want := `SELECT SUM("total") FROM "invoices" WHERE "org_id" = $1`; e.SQL != want || !reflect.DeepEqual(e.Args, []any{int64(7)}) {
		t.Errorf("tenant: %s %v", e.SQL, e.Args)
	}
	// A Rewrite hook may not change the aggregate, and may not leave it
	// without its one column.
	clear := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error { st.Aggregate = 0; return nil })
	rdb := velox.OpenFake(velox.Postgres, e, clear)
	calls := e.Calls
	_, err := velox.Sum(ctx, rdb.Query[testmodel.Invoice](), I.Total)
	if err == nil || err.Error() != "velox: testmodel.Invoice: a Rewrite hook changed the statement's Aggregate" || e.Calls != calls {
		t.Errorf("Rewrite: %v", err)
	}
	// Count and the other reads are unaffected by an unrelated hook.
	nop := velox.WithRewrite(func(context.Context, *velox.Statement) error { return nil })
	if _, err := velox.Sum(ctx, velox.OpenFake(velox.Postgres, e, nop).Query[testmodel.Invoice](), I.Total); err != nil {
		t.Errorf("a harmless hook: %v", err)
	}
	cols := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error { st.Columns = nil; return nil })
	if _, err := velox.Sum(ctx, velox.OpenFake(velox.Postgres, e, cols).Query[testmodel.Invoice](), I.Total); err == nil || !strings.Contains(err.Error(), "aggregate reads one column") {
		t.Errorf("a hook that drops the column: %v", err)
	}
}

// An aggregate is not capped by WithMaxRows, and needs no Limit.
func TestAggregateIsNotRowCapped(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{3.0}}}
	db := velox.OpenFake(velox.Postgres, e, velox.WithMaxRows(2))
	if v, err := velox.Sum(ctx, db.Query[testmodel.Invoice](), I.Total); err != nil || v != 3 {
		t.Fatalf("%v, %v", v, err)
	}
	if strings.Contains(e.SQL, "LIMIT") {
		t.Errorf("limited: %s", e.SQL)
	}
}

// An aggregate takes the tenant scope and the Read policy as Count does.
func TestAggregateIsPolicyScoped(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(3)}}}
	db := velox.OpenFake(velox.Postgres, e)
	if v, err := velox.Sum(asUser("ann"), db.Query[testmodel.Ticket](), Tk.InvoiceID); err != nil || v != 3 {
		t.Fatalf("%v, %v", v, err)
	}
	wantSent(t, e, `SELECT SUM("invoice_id") FROM "tickets" WHERE "org_id" = $1 AND "owner" = $2`, int64(1), "ann")
}
