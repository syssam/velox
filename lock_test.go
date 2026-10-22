package velox_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// sentStatements is the fake's log without BEGIN, COMMIT, ROLLBACK and the
// savepoint statements: what velox sent of its own.
func sentStatements(e *velox.FakeExec) []string {
	var out []string
	for _, s := range e.Log {
		switch {
		case s == "BEGIN", s == "COMMIT", s == "ROLLBACK", strings.HasPrefix(s, "SAVEPOINT"),
			strings.HasPrefix(s, "RELEASE"), strings.HasPrefix(s, "ROLLBACK TO"):
			continue
		}
		out = append(out, s)
	}
	return out
}

// Each refusal of a locking read, with its text, on run's path and on
// plain's: none sends a statement.
func TestLockRefusals(t *testing.T) {
	const pre = "velox: testmodel.Invoice: "
	nop := velox.WithAround(func(c context.Context, _ *velox.Statement, next func(context.Context) error) error { return next(c) })
	for _, opts := range [][]velox.Option{nil, {nop}} {
		inTx := func(d velox.Dialect, fn func(ctx context.Context, tx *velox.DB) error) (*velox.FakeExec, error) {
			e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
			db := velox.OpenFake(d, e, opts...)
			var got error
			_ = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
				got = fn(ctx, tx)
				return got
			})
			return e, got
		}
		cases := []struct {
			name string
			d    velox.Dialect
			fn   func(ctx context.Context, tx *velox.DB) error
			want string
		}{
			{"a wait without a lock", velox.Postgres, func(ctx context.Context, tx *velox.DB) error {
				_, err := tx.Query[testmodel.Invoice]().Limit(1).SkipLocked().All(ctx)
				return err
			}, pre + "SkipLocked and NoWait need ForUpdate or ForShare"},
			{"NoWait without a lock", velox.MySQL, func(ctx context.Context, tx *velox.DB) error {
				_, err := tx.Query[testmodel.Invoice]().Limit(1).NoWait().All(ctx)
				return err
			}, pre + "SkipLocked and NoWait need ForUpdate or ForShare"},
			{"SQLite", velox.SQLite, func(ctx context.Context, tx *velox.DB) error {
				_, err := tx.Query[testmodel.Invoice]().Limit(1).ForUpdate().All(ctx)
				return err
			}, pre + "SQLite has no locking reads; its transactions lock the whole database"},
			{"Count", velox.Postgres, func(ctx context.Context, tx *velox.DB) error {
				_, err := tx.Query[testmodel.Invoice]().ForUpdate().Count(ctx)
				return err
			}, pre + "Count, Sum, Avg, Min and Max cannot lock rows; lock them with All, One, First, Iter or Exists"},
			{"Sum", velox.Postgres, func(ctx context.Context, tx *velox.DB) error {
				_, err := tx.Query[testmodel.Invoice]().ForShare().Sum(ctx, I.Total)
				return err
			}, pre + "Count, Sum, Avg, Min and Max cannot lock rows; lock them with All, One, First, Iter or Exists"},
			{"With", velox.Postgres, func(ctx context.Context, tx *velox.DB) error {
				_, err := tx.Query[testmodel.Invoice]().Limit(1).ForUpdate().With(I.Items).All(ctx)
				return err
			}, pre + "a locking read loads no relations; load them in another query"},
			{"With and First", velox.Postgres, func(ctx context.Context, tx *velox.DB) error {
				_, err := tx.Query[testmodel.Invoice]().ForUpdate().With(I.Items).First(ctx)
				return err
			}, pre + "a locking read loads no relations; load them in another query"},
		}
		for _, c := range cases {
			e, err := inTx(c.d, c.fn)
			if err == nil || err.Error() != c.want {
				t.Errorf("%s (hooks %d): %v, want %s", c.name, len(opts), err, c.want)
			}
			if s := sentStatements(e); len(s) != 0 {
				t.Errorf("%s (hooks %d): sent %q", c.name, len(opts), s)
			}
		}
		// Outside Tx.
		e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
		db := velox.OpenFake(velox.Postgres, e, opts...)
		_, err := db.Query[testmodel.Invoice]().Limit(1).ForUpdate().All(ctx)
		if want := pre + "a locking read (ForUpdate or ForShare) runs only inside Tx"; err == nil || err.Error() != want {
			t.Errorf("outside Tx (hooks %d): %v", len(opts), err)
		}
		if _, err := db.Query[testmodel.Invoice]().ForUpdate().Exists(ctx); err == nil || !strings.Contains(err.Error(), "runs only inside Tx") {
			t.Errorf("Exists outside Tx (hooks %d): %v", len(opts), err)
		}
		if _, _, err := db.Query[testmodel.Invoice]().Limit(1).ForUpdate().SQL(ctx); err == nil || !strings.Contains(err.Error(), "runs only inside Tx") {
			t.Errorf("SQL outside Tx (hooks %d): %v", len(opts), err)
		}
		if e.Calls != 0 {
			t.Errorf("outside Tx (hooks %d): %d statements sent", len(opts), e.Calls)
		}
	}

	// A Rewrite hook may not change the lock, either way.
	for name, rw := range map[string]velox.Rewrite{
		"sets":    func(_ context.Context, st *velox.Statement) error { st.Lock = velox.LockForUpdate; return nil },
		"clears":  func(_ context.Context, st *velox.Statement) error { st.Lock = 0; return nil },
		"waits":   func(_ context.Context, st *velox.Statement) error { st.LockWait = velox.LockNoWait; return nil },
		"unwaits": func(_ context.Context, st *velox.Statement) error { st.LockWait = 0; return nil },
	} {
		e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
		db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(rw))
		var err error
		_ = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			q := tx.Query[testmodel.Invoice]().Limit(1)
			if name != "sets" {
				q = q.ForUpdate().SkipLocked()
			}
			_, err = q.All(ctx)
			return err
		})
		if want := "velox: testmodel.Invoice: a Rewrite hook changed the statement's lock"; err == nil || err.Error() != want {
			t.Errorf("a hook that %s: %v", name, err)
		}
		if s := sentStatements(e); len(s) != 0 {
			t.Errorf("a hook that %s: sent %q", name, s)
		}
	}
}

