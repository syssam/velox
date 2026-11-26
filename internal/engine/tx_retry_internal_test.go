package engine

import (
	"context"
	"errors"
	"github.com/syssam/velox/internal/dberr"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/syssam/velox/internal/fakeconn"
)

// recordRetryWaits replaces the wait between attempts with one that records
// what it was asked to wait, and reports go on.
func recordRetryWaits(t *testing.T) *[]time.Duration {
	t.Helper()
	var waits []time.Duration
	was := retryWait
	retryWait = func(_, _ context.Context, d time.Duration) bool {
		waits = append(waits, d)
		return true
	}
	t.Cleanup(func() { retryWait = was })
	return &waits
}

// TestRetryBacksOff: no wait before the first attempt, a wait before each
// retry, growing and bounded.
func TestRetryBacksOff(t *testing.T) {
	waits := recordRetryWaits(t)
	dead := &mysql.MySQLError{Number: 1213, Message: "Deadlock found"}
	db := newDB(MySQL, &fakeconn.Exec{}, nil)
	calls := 0
	const retries = 12
	err := db.Tx(context.Background(), func(context.Context, *DB) error {
		if calls++; len(*waits) != calls-1 {
			t.Errorf("attempt %d ran after %d waits", calls, len(*waits))
		}
		return dead
	}, TxRetryRolledBack(retries))
	if !dberr.Is(err, dead) {
		t.Fatalf("%v", err)
	}
	if calls != retries+1 || len(*waits) != retries {
		t.Fatalf("%d attempts, %d waits", calls, len(*waits))
	}
	for i, d := range *waits {
		ceiling := min(retryBase<<i, retryMax)
		if d < ceiling/2 || d > ceiling {
			t.Errorf("wait %d is %v, want between %v and %v", i, d, ceiling/2, ceiling)
		}
	}
}

// TestRetryDelayIsJittered: delays of one attempt differ.
func TestRetryDelayIsJittered(t *testing.T) {
	seen := map[time.Duration]bool{}
	for range 64 {
		seen[retryDelay(3)] = true
	}
	if len(seen) < 2 {
		t.Errorf("64 delays are one value: %v", seen)
	}
}

