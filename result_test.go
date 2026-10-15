package velox_test

import (
	"context"
	"errors"
	"testing"

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
