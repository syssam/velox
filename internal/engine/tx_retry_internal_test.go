package engine

import (
	"context"
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
	}, TxRetrySerializable(retries))
	if !is(err, dead) {
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
	}, TxRetrySerializable(3))
	if !is(err, dead) || calls != 1 {
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

// TestRetryStopsAtTheDeadline: a retry the transaction's deadline would cut
// off does not wait for it.
func TestRetryStopsAtTheDeadline(t *testing.T) {
	waits := recordRetryWaits(t)
	was := retryBase
	retryBase = time.Hour
	defer func() { retryBase = was }()
	wasMax := retryMax
	retryMax = time.Hour
	defer func() { retryMax = wasMax }()
	dead := &mysql.MySQLError{Number: 1213, Message: "Deadlock found"}
	db := newDB(MySQL, &fakeconn.Exec{}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	calls := 0
	err := db.Tx(ctx, func(context.Context, *DB) error { calls++; return dead }, TxRetrySerializable(3))
	if !is(err, dead) || calls != 1 || len(*waits) != 0 {
		t.Errorf("%v after %d attempts and %d waits", err, calls, len(*waits))
	}
}

func canceled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
