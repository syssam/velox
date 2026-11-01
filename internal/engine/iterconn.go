package engine

import (
	"database/sql"
	"errors"
	"runtime"
)

// This file refuses the statement that would wait forever: one made in the body
// of a loop over Iter, on a pool of one connection, which the loop holds until
// the body returns. config.iterConn is the goroutine of the loop that holds it.
// A pool of one has one such loop. Only a statement on that goroutine is
// refused, as the wait of any other ends with the loop.

// errIterConn is the error of a statement that waits for its own loop.
var errIterConn = errors.New("a statement inside a loop over Iter would wait forever for the one connection of the pool, which the loop holds; " +
	"read the rows with All first, run both in a Tx, or raise the pool with velox.Pool(db).SetMaxOpenConns")

// onePool reports whether db runs its statements on a pool of one connection.
// With replicas, a read may hold a replica's connection and leave the primary
// free. Not a method of DB, as the export data would then name *sql.DB.
func onePool(db *DB) bool {
	p, ok := db.pool.(*sql.DB)
	return ok && db.Rt == nil && p.Stats().MaxOpenConnections == 1
}

// waitsForIter reports whether a statement of this goroutine on db would wait
// for the loop it is inside. It is one atomic load unless such a loop is open.
func waitsForIter(db *DB) bool {
	g := db.Cfg.iterConn.Load()
	return g != 0 && !db.InTx && g == goid() && onePool(db)
}

// readHeld is readRows, which notes the goroutine of an Iter that holds the
// pool's one connection while its loop runs. It lets go only of its own note:
// by then another goroutine's loop may hold the connection readRows released.
func readHeld(db *DB, st *Statement, r Rows, read func(Rows) error) (readErr, rowsErr error) {
	if !st.iter || db.InTx || !onePool(db) {
		return readRows(r, read)
	}
	g := goid()
	db.Cfg.iterConn.Store(g)
	defer db.Cfg.iterConn.CompareAndSwap(g, 0)
	return readRows(r, read)
}

// goid is the current goroutine's ID, which the runtime never reuses and writes
// first in a stack trace: "goroutine 123 [running]:". It costs about two
// microseconds (BenchmarkGoid), paid by an Iter on a pool of one and by a
// statement made while one is open.
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
