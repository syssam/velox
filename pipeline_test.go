package velox_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func TestRewriteCanRefuse(t *testing.T) {
	refused := errors.New("refused")
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(context.Context, *velox.Statement) error { return refused }))
	if _, err := db.Query[testmodel.Invoice]().Unbounded().All(ctx); !errors.Is(err, refused) || e.Calls != 0 {
		t.Fatalf("%v, calls %d", err, e.Calls)
	}
}

func TestAroundsWrapInOrder(t *testing.T) {
	var trace []string
	around := func(name string) velox.Around {
		return func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
			if st.SQL == "" {
				t.Error("around ran before rendering")
			}
			trace = append(trace, name+">")
			err := next(ctx)
			trace = append(trace, "<"+name)
			return err
		}
	}
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, velox.WithAround(around("a")), velox.WithAround(around("b")))
	if _, err := db.Query[testmodel.Invoice]().Unbounded().All(ctx); err != nil {
		t.Fatal(err)
	}
	if want := []string{"a>", "b>", "<b", "<a"}; !reflect.DeepEqual(trace, want) {
		t.Errorf("%v, want %v", trace, want)
	}
}

func TestAroundMustCallNext(t *testing.T) {
	lazy := func(context.Context, *velox.Statement, func(context.Context) error) error { return nil }
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, velox.WithAround(lazy))
	if _, err := db.Query[testmodel.Invoice]().Unbounded().All(ctx); err == nil || !strings.Contains(err.Error(), "without calling next") {
		t.Fatalf("%v", err)
	}
}

// TestHooksCannotChangeTheQuery: hooks get their own copies of the statement's
// slices, so editing them in place leaves the query as it was.
func TestHooksCannotChangeTheQuery(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(1), "INV-1"}}}
	scribble := func(_ context.Context, st *velox.Statement) error {
		st.Where[0] = velox.ColumnEQ(st.Entity.Tenant, int64(9))
		st.Columns[0], st.Columns[1] = st.Columns[1], st.Columns[0]
		st.OrderBy[0] = velox.OrderTerm{Column: int(I.Total.I), Desc: true}
		return nil
	}
	db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(scribble))
	q := db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).Columns(I.ID, I.Number).OrderBy(I.ID.Asc()).Limit(5)
	wantQuery, wantArgs := sqlOK(t)(q.SQL(ctx))
	want := `SELECT "number", "id" FROM "invoices" WHERE "org_id" = $1 ORDER BY "total" DESC LIMIT $2`
	for run := range 2 {
		e.Rows = [][]any{{"INV-1", int64(1)}}
		if _, err := q.All(ctx); err != nil {
			t.Fatal(err)
		}
		if e.SQL != want {
			t.Fatalf("run %d:\n got %s\nwant %s", run, e.SQL, want)
		}
		if text, args := sqlOK(t)(q.SQL(ctx)); text != wantQuery || !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("run %d: the hook changed the query: %s %v", run, text, args)
		}
	}
}

// TestHooksCannotChangeTheQueryFromAnAround is the same for around hooks.
func TestHooksCannotChangeTheQueryFromAnAround(t *testing.T) {
	e := &velox.FakeExec{}
	scribble := func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		st.OrderBy[0].Desc = true
		st.Columns[0] = 0
		return next(ctx)
	}
	db := velox.OpenFake(velox.Postgres, e, velox.WithAround(scribble))
	q := db.Query[testmodel.Invoice]().Columns(I.ID).OrderBy(I.ID.Asc()).Limit(1)
	want, _ := sqlOK(t)(q.SQL(ctx))
	for range 2 {
		if _, err := q.All(ctx); err != nil {
			t.Fatal(err)
		}
		if text, _ := sqlOK(t)(q.SQL(ctx)); text != want {
			t.Fatalf("the hook changed the query: %s", text)
		}
	}
}

