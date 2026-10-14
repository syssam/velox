package velox

import (
	"context"
	"errors"
	"reflect"
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

// TestOverDoesNotWaitForever: a context past its deadline whose Done never
// closes is over once a short grace has passed, so a transaction on it
// neither hangs nor commits.
func TestOverDoesNotWaitForever(t *testing.T) {
	stuck := lateCtx{context.Background()} // Background's Done never closes
	start := time.Now()
	ended := make(chan bool, 1)
	go func() { ended <- over(stuck) }()
	select {
	case ok := <-ended:
		if !ok {
			t.Error("a context past its deadline is not over")
		}
		if d := time.Since(start); d < overGrace || d > overGrace+time.Second {
			t.Errorf("over took %v, want about %v", d, overGrace)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("over waits for a Done that never closes")
	}

	e := &FakeExec{}
	db := OpenFake(Postgres, e)
	done := make(chan error, 1)
	go func() { done <- db.Tx(stuck, func(*DB) error { return nil }) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) || err.Error() != "velox: transaction: context deadline exceeded" {
			t.Errorf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Tx hangs on a context whose Done never closes")
	}
	if want := []string{"BEGIN", "ROLLBACK"}; !reflect.DeepEqual(e.Log, want) {
		t.Errorf("log %q, want %q", e.Log, want)
	}
}
