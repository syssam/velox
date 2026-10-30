package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/syssam/velox2/internal/conn"
)

// The driver-facing types are internal/conn's, so that velox's driver packages
// can implement them.
type (
	Rows       = conn.Rows
	ExecResult = conn.Result
	txControl  = conn.Tx
	Executor   = conn.Executor
)

// TxOptions are a transaction's options; TxOption sets them.
type TxOptions struct {
	Isolation sql.IsolationLevel
	ReadOnly  bool
	Retries   int
	Timeout   time.Duration // TxTimeout; 0: the DB's Tx default
}

// TxOption configures one call of DB.Tx: TxIsolation, TxReadOnly,
// TxRetrySerializable and TxTimeout. The zero TxOption is not valid: TxRetries
// panics on it, and so does DB.Tx when it begins the transaction.
type TxOption struct{ apply func(*TxOptions) }

// TxIsolation sets the transaction's isolation level; the default is the
// database's.
func TxIsolation(level sql.IsolationLevel) TxOption {
	return TxOption{func(o *TxOptions) { o.Isolation = level }}
}

// TxReadOnly starts a read-only transaction.
func TxReadOnly() TxOption { return TxOption{func(o *TxOptions) { o.ReadOnly = true }} }

// TxRetrySerializable runs the transaction again, up to n more times, when it
// fails on a serialization failure or a deadlock: PostgreSQL 40001 and 40P01,
// MySQL 1213 and 1205, SQLite BUSY and LOCKED. fn must be safe to run again.
func TxRetrySerializable(n int) TxOption { return TxOption{func(o *TxOptions) { o.Retries = n }} }

// TxRetries returns the n of the last TxRetrySerializable in opts, or 0 when
// there is none. It reads options and is not one. It is for code that runs a
// transaction's fn on an application's behalf and cannot run it again.
func TxRetries(opts ...TxOption) int {
	var o TxOptions
	for _, opt := range opts {
		ApplyTxOption(opt, &o)
	}
	return o.Retries
}

// Ended reports whether err says the driver already ended the transaction,
// as it does after a canceled context; rolling back then has nothing to do.
func Ended(err error) bool {
	return Is(err, sql.ErrTxDone) || DriverSentinel(err, pgTxClosed)
}

// Rollback rolls the transaction ctl controls back on RollbackContext. Its
// deadline is RollbackTimeout's alone: the transaction's ROLLBACK must run
// after the transaction's deadline has passed.
func Rollback(ctx context.Context, ctl txControl) error {
	rctx, cancel := RollbackContext(ctx, time.Time{})
	defer cancel()
	return ctl.Rollback(rctx)
}

// RollbackTimeout bounds every rollback velox sends on a context it detached
// from the caller's cancellation: the transaction's ROLLBACK and a savepoint's
// ROLLBACK TO and its RELEASE. A var, so that a test can lower it.
var RollbackTimeout = 5 * time.Second

// RollbackContext is ctx without its cancellation, with RollbackTimeout from
// now; for a savepoint also no later than the transaction's deadline (also).
func RollbackContext(ctx context.Context, also time.Time) (context.Context, context.CancelFunc) {
	dl := time.Now().Add(RollbackTimeout)
	if !also.IsZero() && also.Before(dl) {
		dl = also
	}
	return context.WithDeadline(context.WithoutCancel(ctx), dl)
}

// Past reports whether deadline is set and has passed.
func Past(deadline time.Time) bool {
	return !deadline.IsZero() && !time.Now().Before(deadline)
}

// OverGrace is how long over waits for the Done of a context whose deadline
// has passed.
const OverGrace = 50 * time.Millisecond

// Over reports whether ctx has ended. Once its deadline has passed, it is
// over: a statement in the transaction carries the same deadline on a timer
// of its own, which may fire first, and the statement's timeout must not find
// the transaction still live. over then waits for ctx to end, so that its
// error says why, but at most OverGrace: a context whose Done never closes
// must not hang the transaction.
func Over(ctx context.Context) bool {
	if dl, ok := ctx.Deadline(); ok && !time.Now().Before(dl) {
		t := time.NewTimer(OverGrace)
		defer t.Stop()
		select {
		case <-ctx.Done():
		case <-t.C:
		}
		return true
	}
	return ctx.Err() != nil
}

// SavepointErr is the error of a SAVEPOINT or RELEASE (what) that failed
// with err on sctx; it matches ErrTimeout when velox's deadline ended sctx.
func SavepointErr(sctx context.Context, what string, err error) error {
	err = fmt.Errorf("velox: %s: %w", what, err)
	if TimedOut(sctx) {
		return errors.Join(TimeoutError(what), err)
	}
	return err
}

// WithoutTx returns ctx without the mark that Tx puts on fn's ctx, keeping
// everything else: its values (the tenant, tags, spans), its deadline and its
// cancellation. It is for work inside fn that must not join the transaction,
// such as an audit row that must survive a rollback: under it, a statement on
// db runs outside the transaction, on its own connection, and a Tx on db
// begins a transaction of its own. A statement on tx under it still runs in
// the transaction. On a context without the mark it returns ctx.
//
// A statement on db under it needs a second connection while the
// transaction holds one: on a pool of one connection, as SQLite often has,
// it waits until the transaction ends or its deadline passes.
func WithoutTx(ctx context.Context) context.Context {
	if ctx.Value(TxKey{}) == nil {
		return ctx
	}
	return noTxCtx{ctx}
}

// noTxCtx is WithoutTx's context: its parent without the transaction's mark.
type noTxCtx struct{ context.Context }

// Value returns nil for TxKey, and the parent context's value otherwise.
func (c noTxCtx) Value(key any) any {
	if _, ok := key.(TxKey); ok {
		return nil
	}
	return c.Context.Value(key)
}

// String names the context as the context package's do.
func (c noTxCtx) String() string { return fmt.Sprint(c.Context) + ".WithoutTx" }

// TxKey is the context key of the mark a transaction's fn's context carries.
type TxKey struct{}

func ApplyTxOption(opt TxOption, o *TxOptions) { opt.apply(o) }
