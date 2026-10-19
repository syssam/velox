package velox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
)

// TxOption configures a transaction.
type TxOption func(*txOptions)

// Isolation sets the transaction's isolation level; the default is the
// database's.
func Isolation(level sql.IsolationLevel) TxOption {
	return func(o *txOptions) { o.isolation = level }
}

// ReadOnly starts a read-only transaction.
func ReadOnly() TxOption { return func(o *txOptions) { o.readOnly = true } }

// RetrySerializable runs the transaction again, up to n more times, when it
// fails on a serialization failure or a deadlock: PostgreSQL 40001 and 40P01,
// MySQL 1213 and 1205, SQLite BUSY and LOCKED. fn must be safe to run again.
func RetrySerializable(n int) TxOption { return func(o *txOptions) { o.retries = n } }

// Tx runs fn in a transaction. fn's error rolls it back and is returned; nil
// commits it; a panic, or runtime.Goexit, rolls it back and carries on. tx is
// a *DB like any other, so a function that takes a *DB works inside a
// transaction unchanged.
//
// WithDeadlines' Tx and MaxTx, and Timeout, bound the whole call. The
// transaction's deadline, velox's or ctx's, whichever is earlier, bounds every
// statement inside it, nested Tx included, whatever context the statement is
// given. A transaction whose ctx has ended (past either deadline, or canceled)
// is rolled back, never committed; its error matches ErrTimeout when velox's
// deadline passed, and context.DeadlineExceeded or context.Canceled otherwise.
//
// WithTxAround's hooks wrap the whole call: every attempt and the commit.
//
// On a DB with replicas the transaction runs on the primary. On a DB opened
// with OpenShards it runs on the primary of the shard ctx names, as a
// statement's context names one; when ctx names none, Tx fails with
// ErrNoShard, as "velox: transaction: no shard for the statement: …", before
// anything is sent or any TxAround hook runs. Its statements run on that
// shard too, and one whose context names another fails.
//
// Inside a transaction, Tx makes a savepoint instead, so an error in a nested
// fn rolls back only that fn's work. A nested Tx (a savepoint) ignores options.
//
// tx is valid only inside fn: after Tx returns, statements on it fail (or, for
// a nested tx, run in the enclosing transaction).
func (db *DB) Tx(ctx context.Context, fn func(tx *DB) error, opts ...TxOption) error {
	if db.inTx {
		return db.savepoint(ctx, fn)
	}
	sh, err := db.shardForTx(ctx)
	if err != nil {
		return err
	}
	var o txOptions
	for _, opt := range opts {
		opt(&o)
	}
	def, maxd := db.cfg.deadlines.Tx, db.cfg.deadlines.MaxTx
	if o.timeout > 0 {
		def = 0
		if maxd <= 0 || o.timeout < maxd {
			maxd = o.timeout
		}
	}
	tctx, cancel := bound(ctx, def, maxd, time.Time{}, nil)
	// The deadline, velox's or the caller's, bounds the statements in fn
	// whatever context they are given; it is velox's when bound set one.
	deadline, _ := tctx.Deadline()
	var cause error
	if cancel != nil {
		defer cancel()
		cause = ErrTimeout
	}
	info := db.newTxInfo(tctx, o)
	if len(db.cfg.txArounds) == 0 {
		// No closure here: one passed to the hooks escapes, and would cost
		// a DB with nothing configured an allocation per transaction.
		return db.attempts(tctx, tctx, fn, o, deadline, cause, info, sh)
	}
	return db.txAround(tctx, info, func(ctx context.Context) error {
		return db.attempts(ctx, tctx, fn, o, deadline, cause, info, sh)
	})
}

// attempts runs fn in a transaction on ctx, and again after a retryable
// failure as many times as o allows. tctx is the context Tx bounded, ctx the
// one the TxAround hooks passed on (tctx when there are none); cause is
// ErrTimeout when the deadline is velox's. info, when not nil, is updated
// before each attempt and each retry. sh is the shard the transaction runs
// on; nil on a DB without a router.
func (db *DB) attempts(ctx, tctx context.Context, fn func(*DB) error, o txOptions, deadline time.Time, cause error, info *TxInfo, sh *shard) error {
	for attempt := 1; ; attempt++ {
		if info != nil {
			info.Attempt = attempt
		}
		var wrote *atomic.Bool
		if db.rt != nil {
			wrote = new(atomic.Bool) // one per attempt: a retry forgets a rolled back write
		}
		err := db.transaction(ctx, fn, o, deadline, cause, info, wrote, sh)
		if err == nil && wrote != nil && wrote.Load() {
			db.pinSession(tctx) // the context given to Tx, not a TxAround hook's
		}
		if err != nil && cause != nil && timedOut(tctx) && !errors.Is(err, ErrTimeout) {
			err = errors.Join(errTimeout("transaction"), err)
		}
		if err == nil || attempt > o.retries || !retryable(err) || ctx.Err() != nil || tctx.Err() != nil {
			return err
		}
		if info != nil {
			info.Retries = append(info.Retries, TxRetry{Err: err, At: time.Now()})
		}
	}
}

// ended reports whether err says the driver already ended the transaction,
// as it does after a canceled context; rolling back then has nothing to do.
func ended(err error) bool {
	return errors.Is(err, sql.ErrTxDone) || errors.Is(err, pgx.ErrTxClosed)
}

