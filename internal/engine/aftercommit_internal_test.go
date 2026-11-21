package engine

import (
	"context"
	"errors"
	"slices"
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
