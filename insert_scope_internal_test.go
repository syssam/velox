package velox

import (
	"context"
	"errors"
	"testing"
)

// TestInsertHookCannotMoveTheTenant is Review Focus 4: the tenant is
// checked again after the insert hooks.
func TestInsertHookCannotMoveTheTenant(t *testing.T) {
	e := &FakeExec{Rows: [][]any{{int64(1)}}}
	db := OpenFake(Postgres, e)
	x := moveOrg{}
	err := db.Insert(&x).Exec(WithTenant(context.Background(), int64(7)))
	if !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("got %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
	if err := db.InsertAll([]moveOrg{{ID: 1}}).Exec(WithTenant(context.Background(), int64(7))); !errors.Is(err, ErrTenantMismatch) {
		t.Errorf("InsertAll: %v", err)
	}
}
