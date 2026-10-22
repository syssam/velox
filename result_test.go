package velox_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// resultOf returns a PostgreSQL DB on e, with opts, whose outermost Around
// hook records each statement's Result in *got once next has returned.
func resultOf(e *velox.FakeExec, got *velox.Result, opts ...velox.Option) *velox.DB {
	record := velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		err := next(ctx)
		*got = st.Result
		return err
	})
	return velox.OpenFake(velox.Postgres, e, append([]velox.Option{record}, opts...)...)
}

func threeInvoices() [][]any { return [][]any{invoiceRow(1), invoiceRow(2), invoiceRow(3)} }

func TestResultOfReads(t *testing.T) {
	var got velox.Result
	e := &velox.FakeExec{Rows: threeInvoices()}
	db := resultOf(e, &got)
	if _, err := db.Query[testmodel.Invoice]().Unbounded().All(ctx); err != nil {
		t.Fatal(err)
	}
	if want := (velox.Result{Rows: 3, Affected: -1}); got != want {
		t.Errorf("All: %+v, want %+v", got, want)
	}
	n := 0
	for _, err := range db.Query[testmodel.Invoice]().Unbounded().Iter(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		if n++; n == 2 {
			break
		}
	}
	got.LoopBody = 0 // TestResultLoopBody checks it
	if want := (velox.Result{Rows: 2, Affected: -1}); got != want {
		t.Errorf("Iter broken after 2 rows: %+v, want %+v", got, want)
	}
	e.Rows = [][]any{{int64(5)}}
	if _, err := db.Query[testmodel.Invoice]().Count(ctx); err != nil {
		t.Fatal(err)
	}
	if want := (velox.Result{Rows: 1, Affected: -1}); got != want {
		t.Errorf("Count: %+v, want %+v", got, want)
	}
}

func TestResultOfWrites(t *testing.T) {
	var got velox.Result
	e := &velox.FakeExec{Affected: 4}
	db := resultOf(e, &got)
	if _, err := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.OrgID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if want := (velox.Result{Rows: 0, Affected: 4}); got != want {
		t.Errorf("Update: %+v, want %+v", got, want)
	}
	e.Rows = [][]any{{int64(9)}}
	inv := testmodel.Invoice{OrgID: 1, Number: "N", Data: []byte("d")}
	if err := db.Insert(&inv).Exec(ctx); err != nil || inv.ID != 9 {
		t.Fatalf("Insert: ID %d, %v", inv.ID, err)
	}
	if want := (velox.Result{Rows: 1, Affected: 1}); got != want {
		t.Errorf("Insert with RETURNING: %+v, want %+v", got, want)
	}
}

func TestResultOfAFailedStatement(t *testing.T) {
	var got velox.Result
	boom := errors.New("boom")
	db := resultOf(&velox.FakeExec{Rows: threeInvoices(), Err: boom}, &got)
	if _, err := db.Query[testmodel.Invoice]().Unbounded().All(ctx); !errors.Is(err, boom) {
		t.Fatalf("err %v, want boom", err)
	}
	if want := (velox.Result{Rows: 0, Affected: -1}); got != want {
		t.Errorf("failed: %+v, want %+v", got, want)
	}
}

func TestResultIsTheLastAttempts(t *testing.T) {
	boom := errors.New("boom")
	var first, second velox.Result
	retry := velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		err := next(ctx)
		first = st.Result
		if err != nil {
			err = next(ctx)
			second = st.Result
		}
		return err
	})
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: threeInvoices(), Errs: []error{boom}}, retry)
	if _, err := db.Query[testmodel.Invoice]().Unbounded().All(ctx); err != nil {
		t.Fatal(err)
	}
	if want := (velox.Result{Rows: 0, Affected: -1}); first != want {
		t.Errorf("first attempt: %+v, want %+v", first, want)
	}
	if want := (velox.Result{Rows: 3, Affected: -1}); second != want {
		t.Errorf("retry: %+v, want %+v", second, want)
	}
}

func TestResultUnderARowCap(t *testing.T) {
	var got velox.Result
	db := resultOf(&velox.FakeExec{Rows: threeInvoices()}, &got, velox.WithMaxRows(2))
	if _, err := db.Query[testmodel.Invoice]().Unbounded().All(ctx); !errors.Is(err, velox.ErrTooManyRows) {
		t.Fatalf("err %v, want ErrTooManyRows", err)
	}
	if want := (velox.Result{Rows: 2, Affected: -1}); got != want {
		t.Errorf("capped: %+v, want %+v", got, want)
	}
}

// TestResultWhenAnInnerHookRefuses: an inner Around hook that returns
// without calling next leaves the outer hooks the Result of a statement that
// did not run, not a zero Result whose Affected reads 0.
func TestResultWhenAnInnerHookRefuses(t *testing.T) {
	var got velox.Result
	refused := errors.New("refused")
	refuse := velox.WithAround(func(context.Context, *velox.Statement, func(context.Context) error) error { return refused })
	db := resultOf(&velox.FakeExec{Rows: threeInvoices()}, &got, refuse)
	if _, err := db.Query[testmodel.Invoice]().Unbounded().All(ctx); !errors.Is(err, refused) {
		t.Fatalf("err %v, want refused", err)
	}
	if want := (velox.Result{Rows: 0, Affected: -1}); got != want {
		t.Errorf("an inner hook refused: %+v, want %+v", got, want)
	}
}

