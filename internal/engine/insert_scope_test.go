package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/scope"
)

// TestInsertHookCannotMoveTheScope is Review Focus 4: the scope policy
// checks the row again after the insert hooks.
func TestInsertHookCannotMoveTheScope(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(1)}}}
	db := openPolicied(velox.Postgres, e)
	x := moveOrg{}
	err := db.Insert(&x).Exec(moveOrgKey.With(context.Background(), int64(7)))
	if !errors.Is(err, scope.ErrMismatch) {
		t.Fatalf("got %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
	if err := db.InsertAll([]moveOrg{{ID: 1}}).Exec(moveOrgKey.With(context.Background(), int64(7))); !errors.Is(err, scope.ErrMismatch) {
		t.Errorf("InsertAll: %v", err)
	}
}

// TestUnknownCountFailsClosed: a policy-conditioned upsert whose driver could
// not count its rows (-1, as conn.SQL gives) fails closed.
func TestUnknownCountFailsClosed(t *testing.T) {
	st := &velox.Statement{Op: velox.OpInsert, Entity: invInfo, Rows: [][]any{{1}}, Conflict: &velox.ConflictClause{}}
	engine.SetStmtUpsertPolicy(st, true)
	var pe *velox.PolicyError
	if err := engine.CheckUpserted(st, -1); !errors.As(err, &pe) || !errors.Is(err, engine.ErrUnknownAffected) {
		t.Errorf("checkUpserted with -1: %v, want a *PolicyError wrapping errUnknownAffected", err)
	}
}
