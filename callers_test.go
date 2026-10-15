package velox_test

import (
	"context"
	"runtime"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
	"github.com/syssam/velox2/internal/testrepo"
)

// callerOf returns a DB with opts whose Rewrite hook records each
// statement's Caller in *got.
func callerOf(got *velox.Caller, opts ...velox.Option) *velox.DB {
	opts = append(opts, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error { *got = st.Caller; return nil }))
	return velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}, opts...)
}

func here() int { _, _, line, _ := runtime.Caller(1); return line }

func TestCallerIsTheApplicationFrame(t *testing.T) {
	var got velox.Caller
	db := callerOf(&got, velox.WithCallers())
	line := here() + 1
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	want := velox.Caller{File: "callers_test.go", Line: line, Func: "github.com/syssam/velox2_test.TestCallerIsTheApplicationFrame"}
	if got != want {
		t.Errorf("All: %+v, want %+v", got, want)
	}
	line = here() + 1
	for _, err := range db.Query[testmodel.Invoice]().Limit(1).Iter(ctx) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got.Line != line || got.File != "callers_test.go" {
		t.Errorf("Iter: %+v, want line %d", got, line)
	}
	err := db.Tx(ctx, func(tx *velox.DB) error {
		line = here() + 1
		_, err := tx.Query[testmodel.Invoice]().Limit(1).All(ctx)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Line != line || got.Func != "github.com/syssam/velox2_test.TestCallerIsTheApplicationFrame.func1" {
		t.Errorf("in Tx: %+v, want line %d in func1", got, line)
	}
}

func TestCallerOffIsZero(t *testing.T) {
	var got velox.Caller
	if _, err := callerOf(&got).Query[testmodel.Invoice]().Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if got != (velox.Caller{}) {
		t.Errorf("without WithCallers: %+v", got)
	}
}

func TestCallerSkipsWrapperPackages(t *testing.T) {
	var got velox.Caller
	if err := testrepo.FirstInvoice(ctx, callerOf(&got, velox.WithCallers())); err != nil {
		t.Fatal(err)
	}
	if got.File != "internal/testrepo/testrepo.go" || got.Func != "github.com/syssam/velox2/internal/testrepo.FirstInvoice" {
		t.Errorf("unskipped: %+v", got)
	}
	db := callerOf(&got, velox.WithCallerSkip("github.com/syssam/velox2/internal/testrepo"))
	line := here() + 1
	if err := testrepo.FirstInvoice(ctx, db); err != nil {
		t.Fatal(err)
	}
	if got.File != "callers_test.go" || got.Line != line {
		t.Errorf("skipped: %+v, want callers_test.go:%d", got, line)
	}
}
