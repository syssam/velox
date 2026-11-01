package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime"
)

// This file refuses the statement that would wait forever: one made on the DB
// by the goroutine that holds its pool's one connection, in the body of a loop
// over Iter or in a Tx's fn. config.connHolder notes that goroutine. A pool of one
// has one holder at a time. Only a statement on that goroutine is refused, as the
// wait of any other ends with the loop or the Tx.
//
// The holder is the DB's: another DB on the same *sql.DB (a second Open, or OpenConn
// with SQLConn) sees none, and waits. Finding the pool would cost those DBs a lookup
// in a process-wide table, for each statement.

var (
	// errIterConn is the error of a statement that waits for its own loop.
	errIterConn = errors.New("a statement inside a loop over Iter would wait forever for the one connection of the pool, which the loop holds; " +
		"collect the rows with All first, or run both in a Tx")
	// errTxConn is the error of a statement that waits for its own Tx.
	errTxConn = errors.New("a statement outside a Tx, on the goroutine running it, would wait forever for the one connection of the pool, which the Tx holds; " +
		"run it on the tx that fn was given, or after the Tx returns")
)

// onePool reports whether db runs its statements on a pool of one connection.
// With replicas, a read may hold a replica's connection and leave the primary
// free. Not a method of DB, as the export data would then name *sql.DB.
func onePool(db *DB) bool {
	p, ok := db.pool.(*sql.DB)
	return ok && db.Rt == nil && p.Stats().MaxOpenConnections == 1
}

// waitsForConn is the error of a statement of this goroutine on db that would wait
// for the connection the goroutine holds; nil for any other. It is one atomic load
// unless the connection is held. Once ctx, or the context of the Tx that holds the
// connection, has ended, nothing would wait forever, as database/sql has rolled the Tx
// back: the error says the context ended, and matches its error.
func waitsForConn(ctx context.Context, db *DB) error {
	h := db.Cfg.connHolder.Load()
	if h == 0 || db.InTx || !onePool(db) || h>>1 != goid() {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("the statement's context ended: %w", err)
	}
	if h&1 != 0 {
		if f, _ := db.Cfg.connTx.Load().(*txFrame); f != nil && f.ctx.Err() != nil {
			return fmt.Errorf("the context of the Tx whose fn this is ended: %w", f.ctx.Err())
		}
		return errTxConn
	}
	return errIterConn
}

// holdConn notes this goroutine as the holder of db's one connection, and returns
// the note to give to releaseConn; 0 on any other pool. A holder is the goroutine's
// ID doubled, plus one for a Tx.
func holdConn(db *DB, tx uint64) uint64 {
	if !onePool(db) {
		return 0
	}
	h := goid()<<1 | tx
	db.Cfg.connHolder.Store(h)
	return h
}

// releaseConn lets go of h, only if it is still the note: by then another goroutine
// may hold the connection this one released.
func releaseConn(db *DB, h uint64) {
	if h != 0 {
		if h&1 != 0 {
			db.Cfg.connTx.Store((*txFrame)(nil))
		}
		db.Cfg.connHolder.CompareAndSwap(h, 0)
	}
}

// readHeld is readRows, with the goroutine of an Iter noted as the holder of the
// pool's one connection while its loop runs.
func readHeld(db *DB, st *Statement, r Rows, read func(Rows) error) (readErr, rowsErr error) {
	if !st.iter || db.InTx {
		return readRows(r, read)
	}
	defer releaseConn(db, holdConn(db, 0))
	return readRows(r, read)
}

// goid is the current goroutine's ID, which the runtime never reuses and writes
// first in a stack trace: "goroutine 123 [running]:". The buffer does not bound its
// cost, as runtime.Stack walks the whole stack however little it keeps, and nothing
// else gives the ID without a link to the runtime's internals: it grows with the
// depth of the caller (BenchmarkGoid). It is paid by an Iter or a Tx on a pool of
// one, and by a statement made while one is open.
func goid() uint64 {
	var buf [40]byte
	b := buf[:runtime.Stack(buf[:], false)]
	var id uint64
	for _, c := range b[len("goroutine "):] {
		if c < '0' || c > '9' {
			break
		}
		id = id*10 + uint64(c-'0')
	}
	return id
}
