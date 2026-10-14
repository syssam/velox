package velox

import (
	"context"
	"testing"
	"time"
)

// lateCtx is past its deadline, but its timer has not fired yet: it ends only
// when canceled.
type lateCtx struct{ context.Context }

func (lateCtx) Deadline() (time.Time, bool) { return time.Now().Add(-time.Millisecond), true }

// TestOverWaitsForAPassedDeadline: a transaction's context whose deadline has
// passed is over, even before its timer fires. A statement inside it has the
// same deadline on its own timer, and a timeout there must not find the
// transaction live (it reported a rollback error, and could commit).
func TestOverWaitsForAPassedDeadline(t *testing.T) {
	inner, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	if !over(lateCtx{inner}) {
		t.Error("a context past its deadline is not over")
	}
	if over(context.Background()) {
		t.Error("a context with no deadline is over")
	}
	live, cancelLive := context.WithTimeout(context.Background(), time.Hour)
	defer cancelLive()
	if over(live) {
		t.Error("a context before its deadline is over")
	}
}
