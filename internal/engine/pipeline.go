package engine

import (
	"context"
	"sync/atomic"
	"time"
)

// DB is the root package's DB without what is generic: every statement's path
// through velox. The root's DB holds one by value, and adds the generic API.
type DB struct {
	D Dialect
	// Ex runs the statements of a DB without replicas, and of a transaction;
	// nil on a DB with replicas outside a transaction, whose Rt chooses.
	Ex   Executor
	Rt   *Router // WithReplicas's; nil without replicas
	Cfg  *Config
	InTx bool
	// txDeadline is, in a transaction, its deadline: velox's or the
	// caller's, whichever is earlier; zero when none. txCause is ErrTimeout
	// when the deadline is velox's, nil when it is the caller's.
	txDeadline time.Time
	txCause    error
	tx         *TxInfo // the transaction's, when hooks are configured
	// txWrote is, in a transaction on a DB with replicas, set by its first
	// successful write; the commit then pins the session of the context given
	// to Tx. Its savepoints share it. Nil elsewhere.
	txWrote *atomic.Bool
	// txShard is, in a transaction on a sharded DB, the shard it runs on;
	// nil elsewhere.
	txShard *ShardConn
	// txRoot is, in a transaction, its frame: the identity the mark on its
	// fn's ctx carries, which its savepoints share. Nil elsewhere.
	txRoot *txFrame
}

// Hooked reports whether the pipeline has hooks: whether a statement must go
// through run, or may take the shortcut of plain.
func (db *DB) Hooked() bool { return len(db.Cfg.Rewrites) > 0 || len(db.Cfg.Arounds) > 0 }

// OutsideTx fails a statement, st, under a context marked by a transaction
// that has not ended and that db is not in (foreign), with ErrOutsideTx: one
// context lookup, and a pointer compare. In a transaction a savepoint broke
// (txFrame.broken), it fails st with that error.
func (db *DB) OutsideTx(ctx context.Context, st *Statement) error {
	if db.Foreign(ctx) {
		return StmtErr(st, ErrOutsideTx)
	}
	if db.txRoot != nil && db.txRoot.broken != nil {
		return StmtErr(st, db.txRoot.broken)
	}
	return nil
}

// DryWrite fails a statement that writes, st, under a dry-run context
// (IsDryRun), with ErrDryRun: nothing is sent. It is made where a statement
// is sent, in plain and run, never in prepare, which SQL(ctx) renders
// through; so the statement being dry-run is rendered, and every other write
// a hook makes under its context is refused. Every write goes through plain
// or run, Raw included, so this is the one place the rule is kept. Reads
// pass without a context lookup.
func (db *DB) DryWrite(ctx context.Context, st *Statement) error {
	if !st.Op.Reads() && IsDryRun(ctx) {
		return StmtErr(st, ErrDryRun)
	}
	return nil
}

// Observe sets what st's hooks and adapters read about it: its Dialect, its
// Tags, its transaction's TxInfo and, with WithCallers, its Caller.
func (db *DB) Observe(ctx context.Context, st *Statement) {
	st.Dialect = db.D
	st.Tx = db.tx
	st.Tags = TagsFrom(ctx)
	if c := db.Cfg.Callers; c != nil {
		st.Caller = c.Capture()
	}
}
