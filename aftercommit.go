package velox

import (
	"context"

	"github.com/syssam/velox/internal/engine"
)

// AfterCommit queues fn on tx's transaction, to run once the outermost
// transaction has committed, as for an event that must not go out for work
// that rolls back:
//
//	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
//		if err := tx.Insert(&order).Exec(ctx); err != nil {
//			return err
//		}
//		return velox.AfterCommit(tx, func(ctx context.Context) { publish(ctx, order.ID) })
//	})
//
// tx is the DB a Tx gives its fn, a nested Tx's, or Mutation.DB inside
// one. Outside db.Tx, Mutation.DB is the database's, even for a write that
// velox runs in a transaction of its own, inside the hooks, such as a
// recorded one. The queued functions run in the order queued,
// after COMMIT has succeeded and Tx's connection is back in the pool,
// before Tx returns, on the context given to Tx without its cancellation or
// deadline, even when a TxAround hook then returns an error of its own.
// They never run when the transaction rolls back, nor when its COMMIT
// fails; a retried attempt's are dropped with it. A nested Tx that
// rolls back drops the functions queued while it ran, through its tx or any
// other of the transaction's.
//
// A panic in one does not stop the others: once all have run, Tx panics
// again with the first panic's value, the transaction committed.
//
// AfterCommit returns an error, and queues nothing, for a nil fn, on a DB in
// no transaction, on one whose transaction has ended or is ending, on a
// nested Tx's DB once that Tx has returned, and on a UseTx DB, whose
// commit is your code's: velox never sees it. Call it from the goroutine
// that runs the transaction's fn, as for tx's statements; a call that races
// the end of the transaction returns an error and is never lost.
//
// It is a function, not a method of DB: every method of DB is compiled into
// each package of entities, used or not.
func AfterCommit(tx *DB, fn func(context.Context)) error { return engine.AfterCommit(&tx.e, fn) }

// BeforeCommit queues fn on tx's transaction, to run once the outermost
// Tx's fn has returned nil, before COMMIT, as ent's commit hooks do: a check
// that must see the whole transaction's work, or a last write in it.
//
//	return velox.BeforeCommit(tx, func(ctx context.Context) error {
//		return checkBalanced(ctx, tx)
//	})
//
// The functions run in the order queued, on fn's context, the
// transaction's, so their statements on tx join it; one queued while they
// run runs after them. An error from one stops the others, rolls the
// transaction back, and Tx returns it; a panic rolls back and panics on.
// They run after every recorded write of the transaction has called its
// sink (DB.Record), at the end of that write's savepoint, and a recorded
// write in one calls its sink before it returns; then COMMIT; then the
// AfterRollback functions a rolled back savepoint made due, and the
// AfterCommit functions.
//
// A nested Tx that rolls back drops the functions queued while it ran, as
// for AfterCommit. BeforeCommit returns an error, and queues nothing, where
// AfterCommit does, and once the transaction's BeforeCommit functions have
// run. It is a function, not a method of DB, for AfterCommit's reason.
func BeforeCommit(tx *DB, fn func(context.Context) error) error {
	return engine.BeforeCommit(&tx.e, fn)
}

// AfterRollback queues fn on tx's transaction, to run once the work done
// through tx has been rolled back, as ent's tx.OnRollback does: to undo
// what the transaction cannot, such as a file it uploaded.
//
//	return velox.AfterRollback(tx, func(ctx context.Context) { _ = store.Delete(ctx, key) })
//
// It runs when the outermost transaction does not commit: fn's error, a
// BeforeCommit function's error, a context that ended, a broken
// transaction, or a COMMIT that failed. When tx is a nested Tx's, it also
// runs when that savepoint rolls back, its work being undone, whatever the
// outer transaction then does. A retried attempt's run, as that attempt
// rolled back.
//
// The functions run as AfterCommit's do: in the order queued, once Tx's
// work is done and its connection is back in the pool, before Tx returns,
// on the context given to Tx without its cancellation or deadline; a panic
// in one does not stop the others. On a commit, those a savepoint made due
// run before the AfterCommit functions. None runs when fn panics or calls
// runtime.Goexit: Tx does not return normally. AfterRollback returns an
// error, and queues nothing, where AfterCommit does.
func AfterRollback(tx *DB, fn func(context.Context)) error {
	return engine.AfterRollback(&tx.e, fn)
}