// A locking read runs in a transaction and its savepoints, on the
// transaction's connection; built on the DB outside and run under fn's ctx,
// it fails with ErrOutsideTx before any lock check.
func TestLockInsideAndOutsideTx(t *testing.T) {
	for _, hooked := range []bool{false, true} {
		var opts []velox.Option
		if hooked {
			opts = append(opts, velox.WithAround(func(c context.Context, _ *velox.Statement, next func(context.Context) error) error { return next(c) }))
		}
		e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
		db := velox.OpenFake(velox.Postgres, e, opts...)
		const one = `SELECT ` + allCols + ` FROM "invoices" WHERE "id" = $1 LIMIT $2 FOR UPDATE`
		err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			q := tx.Query[testmodel.Invoice]().Where(I.ID.EQ(1))
			if got, err := q.Limit(1).ForUpdate().All(ctx); err != nil || len(got) != 1 || e.SQL != one {
				t.Errorf("All: %v, %v, %s", got, err, e.SQL)
			}
			if got, err := q.ForUpdate().First(ctx); err != nil || got.ID != 1 || e.SQL != one {
				t.Errorf("First: %v, %v, %s", got, err, e.SQL)
			}
			if got, err := q.ForUpdate().One(ctx); err != nil || got.ID != 1 || !strings.HasSuffix(e.SQL, "LIMIT $2 FOR UPDATE") {
				t.Errorf("One: %v, %v, %s", got, err, e.SQL)
			}
			if ok, err := q.ForShare().NoWait().Exists(ctx); err != nil || !ok || e.SQL != `SELECT 1 FROM "invoices" WHERE "id" = $1 LIMIT $2 FOR SHARE NOWAIT` {
				t.Errorf("Exists: %v, %v, %s", ok, err, e.SQL)
			}
			for _, err := range q.Limit(1).ForUpdate().SkipLocked().Iter(ctx) {
				if err != nil || !strings.HasSuffix(e.SQL, "LIMIT $2 FOR UPDATE SKIP LOCKED") {
					t.Errorf("Iter: %v, %s", err, e.SQL)
				}
			}
			var sent string
			if err := tx.Tx(ctx, func(ctx context.Context, sp *velox.DB) error {
				_, err := sp.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).ForUpdate().All(ctx)
				sent = e.SQL
				return err
			}); err != nil || sent != one {
				t.Errorf("savepoint: %v, %s", err, sent)
			}
			calls := e.Calls
			_, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).ForUpdate().All(ctx)
			if !errors.Is(err, velox.ErrOutsideTx) || e.Calls != calls {
				t.Errorf("built on db (hooked %v): %v", hooked, err)
			}
			// WithoutTx lifts the context's mark, not the handle's
			// transaction: the tx handle still locks, on its connection,
			// and the db handle is outside Tx.
			if got, err := q.Limit(1).ForUpdate().All(velox.WithoutTx(ctx)); err != nil || len(got) != 1 || e.SQL != one || e.Calls != calls+1 {
				t.Errorf("tx under WithoutTx (hooked %v): %v, %v, %s", hooked, got, err, e.SQL)
			}
			_, err = db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).ForUpdate().All(velox.WithoutTx(ctx))
			if want := "velox: testmodel.Invoice: a locking read (ForUpdate or ForShare) runs only inside Tx"; err == nil || err.Error() != want || e.Calls != calls+1 {
				t.Errorf("db under WithoutTx (hooked %v): %v", hooked, err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// On a DB with replicas, the locking read runs on the transaction's
	// connection, the primary's.
	for _, hooked := range []bool{false, true} {
		s := openReplicas(hooked)
		s.p.Rows = [][]any{invoiceRow(1)}
		err := s.db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			_, err := tx.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).ForUpdate().All(ctx)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := s.calls(); got != [3]int{1, 0, 0} || !strings.HasSuffix(s.p.SQL, " FOR UPDATE") {
			t.Errorf("replicas (hooked %v): calls %v, primary sent %s", hooked, got, s.p.SQL)
		}
	}
}

