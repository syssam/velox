package apitest

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

func TestPolicyError(t *testing.T) {
	var err error = &velox.PolicyError{Entity: "testmodel.Ticket", Op: velox.OpUpdate, Err: velox.Deny("not yours")}
	if want := "velox: testmodel.Ticket: update denied by policy: not yours"; err.Error() != want {
		t.Errorf("text\n got %s\nwant %s", err, want)
	}
	if !errors.Is(err, velox.ErrDenied) {
		t.Error("a *PolicyError does not match ErrDenied")
	}
	// A policy function's own error denies too, and stays reachable.
	own := errors.New("lookup failed")
	err = &velox.PolicyError{Entity: "testmodel.Ticket", Op: velox.OpSelect, Err: own}
	if !errors.Is(err, velox.ErrDenied) || !errors.Is(err, own) {
		t.Errorf("%v: Is(ErrDenied) %v, Is(own) %v", err, errors.Is(err, velox.ErrDenied), errors.Is(err, own))
	}
	var pe *velox.PolicyError
	if !errors.As(errors.Join(errors.New("other"), err), &pe) || pe.Op != velox.OpSelect || pe.Err != own {
		t.Errorf("errors.As: %+v", pe)
	}
}

func TestDeny(t *testing.T) {
	err := velox.Deny("x")
	if err.Error() != "x" {
		t.Errorf("text %q", err)
	}
	if !errors.Is(err, velox.ErrDenied) {
		t.Error("Deny does not match ErrDenied")
	}
}

func TestOpString(t *testing.T) {
	for op, want := range map[velox.Op]string{
		velox.OpSelect: "select", velox.OpInsert: "insert", velox.OpUpdate: "update", velox.OpDelete: "delete",
	} {
		if got := op.String(); got != want {
			t.Errorf("Op(%d).String() = %q, want %q", op, got, want)
		}
	}
}

func TestRowScopeOf(t *testing.T) {
	ctx := context.Background()
	if got := velox.RowScopeFromContext(ctx); got != (velox.RowScope{}) {
		t.Errorf("no scope: %+v", got)
	}
	ctx = velox.WithoutPolicy(testmodel.Org.With(context.Background(), int64(7)), "audit")
	if got, want := velox.RowScopeFromContext(ctx), (velox.RowScope{Values: velox.RowScopeFromContext(testmodel.Org.With(context.Background(), int64(7))).Values, PolicyBypass: "audit"}); got != want {
		t.Errorf("%+v, want %+v", got, want)
	}
	if got := velox.RowScopeFromContext(velox.WithoutPolicy(ctx, "nightly")); got.PolicyBypass != "nightly" {
		t.Errorf("a later WithoutPolicy did not replace the reason: %+v", got)
	}
	if got, want := velox.RowScopeFromContext(velox.WithShard(ctx, "eu")), (velox.RowScope{Values: velox.RowScopeFromContext(ctx).Values, PolicyBypass: "audit", Shard: "eu", HasShard: true}); got != want {
		t.Errorf("on a shard: %+v, want %+v", got, want)
	}
	if got := velox.RowScopeFromContext(velox.WithShard(ctx, "")); !got.HasShard || got == velox.RowScopeFromContext(ctx) {
		t.Errorf("WithShard(\"\"): %+v, want it told from no shard", got)
	}
}