func TestAroundErrorAfterNextClosesRows(t *testing.T) {
	failed := errors.New("failed")
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e, velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
		if err := next(ctx); err != nil {
			return err
		}
		return failed
	}))
	if _, err := db.Query[testmodel.Invoice]().Unbounded().All(ctx); !errors.Is(err, failed) || e.Calls != 1 || e.Closed != 1 {
		t.Fatalf("%v, calls %d, closed %d", err, e.Calls, e.Closed)
	}
}

func TestAroundCallingNextTwiceClosesTheFirstRows(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
	db := velox.OpenFake(velox.Postgres, e, velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
		if err := next(ctx); err != nil {
			return err
		}
		return next(ctx)
	}))
	got, err := db.Query[testmodel.Invoice]().Unbounded().All(ctx)
	if err != nil || len(got) != 1 {
		t.Fatalf("%v, %v", got, err)
	}
	if e.Calls != 2 || e.Closed != 2 {
		t.Errorf("calls %d, closed %d: want both rows closed, each before its next returned", e.Calls, e.Closed)
	}
}

func TestAroundThatDiscardsNextsErrorIsReportedAsSuch(t *testing.T) {
	e := &velox.FakeExec{Err: errors.New("boom")}
	db := velox.OpenFake(velox.Postgres, e, velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
		_ = next(ctx)
		return nil
	}))
	_, err := db.Query[testmodel.Invoice]().Unbounded().All(ctx)
	if err == nil || !strings.Contains(err.Error(), "discarded the error from next") || strings.Contains(err.Error(), "without calling next") {
		t.Fatalf("%v", err)
	}
}

func TestRowsAreClosedWhenAnIterConsumerBreaks(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1), invoiceRow(2)}}
	db := velox.OpenFake(velox.Postgres, e)
	for _, err := range db.Query[testmodel.Invoice]().Unbounded().Iter(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		break
	}
	if e.Closed != 1 {
		t.Errorf("closed %d, want 1", e.Closed)
	}
}

// TestRewriteCanReplaceTheEntity: a rewrite hook may point the statement at
// its own EntityInfo (a per-tenant table, say). The SQL sent must name that
// entity's table, even when the garbage collector reuses the memory of an
// EntityInfo an earlier statement used.
func TestRewriteCanReplaceTheEntity(t *testing.T) {
	e := &velox.FakeExec{}
	tables := [2]string{"invoices_a", "invoices_b"}
	var chose string
	call := 0
	perTenant := func(_ context.Context, st *velox.Statement) error {
		info := *st.Entity
		info.Table = tables[call%2]
		chose = info.Table
		call++
		st.Entity = &info
		return nil
	}
	db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(perTenant))
	q := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1)
	wrong := 0
	for i := range 3000 {
		if i%7 == 0 { // an odd period, so a reused address comes back with the other table
			runtime.GC()
		}
		if _, err := q.All(ctx); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(e.SQL, `FROM "`+chose+`"`) {
			if wrong == 0 {
				t.Errorf("call %d: the hook chose %s, the SQL sent was %s", i, chose, e.SQL)
			}
			wrong++
		}
	}
	if wrong > 0 {
		t.Errorf("%d of 3000 queries named the wrong table", wrong)
	}

	// The statement whose entity a hook replaced is rendered without the
	// cache: running it again does not grow the cache.
	before := velox.CacheSize(db)
	for range 3 {
		if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if after := velox.CacheSize(db); after != before {
		t.Errorf("cache grew from %d to %d shapes for statements whose entity a hook replaced", before, after)
	}
}

