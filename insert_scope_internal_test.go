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

// A DefaultFunc returns a pointer field's element type, which fillDefault
// stores through a new pointer; a set pointer keeps its value.
func TestFillDefaultFuncOnPointer(t *testing.T) {
	c := ColumnInfo{DefaultFunc: func() any { return 3 }}
	var p *int
	fillDefault(&c, &p)
	if p == nil || *p != 3 {
		t.Fatalf("unset pointer: %v", p)
	}
	zero := 0
	p = &zero
	fillDefault(&c, &p)
	if *p != 0 {
		t.Fatalf("a set 0 was replaced by %d", *p)
	}
	var v int
	fillDefault(&c, &v)
	if v != 3 {
		t.Fatalf("value field: %d", v)
	}
}

// TestCheckColumnsAllow: an Allow names no column, so checkColumns does not
// check its col, which on an entity of no columns would be out of range.
func TestCheckColumnsAllow(t *testing.T) {
	st := &Statement{Op: OpSelect, Entity: &EntityInfo{Name: "none"}, Where: []Predicate{{node{op: opAllow}}}}
	if err := checkColumns(st); err != nil {
		t.Fatal(err)
	}
}

// TestUnknownCountFailsClosed: a policy-conditioned upsert whose driver could
// not count its rows (-1, as conn.SQL gives) fails closed.
func TestUnknownCountFailsClosed(t *testing.T) {
	st := &Statement{Op: OpInsert, Entity: invInfo, Rows: [][]any{{1}}, Conflict: &ConflictClause{}, upsertPolicy: true}
	var pe *PolicyError
	if err := checkUpserted(st, -1); !errors.As(err, &pe) || !errors.Is(err, errUnknownAffected) {
		t.Errorf("checkUpserted with -1: %v, want a *PolicyError wrapping errUnknownAffected", err)
	}
}