// The last of ForUpdate and ForShare wins, and the last of SkipLocked and
// NoWait.
func TestLockLastWins(t *testing.T) {
	db := velox.OpenFake(velox.MySQL, &velox.FakeExec{})
	_ = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		q := tx.Query[testmodel.Invoice]().Limit(1)
		for _, c := range []struct {
			q    velox.Query[testmodel.Invoice]
			want string
		}{
			{q.ForShare().ForUpdate(), " LIMIT ? FOR UPDATE"},
			{q.ForUpdate().ForShare(), " LIMIT ? FOR SHARE"},
			{q.ForUpdate().NoWait().SkipLocked(), " FOR UPDATE SKIP LOCKED"},
			{q.ForUpdate().SkipLocked().NoWait(), " FOR UPDATE NOWAIT"},
			{q.SkipLocked().ForShare(), " FOR SHARE SKIP LOCKED"},
			{q.ForUpdate().Offset(5).ForUpdate(), " LIMIT ? OFFSET ? FOR UPDATE"},
			{q.Limit(0).Unbounded().Offset(5).ForShare(), " OFFSET ? FOR SHARE"},
		} {
			sql, _, err := c.q.SQL(ctx)
			if err != nil || !strings.HasSuffix(sql, c.want) {
				t.Errorf("%s: %v, want the suffix %q", sql, err, c.want)
			}
		}
		return nil
	})
}
