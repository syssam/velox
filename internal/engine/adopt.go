package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/syssam/velox/internal/conn"
)

// UseTx runs fn with a DB whose statements run on tx, a transaction the caller
// began on a *sql.DB of db's dialect, as Tx runs fn on one of its own. Its
// differences are the ones that follow from the caller owning tx: UseTx never
// commits or rolls back, so fn's error is returned as it is and tx is the
// caller's to end; it retries nothing, applies no Tx option, deadline or
// TxAround hook, and sees no AfterCommit hook, as it cannot know whether the
// caller commits. A nested Tx in fn is a savepoint of tx, as in any
// transaction. The DB fn gets is valid only inside fn, as Tx's is.
//
// It refuses a db that is in a transaction, one that is sharded, and one whose
// connection does not run on a *sql.Tx, veloxpgx's.
func (db *DB) UseTx(ctx context.Context, tx *sql.Tx, fn func(ctx context.Context, tx *DB) error) error {
	if tx == nil {
		return errors.New("velox: UseTx: tx is nil")
	}
	if db.Foreign(ctx) {
		return fmt.Errorf("velox: UseTx: %w", ErrOutsideTx)
	}
	if IsDryRun(ctx) {
		return fmt.Errorf("velox: UseTx: %w", ErrDryRun)
	}
	if db.InTx {
		return errors.New("velox: UseTx: db is in a transaction already; UseTx takes one the caller began, at the top, and a nested Tx is a savepoint")
	}
	primary := db.Ex // a transaction runs on the primary, as Tx's does
	if db.Rt != nil {
		if db.Rt.One == nil {
			return errors.New("velox: UseTx: a sharded DB runs a transaction on one shard's primary, which it chooses itself; UseTx takes a *sql.Tx of a DB that is not sharded")
		}
		primary = db.Rt.One.Primary
	}
	a, ok := primary.(conn.Adopter)
	if !ok {
		return errors.New("velox: UseTx: this DB's connection does not run on a *sql.Tx; veloxpgx's runs on pgx's own transactions")
	}
	ex, ok := a.Adopt(tx)
	if !ok {
		return errors.New("velox: UseTx: this DB's connection does not run on a *sql.Tx; veloxpgx's runs on pgx's own transactions")
	}
	f := newTxFrame(ctx, DB{D: db.D, Ex: ex, Rt: db.Rt, Cfg: db.Cfg, InTx: true, bypass: db.bypass}, nil)
	err := fn(&f.ctx, &f.db)
	if err == nil && f.broken != nil {
		// A savepoint did not roll back: its fn's work may still be in tx,
		// which the caller must not commit.
		err = fmt.Errorf("velox: transaction: %w", f.broken)
	}
	// tx stays open, so a DB kept past fn would still reach it: fail its
	// statements, as a Tx's DB fails on its ended *sql.Tx.
	f.ended.Store(true)
	f.broken = fmt.Errorf("the DB of a UseTx whose fn has returned: %w", ErrOutsideTx)
	return err
}
