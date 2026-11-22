package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox"
)

// TestRawTextEndingTheTransaction: on SQLite and PostgreSQL, through pgx and
// database/sql, a raw text whose later statement ends the transaction breaks it:
// the text fails, as do the next statement and the Tx. The UPDATE before the
// COMMIT is durable by then; the later one is not sent. MySQL without
// multiStatements refuses the text.
func TestRawTextEndingTheTransaction(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		if tg.d == velox.MySQL {
			t.Skip("MySQL without multiStatements refuses multi-statement text")
		}
		err := tg.db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			if _, err := tx.Exec(ctx, "test", "UPDATE invoices SET note = 'a'; COMMIT"); !errors.Is(err, velox.ErrTxControl) {
				t.Errorf("the text: %v", err)
			}
			if _, err := tx.Exec(ctx, "test", "UPDATE invoices SET note = 'b'"); !errors.Is(err, velox.ErrTxControl) {
				t.Errorf("the next statement: %v", err)
			}
			return nil
		})
		if !errors.Is(err, velox.ErrTxControl) {
			t.Errorf("the Tx: %v", err)
		}
		var n int
		if err := tg.direct.QueryRowContext(ctx, "SELECT COUNT(*) FROM invoices WHERE note = 'b'").Scan(&n); err != nil || n != 0 {
			t.Errorf("the later UPDATE: %d rows, %v", n, err)
		}
		err = tg.db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			_, err := tx.Exec(ctx, "test", "UPDATE invoices SET note = 'c'; UPDATE invoices SET paid = paid;")
			return err
		})
		if err != nil {
			t.Errorf("a text that leaves the transaction open: %v", err)
		}
	})
}