// TestAroundWrapsReadingTheRows: next runs the statement and reads all its
// rows, so an around hook times (or bounds with a deadline) the whole query.
func TestAroundWrapsReadingTheRows(t *testing.T) {
	e := &velox.FakeExec{}
	var scanned, closed int
	inside := false
	db := velox.OpenFake(velox.Postgres, e, velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
		inside = true
		defer func() { inside = false }()
		before := e.Scanned
		err := next(ctx)
		scanned, closed = e.Scanned-before, e.Closed
		return err
	}))

	e.Rows = [][]any{invoiceRow(1), invoiceRow(2)}
	got, err := db.Query[testmodel.Invoice]().Unbounded().All(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v, %v", got, err)
	}
	if scanned != 2 || closed != 1 {
		t.Errorf("All: when next returned, %d rows were scanned and %d rows closed; want 2 and 1", scanned, closed)
	}

	e.Rows = [][]any{{int64(7)}}
	if n, err := db.Query[testmodel.Invoice]().Count(ctx); err != nil || n != 7 {
		t.Fatalf("%d, %v", n, err)
	}
	if scanned != 1 || closed != 2 {
		t.Errorf("Count: when next returned, %d rows were scanned and %d rows closed; want 1 and 2", scanned, closed)
	}

	e.Rows = [][]any{invoiceRow(1), invoiceRow(2), invoiceRow(3)}
	seen := 0
	for _, err := range db.Query[testmodel.Invoice]().Unbounded().Iter(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		if !inside {
			t.Error("Iter ran the loop body outside the around hook")
		}
		if seen++; seen == 2 {
			break
		}
	}
	if seen != 2 || scanned != 2 || closed != 3 {
		t.Errorf("Iter: %d rows seen; when next returned, %d rows were scanned and %d rows closed; want 2, 2 and 3", seen, scanned, closed)
	}
}

// TestAroundCallingNextAgainAfterIterYielded: rows Iter has yielded cannot be
// taken back, so running the statement again (a retry) is an error, reported
// once, and no row is yielded twice.
func TestAroundCallingNextAgainAfterIterYielded(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1), invoiceRow(2)}}
	db := velox.OpenFake(velox.Postgres, e, velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
		if err := next(ctx); err != nil {
			return err
		}
		return next(ctx)
	}))
	var ids []int64
	var errs []error
	for inv, err := range db.Query[testmodel.Invoice]().Unbounded().Iter(ctx) {
		if err != nil {
			errs = append(errs, err)
			continue
		}
		ids = append(ids, inv.ID)
	}
	if !reflect.DeepEqual(ids, []int64{1, 2}) || len(errs) != 1 || !strings.Contains(errs[0].Error(), "velox: testmodel.Invoice: an Around hook called next again") {
		t.Errorf("ids %v, errors %v", ids, errs)
	}
	if e.Calls != 2 || e.Closed != 2 {
		t.Errorf("calls %d, closed %d", e.Calls, e.Closed)
	}
}

// TestRewriteColumnsAreChecked: a column index a rewrite hook sets that the
// entity does not have is an error, not a panic, wherever the hook puts it.
func TestRewriteColumnsAreChecked(t *testing.T) {
	type P = velox.Pred[testmodel.Invoice]
	for _, c := range []struct {
		name   string
		column int
		hook   func(st *velox.Statement)
	}{
		{"condition", 99, func(st *velox.Statement) { st.Where = append(st.Where, velox.ColumnEQ(99, 1)) }},
		{"negative", -1, func(st *velox.Statement) { st.Where = append(st.Where, velox.ColumnEQ(-1, 1)) }},
		{"nested condition", 42, func(st *velox.Statement) {
			inner := velox.Or(P(velox.ColumnEQ(2, 1)), velox.Not(P(velox.ColumnEQ(42, 1))))
			st.Where = append(st.Where, velox.Predicate(velox.And(P(velox.ColumnEQ(3, 1)), inner)))
		}},
		{"columns", 77, func(st *velox.Statement) { st.Columns = []int{2, 77} }},
		{"order", 55, func(st *velox.Statement) { st.OrderBy = append(st.OrderBy, velox.OrderTerm{Column: 55}) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := &velox.FakeExec{}
			db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
				c.hook(st)
				return nil
			}))
			var err error
			func() {
				defer func() {
					if p := recover(); p != nil {
						t.Fatalf("panicked: %v", p)
					}
				}()
				_, err = db.Query[testmodel.Invoice]().Unbounded().All(ctx)
			}()
			want := fmt.Sprintf("velox: testmodel.Invoice: a Rewrite hook set column %d, the entity has 10 columns", c.column)
			if err == nil || err.Error() != want || e.Calls != 0 {
				t.Errorf("%v, calls %d; want %s", err, e.Calls, want)
			}
		})
	}
}
