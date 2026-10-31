package engine_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

func TestTenantContextLaterWins(t *testing.T) {
	bg := context.Background()
	ctx := velox.WithAllTenants(velox.WithTenant(bg, int64(7)), "nightly billing")
	if id, all := engine.TenantFrom(ctx); id != nil || all != "nightly billing" {
		t.Errorf("WithTenant then WithAllTenants: id %v, all %q", id, all)
	}
	ctx = velox.WithTenant(velox.WithAllTenants(bg, "nightly billing"), int64(7))
	if id, all := engine.TenantFrom(ctx); id != int64(7) || all != "" {
		t.Errorf("WithAllTenants then WithTenant: id %v, all %q", id, all)
	}
	if id, all := engine.TenantFrom(bg); id != nil || all != "" {
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
	if id, all := engine.TenantFrom(velox.WithAllTenants(ctx, "")); id != int64(7) || all != "" {
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
	if got := engine.PolicyBypassReason(velox.WithoutPolicy(ctx, "support tool")); got != "support tool" {
		t.Errorf("PolicyBypass = %q", got)
	}
	if got := engine.PolicyBypassReason(ctx); got != "" {
		t.Errorf("PolicyBypass of a plain context = %q", got)
	}
}

func TestWithTagsMerges(t *testing.T) {
	bg := context.Background()
	parent := velox.WithTags(bg, "route", "a", "user", "1")
	child := velox.WithTags(parent, "route", "b", "team", "x")
	if got, want := engine.TagsFrom(child), []velox.Tag{{Key: "route", Value: "b"}, {Key: "user", Value: "1"}, {Key: "team", Value: "x"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("child: %v, want %v", got, want)
	}
	if got, want := engine.TagsFrom(parent), []velox.Tag{{Key: "route", Value: "a"}, {Key: "user", Value: "1"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("parent: %v, want %v", got, want)
	}
	if got := engine.TagsFrom(bg); got != nil {
		t.Errorf("no tags: %v", got)
	}
}

func TestWithTagsNoPairs(t *testing.T) {
	bg := context.Background()
	if got := velox.WithTags(bg); got != bg {
		t.Errorf("WithTags(bg) = %v, want bg itself", got)
	}
	parent := velox.WithTags(bg, "route", "a")
	if got := velox.WithTags(parent); got != parent {
		t.Errorf("WithTags(parent) = %v, want parent itself", got)
	}
	if got, want := engine.TagsFrom(velox.WithTags(parent)), []velox.Tag{{Key: "route", Value: "a"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("tags %v, want %v", got, want)
	}
}

func TestTagsEditedByAHookStayInTheStatement(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}}, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		st.Tags[0].Value = "changed"
		return nil
	}))
	tctx := velox.WithTags(ctx, "route", "a")
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(tctx); err != nil {
		t.Fatal(err)
	}
	if got, want := engine.TagsFrom(tctx), []velox.Tag{{Key: "route", Value: "a"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("context's tags: %v, want %v", got, want)
	}
}

// TestWithoutTx: WithoutTx removes the mark and keeps the context's values
// and deadline; on a context without the mark it returns ctx.
func TestWithoutTx(t *testing.T) {
	if got := velox.WithoutTx(ctx); got != ctx {
		t.Error("WithoutTx changed a context without the mark")
	}
	db := openFake(velox.Postgres, &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}})
	tagged := velox.WithTags(ctx, "route", "POST /pay")
	err := db.Tx(tagged, func(ctx context.Context, tx *velox.DB) error {
		free := velox.WithoutTx(ctx)
		if got, want := engine.TagsFrom(free), engine.TagsFrom(ctx); !reflect.DeepEqual(got, want) || len(got) == 0 {
			t.Errorf("tags %v, want %v", got, want)
		}
		if id, all := engine.TenantFrom(free); id != nil || all == "" {
			t.Errorf("tenant %v, %q: the WithAllTenants of the caller's ctx is lost", id, all)
		}
		if a, _ := free.Deadline(); !a.Equal(func() time.Time { d, _ := ctx.Deadline(); return d }()) {
			t.Errorf("deadline %v", a)
		}
		_, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(free)
		return err
	}, velox.TxTimeout(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	// Cancelling the parent cancels the WithoutTx ctx.
	parent, cancel := context.WithCancel(ctx)
	_ = db.Tx(parent, func(ctx context.Context, _ *velox.DB) error {
		free := velox.WithoutTx(ctx)
		cancel()
		select {
		case <-free.Done():
		case <-time.After(time.Second):
			t.Error("cancelling the parent did not cancel the WithoutTx ctx")
		}
		if !errors.Is(free.Err(), context.Canceled) {
			t.Errorf("Err %v, want context.Canceled", free.Err())
		}
		return nil
	})
}
