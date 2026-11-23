package velox

import (
	"context"
	"database/sql"
	"time"

	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/veloxtype"
)

// TxTimeout bounds the transaction to d, retries and commit included, in place
// of the DB's Tx default and within its MaxTx. An earlier deadline on the
// context still applies. TxTimeout(d) with d <= 0 sets no bound of its own:
// the DB's Tx default applies, in place of a TxTimeout in WithTxDefaults. A nested Tx
// refuses it, as it refuses every option: a savepoint runs within the
// enclosing transaction's deadline.
func TxTimeout(d time.Duration) TxOption { return engine.TxTimeout(d) }

// TxOption configures one call of DB.Tx: TxIsolation, TxReadOnly,
// TxRetrySerializable and TxTimeout. The zero TxOption is not valid: TxRetries
// panics on it, and so does DB.Tx when it begins the transaction.
type TxOption = engine.TxOption

// TxIsolation sets the transaction's isolation level; the default is the
// database's. SQLite's transactions are serializable: LevelSerializable is
// accepted, and any other level fails Tx as "velox: begin: …".
func TxIsolation(level sql.IsolationLevel) TxOption { return engine.TxIsolation(level) }

// TxReadOnly starts a read-only transaction. On SQLite it runs under PRAGMA
// query_only, on the transaction's connection, where the setting is as it was once
// the transaction has ended, however it ended; a write in it fails. Under a DSN's
// _txlock=immediate it holds SQLite's write lock all the same.
func TxReadOnly() TxOption { return engine.TxReadOnly() }

// TxRetrySerializable runs the transaction again, up to n more times, when it
// fails on a serialization failure or a deadlock: PostgreSQL 40001 and 40P01,
// MySQL 1213 and 1205, SQLite BUSY and LOCKED. fn must be safe to run again.
// An error that matches ErrCommitUnknown is not retried, MySQL's 1205 at
// COMMIT included. Each retry waits first, a random time from 5 ms that grows to at most a
// second; the transaction's deadline or ctx's end stops the wait, and the
// retry.
func TxRetrySerializable(n int) TxOption { return engine.TxRetrySerializable(n) }

// TxRetries returns the n of the last TxRetrySerializable in opts, or 0 when
// there is none. It reads options and is not one. It is for code that runs a
// transaction's fn on an application's behalf and cannot run it again.
func TxRetries(opts ...TxOption) int { return engine.TxRetries(opts...) }

// WithoutTx returns ctx without the mark that Tx puts on fn's ctx, keeping
// everything else: its values (the scope values, tags, spans), its deadline and its
// cancellation. It is for work inside fn that must not join the transaction,
// such as an audit row that must survive a rollback: under it, a statement on
// db runs outside the transaction, on its own connection, and a Tx on db
// begins a transaction of its own. A statement on tx under it still runs in
// the transaction. On a context without the mark it returns ctx.
//
// A statement on db under it needs a second connection while the transaction
// holds one. On a pool of one connection, as SQLite often has, it fails, from
// any goroutine, rather than wait for the transaction that waits for fn.
func WithoutTx(ctx context.Context) context.Context { return engine.WithoutTx(ctx) }

// TxAround is a hook that wraps a transaction.
type TxAround = veloxtype.TxAround

// TxInfo describes a transaction to the hooks.
type TxInfo = veloxtype.TxInfo

// TxRetry is an attempt that TxRetrySerializable retried.
type TxRetry = veloxtype.TxRetry

// TxOutcome is how a transaction's attempt ended.
type TxOutcome = veloxtype.TxOutcome

// The outcomes of an attempt.
const (
	TxCommit   = veloxtype.TxCommit   // fn returned nil and the commit succeeded
	TxRollback = veloxtype.TxRollback // fn returned an error, or panicked
	TxError    = veloxtype.TxError    // begin or commit failed, or the transaction's deadline passed
)

// WithTxAround adds a transaction hook. The first one given is the
// outermost. A nil hook panics.
func WithTxAround(h TxAround) Option { return engine.WithTxAround(h) }

// WithTxDefaults sets options that every Tx on the DB applies before its
// own, as if each call began with them: every Tx on the DB, its shards and
// the DBs derived from it (WithoutPolicy, Record), and the transactions
// velox begins itself for a write that needs one. A call's own options come
// after, so they override: TxRetrySerializable(0) runs a call once under a
// default of 3, TxTimeout(d) replaces a default timeout, and TxTimeout(0)
// lifts it, leaving WithDeadlines' Tx and MaxTx. A nested Tx, a savepoint, applies none: it runs within the outermost
// transaction, whose retry runs it again with the rest. UseTx applies none
// either: the transaction is the caller's. Several WithTxDefaults add up, in
// the order given; TxRetries reads a call's options, not these. A zero
// TxOption panics.
//
//	db, err := velox.Connect("postgres", dsn, velox.WithTxDefaults(
//		velox.TxRetrySerializable(3), velox.TxTimeout(30*time.Second)))
func WithTxDefaults(opts ...TxOption) Option { return engine.WithTxDefaults(opts) }
