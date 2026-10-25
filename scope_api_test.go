package velox_test

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func TestTenantContextLaterWins(t *testing.T) {
	bg := context.Background()
	ctx := velox.WithAllTenants(velox.WithTenant(bg, int64(7)), "nightly billing")
	if id, all := velox.TenantOf(ctx); id != nil || all != "nightly billing" {
		t.Errorf("WithTenant then WithAllTenants: id %v, all %q", id, all)
	}
	ctx = velox.WithTenant(velox.WithAllTenants(bg, "nightly billing"), int64(7))
	if id, all := velox.TenantOf(ctx); id != int64(7) || all != "" {
		t.Errorf("WithAllTenants then WithTenant: id %v, all %q", id, all)
	}
	if id, all := velox.TenantOf(bg); id != nil || all != "" {
		t.Errorf("no tenant: id %v, all %q", id, all)
	}
}

func TestTenantBypassNeedsAReason(t *testing.T) {
	ctx := velox.WithTenant(context.Background(), int64(7))
	for _, reason := range []string{"", " \t\n"} {
		if velox.WithAllTenants(ctx, reason) != ctx {
			t.Errorf("WithAllTenants with the reason %q changed the context", reason)
		}
	}
	if id, all := velox.TenantOf(velox.WithAllTenants(ctx, "")); id != int64(7) || all != "" {
		t.Errorf("WithAllTenants with no reason: id %v, all %q", id, all)
	}
}

func TestSkipPolicyNeedsAReason(t *testing.T) {
	ctx := context.Background()
	for _, reason := range []string{"", " \t\n"} {
		if velox.WithoutPolicy(ctx, reason) != ctx {
			t.Errorf("WithoutPolicy with the reason %q changed the context", reason)
		}
	}
	if got := velox.PolicyBypass(velox.WithoutPolicy(ctx, "support tool")); got != "support tool" {
		t.Errorf("PolicyBypass = %q", got)
	}
	if got := velox.PolicyBypass(ctx); got != "" {
		t.Errorf("PolicyBypass of a plain context = %q", got)
	}
}

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

func TestTicketMetadata(t *testing.T) {
	if err := velox.CheckEntity[testmodel.Ticket](); err != nil {
		t.Fatal(err)
	}
	info := velox.InfoOf[testmodel.Ticket]()
	if i, ok := info.Column("org_id"); !ok || info.Tenant != i || info.Columns[i].Field != "OrgID" {
		t.Errorf("tenant %d, org_id is %d (%v)", info.Tenant, i, ok)
	}
}

func TestRowScopeOf(t *testing.T) {
	ctx := context.Background()
	if got := velox.RowScopeFromContext(ctx); got != (velox.RowScope{}) {
		t.Errorf("no scope: %+v", got)
	}
	ctx = velox.WithoutPolicy(velox.WithTenant(ctx, int64(7)), "audit")
	if got, want := velox.RowScopeFromContext(ctx), (velox.RowScope{Tenant: int64(7), PolicyBypass: "audit"}); got != want {
		t.Errorf("%+v, want %+v", got, want)
	}
	if got := velox.RowScopeFromContext(velox.WithAllTenants(ctx, "nightly")); got.Tenant != nil || got.TenantBypass != "nightly" || got.PolicyBypass != "audit" {
		t.Errorf("all tenants: %+v", got)
	}
	if got, want := velox.RowScopeFromContext(velox.WithShard(ctx, "eu")), (velox.RowScope{Tenant: int64(7), PolicyBypass: "audit", Shard: "eu", HasShard: true}); got != want {
		t.Errorf("on a shard: %+v, want %+v", got, want)
	}
	if got := velox.RowScopeFromContext(velox.WithShard(ctx, "")); !got.HasShard || got == velox.RowScopeFromContext(ctx) {
		t.Errorf("WithShard(\"\"): %+v, want it told from no shard", got)
	}
}
