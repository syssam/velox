package itest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func TestGuardsOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		if _, err := tg.db.Query[testmodel.Invoice]().All(ctx); !errors.Is(err, velox.ErrUnbounded) {
			t.Errorf("no limit: %v", err)
		}
		if _, err := tg.db.Delete[testmodel.Invoice]().Exec(ctx); !errors.Is(err, velox.ErrNoWhere) {
			t.Errorf("no where: %v", err)
		}
		if _, err := tg.db.Query[testmodel.Invoice]().Unbounded().All(ctx); !errors.Is(err, velox.ErrTooManyRows) {
			t.Errorf("five rows, cap 3: %v", err)
		}
		if got, err := tg.db.Query[testmodel.Invoice]().Limit(3).All(ctx); err != nil || len(got) != 3 {
			t.Errorf("limit 3: %d, %v", len(got), err)
		}
		if n, err := tg.db.Query[testmodel.Invoice]().Count(ctx); err != nil || n != 5 {
			t.Errorf("count: %d, %v", n, err)
		}
	}, velox.WithMaxRows(3))
}

// lockRow holds a lock on invoice 1 from another connection until the test
// ends, so a velox write to it waits.
func lockRow(t *testing.T, tg target) {
	t.Helper()
	lock, err := tg.raw.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Rollback() })
	if _, err := lock.ExecContext(ctx, rebind(tg.d, "UPDATE invoices SET total = total WHERE id = ?"), 1); err != nil {
		t.Fatal(err)
	}
}

func TestStatementDeadlineOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		if tg.raw == nil {
			return // SQLite: one connection, nothing to wait on
		}
		lockRow(t, tg)
		start := time.Now()
		_, err := tg.db.Update[testmodel.Invoice]().Set(I.Total.Set(1)).Where(I.ID.EQ(1)).Exec(ctx)
		if !errors.Is(err, velox.ErrTimeout) || !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("%v", err)
		}
		if d := time.Since(start); d > 10*time.Second {
			t.Errorf("returned after %v", d)
		}
	}, velox.WithDeadlines(velox.Deadlines{Statement: 300 * time.Millisecond}))
}

func TestTxDeadlineOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		if tg.raw == nil {
			return
		}
		err := tg.db.Tx(ctx, func(_ context.Context, tx *velox.DB) error {
			if _, err := tx.Update[testmodel.Invoice]().Set(I.Total.Set(2)).Where(I.ID.EQ(2)).Exec(ctx); err != nil {
				return err
			}
			lockRow(t, tg)
			_, err := tx.Update[testmodel.Invoice]().Set(I.Total.Set(1)).Where(I.ID.EQ(1)).Exec(ctx)
			return err
		}, velox.Timeout(300*time.Millisecond))
		if !errors.Is(err, velox.ErrTimeout) {
			t.Fatalf("%v", err)
		}
		// pgx closes the connection the deadline cut the statement off on, so
		// the rollback fails; the server aborts the transaction anyway.
		if strings.Contains(err.Error(), "rollback") {
			t.Errorf("reports a rollback failure: %v", err)
		}
		var total float64
		if err := tg.raw.QueryRowContext(ctx, rebind(tg.d, "SELECT total FROM invoices WHERE id = ?"), 2).Scan(&total); err != nil {
			t.Fatal(err)
		}
		if total != 20 {
			t.Errorf("invoice 2 total %v: the timed-out transaction was not rolled back", total)
		}
	})
}

// TestCallersTxDeadlineOnRealDatabases: the caller's deadline on the
// transaction's context, earlier than velox's, bounds a statement inside it
// that is given a context without one.
func TestCallersTxDeadlineOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		if tg.raw == nil {
			return
		}
		cctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		defer cancel()
		start := time.Now()
		err := tg.db.Tx(cctx, func(_ context.Context, tx *velox.DB) error {
			lockRow(t, tg)
			_, err := tx.Update[testmodel.Invoice]().Set(I.Total.Set(1)).Where(I.ID.EQ(1)).Exec(ctx)
			return err
		})
		if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, velox.ErrTimeout) || strings.Contains(err.Error(), "rollback") {
			t.Errorf("%v", err)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("returned after %v", d)
		}
	}, velox.WithDeadlines(velox.Deadlines{MaxTx: time.Hour}))
}