// TestRetryWaitEndsWithContext: the wait ends with either context, at once,
// and the transaction's error is returned without another attempt.
func TestRetryWaitEndsWithContext(t *testing.T) {
	dead := &mysql.MySQLError{Number: 1213, Message: "Deadlock found"}
	was := retryBase
	retryBase = time.Hour
	defer func() { retryBase = was }()
	wasMax := retryMax
	retryMax = time.Hour
	defer func() { retryMax = wasMax }()

	db := newDB(MySQL, &fakeconn.Exec{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	start := time.Now()
	err := db.Tx(ctx, func(context.Context, *DB) error {
		calls++
		time.AfterFunc(time.Millisecond, cancel)
		return dead
	}, TxRetryRolledBack(3))
	if !dberr.Is(err, dead) || calls != 1 {
		t.Errorf("%v after %d attempts", err, calls)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("Tx returned after %v", d)
	}

	if waitRetry(context.Background(), canceled(), time.Hour) {
		t.Error("a wait on a canceled tctx went on")
	}
	if !waitRetry(context.Background(), context.Background(), time.Nanosecond) {
		t.Error("a wait that ran out says stop")
	}
}

// TestRetryWaitStopsAtTheTimeout: velox's deadline ends the wait, and the
// error says so.
func TestRetryWaitStopsAtTheTimeout(t *testing.T) {
	was := retryBase
	retryBase = time.Hour
	defer func() { retryBase = was }()
	wasMax := retryMax
	retryMax = time.Hour
	defer func() { retryMax = wasMax }()
	dead := &mysql.MySQLError{Number: 1213, Message: "Deadlock found"}
	db := newDB(MySQL, &fakeconn.Exec{}, nil)
	calls := 0
	err := db.Tx(context.Background(), func(context.Context, *DB) error { calls++; return dead }, TxRetryRolledBack(3), TxTimeout(5*time.Millisecond))
	if !dberr.Is(err, dead) || !dberr.Is(err, ErrTimeout) || calls != 1 {
		t.Errorf("%v after %d attempts", err, calls)
	}
}

// TestRetryEndedMatchesTheContext: a transaction whose ctx ended while it waited to
// retry says so, as Tx's documentation has it, and still shows the last attempt's
// cause. So does one whose ctx ended before the wait began. A deadline of velox's
// is ErrTimeout, as TestRetryWaitStopsAtTheTimeout has it.
func TestRetryEndedMatchesTheContext(t *testing.T) {
	was, wasMax := retryBase, retryMax
	retryBase, retryMax = time.Hour, time.Hour
	defer func() { retryBase, retryMax = was, wasMax }()
	dead := &mysql.MySQLError{Number: 1213, Message: "Deadlock found"}
	type cause struct{ error }
	mine := cause{errors.New("the caller's reason")}
	db := newDB(MySQL, &fakeconn.Exec{}, nil)
	for name, c := range map[string]struct {
		ctx  func() (context.Context, context.CancelFunc)
		fn   func(cancel context.CancelFunc)
		want []error
	}{
		"canceled during the wait": {
			ctx:  func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			fn:   func(cancel context.CancelFunc) { time.AfterFunc(time.Millisecond, cancel) },
			want: []error{context.Canceled},
		},
		"canceled before the wait": {
			ctx:  func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			fn:   func(cancel context.CancelFunc) { cancel() },
			want: []error{context.Canceled},
		},
		"the caller's deadline during the wait": {
			ctx: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 5*time.Millisecond)
			},
			fn:   func(context.CancelFunc) {},
			want: []error{context.DeadlineExceeded},
		},
		"canceled with a cause": {
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancelCause(context.Background())
				return ctx, func() { cancel(mine) }
			},
			fn:   func(cancel context.CancelFunc) { time.AfterFunc(time.Millisecond, cancel) },
			want: []error{context.Canceled, mine},
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := c.ctx()
			defer cancel()
			calls := 0
			err := db.Tx(ctx, func(context.Context, *DB) error {
				calls++
				c.fn(cancel)
				return dead
			}, TxRetryRolledBack(3))
			if calls != 1 || !dberr.Is(err, dead) {
				t.Errorf("%v after %d attempts", err, calls)
			}
			for _, w := range c.want {
				if !dberr.Is(err, w) {
					t.Errorf("%v does not match %v", err, w)
				}
			}
			if dberr.Is(err, ErrTimeout) {
				t.Errorf("%v matches ErrTimeout", err)
			}
		})
	}
}

// TestRetryEndedThroughATxAround: a TxAround hook may pass on a context without the
// cancellation of the one Tx bounded, whose end still stops the retry.
func TestRetryEndedThroughATxAround(t *testing.T) {
	was, wasMax := retryBase, retryMax
	retryBase, retryMax = time.Hour, time.Hour
	defer func() { retryBase, retryMax = was, wasMax }()
	dead := &mysql.MySQLError{Number: 1213, Message: "Deadlock found"}
	db := newDB(MySQL, &fakeconn.Exec{}, []Option{WithTxAround(func(ctx context.Context, _ *TxInfo, next func(context.Context) error) error {
		return next(context.WithoutCancel(ctx))
	})})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := db.Tx(ctx, func(context.Context, *DB) error {
		time.AfterFunc(time.Millisecond, cancel)
		return dead
	}, TxRetryRolledBack(3))
	if !dberr.Is(err, dead) || !dberr.Is(err, context.Canceled) {
		t.Errorf("%v", err)
	}
}

// TestRetryEndedAtTheTimeoutBeforeTheWait: velox's deadline passed before the wait
// began, so the error already says so, once.
func TestRetryEndedAtTheTimeoutBeforeTheWait(t *testing.T) {
	dead := &mysql.MySQLError{Number: 1213, Message: "Deadlock found"}
	db := newDB(MySQL, &fakeconn.Exec{}, nil)
	err := db.Tx(context.Background(), func(ctx context.Context, _ *DB) error {
		<-ctx.Done()
		return dead
	}, TxRetryRolledBack(3), TxTimeout(time.Millisecond))
	if !dberr.Is(err, dead) || !dberr.Is(err, ErrTimeout) || strings.Count(err.Error(), ErrTimeout.Error()) != 1 {
		t.Errorf("%v", err)
	}
}

func canceled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
