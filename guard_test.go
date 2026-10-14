package velox_test

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func TestReadWithoutLimitIsRefused(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
	db := velox.OpenFake(velox.Postgres, e)
	if _, err := db.Query[testmodel.Invoice]().All(ctx); !errors.Is(err, velox.ErrUnbounded) {
		t.Errorf("All: %v", err)
	}
	if _, err := db.Query[testmodel.Invoice]().Limit(0).All(ctx); !errors.Is(err, velox.ErrUnbounded) {
		t.Errorf("Limit(0): %v", err)
	}
	for _, err := range db.Query[testmodel.Invoice]().Iter(ctx) {
		if !errors.Is(err, velox.ErrUnbounded) {
			t.Errorf("Iter: %v", err)
		}
	}
	if e.Calls != 0 {
		t.Fatalf("%d statements sent", e.Calls)
	}
	// Review Focus 4: One, Unbounded and Count are not refused.
	if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx); err != nil {
		t.Errorf("One: %v", err)
	}
	if _, err := db.Query[testmodel.Invoice]().Unbounded().All(ctx); err != nil {
		t.Errorf("Unbounded: %v", err)
	}
	e.Rows = [][]any{{int64(1)}} // Count scans one number
	if _, err := db.Query[testmodel.Invoice]().Count(ctx); err != nil {
		t.Errorf("Count: %v", err)
	}
}

func TestWriteWithoutWhereIsRefused(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	if _, err := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Exec(ctx); !errors.Is(err, velox.ErrNoWhere) {
		t.Errorf("Update: %v", err)
	}
	if _, err := db.Delete[testmodel.Invoice]().Exec(ctx); !errors.Is(err, velox.ErrNoWhere) {
		t.Errorf("Delete: %v", err)
	}
	if e.Calls != 0 {
		t.Fatalf("%d statements sent", e.Calls)
	}
	if _, err := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).All().Exec(ctx); err != nil {
		t.Errorf("Update All: %v", err)
	}
	if _, err := db.Delete[testmodel.Invoice]().All().Exec(ctx); err != nil {
		t.Errorf("Delete All: %v", err)
	}
	inv := testmodel.Invoice{ID: 1}
	if err := db.Save(ctx, &inv, I.Number); err != nil {
		t.Errorf("Save: %v", err)
	}
}

// TestGuardRunsBeforeHooks: a Where a hook adds does not make a write
// filtered, and a refused statement never reaches the hooks.
func TestGuardRunsBeforeHooks(t *testing.T) {
	ran := 0
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		ran++
		st.Where = append(st.Where, velox.ColumnEQ(st.Entity.Tenant, int64(1)))
		return nil
	}))
	if _, err := db.Delete[testmodel.Invoice]().Exec(ctx); !errors.Is(err, velox.ErrNoWhere) || ran != 0 {
		t.Errorf("%v, hook ran %d times", err, ran)
	}
}

func TestMaxLimit(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
	db := velox.OpenFake(velox.Postgres, e, velox.WithMaxLimit(50))
	if _, err := db.Query[testmodel.Invoice]().Limit(51).All(ctx); !errors.Is(err, velox.ErrLimitTooLarge) || e.Calls != 0 {
		t.Errorf("51: %v, calls %d", err, e.Calls)
	}
	if _, err := db.Query[testmodel.Invoice]().Limit(50).All(ctx); err != nil {
		t.Errorf("50: %v", err)
	}
	if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx); err != nil {
		t.Errorf("One: %v", err)
	}
	defer func() {
		if recover() == nil {
			t.Error("WithMaxLimit(1) did not panic")
		}
	}()
	velox.WithMaxLimit(1)
}
