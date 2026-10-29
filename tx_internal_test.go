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

// TestOverWaitsWhileBlocked: over waits, up to overGrace, for the Done of a
// context past its deadline, and returns once it closes, not before. It
// compares times taken in the test, not a fixed slack. The blocked case is
// the one a loaded machine can disturb: a 10 ms sleep that overruns the grace
// makes over return before the cancel. It retries once, and logs that.
func TestOverWaitsWhileBlocked(t *testing.T) {
	blocked := func() (ok bool, early time.Duration) {
		inner, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan time.Time, 1)
		result := make(chan bool, 1)
		go func() {
			r := over(lateCtx{inner})
			done <- time.Now()
			result <- r
		}()
		time.Sleep(10 * time.Millisecond)
		canceled := time.Now()
		cancel()
		returned := <-done
		return <-result && !returned.Before(canceled), canceled.Sub(returned)
	}
	ok, _ := blocked()
	if !ok {
		t.Log("over returned before the cancel; the machine is loaded, retrying once")
		if ok, early := blocked(); !ok {
			t.Errorf("over returned %v before the context was canceled, or not true", early)
		}
	}

	// An already-canceled context makes over return at once. The time it
	// takes is the test goroutine's own scheduling, which a loaded machine
	// can stretch past the grace, so this retries once too. An over that
	// ignores Done takes the whole grace on both tries.
	canceledOver := func() time.Duration {
		inner, cancel := context.WithCancel(context.Background())
		cancel()
		start := time.Now()
		if !over(lateCtx{inner}) {
			t.Error("a canceled context past its deadline is not over")
		}
		return time.Since(start)
	}
	if d := canceledOver(); d >= overGrace {
		t.Logf("over took %v on a context already canceled; the machine is loaded, retrying once", d)
		if d := canceledOver(); d >= overGrace {
			t.Errorf("over took %v on a context already canceled, want less than the grace %v", d, overGrace)
		}
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
	go func() { done <- db.Tx(stuck, func(context.Context, *DB) error { return nil }) }()
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
