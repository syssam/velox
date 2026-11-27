package integration

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// TestBeforeCommitOrder: a recorded write's sink runs, then the BeforeCommit
// functions in order, whose write joins the transaction, then COMMIT, then
// the AfterCommit functions; no AfterRollback function runs.
func TestBeforeCommitOrder(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		var got []string
		db := tg.db.Record(velox.RecordFunc(func(context.Context, *velox.DB, []velox.Change) error {
			got = append(got, "sink")
			return nil
		}))
		err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			if err := velox.AfterCommit(ctx, tx, func(context.Context) { got = append(got, "after") }); err != nil {
				return err
			}
			if err := velox.AfterRollback(ctx, tx, func(context.Context) { got = append(got, "rolled") }); err != nil {
				return err
			}
			if err := velox.BeforeCommit(ctx, tx, func(ctx context.Context) error {
				got = append(got, "before")
				_, err := tx.Update[testmodel.Invoice]().Set(I.Total.Set(77)).Where(I.ID.EQ(1)).Exec(ctx)
				return err
			}); err != nil {
				return err
			}
			_, err := tx.Update[testmodel.Invoice]().Set(I.Total.Set(42)).Where(I.ID.EQ(2)).Exec(ctx)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"sink", "before", "sink", "after"}; !slices.Equal(got, want) {
			t.Errorf("order %v, want %v", got, want)
		}
		var v float64
		if err := tg.direct.QueryRowContext(ctx, rebind(tg.d, "SELECT total FROM invoices WHERE id = ?"), 1).Scan(&v); err != nil || v != 77 {
			t.Errorf("BeforeCommit's write: %v, %v", v, err)
		}
	})
}

// TestBeforeCommitErrorRollsBack: a BeforeCommit function's error rolls the
// transaction back and is Tx's; the AfterRollback functions run, the
// AfterCommit ones do not, and the queues are closed after Tx.
func TestBeforeCommitErrorRollsBack(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		boom := errors.New("boom")
		var got []string
		var stale *velox.DB
		err := tg.db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			stale = tx
			if _, err := tx.Update[testmodel.Invoice]().Set(I.Total.Set(42)).Where(I.ID.EQ(1)).Exec(ctx); err != nil {
				return err
			}
			_ = velox.AfterCommit(ctx, tx, func(context.Context) { got = append(got, "after") })
			_ = velox.AfterRollback(ctx, tx, func(context.Context) { got = append(got, "rolled") })
			return velox.BeforeCommit(ctx, tx, func(context.Context) error { return boom })
		})
		if !errors.Is(err, boom) {
			t.Fatalf("Tx: %v, want boom", err)
		}
		if want := []string{"rolled"}; !slices.Equal(got, want) {
			t.Errorf("ran %v, want %v", got, want)
		}
		var v float64
		if err := tg.direct.QueryRowContext(ctx, rebind(tg.d, "SELECT total FROM invoices WHERE id = ?"), 1).Scan(&v); err != nil || v != 10 {
			t.Errorf("total %v, %v; want 10, rolled back", v, err)
		}
		if err := velox.BeforeCommit(ctx, stale, func(context.Context) error { return nil }); err == nil {
			t.Error("BeforeCommit on an ended transaction")
		}
		if err := velox.AfterRollback(ctx, stale, func(context.Context) {}); err == nil {
			t.Error("AfterRollback on an ended transaction")
		}
		if err := velox.AfterRollback(ctx, tg.db, func(context.Context) {}); err == nil {
			t.Error("AfterRollback outside a transaction")
		}
	})
}

// TestSavepointRollbackHooks: a nested Tx that rolls back drops its
// BeforeCommit and AfterCommit functions and runs its AfterRollback ones,
// though the outer transaction commits; the outer ones run as on any
// commit.
func TestSavepointRollbackHooks(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		var got []string
		note := func(s string) func(context.Context) { return func(context.Context) { got = append(got, s) } }
		err := tg.db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			_ = velox.AfterRollback(ctx, tx, note("outer rolled"))
			_ = velox.BeforeCommit(ctx, tx, func(context.Context) error { got = append(got, "outer before"); return nil })
			err := tx.Tx(ctx, func(ctx context.Context, sp *velox.DB) error {
				_ = velox.AfterRollback(ctx, sp, note("savepoint rolled"))
				_ = velox.AfterCommit(ctx, sp, note("savepoint after"))
				_ = velox.BeforeCommit(ctx, sp, func(context.Context) error { got = append(got, "savepoint before"); return nil })
				return errors.New("undo")
			})
			if err == nil {
				t.Error("the nested Tx succeeded")
			}
			return velox.AfterCommit(ctx, tx, note("outer after"))
		})
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"outer before", "savepoint rolled", "outer after"}; !slices.Equal(got, want) {
			t.Errorf("ran %v, want %v", got, want)
		}
	})
}
