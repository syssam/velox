package integration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/syssam/velox"
)

// TestCommitRolledBackIsNotUnknown: PostgreSQL answers the COMMIT of a
// transaction that a failed statement aborted with ROLLBACK, which pgx
// returns as ErrTxCommitRollback, natively and through database/sql. The
// transaction rolled back, so the Tx's error does not match
// ErrCommitUnknown; it matches pgx's error, and the UPDATE before the
// failed statement is not kept. A deferred unique constraint checked at
// COMMIT is a *ConstraintError, a rollback too.
func TestCommitRolledBackIsNotUnknown(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		if tg.d != velox.Postgres {
			t.Skip("PostgreSQL keeps an aborted transaction open and answers its COMMIT with ROLLBACK")
		}
		err := tg.db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			if _, err := tx.Exec(ctx, "test", "UPDATE invoices SET note = 'rolled back'"); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, "test", "INSERT INTO velox_no_such_table VALUES (1)"); err == nil {
				t.Fatal("the failing statement succeeded")
			}
			return nil // the statement's error is swallowed, so velox commits
		})
		if err == nil || errors.Is(err, velox.ErrCommitUnknown) || !errors.Is(err, pgx.ErrTxCommitRollback) || !strings.HasPrefix(err.Error(), "velox: commit: ") {
			t.Errorf("COMMIT answered with ROLLBACK: %v; want pgx.ErrTxCommitRollback and not ErrCommitUnknown", err)
		}
		var n int
		if err := tg.direct.QueryRowContext(ctx, "SELECT COUNT(*) FROM invoices WHERE note = 'rolled back'").Scan(&n); err != nil || n != 0 {
			t.Errorf("the UPDATE before the failed statement: %d rows, %v", n, err)
		}

		tg.exec(t, "DROP TABLE IF EXISTS velox_commit_deferred")
		tg.exec(t, "CREATE TABLE velox_commit_deferred (k bigint, CONSTRAINT velox_commit_deferred_uq UNIQUE (k) DEFERRABLE INITIALLY DEFERRED)")
		t.Cleanup(func() { tg.exec(t, "DROP TABLE IF EXISTS velox_commit_deferred") })
		err = tg.db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			_, err := tx.Exec(ctx, "test", "INSERT INTO velox_commit_deferred VALUES (1), (1)")
			return err
		})
		var ce *velox.ConstraintError
		if errors.Is(err, velox.ErrCommitUnknown) || !errors.As(err, &ce) || ce.Constraint != "velox_commit_deferred_uq" {
			t.Errorf("deferred unique constraint at COMMIT: %v; want a *ConstraintError, not ErrCommitUnknown", err)
		}
	})
}
