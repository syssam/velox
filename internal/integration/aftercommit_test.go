package integration

import (
	"context"
	"slices"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// TestAfterCommitSavepointOnRealDatabases: a nested Tx whose statement
// failed rolls back to its savepoint and drops the function queued in it;
// the outer transaction carries on, commits, and its functions run after the
// commit, seeing its write from another connection.
func TestAfterCommitSavepointOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		var got []string
		total := func() (v float64) {
			t.Helper()
			if err := tg.direct.QueryRowContext(ctx, rebind(tg.d, "SELECT total FROM invoices WHERE id = ?"), 1).Scan(&v); err != nil {
				t.Fatal(err)
			}
			return v
		}
		err := tg.db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			if err := velox.AfterCommit(ctx, tx, func(context.Context) { got = append(got, "outer") }); err != nil {
				return err
			}
			if err := tx.Tx(ctx, func(ctx context.Context, sp *velox.DB) error {
				if err := velox.AfterCommit(ctx, sp, func(context.Context) { got = append(got, "savepoint") }); err != nil {
					return err
				}
				// The row with id 1 exists: the insert fails.
				return sp.Insert(&testmodel.Invoice{ID: 1, OrgID: 1, Number: "dup", Data: []byte{}}).Exec(ctx)
			}); err == nil {
				t.Error("the duplicate insert succeeded")
			}
			if _, err := tx.Update[testmodel.Invoice]().Set(I.Total.Set(42)).Where(I.ID.EQ(1)).Exec(ctx); err != nil {
				return err
			}
			return velox.AfterCommit(ctx, tx, func(context.Context) { got = append(got, "after-savepoint") })
		})
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"outer", "after-savepoint"}; !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
		if v := total(); v != 42 {
			t.Errorf("total %v, want 42", v)
		}
	})
}
