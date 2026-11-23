package apitest

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// TestSQLiteLockingReadsOption: under WithSQLiteLockingReads a locking read
// on SQLite is sent without its lock clause, SkipLocked's and NoWait's
// included, and SQL renders it so; the other refusals stand. PostgreSQL
// keeps its clause.
func TestSQLiteLockingReadsOption(t *testing.T) {
	const pre = "velox: testmodel.Invoice: "
	nop := velox.WithAround(func(c context.Context, _ *velox.Statement, next func(context.Context) error) error { return next(c) })
	for _, opts := range [][]velox.Option{{velox.WithSQLiteLockingReads()}, {velox.WithSQLiteLockingReads(), nop}} {
		run := func(d velox.Dialect, fn func(ctx context.Context, tx *velox.DB) error) (*fakeconn.Exec, error) {
			e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}}
			var got error
			_ = openFake(d, e, opts...).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
				got = fn(ctx, tx)
				return got
			})
			return e, got
		}
		for _, q := range []func(velox.Query[testmodel.Invoice]) velox.Query[testmodel.Invoice]{
			func(q velox.Query[testmodel.Invoice]) velox.Query[testmodel.Invoice] { return q.ForUpdate() },
			func(q velox.Query[testmodel.Invoice]) velox.Query[testmodel.Invoice] { return q.ForShare() },
			func(q velox.Query[testmodel.Invoice]) velox.Query[testmodel.Invoice] {
				return q.ForUpdate().SkipLocked()
			},
			func(q velox.Query[testmodel.Invoice]) velox.Query[testmodel.Invoice] { return q.ForUpdate().NoWait() },
		} {
			var dry string
			e, err := run(velox.SQLite, func(ctx context.Context, tx *velox.DB) error {
				var err error
				if dry, _, err = q(tx.Query[testmodel.Invoice]()).Where(I.ID.EQ(1)).Limit(1).SQL(ctx); err != nil {
					return err
				}
				_, err = q(tx.Query[testmodel.Invoice]()).Where(I.ID.EQ(1)).One(ctx)
				return err
			})
			s := sentStatements(e)
			if err != nil || len(s) != 1 || !strings.HasSuffix(s[0], `WHERE "id" = ? LIMIT 2`) || !strings.HasSuffix(dry, `WHERE "id" = ? LIMIT 1`) {
				t.Errorf("SQLite (hooks %d): %v; sent %q, SQL %q", len(opts)-1, err, s, dry)
			}
		}
		e, err := run(velox.Postgres, func(ctx context.Context, tx *velox.DB) error {
			_, err := tx.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).ForUpdate().One(ctx)
			return err
		})
		if s := sentStatements(e); err != nil || len(s) != 1 || !strings.HasSuffix(s[0], "LIMIT 2 FOR UPDATE") {
			t.Errorf("PostgreSQL (hooks %d): %v; sent %q", len(opts)-1, err, s)
		}

		// The other checks stand.
		for name, c := range map[string]struct {
			fn   func(ctx context.Context, tx *velox.DB) error
			want string
		}{
			"a wait without a lock": {func(ctx context.Context, tx *velox.DB) error {
				_, err := tx.Query[testmodel.Invoice]().Limit(1).SkipLocked().All(ctx)
				return err
			}, "SkipLocked and NoWait need ForUpdate or ForShare"},
			"Count": {func(ctx context.Context, tx *velox.DB) error {
				_, err := tx.Query[testmodel.Invoice]().ForUpdate().Count(ctx)
				return err
			}, "Count, Sum, Avg, Min and Max cannot lock rows; lock them with All, One, First, Iter or Exists"},
			"Sum": {func(ctx context.Context, tx *velox.DB) error {
				_, err := velox.Sum(ctx, tx.Query[testmodel.Invoice]().ForShare(), I.Total)
				return err
			}, "Count, Sum, Avg, Min and Max cannot lock rows; lock them with All, One, First, Iter or Exists"},
			"With": {func(ctx context.Context, tx *velox.DB) error {
				_, err := tx.Query[testmodel.Invoice]().Limit(1).ForUpdate().With(I.Items).All(ctx)
				return err
			}, "a locking read loads no relations; load them in another query"},
		} {
			e, err := run(velox.SQLite, c.fn)
			if err == nil || err.Error() != pre+c.want {
				t.Errorf("%s (hooks %d): %v, want %s", name, len(opts)-1, err, c.want)
			}
			if s := sentStatements(e); len(s) != 0 {
				t.Errorf("%s (hooks %d): sent %q", name, len(opts)-1, s)
			}
		}
		e = &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}}
		_, err = openFake(velox.SQLite, e, opts...).Query[testmodel.Invoice]().Limit(1).ForUpdate().All(ctx)
		if want := pre + "a locking read (ForUpdate or ForShare) runs only inside Tx"; err == nil || err.Error() != want || e.Calls != 0 {
			t.Errorf("outside Tx (hooks %d): %v; %d statements", len(opts)-1, err, e.Calls)
		}
	}
}

