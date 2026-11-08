package apitest

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// connectInvoices is a DB from Connect with three invoices.
func connectInvoices(t *testing.T, dsn string) *velox.DB {
	t.Helper()
	db, err := velox.Connect("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = velox.Pool(db).Exec(`CREATE TABLE invoices (created_at datetime NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at datetime NOT NULL DEFAULT CURRENT_TIMESTAMP, id integer PRIMARY KEY,
		org_id integer NOT NULL, number text NOT NULL, total real NOT NULL, paid boolean NOT NULL,
		paid_at datetime, note text NOT NULL, data blob NOT NULL);
		INSERT INTO invoices (id, org_id, number, total, paid, note, data) VALUES (1, 1, 'a', 0, false, '', x''), (2, 1, 'b', 0, false, '', x''), (3, 1, 'c', 0, false, '', x'')`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// noHang fails t when f has not returned in time: a nested statement that
// waits for the connection its own loop holds hangs.
func noHang(t *testing.T, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("hung: a statement waited for the connection its own loop or Tx holds")
	}
}

// TestIterNestedStatementFails: on Connect's one connection, a statement in
// the body of a loop over Iter cannot get a connection until the loop ends. It
// fails, saying so, whichever way it is sent, on :memory: and on a file.
func TestIterNestedStatementFails(t *testing.T) {
	const want = "a statement inside a loop over Iter"
	for name, dsn := range map[string]string{"memory": ":memory:", "file": filepath.Join(t.TempDir(), "t.db")} {
		t.Run(name, func(t *testing.T) {
			db := connectInvoices(t, dsn)
			loops := map[string]func(*velox.DB) func(func(testmodel.Invoice, error) bool){
				"Query": func(db *velox.DB) func(func(testmodel.Invoice, error) bool) {
					return db.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).Limit(3).Iter(ctx)
				},
				"Raw": func(db *velox.DB) func(func(testmodel.Invoice, error) bool) {
					return db.Raw[testmodel.Invoice]("test", "SELECT id FROM invoices").Iter(ctx)
				},
			}
			nested := map[string]func() error{
				"Query": func() error { _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); return err },
				"Count": func() error { _, err := db.Query[testmodel.Invoice]().Count(ctx); return err },
				"Exec":  func() error { _, err := db.Exec(ctx, "test", "UPDATE invoices SET note = ''"); return err },
				"Tx": func() error {
					return db.Tx(ctx, func(context.Context, *velox.DB) error { return nil })
				},
			}
			for ln, loop := range loops {
				for nn, stmt := range nested {
					t.Run(ln+"/"+nn, func(t *testing.T) {
						noHang(t, func() {
							rows := 0
							for _, err := range loop(db) {
								if err != nil {
									t.Errorf("the loop: %v", err)
									return
								}
								rows++
								// A raised pool is no cure: :memory: has a database per
								// connection, and a nested write on a file is SQLITE_BUSY.
								if err := stmt(); err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "with All first") || strings.Contains(err.Error(), "raise") {
									t.Errorf("nested: %v", err)
								}
							}
							if rows != 3 {
								t.Errorf("%d rows", rows)
							}
							// The loop has ended: the pool is free again.
							if err := stmt(); err != nil {
								t.Errorf("after the loop: %v", err)
							}
						})
					})
				}
			}
		})
	}
}

// TestIterBreakPanicFreeThePool: however the loop ends, the next statement on
// its goroutine runs.
func TestIterBreakPanicFreeThePool(t *testing.T) {
	db := connectInvoices(t, ":memory:")
	q := db.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).Limit(3)
	noHang(t, func() {
		for range q.Iter(ctx) {
			break
		}
		if _, err := q.All(ctx); err != nil {
			t.Errorf("after a break: %v", err)
		}
		func() {
			defer func() { _ = recover() }()
			for range q.Iter(ctx) {
				panic("boom")
			}
		}()
		if _, err := q.All(ctx); err != nil {
			t.Errorf("after a panic: %v", err)
		}
	})
}

// TestIterConcurrentStatementWaits: a statement on another goroutine, under
// another context, is not nested: it waits for the loop and then runs,
// however long the loop takes. Under the loop's own context it is refused
// from any goroutine, as velox tells a nested statement by its context.
func TestIterConcurrentStatementWaits(t *testing.T) {
	db := connectInvoices(t, ":memory:")
	other := context.WithoutCancel(ctx)
	noHang(t, func() {
		started := make(chan struct{})
		var ranAt time.Time
		var wg sync.WaitGroup
		var loopEnd time.Time
		for _, err := range db.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).Limit(1).Iter(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			wg.Go(func() {
				if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); err == nil || !strings.Contains(err.Error(), "a statement inside a loop over Iter") {
					t.Errorf("under the loop's context: %v", err)
				}
				close(started)
				if _, err := db.Query[testmodel.Invoice]().Limit(1).All(other); err != nil {
					t.Errorf("the concurrent statement: %v", err)
				}
				ranAt = time.Now()
			})
			<-started
			time.Sleep(20 * time.Millisecond)
			loopEnd = time.Now()
		}
		wg.Wait()
		if ranAt.Before(loopEnd) {
			t.Errorf("the concurrent statement finished %v before the loop ended", loopEnd.Sub(ranAt))
		}
	})
}

// TestIterInTxAndDryRunAreNotNested: a statement on the transaction's own
// connection, and a statement that sends nothing, need no second connection.
func TestIterInTxAndDryRunAreNotNested(t *testing.T) {
	db := connectInvoices(t, ":memory:")
	q := func(d *velox.DB) velox.Query[testmodel.Invoice] {
		return d.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).Limit(3)
	}
	noHang(t, func() {
		for _, err := range q(db).Iter(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := q(db).SQL(ctx); err != nil {
				t.Errorf("SQL in a loop: %v", err)
			}
		}
		err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			for _, err := range q(tx).Iter(ctx) {
				if err != nil {
					return err
				}
				if _, err := q(tx).All(ctx); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Errorf("in a transaction: %v", err)
		}
	})
}

// TestIterNestedOnARaisedPool: a pool the application raised has a second
// connection, so the statement runs.
func TestIterNestedOnARaisedPool(t *testing.T) {
	db := connectInvoices(t, filepath.Join(t.TempDir(), "t.db"))
	velox.Pool(db).SetMaxOpenConns(2)
	noHang(t, func() {
		for _, err := range db.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).Limit(3).Iter(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); err != nil {
				t.Errorf("nested: %v", err)
			}
		}
	})
}

// TestIterNestedWithReplicas: the loop reads from a replica, and the primary's
// one connection is free for a write.
func TestIterNestedWithReplicas(t *testing.T) {
	file := filepath.Join(t.TempDir(), "t.db")
	seed := connectInvoices(t, file)
	primary, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = primary.Close() }()
	primary.SetMaxOpenConns(1)
	if _, err := primary.Exec("CREATE TABLE t (id integer)"); err != nil {
		t.Fatal(err)
	}
	db := velox.Open(velox.SQLite, primary, velox.WithReplicas(velox.SQLConn(velox.Pool(seed))))
	noHang(t, func() {
		rows := 0
		for _, err := range db.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).Limit(3).Iter(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			rows++
			if _, err := db.Exec(ctx, "test", "INSERT INTO t (id) VALUES (1)"); err != nil {
				t.Errorf("nested: %v", err)
			}
		}
		if rows != 3 {
			t.Errorf("%d rows", rows)
		}
	})
}

// TestConnectFileTakesConcurrentWrites: the reason a file database keeps one
// connection. Writers on goroutines all succeed, with no busy_timeout in the
// DSN, where a pool of several connections fails with SQLITE_BUSY.
func TestConnectFileTakesConcurrentWrites(t *testing.T) {
	db := connectInvoices(t, filepath.Join(t.TempDir(), "t.db"))
	if got := velox.Pool(db).Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", got)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 20 {
				if _, err := db.Exec(ctx, "test", "UPDATE invoices SET total = total + 1"); err != nil {
					t.Errorf("a concurrent write: %v", err)
					return
				}
			}
		})
	}
	wg.Wait()
}

// TestIterNestedUnderAnotherContextSaysHeld: a statement in the loop's body
// under a context that is not the loop's waits, and its deadline ends it with
// an error that says the pool's one connection was held.
func TestIterNestedUnderAnotherContextSaysHeld(t *testing.T) {
	db := connectInvoices(t, ":memory:")
	noHang(t, func() {
		for _, err := range db.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).Limit(1).Iter(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			short, cancel := context.WithTimeout(context.WithoutCancel(ctx), 50*time.Millisecond)
			_, err := db.Query[testmodel.Invoice]().Count(short)
			cancel()
			if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "held by a loop over Iter or by a Tx") {
				t.Errorf("nested under another context: %v", err)
			}
		}
	})
}
