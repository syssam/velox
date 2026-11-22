package engine

import (
	"context"
	"errors"
)

// AfterCommit queues fn on db's transaction, to run once the outermost
// transaction has committed (ADR 0100). db must be a transaction's DB: the
// tx a Tx gives its fn, a savepoint's, or Mutation.DB inside one. It
// returns an error, and queues nothing, for a nil fn, for a db in no
// transaction, for one whose transaction has ended or is ending, for a
// nested Tx's db once that Tx has returned (its savepoint released or
// rolled back), and for a UseTx DB, whose commit velox never sees.
//
// The root frame's mutex orders it with the transaction's end (takeAfter,
// closeAfter): a call that comes too late gets an error, and is never
// queued where nothing will run it.
func AfterCommit(db *DB, fn func(context.Context)) error {
	if err := txOf(db, fn == nil, "AfterCommit"); err != nil {
		return err
	}
	f := db.txRoot
	f.afterMu.Lock()
	defer f.afterMu.Unlock()
	if err := queueErr(db, "AfterCommit"); err != nil {
		return err
	}
	f.after = append(f.after, fn)
	return nil
}

// rolledFn is an AfterRollback function; due says a savepoint it was
// queued in rolled back, so it runs whatever the transaction does.
type rolledFn struct {
	fn  func(context.Context)
	due bool
}

// queueErr is why fn cannot be queued on db's transaction by the function
// what, or nil. The caller holds the root frame's afterMu.
func queueErr(db *DB, what string) error {
	f := db.txRoot
	if f.afterClosed || f.ended.Load() {
		return errors.New("velox: " + what + ": the transaction has ended")
	}
	if db.txSelf != nil && db.txSelf.ended.Load() {
		return errors.New("velox: " + what + ": the DB is a nested Tx's that has returned; call it on a DB of the transaction that is still running")
	}
	return nil
}

// txOf checks db for the queue functions: fn is not nil, db is in a
// transaction velox began. what names the function.
func txOf(db *DB, isNil bool, what string) error {
	switch {
	case isNil:
		return errors.New("velox: " + what + ": fn is nil")
	case !db.InTx:
		return errors.New("velox: " + what + ": the DB is in no transaction; call it on the tx that Tx gives fn")
	case db.txRoot.adopted:
		return errors.New("velox: " + what + ": the transaction is a UseTx one, which your code commits and velox never sees end")
	}
	return nil
}

// BeforeCommit queues fn on db's transaction, to run once the outermost
// Tx's fn has returned nil, before COMMIT (ADR 0111). An error from fn
// rolls the transaction back, and Tx returns it. It refuses what
// AfterCommit refuses, and a call once the BeforeCommit functions have run.
func BeforeCommit(db *DB, fn func(context.Context) error) error {
	if err := txOf(db, fn == nil, "BeforeCommit"); err != nil {
		return err
	}
	f := db.txRoot
	f.afterMu.Lock()
	defer f.afterMu.Unlock()
	if err := queueErr(db, "BeforeCommit"); err != nil {
		return err
	}
	if f.beforeDone {
		return errors.New("velox: BeforeCommit: the transaction is committing")
	}
	f.before = append(f.before, fn)
	return nil
}

// AfterRollback queues fn on db's transaction, to run once the work done
// through db is rolled back (ADR 0111): when the outermost transaction does
// not commit, or when db is a nested Tx's and its savepoint rolls back. It
// refuses what AfterCommit refuses.
func AfterRollback(db *DB, fn func(context.Context)) error {
	if err := txOf(db, fn == nil, "AfterRollback"); err != nil {
		return err
	}
	f := db.txRoot
	f.afterMu.Lock()
	defer f.afterMu.Unlock()
	if err := queueErr(db, "AfterRollback"); err != nil {
		return err
	}
	f.rolled = append(f.rolled, rolledFn{fn: fn})
	return nil
}

// runBefore runs the BeforeCommit functions of f, the root frame, in
// order, on ctx, the transaction's; one queued while they run runs too.
// The first error stops them and is returned.
func (f *txFrame) runBefore(ctx context.Context) error {
	for i := 0; ; i++ {
		f.afterMu.Lock()
		if i >= len(f.before) {
			f.beforeDone = true
			f.afterMu.Unlock()
			return nil
		}
		fn := f.before[i]
		f.afterMu.Unlock()
		if err := fn(ctx); err != nil {
			f.afterMu.Lock()
			f.beforeDone = true
			f.afterMu.Unlock()
			return err
		}
	}
}

// queueMark is a savepoint's mark into the root frame's queues.
type queueMark struct{ after, before, rolled int }

// queueMark is the length of each queue of f's transaction, a savepoint's
// mark. f is the root frame.
func (f *txFrame) queueMark() queueMark {
	f.afterMu.Lock()
	defer f.afterMu.Unlock()
	return queueMark{len(f.after), len(f.before), len(f.rolled)}
}

// unqueue undoes, after the mark m, what a savepoint that rolled back
// queued: it drops its AfterCommit and BeforeCommit functions, and makes
// its AfterRollback functions due. f is the root frame.
func (f *txFrame) unqueue(m queueMark) {
	f.afterMu.Lock()
	defer f.afterMu.Unlock()
	if m.after < len(f.after) {
		clear(f.after[m.after:])
		f.after = f.after[:m.after]
	}
	if m.before < len(f.before) {
		clear(f.before[m.before:])
		f.before = f.before[:m.before]
	}
	for i := m.rolled; i < len(f.rolled); i++ {
		f.rolled[i].due = true
	}
}

// takeAfter closes f's queues, once its transaction has committed, and
// returns what then runs: the due AfterRollback functions, then the
// AfterCommit ones.
func (f *txFrame) takeAfter() []func(context.Context) {
	f.afterMu.Lock()
	defer f.afterMu.Unlock()
	f.afterClosed = true
	var out []func(context.Context)
	for _, r := range f.rolled {
		if r.due {
			out = append(out, r.fn)
		}
	}
	if out == nil {
		return f.after
	}
	return append(out, f.after...)
}

// closeAfter closes f's queues and drops the AfterCommit ones: the
// transaction did not commit. It returns the AfterRollback functions, all
// of which then run.
func (f *txFrame) closeAfter() []func(context.Context) {
	f.afterMu.Lock()
	defer f.afterMu.Unlock()
	f.afterClosed = true
	f.after, f.before = nil, nil
	var out []func(context.Context)
	for _, r := range f.rolled {
		out = append(out, r.fn)
	}
	return out
}

// runAfter runs fns, a committed transaction's AfterCommit functions, in
// order, on ctx without its cancellation or deadline. A panic in one does
// not stop the others; the first is raised again once all have run.
func runAfter(ctx context.Context, fns []func(context.Context)) {
	if len(fns) == 0 {
		return
	}
	ctx = context.WithoutCancel(ctx)
	var first any
	panicked := false
	for _, fn := range fns {
		func() {
			defer func() {
				if r := recover(); r != nil && !panicked {
					first, panicked = r, true
				}
			}()
			fn(ctx)
		}()
	}
	if panicked {
		panic(first)
	}
}
