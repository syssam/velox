package itest

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// lockWaiter returns a function reporting whether a session running the
// statement text is waiting for a row lock another session holds, or nil when
// the database will not tell: MySQL tells only an administrator
// (VELOX_TEST_MYSQL_ADMIN), which CI and scripts/ci-local.sh set.
func lockWaiter(t *testing.T, tg target, text string) func(context.Context) bool {
	db, q := tg.raw, `SELECT COUNT(*) FROM pg_stat_activity WHERE datname = current_database()
		AND cardinality(pg_blocking_pids(pid)) > 0 AND query = $1`
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
		// A prepared statement's text shows its arguments in place of ?.
		db, q = admin, `SELECT COUNT(*) FROM performance_schema.data_lock_waits w
			JOIN performance_schema.threads th ON th.THREAD_ID = w.REQUESTING_THREAD_ID WHERE th.PROCESSLIST_INFO LIKE ?`
		text = strings.ReplaceAll(text, "?", "%")
	}
	return func(ctx context.Context) bool {
		var n int
		if err := db.QueryRowContext(ctx, q, text).Scan(&n); err != nil {
			t.Errorf("counting lock waits: %v", err)
		}
		return n > 0
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

		a := hold(t, lctx, tg, func(ctx context.Context, tx *velox.DB) error {
			got, err := inv(tx).Where(I.ID.EQ(1)).ForUpdate().All(ctx)
			if err == nil && len(got) != 1 {
				t.Errorf("A read %v", ids(got))
			}
			return err
		})

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
		t.Logf("NoWait: %v", err)
		wantLockFailure(t, tg, err)
		if d := time.Since(start); d > 10*time.Second {
			t.Errorf("NoWait waited %v", d)
		}

		// ForUpdate waits for A's commit.
		var text string
		if err := tg.db.Tx(lctx, func(ctx context.Context, tx *velox.DB) error {
			var err error
			text, _, err = inv(tx).Where(I.ID.EQ(1)).ForUpdate().SQL(ctx)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		a.waitFor(t, lctx, tg, text, func(ctx context.Context, tx *velox.DB) error {
			got, err := inv(tx).Where(I.ID.EQ(1)).ForUpdate().All(ctx)
			if err == nil && len(got) != 1 {
				t.Errorf("B read %v", ids(got))
			}
			return err
		})
	})
}

// A locking read on an entity with a tenant, a Read policy and an EXISTS
// condition locks the rows it returns, and only those: not the rows the
// EXISTS reads.
func TestLockWithScopeOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		if tg.d == velox.SQLite {
			return // TestLocksOnRealDatabases pins SQLite's refusal
		}
		// Not the package's ctx, which lifts the tenant scope.
		lctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		Tk := testmodel.Tickets
		// ann's tickets in org 1 whose invoice is paid: ticket 1 (invoice 1).
		mine := func(tx *velox.DB) velox.Query[testmodel.Ticket] {
			return tx.Query[testmodel.Ticket]().Where(Tk.Invoice.HasWhere(I.Paid.EQ(true))).OrderBy(Tk.ID.Asc()).Limit(10)
		}
		asAnn := func(ctx context.Context) context.Context {
			return testmodel.AsUser(velox.WithTenant(ctx, int64(1)), "ann")
		}
		org1 := func(ctx context.Context) context.Context {
			return velox.SkipPolicy(velox.WithTenant(ctx, int64(1)), "velox tests")
		}
		var text string
		a := hold(t, lctx, tg, func(ctx context.Context, tx *velox.DB) error {
			q := mine(tx).ForUpdate()
			var err error
			if text, _, err = q.SQL(asAnn(ctx)); err != nil {
				return err
			}
			got, err := q.All(asAnn(ctx))
			if err == nil {
				wantTicketIDs(t, got, 1)
			}
			return err
		})
		if !strings.Contains(text, "EXISTS") || !strings.Contains(text, "owner") || !strings.Contains(text, "org_id") {
			t.Errorf("the locking read is not scoped: %s", text)
		}

		// The ticket is locked; the invoice its EXISTS read is not.
		err := tg.db.Tx(lctx, func(ctx context.Context, tx *velox.DB) error {
			_, err := tx.Query[testmodel.Ticket]().Where(Tk.ID.EQ(1)).Limit(1).ForUpdate().NoWait().All(org1(ctx))
			return err
		})
		wantLockFailure(t, tg, err)
		err = tg.db.Tx(lctx, func(ctx context.Context, tx *velox.DB) error {
			got, err := tx.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).ForUpdate().NoWait().All(org1(ctx))
			if err == nil && len(got) != 1 {
				t.Errorf("invoice 1: %v", ids(got))
			}
			return err
		})
		if err != nil {
			t.Errorf("the EXISTS's invoice is locked: %v", err)
		}
		// SkipLocked under the same scope leaves the held ticket out. On
		// PostgreSQL A locked only the row it returned. InnoDB, under
		// REPEATABLE READ, also keeps the locks of the rows A's read
		// scanned and did not return (tickets has no index for it to use),
		// so on MySQL the others are left out too.
		err = tg.db.Tx(lctx, func(ctx context.Context, tx *velox.DB) error {
			got, err := tx.Query[testmodel.Ticket]().OrderBy(Tk.ID.Asc()).Limit(10).ForUpdate().SkipLocked().All(asAnn(ctx))
			if err == nil && tg.d == velox.Postgres {
				wantTicketIDs(t, got, 2, 5)
			}
			for _, x := range got {
				if x.ID == 1 {
					t.Errorf("SkipLocked returned the held ticket: %v", got)
				}
			}
			return err
		})
		if err != nil {
			t.Errorf("SkipLocked: %v", err)
		}
		a.waitFor(t, lctx, tg, text, func(ctx context.Context, tx *velox.DB) error {
			got, err := mine(tx).ForUpdate().All(asAnn(ctx))
			if err == nil {
				wantTicketIDs(t, got, 1)
			}
			return err
		})
	})
}

