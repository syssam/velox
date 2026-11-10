package apitest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

func TestReadWithoutLimitIsRefused(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}}
	db := openFake(velox.Postgres, e)
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
	if _, err := db.Query[testmodel.Invoice]().AllowUnbounded().All(ctx); err != nil {
		t.Errorf("Unbounded: %v", err)
	}
	e.Rows = [][]any{{int64(1)}} // Count scans one number
	if _, err := db.Query[testmodel.Invoice]().Count(ctx); err != nil {
		t.Errorf("Count: %v", err)
	}
}

func TestWriteWithoutWhereIsRefused(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
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
	if err := db.Save(&inv, I.Number).Exec(ctx); err != nil {
		t.Errorf("Save: %v", err)
	}
}

// TestWhereThatHoldsForEveryRowIsNoWhere: a Where that matches every row by
// its structure alone (DELETE … WHERE 1 = 1) is refused as if it were absent.
func TestWhereThatHoldsForEveryRowIsNoWhere(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	always := map[string]velox.Pred[testmodel.Invoice]{
		"And()":     velox.And[testmodel.Invoice](),
		"Not(Or())": velox.Not(velox.Or[testmodel.Invoice]()),
		"NotIn()":   I.ID.NotIn(),
	}
	const msg = "velox: testmodel.Invoice: update or delete has no Where: its conditions match every row"
	for name, p := range always {
		if _, err := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(p).Exec(ctx); !errors.Is(err, velox.ErrNoWhere) || !strings.HasPrefix(err.Error(), msg) {
			t.Errorf("Update %s: %v", name, err)
		}
		if _, err := db.Delete[testmodel.Invoice]().Where(p).Exec(ctx); !errors.Is(err, velox.ErrNoWhere) || !strings.HasPrefix(err.Error(), msg) {
			t.Errorf("Delete %s: %v", name, err)
		}
	}
	if e.Calls != 0 {
		t.Fatalf("%d statements sent: %v", e.Calls, e.Log)
	}
	for name, p := range always {
		if _, err := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(p).All().Exec(ctx); err != nil {
			t.Errorf("Update %s All: %v", name, err)
		}
		if _, err := db.Delete[testmodel.Invoice]().Where(p).All().Exec(ctx); err != nil {
			t.Errorf("Delete %s All: %v", name, err)
		}
	}
	for name, ps := range map[string][]velox.Pred[testmodel.Invoice]{
		"And(EQ)":            {velox.And(I.ID.EQ(1))},
		"And(), EQ":          {velox.And[testmodel.Invoice](), I.ID.EQ(1)},
		"In()":               {I.ID.In()},
		"And(And, Or())":     {velox.And(velox.And[testmodel.Invoice](), velox.Or[testmodel.Invoice]())},
		"Or(EQ, Not(And()))": {velox.Or(I.ID.EQ(1), velox.Not(velox.And[testmodel.Invoice]()))},
	} {
		if _, err := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(ps...).Exec(ctx); err != nil {
			t.Errorf("Update %s: %v", name, err)
		}
		if _, err := db.Delete[testmodel.Invoice]().Where(ps...).Exec(ctx); err != nil {
			t.Errorf("Delete %s: %v", name, err)
		}
	}
}

// TestGuardRunsBeforeHooks: a Where a hook adds does not make a write
// filtered, and a refused statement never reaches the hooks.
func TestGuardRunsBeforeHooks(t *testing.T) {
	ran := 0
	db := openFake(velox.Postgres, &fakeconn.Exec{}, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		ran++
		st.Where = append(st.Where, velox.Predicate(I.OrgID.EQ(1)))
		return nil
	}))
	if _, err := db.Delete[testmodel.Invoice]().Exec(ctx); !errors.Is(err, velox.ErrNoWhere) || ran != 0 {
		t.Errorf("%v, hook ran %d times", err, ran)
	}
}

func TestMaxLimit(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}}
	db := openFake(velox.Postgres, e, velox.WithMaxLimit(50))
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
