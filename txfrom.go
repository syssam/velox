package velox

import (
	"context"
	"unsafe"

	"github.com/syssam/velox/internal/engine"
)

// TxFrom returns the transaction ctx belongs to, as a DB like db: ctx is the
// context a Tx of db (or of a DB derived from it, such as WithoutPolicy's)
// gave its fn, or one derived from it. It is for a helper that is handed db
// and ctx and should join the caller's transaction with db's own policy
// bypass, where db is not the DB that began it (which joins by itself) and
// db.Tx under that ctx fails with ErrOutsideTx:
//
//	func WithTx(ctx context.Context, db *velox.DB, fn func(ctx context.Context, tx *velox.DB) error) error {
//		if tx, ok := velox.TxFrom(ctx, db); ok {
//			return fn(ctx, tx) // join; tx.Tx(ctx, fn) makes a savepoint instead
//		}
//		return db.Tx(ctx, fn)
//	}
//
// The DB it returns runs in that transaction and has db's WithoutPolicy
// reason, or none when db has none, whatever the transaction's DB has: a
// helper that joins gets no bypass it was not given. ok is false when ctx
// carries no transaction, or one that has ended, when the transaction is
// another DB's (another Open, or a DB with other options), or when db is a
// recording handle (Record) and the transaction's is not, or the other way
// round. db already in that transaction is returned as it is.
//
// The DB is valid while the transaction is, as a Tx's tx is, and is not safe
// for concurrent use.
func TxFrom(ctx context.Context, db *DB) (*DB, bool) {
	e, ok := engine.TxFrom(ctx, &db.e)
	if !ok {
		return nil, false
	}
	return (*DB)(unsafe.Pointer(e)), true // DB is its engine.DB (see DB)
}
