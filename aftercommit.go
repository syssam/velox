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