// lockPairs opens the SQLite file database dsn names, in WAL mode, with
// WithSQLiteLockingReads, on conns connections, and the pair (1, 2) priced
// 0 in it.
func lockPairs(t *testing.T, query string, conns int) *velox.DB {
	t.Helper()
	db, err := velox.Connect("sqlite", filepath.Join(t.TempDir(), "lock.db")+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)"+query,
		velox.WithSQLiteLockingReads())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	velox.Pool(db).SetMaxOpenConns(conns)
	for _, s := range []string{"CREATE TABLE pairs (a INTEGER NOT NULL, b INTEGER NOT NULL, price INTEGER, PRIMARY KEY (a, b))",
		"INSERT INTO pairs (a, b, price) VALUES (1, 2, 0)"} {
		if _, err := db.Exec(ctx, "test", s); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// bump reads the pair for update and writes its price plus one, a
// read-modify-write; it returns the price it read. between runs between
// the read and the write.
func bump(ctx context.Context, tx *velox.DB, between func()) (wgCents, error) {
	p, err := tx.Query[wgPair]().Where(wgPairs.A.EQ(1), wgPairs.B.EQ(2)).ForUpdate().One(ctx)
	if err != nil {
		return 0, err
	}
	if between != nil {
		between()
	}
	_, err = tx.Update[wgPair]().Where(wgPairs.A.EQ(1), wgPairs.B.EQ(2)).Set(wgPairs.Price.Set(*p.Price + 1)).Exec(ctx)
	return *p.Price, err
}

// fresh is a context of its own. A transaction's context tells its own
// statements from another caller's on a pool of one connection, so callers
// that must take turns each need their own: context.Background() is one
// value for all of them.
func fresh(t *testing.T) context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

// price reads the pair's price.
func price(t *testing.T, db *velox.DB) wgCents {
	t.Helper()
	p, err := db.Query[wgPair]().Where(wgPairs.A.EQ(1), wgPairs.B.EQ(2)).One(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return *p.Price
}

// TestSQLiteLockingReadSemantics shows what a read-modify-write under
// ForUpdate gets on SQLite with WithSQLiteLockingReads, as its doc says. On
// Connect's one connection, transactions take turns. On two connections a
// DEFERRED transaction takes the write lock at its write: the one that
// writes second fails with SQLITE_BUSY, losing no update, and
// TxRetrySerializable runs it again. Under _txlock=immediate the second
// transaction waits at its BEGIN and reads what the first wrote.
func TestSQLiteLockingReadSemantics(t *testing.T) {
	t.Run("one connection", func(t *testing.T) {
		db := lockPairs(t, "", 1)
		var wg sync.WaitGroup
		errs := make([]error, 4)
		for i := range errs {
			wg.Go(func() {
				errs[i] = db.Tx(fresh(t), func(ctx context.Context, tx *velox.DB) error {
					_, err := bump(ctx, tx, nil)
					return err
				})
			})
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil || price(t, db) != 4 {
			t.Errorf("price %d, %v; want 4", price(t, db), err)
		}
	})
	t.Run("deferred", func(t *testing.T) {
		for _, retries := range []int{0, 1} {
			db := lockPairs(t, "", 2)
			attempts := 0
			err := db.Tx(fresh(t), func(ctx context.Context, tx *velox.DB) error {
				attempts++
				_, err := bump(ctx, tx, func() {
					if attempts > 1 {
						return
					}
					// Another transaction, on the other connection, reads and
					// writes the row between this one's read and its write.
					if err := db.Tx(fresh(t), func(ctx context.Context, tx *velox.DB) error {
						_, err := bump(ctx, tx, nil)
						return err
					}); err != nil {
						t.Errorf("the other transaction: %v", err)
					}
				})
				return err
			}, velox.TxRetrySerializable(retries))
			switch retries {
			case 0:
				if err == nil || !strings.Contains(err.Error(), "locked") || price(t, db) != 1 {
					t.Errorf("no retry: %v, price %d; want SQLITE_BUSY and 1", err, price(t, db))
				}
			case 1:
				if err != nil || attempts != 2 || price(t, db) != 2 {
					t.Errorf("one retry: %v after %d attempts, price %d; want 2", err, attempts, price(t, db))
				}
			}
		}
	})
	t.Run("immediate", func(t *testing.T) {
		db := lockPairs(t, "&_txlock=immediate", 2)
		var (
			wg    sync.WaitGroup
			read  wgCents
			other error
		)
		err := db.Tx(fresh(t), func(ctx context.Context, tx *velox.DB) error {
			_, err := bump(ctx, tx, func() {
				wg.Go(func() {
					other = db.Tx(fresh(t), func(ctx context.Context, tx *velox.DB) error {
						var err error
						read, err = bump(ctx, tx, nil)
						return err
					})
				})
			})
			return err
		})
		wg.Wait()
		if err != nil || other != nil || read != 1 || price(t, db) != 2 {
			t.Errorf("%v, %v: the second read %d, price %d; want 1 and 2", err, other, read, price(t, db))
		}
	})
}
