package velox_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox2"
)

// TestRollbackIsBounded: a rollback velox sends on a context it detached
// from the caller's is bounded, so a dead connection cannot hang Tx: after fn
// fails, after a nested fn fails, and after fn's ctx was canceled.
func TestRollbackIsBounded(t *testing.T) {
	defer velox.SetRollbackTimeout(50 * time.Millisecond)()
	boom := errors.New("boom")
	bounded := func(t *testing.T, what string, start, dl time.Time) {
		t.Helper()
		if dl.IsZero() || dl.After(start.Add(70*time.Millisecond)) {
			t.Errorf("%s: its deadline is %v after the start, want at most 70ms", what, dl.Sub(start))
		}
	}
	unbounded := func(t *testing.T, what string, err error, start time.Time) {
		t.Helper()
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%s: Tx returned after %v", what, d)
		}
		if !errors.Is(err, boom) || !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, velox.ErrTimeout) {
			t.Errorf("%s: %v, want boom and context.DeadlineExceeded, not ErrTimeout", what, err)
		}
	}

	e := &velox.FakeExec{BlockRollback: true}
	db := velox.OpenFake(velox.Postgres, e)
	start := time.Now()
	err := db.Tx(ctx, func(context.Context, *velox.DB) error { return boom })
	unbounded(t, "ROLLBACK", err, start)
	bounded(t, "ROLLBACK", start, e.RollbackDeadline)

	e = &velox.FakeExec{BlockRollback: true}
	db = velox.OpenFake(velox.Postgres, e)
	var nested error
	var nestedStart, nestedDeadline time.Time
	_ = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		nestedStart = time.Now()
		nested = tx.Tx(ctx, func(context.Context, *velox.DB) error { return boom })
		nestedDeadline = e.RollbackDeadline
		return nested
	})
	unbounded(t, "ROLLBACK TO", nested, nestedStart)
	bounded(t, "ROLLBACK TO", nestedStart, nestedDeadline)
	if !strings.Contains(fmt.Sprint(nested), "velox: rollback to savepoint: ") {
		t.Errorf("ROLLBACK TO: %v, want it to say so", nested)
	}

	e = &velox.FakeExec{BlockRollback: true}
	db = velox.OpenFake(velox.Postgres, e)
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	start = time.Now()
	err = db.Tx(cctx, func(context.Context, *velox.DB) error { cancel(); return nil })
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("canceled: Tx returned after %v", d)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: %v", err)
	}
	bounded(t, "canceled", start, e.RollbackDeadline)
	if !errors.Is(e.RollbackCtxErr, context.DeadlineExceeded) {
		t.Errorf("canceled: the ROLLBACK's context ended with %v, want DeadlineExceeded: detached from the cancellation, then bounded", e.RollbackCtxErr)
	}
}
