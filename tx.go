package velox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
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
// WithDeadlines' Tx and MaxTx, and Timeout, bound the whole call; a transaction
// past its deadline is rolled back.
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
	tctx, cancel := bound(ctx, def, maxd, time.Time{})
	var deadline time.Time
	if cancel != nil {
		defer cancel()
		deadline, _ = tctx.Deadline()
	}
	for attempt := 0; ; attempt++ {
		err := db.transaction(tctx, fn, o, deadline)
		if err != nil && cancel != nil && timedOut(tctx) && !errors.Is(err, ErrTimeout) {
			err = errors.Join(errTimeout("transaction"), err)
		}
		if err == nil || attempt >= o.retries || !retryable(err) || tctx.Err() != nil {
			return err
		}
	}
}

// ended reports whether err says the driver already ended the transaction,
// as it does after a canceled context; rolling back then has nothing to do.
func ended(err error) bool {
	return errors.Is(err, sql.ErrTxDone) || errors.Is(err, pgx.ErrTxClosed)
}

// transaction runs fn once in a new transaction. A transaction that did not
// finish, because fn panicked or called runtime.Goexit, is rolled back by the
// deferred function. Rollback uses a context without ctx's cancellation, so a
// canceled ctx still rolls back.
func (db *DB) transaction(ctx context.Context, fn func(*DB) error, o txOptions, deadline time.Time) error {
	ex, ctl, err := db.ex.begin(ctx, o)
	if err != nil {
		return fmt.Errorf("velox: begin: %w", err)
	}
	end := context.WithoutCancel(ctx)
	finished := false
	defer func() {
		if !finished {
			_ = ctl.rollback(end)
		}
	}()
	if err := fn(&DB{d: db.d, ex: ex, cfg: db.cfg, inTx: true, txDeadline: deadline}); err != nil {
		finished = true
		if rerr := ctl.rollback(end); rerr != nil && !ended(rerr) {
			return errors.Join(err, fmt.Errorf("velox: rollback: %w", rerr))
		}
		return err
	}
	finished = true
	if timedOut(ctx) {
		// Past velox's deadline: roll back on every driver rather than leave
		// it to the driver whether a late commit succeeds.
		if rerr := ctl.rollback(end); rerr != nil && !ended(rerr) {
			return errors.Join(errTimeout("transaction"), fmt.Errorf("velox: rollback: %w", rerr))
		}
		return errTimeout("transaction")
	}
	if err := ctl.commit(ctx); err != nil {
		return fmt.Errorf("velox: commit: %w", err)
	}
	return nil
}

// savepoint runs fn under a savepoint of the current transaction.
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
	if err := fn(&DB{d: db.d, ex: db.ex, cfg: db.cfg, inTx: true, depth: db.depth + 1, txDeadline: db.txDeadline}); err != nil {
		finished = true
		if rerr := db.rollbackSavepoint(end, name); rerr != nil && !ended(rerr) {
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
