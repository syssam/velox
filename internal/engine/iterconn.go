package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"time"
)

// This file refuses the statement that would wait forever for a pool's one
// connection: one made on the DB in the body of a loop over Iter, or in a
// Tx's fn, while the loop or the Tx holds the connection. A pool of one has
// one holder at a time, noted in config.connHold.
//
// The statement is told apart by its context, not by its goroutine, which Go
// does not name: a statement under the context the loop or the Tx was given,
// or under WithoutTx of fn's, is the holder's own, and is refused. Any other
// waits, as database/sql's does: a statement of another goroutine waits for
// the loop or the Tx to end. A statement of the holder's own goroutine under
// an unrelated context waits too, until its deadline: then its error says
// that the connection was held (heldHint). After heldWarnAfter of such a
// wait velox logs a warning (watchHeld), as one without a deadline would
// otherwise hang with nothing said.
//
// The holder is the DB's: another DB on the same *sql.DB (a second Open, or
// OpenConn with SQLConn) sees none, and waits. Finding the pool would cost
// those DBs a lookup in a process-wide table, for each statement.

var (
	// errIterConn is the error of a statement that waits for its own loop.
	errIterConn = errors.New("a statement inside a loop over Iter would wait forever for the one connection of the pool, which the loop holds; " +
		"collect the rows with All first, or run both in a Tx")
	// errTxConn is the error of a statement that waits for its own Tx.
	errTxConn = errors.New("a statement outside a Tx, under the context the Tx was given or WithoutTx of fn's, would wait forever for the one connection of the pool, which the Tx holds; " +
		"run it on the tx that fn was given, or after the Tx returns")
	// errHeld is joined to the error of a statement whose context ended while
	// it waited for a pool's one connection that an Iter or a Tx held.
	errHeld = errors.New("the pool's one connection was held by a loop over Iter or by a Tx while the statement waited; " +
		"a statement inside the loop or fn under another context waits until its deadline")
)

// heldWarnAfter is how long a statement waits for a pool's one connection,
// held by an Iter or a Tx of its DB, before watchHeld logs a warning.
var heldWarnAfter = 5 * time.Second

// heldWarning is what watchHeld logs.
const heldWarning = "velox: a statement has waited for the pool's one connection, which a Tx or a loop over Iter of its DB holds; " +
	"inside that Tx's fn or that loop it waits forever: run it on fn's tx, under fn's ctx, or after the loop"

// watchHeld watches a statement or BEGIN on db, of the entity named what,
// that may wait for the pool's one connection, which an Iter or a Tx of db
// holds under a context the statement's is not (waitsForConn refuses one
// under that context). When the connection is still held, after
// heldWarnAfter, and not by the statement's own loop (own reports that of
// a holder; nil for a statement with no loop), it logs heldWarning with
// slog.Warn: the statement may wait forever, and nothing else would say so.
// The returned stop ends the watch. It costs one atomic load while nothing
// holds the connection.
func watchHeld(db *DB, what string, own func(*connHold) bool) (stop func()) {
	if db.InTx || holder(db) == nil || !onePool(db) {
		return noStop
	}
	t := time.AfterFunc(heldWarnAfter, func() {
		h := holder(db)
		if h == nil || (own != nil && own(h)) {
			return
		}
		holder := "loop over Iter"
		if h.tx != nil {
			holder = "Tx"
		}
		slog.Warn(heldWarning, "entity", what, "waited", heldWarnAfter, "holder", holder)
	})
	return func() { t.Stop() }
}

// watchStatement is watchHeld of st. It keeps no pointer to st, which Plain
// keeps on the stack.
func watchStatement(db *DB, st *Statement) (stop func()) {
	if db.InTx || holder(db) == nil {
		return noStop
	}
	what := ""
	if st.Entity != nil {
		what = st.Entity.Name
	}
	iter := st.iterCtx
	if iter == nil {
		return watchHeld(db, what, nil)
	}
	return watchHeld(db, what, func(h *connHold) bool { return sameCtx(h.caller, iter) })
}

