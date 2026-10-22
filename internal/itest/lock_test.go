package itest

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// lockWaiters returns a function counting the sessions waiting for a row
// lock, or nil when the database will not tell: MySQL tells only an
// administrator (VELOX_TEST_MYSQL_ADMIN).
func lockWaiters(t *testing.T, tg target) func(context.Context) int {
	db, q := tg.raw, `SELECT COUNT(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`
	if tg.d == velox.MySQL {
		dsn := os.Getenv("VELOX_TEST_MYSQL_ADMIN")
		if dsn == "" {
			return nil
		}
		admin, err := sql.Open("mysql", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = admin.Close() })
		db, q = admin, `SELECT COUNT(*) FROM performance_schema.data_lock_waits`
	}
	return func(ctx context.Context) int {
		var n int
		if err := db.QueryRowContext(ctx, q).Scan(&n); err != nil {
			t.Errorf("counting lock waits: %v", err)
		}
		return n
	}
}

// Two transactions on two connections: A holds invoice 1 with ForUpdate
// while B's SkipLocked leaves it out, B's NoWait fails at once with the
// driver's lock error, B's ForShare of another row succeeds, and B's
// ForUpdate of invoice 1 waits until A commits. SQLite refuses a locking
// read.
func TestLocksOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		lctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		inv := func(tx *velox.DB) velox.Query[testmodel.Invoice] {
			return tx.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).Limit(10)
		}
		if tg.d == velox.SQLite {
			err := tg.db.Tx(lctx, func(ctx context.Context, tx *velox.DB) error {
				_, err := inv(tx).Where(I.ID.EQ(1)).ForUpdate().All(ctx)
				return err
			})
			if want := "velox: testmodel.Invoice: SQLite has no locking reads; its transactions lock the whole database"; err == nil || err.Error() != want {
				t.Errorf("SQLite: %v", err)
			}
			return
		}

		locked, release := make(chan struct{}), make(chan struct{})
		var released atomic.Bool // A's fn has returned: its commit follows
		aDone := make(chan error, 1)
		go func() {
			aDone <- tg.db.Tx(lctx, func(ctx context.Context, tx *velox.DB) error {
				got, err := inv(tx).Where(I.ID.EQ(1)).ForUpdate().All(ctx)
				if err != nil {
					close(locked)
					return err
				}
				if len(got) != 1 {
					t.Errorf("A read %v", ids(got))
				}
				close(locked)
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
				released.Store(true)
				return nil
			})
		}()
		defer func() {
			select {
			case <-release:
			default:
				close(release)
			}
		}()
		<-locked

		// SkipLocked leaves out the row A holds; ForShare of another row
		// is not blocked by it.
		err := tg.db.Tx(lctx, func(ctx context.Context, tx *velox.DB) error {
			got, err := inv(tx).Where(I.ID.In(1, 2)).ForUpdate().SkipLocked().All(ctx)
			if err != nil {
				return err
			}
			wantIDs(t, ids(got), []int64{2})
			shared, err := inv(tx).Where(I.ID.EQ(2)).ForShare().All(ctx)
			if err != nil {
				return err
			}
			wantIDs(t, ids(shared), []int64{2})
			return nil
		})
		if err != nil {
			t.Fatalf("SkipLocked and ForShare: %v", err)
		}

		// NoWait fails at once with the driver's code.
		start := time.Now()
		err = tg.db.Tx(lctx, func(ctx context.Context, tx *velox.DB) error {
			_, err := inv(tx).Where(I.ID.EQ(1)).ForUpdate().NoWait().All(ctx)
			return err
		})
		want := map[velox.Dialect]string{velox.Postgres: "55P03", velox.MySQL: "3572"}[tg.d]
		t.Logf("NoWait: %v", err)
		if code, ok := velox.DriverCode(err); !ok || code != want {
			t.Errorf("NoWait: %v, code %q, want %s", err, code, want)
		}
		if d := time.Since(start); d > 10*time.Second {
			t.Errorf("NoWait waited %v", d)
		}
		var ce *velox.ConstraintError
		if errors.As(err, &ce) {
			t.Errorf("NoWait's failure is a constraint error: %v", err)
		}

		// ForUpdate waits for A's commit.
		bDone := make(chan error, 1)
		var afterA bool
		go func() {
			bDone <- tg.db.Tx(lctx, func(ctx context.Context, tx *velox.DB) error {
				got, err := inv(tx).Where(I.ID.EQ(1)).ForUpdate().All(ctx)
				afterA = released.Load()
				if err == nil && len(got) != 1 {
					t.Errorf("B read %v", ids(got))
				}
				return err
			})
		}()
		// Wait until B is seen waiting for the lock, where the database
		// tells, and for a while in any case.
		waiters := lockWaiters(t, tg)
		for blind := time.Now().Add(300 * time.Millisecond); ; time.Sleep(20 * time.Millisecond) {
			select {
			case err := <-bDone:
				t.Fatalf("B's ForUpdate returned while A held the row: %v", err)
			default:
			}
			if waiters == nil && time.Now().After(blind) || waiters != nil && waiters(lctx) > 0 {
				break
			}
			if lctx.Err() != nil {
				t.Fatal("B was never seen waiting for the lock")
			}
		}
		select {
		case err := <-bDone:
			t.Fatalf("B's ForUpdate returned while A held the row: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
		close(release)
		if err := <-aDone; err != nil {
			t.Fatalf("A: %v", err)
		}
		if err := <-bDone; err != nil {
			t.Fatalf("B after A committed: %v", err)
		}
		if !afterA {
			t.Error("B's ForUpdate returned before A's transaction ended")
		}
	})
}