// sleepIter ranges over seq, sleeping d in the loop body of each row, and
// breaks after n rows (n < 0: never).
func sleepIter(t *testing.T, seq func(func(testmodel.Invoice, error) bool), d time.Duration, n int) {
	t.Helper()
	i := 0
	for _, err := range seq {
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(d)
		if i++; i == n {
			break
		}
	}
}

// TestResultLoopBody: with Iter, Result.LoopBody is the time spent in the
// caller's loop body, summed over the rows yielded, the row a loop broke on
// or panicked in included; every other method leaves it 0.
func TestResultLoopBody(t *testing.T) {
	const body = 20 * time.Millisecond
	t.Run("three rows", func(t *testing.T) {
		var got velox.Result
		db := resultOf(&velox.FakeExec{Rows: threeInvoices()}, &got)
		sleepIter(t, db.Query[testmodel.Invoice]().Unbounded().Iter(ctx), body, -1)
		if got.Rows != 3 || got.LoopBody < 3*body {
			t.Errorf("%+v, want 3 rows and LoopBody >= %v", got, 3*body)
		}
	})
	t.Run("broken after the first row", func(t *testing.T) {
		var got velox.Result
		db := resultOf(&velox.FakeExec{Rows: threeInvoices()}, &got)
		sleepIter(t, db.Query[testmodel.Invoice]().Unbounded().Iter(ctx), body, 1)
		if got.Rows != 1 || got.LoopBody < body || got.LoopBody >= 2*body {
			t.Errorf("%+v, want 1 row and %v <= LoopBody < %v", got, body, 2*body)
		}
	})
	t.Run("a panicking loop body", func(t *testing.T) {
		var got velox.Result
		deferred := velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
			defer func() { got = st.Result }()
			return next(ctx)
		})
		db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: threeInvoices()}, deferred)
		func() {
			defer func() {
				if recover() != "boom" {
					t.Error("the loop body's panic did not reach the caller")
				}
			}()
			for range db.Query[testmodel.Invoice]().Unbounded().Iter(ctx) {
				time.Sleep(body)
				panic("boom")
			}
		}()
		if got.LoopBody < body || got.LoopBody >= 2*body {
			t.Errorf("%+v, want %v <= LoopBody < %v", got, body, 2*body)
		}
	})
	t.Run("All, Raw's All and a write", func(t *testing.T) {
		var got velox.Result
		e := &velox.FakeExec{Rows: threeInvoices(), Affected: 1}
		db := resultOf(e, &got)
		if _, err := db.Query[testmodel.Invoice]().Unbounded().All(ctx); err != nil || got.LoopBody != 0 {
			t.Errorf("All: %+v, %v; want LoopBody 0", got, err)
		}
		e.Cols, e.Rows = []string{"id"}, [][]any{{int64(1)}, {int64(2)}}
		if _, err := db.Raw[testmodel.Invoice]("r", "SELECT id FROM invoices").All(ctx); err != nil || got.LoopBody != 0 {
			t.Errorf("Raw's All: %+v, %v; want LoopBody 0", got, err)
		}
		if _, err := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.OrgID.EQ(1)).Exec(ctx); err != nil || got.LoopBody != 0 {
			t.Errorf("Update: %+v, %v; want LoopBody 0", got, err)
		}
	})
	t.Run("not ranged, or refused", func(t *testing.T) {
		calls := 0
		count := velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
			calls++
			return next(ctx)
		})
		db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: threeInvoices()}, count)
		_ = db.Query[testmodel.Invoice]().Unbounded().Iter(ctx) // never ranged: nothing runs
		for _, err := range db.Query[testmodel.Invoice]().With(I.Items).Unbounded().Iter(ctx) {
			if err == nil {
				t.Error("Iter with a With load yielded a row")
			}
		}
		if calls != 0 {
			t.Errorf("the hook ran %d times, want 0", calls)
		}
	})
	t.Run("a retrying hook sees the last attempt's", func(t *testing.T) {
		var first, second velox.Result
		retry := velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
			err := next(ctx)
			first = st.Result
			if err != nil {
				err = next(ctx)
				second = st.Result
			}
			return err
		})
		db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: threeInvoices(), Errs: []error{errors.New("boom")}}, retry)
		sleepIter(t, db.Query[testmodel.Invoice]().Unbounded().Iter(ctx), body, -1)
		if first.LoopBody != 0 || second.Rows != 3 || second.LoopBody < 3*body {
			t.Errorf("first %+v, retry %+v; want LoopBody 0, then 3 rows and >= %v", first, second, 3*body)
		}
	})
	t.Run("a read that fell over", func(t *testing.T) {
		var got velox.Result
		s := openFailover(false, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
			err := next(ctx)
			got = st.Result
			return err
		}))
		s.r0.Rows, s.r0.RowsErr = nil, driver.ErrBadConn
		s.p.Rows = [][]any{invoiceRow(1), invoiceRow(2)}
		for _, err := range s.db.Query[testmodel.Invoice]().Limit(10).Iter(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(body)
		}
		if got.Rows != 2 || got.LoopBody < 2*body || got.LoopBody >= 3*body {
			t.Errorf("%+v, want the primary's 2 rows and %v <= LoopBody < %v", got, 2*body, 3*body)
		}
	})
}
