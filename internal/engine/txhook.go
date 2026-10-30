package engine

import (
	"context"
	"database/sql"
	"sync/atomic"
	"time"
)

// TxAround is a hook that wraps a transaction: a db.Tx call, every attempt
// TxRetrySerializable makes and the commit included. It is called once per
// db.Tx call, not for a nested Tx (a savepoint). A multi-batch InsertAll
// outside a transaction runs in one db.Tx, so the hooks see it too.
//
// A hook must call next once, and return its error, unless it returns an
// error of its own instead; a second call to next returns an error without
// running anything. The context passed to next is the one the transaction
// runs on, which Tx's fn gets; it must stay valid until next returns. The
// hook's own context is not the transaction's: a statement the hook runs on
// the DB, before or after next, runs outside the transaction. The first
// TxAround given to Open is the outermost.
type TxAround func(ctx context.Context, tx *TxInfo, next func(context.Context) error) error

// TxInfo describes a transaction to the hooks: a TxAround hook gets it, and
// every statement in the transaction, a savepoint's included, has it in
// Statement.Tx. velox makes one only when the DB has hooks of some kind.
type TxInfo struct {
	ID        uint64 // unique within the process
	Isolation sql.IsolationLevel
	ReadOnly  bool
	// Route is where the transaction runs: its shard ("" on an unsharded DB), on
	// the primary, so Replica is always -1 and Fallback always false.
	Route   Route
	Attempt int       // 1 for the first try; TxRetrySerializable increments it
	Retries []TxRetry // the attempts TxRetrySerializable retried, in order
	Outcome TxOutcome // how the last attempt ended; set before next returns
}

// TxRetry is an attempt that TxRetrySerializable retried.
type TxRetry struct {
	Err error     // what the attempt failed with
	At  time.Time // when velox decided to retry
}

// TxOutcome is how a transaction's attempt ended.
type TxOutcome uint8

// The outcomes of an attempt.
const (
	TxCommit   TxOutcome = iota + 1 // fn returned nil and the commit succeeded
	TxRollback                      // fn returned an error, or panicked
	TxError                         // begin or commit failed, or the transaction's deadline passed
)

// String returns "commit", "rollback" or "error"; "unknown" otherwise.
func (o TxOutcome) String() string {
	switch o {
	case TxCommit:
		return "commit"
	case TxRollback:
		return "rollback"
	case TxError:
		return "error"
	}
	return "unknown"
}

// WithTxAround adds a transaction hook. The first one given is the
// outermost. A nil hook panics.
func WithTxAround(h TxAround) Option {
	if h == nil {
		panic("velox: WithTxAround(nil)")
	}
	return Option{func(c *Config) { c.TxArounds = append(c.TxArounds, h) }}
}

// TxIDs numbers transactions; the first is 1.
var TxIDs atomic.Uint64

// end records how the transaction's attempt ended; info may be nil.
func EndTx(info *TxInfo, o TxOutcome) {
	if info != nil {
		info.Outcome = o
	}
}