func wantTicketIDs(t *testing.T, got []testmodel.Ticket, want ...int64) {
	t.Helper()
	ids := make([]int64, len(got))
	for i, x := range got {
		ids[i] = x.ID
	}
	wantIDs(t, ids, want)
}

// wantLockFailure checks that err is NOWAIT's failure: the driver's code,
// and no constraint error.
func wantLockFailure(t *testing.T, tg target, err error) {
	t.Helper()
	want := map[velox.Dialect]string{velox.Postgres: "55P03", velox.MySQL: "3572"}[tg.d]
	if code, ok := velox.DriverCode(err); !ok || code != want {
		t.Errorf("NoWait: %v, code %q, want %s", err, code, want)
	}
	var ce *velox.ConstraintError
	if errors.As(err, &ce) {
		t.Errorf("NoWait's failure is a constraint error: %v", err)
	}
}

// holder is transaction A, holding the locks its fn took until released.
type holder struct {
	release  chan struct{}
	done     chan error
	released atomic.Bool // A's fn has returned: its commit follows
	once     sync.Once
}

func (h *holder) let() { h.once.Do(func() { close(h.release) }) }

// hold runs lock in transaction A on a goroutine of its own and returns once
// lock has returned; A then waits for waitFor, or the test's end, to commit.
// The test waits for A to end, on every path.
func hold(t *testing.T, ctx context.Context, tg target, lock func(context.Context, *velox.DB) error) *holder {
	t.Helper()
	h := &holder{release: make(chan struct{}), done: make(chan error, 1)}
	locked := make(chan struct{})
	go func() {
		h.done <- tg.db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			err := lock(ctx, tx)
			close(locked)
			if err != nil {
				return err
			}
			select {
			case <-h.release:
			case <-ctx.Done():
				return ctx.Err()
			}
			h.released.Store(true)
			return nil
		})
	}()
	t.Cleanup(func() {
		h.let()
		<-h.done
	})
	<-locked
	return h
}

// waitFor runs wait, a locking read of a row A holds whose statement text is
// text, in transaction B, and checks that it waits until A commits. The test
// waits for B to end, on every path.
func (h *holder) waitFor(t *testing.T, ctx context.Context, tg target, text string, wait func(context.Context, *velox.DB) error) {
	t.Helper()
	bDone := make(chan error, 1)
	var afterA bool
	go func() {
		bDone <- tg.db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			err := wait(ctx, tx)
			afterA = h.released.Load()
			return err
		})
	}()
	drained := false
	defer func() {
		if !drained {
			h.let()
			<-bDone
		}
	}()
	// Wait until B is seen waiting for A's lock, where the database tells,
	// and for a while in any case.
	waiting := lockWaiter(t, tg, text)
	for blind := time.Now().Add(300 * time.Millisecond); ; time.Sleep(20 * time.Millisecond) {
		select {
		case err := <-bDone:
			drained = true
			t.Fatalf("B's locking read returned while A held the row: %v", err)
		default:
		}
		if waiting == nil && time.Now().After(blind) || waiting != nil && waiting(ctx) {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("B was never seen waiting for the lock")
		}
	}
	select {
	case err := <-bDone:
		drained = true
		t.Fatalf("B's locking read returned while A held the row: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	h.let()
	if err := <-h.done; err != nil {
		t.Errorf("A: %v", err)
	}
	h.done <- nil // for the cleanup's drain
	err := <-bDone
	drained = true
	if err != nil {
		t.Fatalf("B after A committed: %v", err)
	}
	if !afterA {
		t.Error("B's locking read returned before A's transaction ended")
	}
}
