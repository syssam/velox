package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// TestUseTxOnRealDatabases: velox's write and the caller's raw SQL on one *sql.Tx
// are both undone by the caller's Rollback and both kept by its Commit, on every
// database. A DB on veloxpgx, whose connection is not a *sql.Tx, is refused.
func TestUseTxOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		if tg.name == "postgres-pgx" {
			tx, err := tg.direct.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			err = velox.UseTx(ctx, tg.db, tx, func(context.Context, *velox.DB) error {
				t.Error("fn ran")
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), "does not run on a *sql.Tx") {
				t.Errorf("veloxpgx: %v", err)
			}
			return
		}
		total := func(id int64) (v float64) {
			t.Helper()
			if err := tg.direct.QueryRowContext(ctx, rebind(tg.d, "SELECT total FROM invoices WHERE id = ?"), id).Scan(&v); err != nil {
				t.Fatal(err)
			}
			return v
		}
		for _, commit := range []bool{false, true} {
			tx, err := tg.direct.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			err = velox.UseTx(ctx, tg.db, tx, func(ctx context.Context, vtx *velox.DB) error {
				if _, err := vtx.Update[testmodel.Invoice]().Set(I.Total.Set(1)).Where(I.ID.EQ(1)).Exec(ctx); err != nil {
					return err
				}
				_, err := tx.ExecContext(ctx, rebind(tg.d, "UPDATE invoices SET total = ? WHERE id = ?"), 2, 2)
				return err
			})
			if err != nil {
				_ = tx.Rollback()
				t.Fatalf("commit=%v: %v", commit, err)
			}
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
			a, b := total(1), total(2)
			if commit != (a == 1 && b == 2) || (!commit && (a == 1 || b == 2)) {
				t.Errorf("commit=%v: totals %v and %v", commit, a, b)
			}
		}
	})
}
