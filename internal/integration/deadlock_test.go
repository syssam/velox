package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// TestMySQLDeadlockSwallowedDoesNotCommit: two transactions lock two rows in
// opposite orders, and the one MySQL picks as the victim swallows 1213. The
// server has rolled it back, so neither its work before the deadlock nor a
// statement after it may commit, and its Tx must say so.
func TestMySQLDeadlockSwallowedDoesNotCommit(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		if tg.d != velox.MySQL {
			t.Skip("MySQL's deadlock rolls the transaction back and leaves the connection in autocommit")
		}
		dctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		mark := func(ctx context.Context, tx *velox.DB, id int64, note string) error {
			_, err := tx.Update[testmodel.Invoice]().Set(I.Note.Set(note)).Where(I.ID.EQ(id)).Exec(ctx)
			return err
		}
		// Each locks its first row, waits for the other's, then wants the
		// other's first row.
		aHolds, bHolds := make(chan struct{}), make(chan struct{})
		run := func(first, second int64, note string, holds, other chan struct{}) (bool, error) {
			var dead bool
			err := tg.db.Tx(dctx, func(ctx context.Context, tx *velox.DB) error {
				if err := mark(ctx, tx, first, note); err != nil {
					return err
				}
				close(holds)
				select {
				case <-other:
				case <-ctx.Done():
					return ctx.Err()
				}
				err := mark(ctx, tx, second, note)
				var my *mysql.MySQLError
				if errors.As(err, &my) && my.Number == 1213 {
					dead = true
					// Swallowed, and a statement made as though the
					// transaction were alive.
					_ = mark(ctx, tx, first, note+" after")
					return nil
				}
				return err
			})
			return dead, err
		}
		type result struct {
			err  error
			dead bool
			note string
		}
		results := make(chan result, 2)
		go func() {
			dead, err := run(1, 2, "a", aHolds, bHolds)
			results <- result{err, dead, "a"}
		}()
		go func() {
			dead, err := run(2, 1, "b", bHolds, aHolds)
			results <- result{err, dead, "b"}
		}()
		first, second := <-results, <-results
		victim := first
		if second.dead {
			victim = second
		}
		if !victim.dead {
			t.Fatalf("no deadlock: %v, %v", first.err, second.err)
		}
		var my *mysql.MySQLError
		if !errors.As(victim.err, &my) || my.Number != 1213 {
			t.Errorf("the victim's Tx returned %v, want the deadlock", victim.err)
		}
		for _, x := range []int64{1, 2} {
			inv, err := tg.db.Query[testmodel.Invoice]().Where(I.ID.EQ(x)).One(dctx)
			if err != nil {
				t.Fatal(err)
			}
			if inv.Note == victim.note || inv.Note == victim.note+" after" {
				t.Errorf("invoice %d holds the victim's note %q", x, inv.Note)
			}
		}
	})
}
