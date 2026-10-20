package velox

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"time"
)

// TxAround is a hook that wraps a transaction: a db.Tx call, every attempt
// RetrySerializable makes and the commit included. It is called once per
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
	Attempt   int       // 1 for the first try; RetrySerializable increments it
	Retries   []TxRetry // the attempts RetrySerializable retried, in order
	Outcome   TxOutcome // how the last attempt ended; set before next returns
}

// TxRetry is an attempt that RetrySerializable retried.
type TxRetry struct {
	Err error     // what the attempt failed with
	At  time.Time // when velox decided to retry
}

// TxOutcome is how a transaction's attempt ended.
type TxOutcome uint8

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
	return func(c *config) { c.txArounds = append(c.txArounds, h) }
}

// txIDs numbers transactions; the first is 1.
var txIDs atomic.Uint64

// newTxInfo returns the TxInfo of a transaction with options o, or nil when
// db has no hooks to read it.
func (db *DB) newTxInfo(o txOptions) *TxInfo {
	if !db.hooked() && len(db.cfg.txArounds) == 0 {
		return nil
	}
	return &TxInfo{ID: txIDs.Add(1), Isolation: o.isolation, ReadOnly: o.readOnly}
}

// end records how the transaction's attempt ended; info may be nil.
func (info *TxInfo) end(o TxOutcome) {
	if info != nil {
		info.Outcome = o
	}
}

// txAround runs attempts through db.cfg.txArounds, the first given outermost.
func (db *DB) txAround(ctx context.Context, info *TxInfo, attempts func(context.Context) error) error {
	called, returned := false, false
	var last error // what attempts returned
	next := func(ctx context.Context) error {
		if called {
			return errors.New("velox: transaction: a TxAround hook called next twice")
		}
		called = true
		last = attempts(ctx)
		returned = true
		return last
	}
	for i := len(db.cfg.txArounds) - 1; i >= 0; i-- {
		h, inner := db.cfg.txArounds[i], next
		next = func(ctx context.Context) error { return h(ctx, info, inner) }
	}
	if err := next(ctx); err != nil {
		return err
	}
	if !called {
		return errors.New("velox: transaction: a TxAround hook returned without calling next")
	}
	if !returned {
		// attempts panicked and a hook recovered the panic: velox rolled the
		// transaction back, so Tx must not report success.
		return errors.New("velox: transaction: a TxAround hook recovered a panic from next")
	}
	if last != nil {
		return errors.New("velox: transaction: a TxAround hook discarded the error from next")
	}
	return nil
}