// transaction runs fn once in a new transaction, and records how it ended in
// info, which may be nil. A transaction that did not finish, because fn
// panicked or called runtime.Goexit, is rolled back by the deferred function.
// Rollback uses a context without ctx's cancellation, so a canceled ctx still
// rolls back.
//
// Once ctx has ended, a rollback's error is not reported: the driver may have
// closed the connection that the deadline cut a statement off on (pgx does),
// and the server then aborts the transaction itself.
//
// deadline, Tx's, ends the transaction as ctx's would, even when a TxAround
// hook passed on a context without it: past it, nothing commits.
//
// wrote, nil on a DB without a router, is the flag the transaction's
// statements set on a successful write. sh, nil on a DB without a router, is
// the shard whose primary the transaction runs on.
func (db *DB) transaction(ctx context.Context, fn func(*DB) error, o txOptions, deadline time.Time, cause error, info *TxInfo, wrote *atomic.Bool, sh *shard) error {
	primary := db.ex
	var txShard *shard // its statements check their shard against it
	if sh != nil {
		primary = sh.primary // a transaction runs on the primary
		if db.rt.one == nil {
			txShard = sh
		}
	}
	ex, ctl, err := primary.begin(ctx, o)
	if err != nil {
		info.end(TxError)
		return fmt.Errorf("velox: begin: %w", err)
	}
	end := context.WithoutCancel(ctx)
	finished := false
	defer func() {
		if !finished {
			info.end(TxRollback)
			_ = ctl.rollback(end)
		}
	}()
	if err := fn(&DB{d: db.d, ex: ex, rt: db.rt, cfg: db.cfg, inTx: true, txDeadline: deadline, txCause: cause, tx: info, txWrote: wrote, txShard: txShard}); err != nil {
		finished = true
		if ctx.Err() != nil || past(deadline) {
			info.end(TxError)
		} else {
			info.end(TxRollback)
		}
		if rerr := ctl.rollback(end); rerr != nil && !ended(rerr) && !over(ctx) && !past(deadline) {
			return errors.Join(err, fmt.Errorf("velox: rollback: %w", rerr))
		}
		if cause != nil && past(deadline) && !errors.Is(err, ErrTimeout) {
			// velox's deadline passed, whatever context fn's statements ran
			// on: say so without waiting for the deadline's timer.
			return errors.Join(errTimeout("transaction"), err)
		}
		return err
	}
	finished = true
	if over(ctx) || past(deadline) {
		info.end(TxError)
		// Past the deadline, or canceled: roll back on every driver rather
		// than leave it to the driver whether a late commit succeeds.
		_ = ctl.rollback(end)
		// velox's deadline is the cause even when a TxAround hook passed on
		// a context without it.
		if timedOut(ctx) || cause != nil && past(deadline) {
			return errTimeout("transaction")
		}
		err := ctxErr(ctx)
		if err == nil {
			// Past its deadline, its Done not closed within the grace.
			err = context.DeadlineExceeded
		}
		return fmt.Errorf("velox: transaction: %w", err)
	}
	if err := ctl.commit(ctx); err != nil {
		info.end(TxError)
		return fmt.Errorf("velox: commit: %w", err)
	}
	info.end(TxCommit)
	return nil
}

// past reports whether deadline is set and has passed.
func past(deadline time.Time) bool {
	return !deadline.IsZero() && !time.Now().Before(deadline)
}

// overGrace is how long over waits for the Done of a context whose deadline
// has passed.
const overGrace = 50 * time.Millisecond

// over reports whether ctx has ended. Once its deadline has passed, it is
// over: a statement in the transaction carries the same deadline on a timer
// of its own, which may fire first, and the statement's timeout must not find
// the transaction still live. over then waits for ctx to end, so that its
// error says why, but at most overGrace: a context whose Done never closes
// must not hang the transaction.
func over(ctx context.Context) bool {
	if dl, ok := ctx.Deadline(); ok && !time.Now().Before(dl) {
		t := time.NewTimer(overGrace)
		defer t.Stop()
		select {
		case <-ctx.Done():
		case <-t.C:
		}
		return true
	}
	return ctx.Err() != nil
}

// savepoint runs fn under a savepoint of the current transaction. It runs no
// TxAround hook; its statements see the transaction's TxInfo.
func (db *DB) savepoint(ctx context.Context, fn func(*DB) error) error {
	name := "velox_" + strconv.Itoa(db.depth+1)
	if _, err := db.ex.exec(ctx, "SAVEPOINT "+name, nil); err != nil {
		return fmt.Errorf("velox: savepoint: %w", err)
	}
	end := context.WithoutCancel(ctx)
	finished := false
	defer func() {
		if !finished {
			_ = db.rollbackSavepoint(end, name)
		}
	}()
	if err := fn(&DB{d: db.d, ex: db.ex, rt: db.rt, cfg: db.cfg, inTx: true, depth: db.depth + 1, txDeadline: db.txDeadline, txCause: db.txCause, tx: db.tx, txWrote: db.txWrote, txShard: db.txShard}); err != nil {
		finished = true
		// As in transaction: once the transaction's deadline has passed, the
		// connection may be gone and the rollback's error is noise.
		if rerr := db.rollbackSavepoint(end, name); rerr != nil && !ended(rerr) && ctx.Err() == nil && !db.pastTxDeadline() {
			return errors.Join(err, fmt.Errorf("velox: rollback to savepoint: %w", rerr))
		}
		return err
	}
	finished = true
	if _, err := db.ex.exec(ctx, "RELEASE SAVEPOINT "+name, nil); err != nil {
		return fmt.Errorf("velox: release savepoint: %w", err)
	}
	return nil
}

// rollbackSavepoint undoes the work since the savepoint and releases it,
// because PostgreSQL keeps a savepoint after ROLLBACK TO and a loop of failing
// nested Tx would pile up subtransactions.
func (db *DB) rollbackSavepoint(ctx context.Context, name string) error {
	if _, err := db.ex.exec(ctx, "ROLLBACK TO SAVEPOINT "+name, nil); err != nil {
		return err
	}
	_, err := db.ex.exec(ctx, "RELEASE SAVEPOINT "+name, nil)
	return err
}
