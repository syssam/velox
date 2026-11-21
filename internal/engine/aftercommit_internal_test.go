package engine

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/syssam/velox/internal/fakeconn"
)

// TestAfterCommitRetryDropsTheFailedAttempts: an attempt that failed and
// was retried takes its queued functions with it; only the committed
// attempt's run, once, after the TxAround hooks have returned.
func TestAfterCommitRetryDropsTheFailedAttempts(t *testing.T) {
	recordRetryWaits(t)
	dead := &mysql.MySQLError{Number: 1213, Message: "Deadlock found"}
	var got []string
	db := newDB(MySQL, &fakeconn.Exec{}, []Option{WithTxAround(func(ctx context.Context, _ *TxInfo, next func(context.Context) error) error {
		err := next(ctx)
		got = append(got, "hook returned")
		return err
	})})
	attempt := 0
	err := db.Tx(context.Background(), func(_ context.Context, tx *DB) error {
		attempt++
		n := attempt
		if err := AfterCommit(tx, func(context.Context) { got = append(got, "attempt "+string(rune('0'+n))) }); err != nil {
			return err
		}
		if n < 3 {
			return dead
		}
		return nil
	}, TxRetrySerializable(5))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"hook returned", "attempt 3"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestAfterCommitRunsWhenAHookFailsAfterTheCommit: a TxAround hook that
// returns an error of its own once next committed does not undo the
// commit, so the queued functions run and Tx returns the hook's error.
func TestAfterCommitRunsWhenAHookFailsAfterTheCommit(t *testing.T) {
	hookErr := errors.New("hook")
	db := newDB(Postgres, &fakeconn.Exec{}, []Option{WithTxAround(func(ctx context.Context, _ *TxInfo, next func(context.Context) error) error {
		if err := next(ctx); err != nil {
			return err
		}
		return hookErr
	})})
	ran := false
	err := db.Tx(context.Background(), func(_ context.Context, tx *DB) error {
		return AfterCommit(tx, func(context.Context) { ran = true })
	})
	if !errors.Is(err, hookErr) || !ran {
		t.Errorf("err %v, ran %v", err, ran)
	}
}

// TestAfterCommitRunsWhenAHookPanicsAfterTheCommit: a TxAround hook that
// panics once next committed does not skip the queued functions; they run,
// then the hook's panic goes on. The allocation budgets
// (TestTxAroundAllocationBudget) hold with the deferred recover.
func TestAfterCommitRunsWhenAHookPanicsAfterTheCommit(t *testing.T) {
	db := newDB(Postgres, &fakeconn.Exec{}, []Option{WithTxAround(func(ctx context.Context, _ *TxInfo, next func(context.Context) error) error {
		if err := next(ctx); err != nil {
			return err
		}
		panic("hook")
	})})
	ran := 0
	var rec any
	func() {
		defer func() { rec = recover() }()
		_ = db.Tx(context.Background(), func(_ context.Context, tx *DB) error {
			if err := AfterCommit(tx, func(context.Context) { ran++ }); err != nil {
				return err
			}
			return AfterCommit(tx, func(context.Context) { ran++; panic("fn") })
		})
	}()
	if rec != "hook" || ran != 2 {
		t.Errorf("recovered %v, ran %d", rec, ran)
	}

	// A hook that panics before any commit lets the panic go, and runs nothing.
	db = newDB(Postgres, &fakeconn.Exec{}, []Option{WithTxAround(func(context.Context, *TxInfo, func(context.Context) error) error {
		panic("early")
	})})
	func() {
		defer func() { rec = recover() }()
		_ = db.Tx(context.Background(), func(context.Context, *DB) error { return nil })
	}()
	if rec != "early" {
		t.Errorf("recovered %v", rec)
	}
}

// TestAfterCommitConcurrentCallIsNeverLost: a call that races the end of
// the transaction either is queued and runs, or returns an error.
func TestAfterCommitConcurrentCallIsNeverLost(t *testing.T) {
	for range 20 {
		db := newDB(Postgres, &fakeconn.Exec{}, nil)
		var mu sync.Mutex
		queued, ran := 0, 0
		done := make(chan struct{})
		started := make(chan struct{})
		err := db.Tx(context.Background(), func(_ context.Context, tx *DB) error {
			go func() {
				defer close(done)
				close(started)
				for {
					err := AfterCommit(tx, func(context.Context) { mu.Lock(); ran++; mu.Unlock() })
					if err != nil {
						return
					}
					mu.Lock()
					queued++
					mu.Unlock()
				}
			}()
			<-started
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		<-done
		mu.Lock()
		if queued != ran {
			t.Errorf("queued %d, ran %d", queued, ran)
		}
		mu.Unlock()
	}
}