// noStop is watchHeld's stop when there is nothing to watch.
func noStop() {}

// connHold is the holder of a pool's one connection: the context its Iter or
// Tx was given and, for a Tx, its frame.
type connHold struct {
	caller context.Context
	tx     *txFrame // nil for an Iter
}

// owns reports whether ctx is the holder's own: the context it was given, or
// WithoutTx of its fn's, either as a WithFilter filter is given it
// (unfiltered).
func (h *connHold) owns(ctx context.Context) bool {
	ctx = unfiltered(ctx)
	if sameCtx(ctx, h.caller) {
		return true
	}
	if n, ok := ctx.(noTxCtx); ok && h.tx != nil {
		return sameCtx(unfiltered(n.Context), &h.tx.ctx)
	}
	return false
}

// sameCtx reports whether a and b are the same context. == on two interfaces
// panics when their dynamic type cannot be compared, so the type is checked.
func sameCtx(a, b context.Context) bool {
	ta := reflect.TypeOf(a)
	return ta == reflect.TypeOf(b) && ta != nil && ta.Comparable() && a == b
}

// onePool reports whether db runs its statements on a pool of one connection.
// With replicas, a read may hold a replica's connection and leave the primary
// free. Not a method of DB, as the export data would then name *sql.DB.
func onePool(db *DB) bool {
	p, ok := db.pool.(*sql.DB)
	return ok && db.Rt == nil && p.Stats().MaxOpenConnections == 1
}

// holder is db's holder of its pool's one connection, or nil.
func holder(db *DB) *connHold {
	h, _ := db.Cfg.connHold.Load().(*connHold)
	return h
}

// waitsForConn is the error of a statement under ctx on db that would wait
// for the connection the holder of ctx holds; nil for any other. It is one
// atomic load unless the connection is held. Once ctx, or the context of the
// Tx that holds the connection, has ended, nothing would wait forever, as
// database/sql has rolled the Tx back: the error says the context ended, and
// matches its error.
func waitsForConn(ctx context.Context, db *DB) error {
	h := holder(db)
	if h == nil || db.InTx || !h.owns(ctx) || !onePool(db) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("the statement's context ended: %w", err)
	}
	if h.tx != nil {
		if err := h.tx.ctx.Err(); err != nil {
			return fmt.Errorf("the context of the Tx whose fn this is ended: %w", err)
		}
		return errTxConn
	}
	return errIterConn
}

// heldHint joins errHeld to err when err is ctx's end and db's pool's one
// connection is held: the statement may have waited for its own loop or Tx.
func heldHint(ctx context.Context, db *DB, err error) error {
	if err == nil || ctx.Err() == nil || db.InTx || holder(db) == nil || !onePool(db) {
		return err
	}
	return errors.Join(err, errHeld)
}

// holdConn notes the Iter or Tx given caller (and tx, a Tx's frame) as the
// holder of db's one connection, and returns the note to give to
// releaseConn; nil, and no allocation, on any other pool.
func holdConn(caller context.Context, db *DB, tx *txFrame) *connHold {
	if !onePool(db) {
		return nil
	}
	h := &connHold{caller: caller, tx: tx}
	db.Cfg.connHold.Store(h)
	return h
}

// releaseConn lets go of h, only if it is still the note: by then another
// holder may hold the connection this one released.
func releaseConn(db *DB, h *connHold) {
	if h != nil {
		db.Cfg.connHold.CompareAndSwap(h, (*connHold)(nil))
	}
}

// readHeld is readRows, with the Iter's context noted as the holder of the
// pool's one connection while its loop runs.
func readHeld(db *DB, st *Statement, r Rows, read func(Rows) error) (readErr, rowsErr error) {
	if !st.iter || db.InTx || st.iterCtx == nil {
		return readRows(r, read)
	}
	defer releaseConn(db, holdConn(st.iterCtx, db, nil))
	return readRows(r, read)
}
