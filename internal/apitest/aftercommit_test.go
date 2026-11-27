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
			if err := velox.AfterCommit(ctx, tx, func(ctx context.Context) {
				if ctx.Done() != nil || ctx.Value(afterKey{}) != "v" {
					t.Errorf("%s: Done %v, value %v", name, ctx.Done(), ctx.Value(afterKey{}))
				}
				got = append(got, name+":"+strings.Repeat("x", invoiceCount(t, db)))
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
		if err := velox.AfterCommit(ctx, tx, func(context.Context) { ran++ }); err != nil {
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
		if err := velox.AfterCommit(ctx, tx, func(context.Context) { got = append(got, s) }); err != nil {
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

// TestAfterCommitStaleSavepoint: a nested Tx's DB, kept past its return,
// queues nothing on the transaction still running: AfterCommit returns an
// error, whether the savepoint rolled back or was released, and nothing
// runs after the outer commit.
func TestAfterCommitStaleSavepoint(t *testing.T) {
	db := useTxDB(t)
	ran := 0
	fn := func(context.Context) { ran++ }
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		var rolled, released *velox.DB
		_ = tx.Tx(ctx, func(_ context.Context, sp *velox.DB) error { rolled = sp; return errors.New("boom") })
		if err := tx.Tx(ctx, func(_ context.Context, sp *velox.DB) error { released = sp; return nil }); err != nil {
			return err
		}
		if err := velox.AfterCommit(ctx, rolled, fn); !errors.Is(err, velox.ErrNoTx) {
			t.Error("a rolled-back savepoint's DB queued a function")
		}
		if err := velox.AfterCommit(ctx, released, fn); !errors.Is(err, velox.ErrNoTx) {
			t.Error("a released savepoint's DB queued a function")
		}
		return velox.AfterCommit(ctx, tx, func(context.Context) {})
	})
	if err != nil {
		t.Fatal(err)
	}
	if ran != 0 {
		t.Errorf("a stale savepoint's function ran %d times", ran)
	}
}

// TestAfterCommitRefusals: outside a transaction, after it ended, on a
// UseTx DB (and its savepoint) and with a nil fn, AfterCommit returns an
// error and nothing runs.
func TestAfterCommitRefusals(t *testing.T) {
	db := useTxDB(t)
	ran := false
	fn := func(context.Context) { ran = true }
	if err := velox.AfterCommit(ctx, db, fn); !errors.Is(err, velox.ErrNoTx) || errors.Is(err, velox.ErrAdoptedTx) || !strings.Contains(err.Error(), "no transaction") {
		t.Errorf("outside a transaction: %v", err)
	}
	if velox.InTx(db) {
		t.Error("InTx of the pool DB is true")
	}
	var kept *velox.DB
	if err := db.Tx(ctx, func(_ context.Context, tx *velox.DB) error {
		kept = tx
		if err := velox.AfterCommit(ctx, tx, nil); err == nil || !strings.Contains(err.Error(), "nil") {
			t.Errorf("nil fn: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := velox.AfterCommit(ctx, kept, fn); !errors.Is(err, velox.ErrNoTx) || !strings.Contains(err.Error(), "ended") {
		t.Errorf("after the transaction: %v", err)
	}
	if !velox.InTx(kept) {
		t.Error("InTx of an ended transaction's DB is false")
	}
	tx, vtx := beginUse(t, db)
	if !velox.InTx(vtx) {
		t.Error("InTx of a UseTx DB is false")
	}
	for _, refuse := range []func(*velox.DB) error{
		func(d *velox.DB) error { return velox.AfterCommit(ctx, d, fn) },
		func(d *velox.DB) error { return velox.AfterRollback(ctx, d, fn) },
		func(d *velox.DB) error { return velox.BeforeCommit(ctx, d, func(context.Context) error { return nil }) },
	} {
		if err := refuse(vtx); !errors.Is(err, velox.ErrAdoptedTx) || errors.Is(err, velox.ErrNoTx) || !strings.Contains(err.Error(), "UseTx") {
			t.Errorf("UseTx: %v", err)
		}
		if err := vtx.Tx(ctx, func(_ context.Context, sp *velox.DB) error { return refuse(sp) }); !errors.Is(err, velox.ErrAdoptedTx) || !strings.Contains(err.Error(), "UseTx") {
			t.Errorf("UseTx savepoint: %v", err)
		}
		if err := refuse(db); !errors.Is(err, velox.ErrNoTx) || errors.Is(err, velox.ErrAdoptedTx) {
			t.Errorf("no transaction: %v", err)
		}
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
				if err := velox.AfterCommit(ctx, tx, func(context.Context) {
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
		return velox.AfterCommit(ctx, m.DB(), func(context.Context) { ran++ })
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

// TestQueueResolvesFromContext: under the context a transaction's fn got, the
// DB that began it queues as its statements join (0171): on the transaction,
// or on the savepoint whose fn got the context. The same DB under another
// context, another DB under this one, and a handle whose transaction or
// savepoint ended are refused with ErrNoTx, which says the handle is not a
// running transaction's, not that the work is committed.
func TestQueueResolvesFromContext(t *testing.T) {
	db, other := useTxDB(t), useTxDB(t)
	var got []string
	note := func(s string) func(context.Context) { return func(context.Context) { got = append(got, s) } }
	var released, kept *velox.DB
	err := db.Tx(ctx, func(tctx context.Context, tx *velox.DB) error {
		if err := velox.AfterCommit(tctx, db, note("commit")); err != nil {
			t.Errorf("pool DB under the transaction's ctx: %v", err)
		}
		if err := velox.BeforeCommit(tctx, db, func(context.Context) error { got = append(got, "before"); return nil }); err != nil {
			t.Errorf("BeforeCommit, pool DB under the transaction's ctx: %v", err)
		}
		if err := velox.AfterCommit(ctx, db, note("never")); !errors.Is(err, velox.ErrNoTx) {
			t.Errorf("pool DB under another ctx: %v", err)
		}
		if err := velox.AfterCommit(tctx, other, note("never")); !errors.Is(err, velox.ErrNoTx) {
			t.Errorf("another DB under the transaction's ctx: %v", err)
		}
		_ = tx.Tx(tctx, func(sctx context.Context, sp *velox.DB) error {
			released = sp
			if err := velox.AfterRollback(sctx, db, note("savepoint rolled back")); err != nil {
				t.Errorf("AfterRollback, pool DB under a savepoint's ctx: %v", err)
			}
			return errors.New("roll the savepoint back")
		})
		if err := velox.AfterCommit(tctx, released, note("never")); !errors.Is(err, velox.ErrNoTx) {
			t.Errorf("returned savepoint's DB, transaction running: %v", err)
		}
		kept = tx
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"before", "savepoint rolled back", "commit"}; !slices.Equal(got, want) {
		t.Errorf("ran %v, want %v", got, want)
	}
	if err := velox.AfterCommit(ctx, kept, note("never")); !errors.Is(err, velox.ErrNoTx) {
		t.Errorf("ended transaction's DB: %v", err)
	}
	_ = db.Tx(ctx, func(tctx context.Context, tx *velox.DB) error { kept = tx; return errors.New("roll back") })
	if err := velox.AfterCommit(ctx, kept, note("never")); !errors.Is(err, velox.ErrNoTx) {
		t.Errorf("rolled-back transaction's DB: %v", err)
	}
	if slices.Contains(got, "never") {
		t.Errorf("a refused function ran: %v", got)
	}
}
