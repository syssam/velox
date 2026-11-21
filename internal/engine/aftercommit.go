package engine

import (
	"context"
	"errors"
)

// AfterCommit queues fn on db's transaction, to run once the outermost
// transaction has committed (ADR 0100). db must be a transaction's DB: the
// tx a Tx gives its fn, a savepoint's, or Mutation.DB inside one. It
// returns an error, and queues nothing, for a nil fn, for a db in no
// transaction, for one whose transaction has ended or is ending, and for a
// UseTx DB, whose commit velox never sees.
//
// The root frame's mutex orders it with the transaction's end (takeAfter,
// closeAfter): a call that comes too late gets an error, and is never
// queued where nothing will run it.
func AfterCommit(db *DB, fn func(context.Context)) error {
	switch {
	case fn == nil:
		return errors.New("velox: AfterCommit: fn is nil")
	case !db.InTx:
		return errors.New("velox: AfterCommit: the DB is in no transaction, so there is no commit to wait for; call it on the tx that Tx gives fn")
	case db.txRoot.adopted:
		return errors.New("velox: AfterCommit: the transaction is a UseTx one, which your code commits and velox never sees end; run fn after your tx.Commit")
	}
	f := db.txRoot
	f.afterMu.Lock()
	defer f.afterMu.Unlock()
	if f.afterClosed || f.ended.Load() {
		return errors.New("velox: AfterCommit: the transaction has ended")
	}
	f.after = append(f.after, fn)
	return nil
}

// afterLen is the number of functions queued in f's transaction, a
// savepoint's mark. f is the root frame.
func (f *txFrame) afterLen() int {
	f.afterMu.Lock()
	defer f.afterMu.Unlock()
	return len(f.after)
}

// unqueue drops the AfterCommit functions queued in f's transaction after
// the first n, those of a savepoint that rolled back. f is the root frame.
func (f *txFrame) unqueue(n int) {
	f.afterMu.Lock()
	defer f.afterMu.Unlock()
	if n < len(f.after) {
		clear(f.after[n:])
		f.after = f.after[:n]
	}
}

// takeAfter closes f's queue, once its transaction has committed, and
// returns what it holds.
func (f *txFrame) takeAfter() []func(context.Context) {
	f.afterMu.Lock()
	defer f.afterMu.Unlock()
	f.afterClosed = true
	return f.after
}

// closeAfter closes f's queue and drops it: the transaction did not commit.
func (f *txFrame) closeAfter() {
	f.afterMu.Lock()
	defer f.afterMu.Unlock()
	f.afterClosed = true
	f.after = nil
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
