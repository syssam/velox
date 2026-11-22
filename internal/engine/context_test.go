package engine_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

func TestScopeValuesAreTheContexts(t *testing.T) {
	bg := context.Background()
	if got := velox.RowScopeFromContext(bg).Values; got != "" {
		t.Errorf("no value: %q", got)
	}
	a := velox.WithScopeValue(bg, "org", int64(7))
	b := velox.WithScopeValue(a, "region", "eu")
	if got := velox.RowScopeFromContext(b).Values; got == "" || got == velox.RowScopeFromContext(a).Values {
		t.Errorf("a second value: %q, first %q", got, velox.RowScopeFromContext(a).Values)
	}
	// The same names and values in either order are one scope; another value is not.
	c := velox.WithScopeValue(velox.WithScopeValue(bg, "region", "eu"), "org", int64(7))
	if velox.RowScopeFromContext(b).Values != velox.RowScopeFromContext(c).Values {
		t.Errorf("order matters: %q vs %q", velox.RowScopeFromContext(b).Values, velox.RowScopeFromContext(c).Values)
	}
	d := velox.WithScopeValue(a, "org", int64(8))
	if velox.RowScopeFromContext(d).Values == velox.RowScopeFromContext(a).Values {
		t.Errorf("a later value did not replace the first: %q", velox.RowScopeFromContext(d).Values)
	}
}

func TestSkipPolicyNeedsAReason(t *testing.T) {
	db := velox.OpenConn(velox.Postgres, fakeConn(&fakeconn.Exec{}))
	for _, reason := range []string{"", " \t\n"} {
		_, err := db.WithoutPolicy(reason).Exec(context.Background(), "x", "SELECT 1")
		if err == nil || !strings.Contains(err.Error(), "WithoutPolicy needs a reason") {
			t.Errorf("WithoutPolicy with the reason %q: %v", reason, err)
		}
	}
	free := db.WithoutPolicy("support tool")
	if got := free.PolicyBypass(); got != "support tool" {
		t.Errorf("PolicyBypass = %q", got)
	}
	if got := db.PolicyBypass(); got != "" {
		t.Errorf("PolicyBypass of the DB it came from = %q", got)
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
	tagged := velox.WithScopeValue(velox.WithTags(ctx, "route", "POST /pay"), "org", int64(1))
	err := db.Tx(tagged, func(ctx context.Context, tx *velox.DB) error {
		free := velox.WithoutTx(ctx)
		if got, want := engine.TagsFrom(free), engine.TagsFrom(ctx); !reflect.DeepEqual(got, want) || len(got) == 0 {
			t.Errorf("tags %v, want %v", got, want)
		}
		if got, want := velox.RowScopeFromContext(free), velox.RowScopeFromContext(ctx); got != want || got.Values == "" {
			t.Errorf("row scope %+v, want %+v: the scope values of the caller's ctx are lost", got, want)
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
