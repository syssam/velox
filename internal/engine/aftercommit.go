package engine

import (
	"context"
	"errors"
)

// AfterCommit queues fn on db's transaction, to run once the outermost
// transaction has committed (ADR 0100). db must be a transaction's DB: the
// tx a Tx gives its fn, a savepoint's, or Mutation.DB inside one. It
// returns an error, and queues nothing, for a nil fn, for a db in no
// transaction, for one whose transaction has ended, and for a UseTx DB,
// whose commit velox never sees.
//
// It is a function, not a method, as UseTx is: the engine's DB's method set
// is part of what every entity's package compiles.
func AfterCommit(db *DB, fn func(context.Context)) error {
	switch {
	case fn == nil:
		return errors.New("velox: AfterCommit: fn is nil")
	case !db.InTx:
		return errors.New("velox: AfterCommit: the DB is in no transaction, so there is no commit to wait for; call it on the tx that Tx gives fn")
	case db.txRoot.adopted:
		return errors.New("velox: AfterCommit: the transaction is a UseTx one, which your code commits and velox never sees end; run fn after your tx.Commit")
	case db.txRoot.ended.Load():
		return errors.New("velox: AfterCommit: the transaction has ended")
	}
	db.txRoot.after = append(db.txRoot.after, fn)
	return nil
}

// unqueue drops the AfterCommit functions queued in f's transaction after
// the first n, those of a savepoint that rolled back. f is the root frame.
func (f *txFrame) unqueue(n int) {
	if n < len(f.after) {
		clear(f.after[n:])
		f.after = f.after[:n]
	}
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
