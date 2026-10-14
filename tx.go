package velox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
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
// commits it; a panic rolls it back and is raised again. tx is a *DB like any
// other, so a function that takes a *DB works inside a transaction unchanged.
//
// Inside a transaction, Tx makes a savepoint instead, so an error in a nested
// fn rolls back only that fn's work. Options apply to the outermost Tx only.
func (db *DB) Tx(ctx context.Context, fn func(tx *DB) error, opts ...TxOption) error {
	if db.inTx {
		return db.savepoint(ctx, fn)
	}
	var o txOptions
	for _, opt := range opts {
		opt(&o)
	}
	for attempt := 0; ; attempt++ {
		err := db.transaction(ctx, fn, o)
		if err == nil || attempt >= o.retries || !retryable(err) {
			return err
		}
	}
}

// transaction runs fn once in a new transaction. Rollback uses a context
// without ctx's cancellation, so a canceled ctx still rolls back.
func (db *DB) transaction(ctx context.Context, fn func(*DB) error, o txOptions) error {
	ex, ctl, err := db.ex.begin(ctx, o)
	if err != nil {
		return fmt.Errorf("velox: begin: %w", err)
	}
	end := context.WithoutCancel(ctx)
	defer func() {
		if p := recover(); p != nil {
			_ = ctl.rollback(end)
			panic(p)
		}
	}()
	if err := fn(&DB{d: db.d, ex: ex, cfg: db.cfg, inTx: true}); err != nil {
		if rerr := ctl.rollback(end); rerr != nil {
			return errors.Join(err, fmt.Errorf("velox: rollback: %w", rerr))
		}
		return err
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
	defer func() {
		if p := recover(); p != nil {
			_, _ = db.ex.exec(end, "ROLLBACK TO SAVEPOINT "+name, nil)
			panic(p)
		}
	}()
	if err := fn(&DB{d: db.d, ex: db.ex, cfg: db.cfg, inTx: true, depth: db.depth + 1}); err != nil {
		if _, rerr := db.ex.exec(end, "ROLLBACK TO SAVEPOINT "+name, nil); rerr != nil {
			return errors.Join(err, fmt.Errorf("velox: rollback to savepoint: %w", rerr))
		}
		return err
	}
	if _, err := db.ex.exec(ctx, "RELEASE SAVEPOINT "+name, nil); err != nil {
		return fmt.Errorf("velox: release savepoint: %w", err)
	}
	return nil
}
