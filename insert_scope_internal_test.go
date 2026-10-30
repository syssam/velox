package velox

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox2/internal/engine"
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

// TestUnknownCountFailsClosed: a policy-conditioned upsert whose driver could
// not count its rows (-1, as conn.SQL gives) fails closed.
func TestUnknownCountFailsClosed(t *testing.T) {
	st := &Statement{Op: OpInsert, Entity: invInfo, Rows: [][]any{{1}}, Conflict: &ConflictClause{}}
	engine.SetStmtUpsertPolicy(st, true)
	var pe *PolicyError
	if err := engine.CheckUpserted(st, -1); !errors.As(err, &pe) || !errors.Is(err, engine.ErrUnknownAffected) {
		t.Errorf("checkUpserted with -1: %v, want a *PolicyError wrapping errUnknownAffected", err)
	}
}
