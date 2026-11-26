package apitest

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// TestDeferredFailsClosedUntilSet: on a DB opened with Deferred.Option, a
// read, a write and a transaction are refused with ErrNotReady, and nothing
// is sent, until Set; then the filter judges reads and the hooks run, the
// first given outermost.
func TestDeferredFailsClosedUntilSet(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}}
	late := velox.NewDeferred()
	db := openFake(velox.Postgres, e, late.Option())

	if _, err := db.Query[testmodel.Invoice]().AllowUnbounded().All(ctx); !errors.Is(err, velox.ErrNotReady) {
		t.Errorf("read before Set: %v", err)
	}
	if _, err := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.ID.EQ(1)).Exec(ctx); !errors.Is(err, velox.ErrNotReady) {
		t.Errorf("write before Set: %v", err)
	}
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { return touch(ctx, tx) })
	if !errors.Is(err, velox.ErrNotReady) {
		t.Errorf("tx before Set: %v", err)
	}
	for _, q := range e.Log {
		if q != "BEGIN" && q != "ROLLBACK" {
			t.Errorf("sent %q before Set", q)
		}
	}

	var calls []string
	filter := func(_ context.Context, _ *velox.DB, fe velox.FilterEntity, op velox.Op) (velox.Predicate, error) {
		calls = append(calls, "filter "+fe.Name())
		return velox.Allow(), nil
	}
	hook := func(name string) velox.Hook {
		return func(next velox.Mutator) velox.Mutator {
			return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
				calls = append(calls, name)
				return next.Mutate(ctx, m)
			})
		}
	}
	if err := late.Set(filter, hook("outer"), hook("inner")); err != nil {
		t.Fatal(err)
	}
	e.Log = nil
	if _, err := db.Query[testmodel.Invoice]().AllowUnbounded().All(ctx); err != nil {
		t.Errorf("read after Set: %v", err)
	}
	if err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { return touch(ctx, tx) }); err != nil {
		t.Errorf("tx after Set: %v", err)
	}
	// The read asks the filter; the write runs the hooks, the first outermost,
	// and then, in next, asks the filter for the rows it changes.
	want := []string{"filter Invoice", "outer", "inner", "filter Invoice"}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls %q, want %q", calls, want)
	}

	if err := late.Set(nil); err == nil {
		t.Error("a second Set returned no error")
	}
}

// A nil filter given to Set allows; a DB opened without Option waits for
// nothing.
func TestDeferredNilFilterAllowsAndNoOptionWaitsForNothing(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}}
	late := velox.NewDeferred()
	db := openFake(velox.Postgres, e, late.Option())
	if err := late.Set(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Query[testmodel.Invoice]().AllowUnbounded().All(ctx); err != nil {
		t.Errorf("nil filter: %v", err)
	}
	plain := openFake(velox.Postgres, &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}})
	if _, err := plain.Query[testmodel.Invoice]().AllowUnbounded().All(ctx); err != nil {
		t.Errorf("no Option: %v", err)
	}
}
