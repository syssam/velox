package apitest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

type afterKey struct{}

// TestAfterCommitRunsAfterTheCommit: the functions run in the order queued,
// after COMMIT and before Tx returns, on the context given to Tx without its
// cancellation, and see the committed row from db: the connection, a pool's
// only one, is free.
func TestAfterCommitRunsAfterTheCommit(t *testing.T) {
	db := useTxDB(t)
	var got []string
	tctx, cancel := context.WithCancel(context.WithValue(ctx, afterKey{}, "v"))
	defer cancel()
	err := db.Tx(tctx, func(ctx context.Context, tx *velox.DB) error {
		if err := tx.Insert(&testmodel.Invoice{OrgID: 1, Number: "a", Data: []byte{}}).Exec(ctx); err != nil {
			return err
		}
		for _, name := range []string{"first", "second"} {
			if err := tx.AfterCommit(func(ctx context.Context) {
				if ctx.Done() != nil || ctx.Value(afterKey{}) != "v" {
					t.Errorf("%s: Done %v, value %v", name, ctx.Done(), ctx.Value(afterKey{}))
				}
				got = append(got, name+":"+strings.Repeat("x", int(invoiceCount(t, db))))
			}); err != nil {
				return err
			}
		}
		if len(got) != 0 {
			t.Errorf("ran before the commit: %v", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"first:x", "second:x"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestAfterCommitNotOnRollback: neither fn's error, nor its panic, nor a
// commit refused runs a queued function.
func TestAfterCommitNotOnRollback(t *testing.T) {
	db := useTxDB(t)
	ran := 0
	queue := func(tx *velox.DB) {
		if err := tx.AfterCommit(func(context.Context) { ran++ }); err != nil {
			t.Fatal(err)
		}
	}
	boom := errors.New("boom")
	if err := db.Tx(ctx, func(_ context.Context, tx *velox.DB) error { queue(tx); return boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	func() {
		defer func() { _ = recover() }()
		_ = db.Tx(ctx, func(_ context.Context, tx *velox.DB) error { queue(tx); panic("in fn") })
	}()
	cctx, cancel := context.WithCancel(ctx)
	if err := db.Tx(cctx, func(_ context.Context, tx *velox.DB) error { queue(tx); cancel(); return nil }); err == nil {
		t.Fatal("a canceled transaction committed")
	}
	if ran != 0 {
		t.Errorf("ran %d times", ran)
	}
}

// TestAfterCommitSavepoint: a nested Tx that rolls back drops what was
// queued while it ran, through its own tx or the outer one; one that
// succeeds keeps it, and all run in order after the outer commit.
func TestAfterCommitSavepoint(t *testing.T) {
	db := useTxDB(t)
	var got []string
	q := func(tx *velox.DB, s string) {
		if err := tx.AfterCommit(func(context.Context) { got = append(got, s) }); err != nil {
			t.Fatal(err)
		}
	}
	boom := errors.New("boom")
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		q(tx, "outer1")
		if err := tx.Tx(ctx, func(ctx context.Context, sp *velox.DB) error {
			q(sp, "kept")
			return nil
		}); err != nil {
			return err
		}
		if err := tx.Tx(ctx, func(ctx context.Context, sp *velox.DB) error {
			q(sp, "kept2")
			q(tx, "outer-in-savepoint")
			return sp.Tx(ctx, func(context.Context, *velox.DB) error { return nil })
		}); err != nil {
			return err
		}
		_ = tx.Tx(ctx, func(ctx context.Context, sp *velox.DB) error {
			q(sp, "rolled-back")
			q(tx, "outer-in-rolled-back")
			if err := sp.Tx(ctx, func(_ context.Context, sp2 *velox.DB) error { q(sp2, "inner"); return nil }); err != nil {
				return err
			}
			return boom
		})
		func() {
			defer func() { _ = recover() }()
			_ = tx.Tx(ctx, func(_ context.Context, sp *velox.DB) error { q(sp, "panicked"); panic("x") })
		}()
		q(tx, "outer2")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"outer1", "kept", "kept2", "outer-in-savepoint", "outer2"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestAfterCommitRefusals: outside a transaction, after it ended, on a
// UseTx DB (and its savepoint) and with a nil fn, AfterCommit returns an
// error and nothing runs.
func TestAfterCommitRefusals(t *testing.T) {
	db := useTxDB(t)
	ran := false
	fn := func(context.Context) { ran = true }
	if err := db.AfterCommit(fn); err == nil || !strings.Contains(err.Error(), "no transaction") {
		t.Errorf("outside a transaction: %v", err)
	}
	var kept *velox.DB
	if err := db.Tx(ctx, func(_ context.Context, tx *velox.DB) error {
		kept = tx
		if err := tx.AfterCommit(nil); err == nil || !strings.Contains(err.Error(), "nil") {
			t.Errorf("nil fn: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := kept.AfterCommit(fn); err == nil || !strings.Contains(err.Error(), "ended") {
		t.Errorf("after the transaction: %v", err)
	}
	tx, vtx := beginUse(t, db)
	if err := vtx.AfterCommit(fn); err == nil || !strings.Contains(err.Error(), "UseTx") {
		t.Errorf("UseTx: %v", err)
	}
	if err := vtx.Tx(ctx, func(_ context.Context, sp *velox.DB) error { return sp.AfterCommit(fn) }); err == nil || !strings.Contains(err.Error(), "UseTx") {
		t.Errorf("UseTx savepoint: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Error("a refused fn ran")
	}
}

// TestAfterCommitPanics: a panicking function does not stop the others;
// Tx panics with the first value once all ran, the transaction committed.
func TestAfterCommitPanics(t *testing.T) {
	db := useTxDB(t)
	var got []int
	var rec any
	func() {
		defer func() { rec = recover() }()
		_ = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			if err := tx.Insert(&testmodel.Invoice{OrgID: 1, Number: "a", Data: []byte{}}).Exec(ctx); err != nil {
				return err
			}
			for i := range 3 {
				if err := tx.AfterCommit(func(context.Context) {
					got = append(got, i)
					if i != 1 {
						panic(i)
					}
				}); err != nil {
					return err
				}
			}
			return nil
		})
	}()
	if rec != 0 || !slices.Equal(got, []int{0, 1, 2}) {
		t.Errorf("recovered %v, ran %v", rec, got)
	}
	if n := invoiceCount(t, db); n != 1 {
		t.Errorf("%d rows, want the committed 1", n)
	}
}

// TestAfterCommitFromAHook: a hook queues through Mutation.DB, inside a Tx.
func TestAfterCommitFromAHook(t *testing.T) {
	ran := 0
	db := useTxDB(t)
	hooked := velox.Open(velox.SQLite, velox.Pool(db), velox.WithHooks[testmodel.Invoice](velox.Before(func(_ context.Context, m *velox.Mutation) error {
		return m.DB().AfterCommit(func(context.Context) { ran++ })
	}, velox.OpInsert))).WithoutPolicy("velox tests")
	err := hooked.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		return tx.Insert(&testmodel.Invoice{OrgID: 1, Number: "a", Data: []byte{}}).Exec(ctx)
	})
	if err != nil || ran != 1 {
		t.Errorf("in a Tx: ran %d, %v", ran, err)
	}
	err = hooked.Insert(&testmodel.Invoice{OrgID: 1, Number: "b", Data: []byte{}}).Exec(ctx)
	if err == nil || !strings.Contains(err.Error(), "no transaction") || ran != 1 {
		t.Errorf("outside a Tx: ran %d, %v", ran, err)
	}
	// A recorded write runs in a transaction velox opens for it, inside the
	// hooks: Mutation.DB is still the database's.
	rec := hooked.Record(velox.RecordFunc(func(context.Context, *velox.DB, []velox.Change) error { return nil }))
	err = rec.Insert(&testmodel.Invoice{OrgID: 1, Number: "c", Data: []byte{}}).Exec(ctx)
	if err == nil || !strings.Contains(err.Error(), "no transaction") || ran != 1 {
		t.Errorf("a recorded write: ran %d, %v", ran, err)
	}
}
