package apitest

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

const txConnErr = "a statement outside a Tx, on the goroutine running it"

// TestTxNestedStatementFails: on Connect's one connection, a statement on db in a
// Tx's fn cannot get a connection until the Tx returns, which waits for fn. It
// fails, saying so, under whichever unmarked context it is given.
func TestTxNestedStatementFails(t *testing.T) {
	outer := ctx
	for name, dsn := range map[string]string{"memory": ":memory:", "file": filepath.Join(t.TempDir(), "t.db")} {
		t.Run(name, func(t *testing.T) {
			db := connectInvoices(t, dsn)
			nested := map[string]func(context.Context) error{
				"Query": func(c context.Context) error { _, err := db.Query[testmodel.Invoice]().Limit(1).All(c); return err },
				"Count": func(c context.Context) error { _, err := db.Query[testmodel.Invoice]().Count(c); return err },
				"Exec": func(c context.Context) error {
					_, err := db.Exec(c, "test", "UPDATE invoices SET note = ''")
					return err
				},
				"Iter": func(c context.Context) error {
					for _, err := range db.Query[testmodel.Invoice]().Limit(1).Iter(c) {
						return err
					}
					return nil
				},
				"Tx": func(c context.Context) error {
					return db.Tx(c, func(context.Context, *velox.DB) error { return nil })
				},
			}
			for nn, stmt := range nested {
				t.Run(nn, func(t *testing.T) {
					noHang(t, func() {
						for _, opts := range [][]velox.TxOption{nil, {velox.TxReadOnly()}} {
							err := db.Tx(outer, func(ctx context.Context, tx *velox.DB) error {
								if err := stmt(outer); err == nil || !strings.Contains(err.Error(), txConnErr) {
									t.Errorf("under the outer context: %v", err)
								}
								if err := stmt(velox.WithoutTx(ctx)); err == nil || !strings.Contains(err.Error(), txConnErr) {
									t.Errorf("under WithoutTx: %v", err)
								}
								// The Tx is not harmed: the statement never got as far as its connection.
								_, err := tx.Query[testmodel.Invoice]().Count(ctx)
								return err
							}, opts...)
							if err != nil {
								t.Errorf("the Tx: %v", err)
							}
						}
						if err := stmt(outer); err != nil {
							t.Errorf("after the Tx: %v", err)
						}
					})
				})
			}
		})
	}
}

// TestTxNestedInASavepoint: a nested Tx holds the same connection.
func TestTxNestedInASavepoint(t *testing.T) {
	db := connectInvoices(t, ":memory:")
	noHang(t, func() {
		err := db.Tx(ctx, func(c context.Context, tx *velox.DB) error {
			return tx.Tx(c, func(context.Context, *velox.DB) error {
				_, err := db.Query[testmodel.Invoice]().Count(ctx)
				return err
			})
		})
		if err == nil || !strings.Contains(err.Error(), txConnErr) {
			t.Errorf("%v", err)
		}
	})
}

// TestTxEndsFreeThePool: however the Tx ends, its goroutine's next statement runs.
func TestTxEndsFreeThePool(t *testing.T) {
	db := connectInvoices(t, ":memory:")
	count := func() error { _, err := db.Query[testmodel.Invoice]().Count(ctx); return err }
	boom := errors.New("boom")
	noHang(t, func() {
		for name, end := range map[string]func(){
			"commit": func() { _ = db.Tx(ctx, func(context.Context, *velox.DB) error { return nil }) },
			"error":  func() { _ = db.Tx(ctx, func(context.Context, *velox.DB) error { return boom }) },
			"panic": func() {
				defer func() { _ = recover() }()
				_ = db.Tx(ctx, func(context.Context, *velox.DB) error { panic(boom) })
			},
		} {
			end()
			if err := count(); err != nil {
				t.Errorf("after %s: %v", name, err)
			}
		}
	})
	noHang(t, func() {
		var wg sync.WaitGroup
		wg.Go(func() {
			_ = db.Tx(ctx, func(context.Context, *velox.DB) error { runtime.Goexit(); return nil })
		})
		wg.Wait()
		if err := count(); err != nil {
			t.Errorf("after Goexit: %v", err)
		}
	})
}

// TestTxConcurrentStatementWaits: a statement on another goroutine is not nested:
// it waits for the Tx and then runs.
func TestTxConcurrentStatementWaits(t *testing.T) {
	db := connectInvoices(t, ":memory:")
	noHang(t, func() {
		var wg sync.WaitGroup
		started := make(chan struct{})
		var ran bool
		err := db.Tx(ctx, func(context.Context, *velox.DB) error {
			wg.Go(func() {
				close(started)
				if _, err := db.Query[testmodel.Invoice]().Count(ctx); err != nil {
					t.Errorf("the concurrent statement: %v", err)
				}
				ran = true
			})
			<-started
			return nil
		})
		wg.Wait()
		if err != nil || !ran {
			t.Errorf("Tx %v; ran %v", err, ran)
		}
	})
}

// TestTxNestedOnARaisedPool: a pool the application raised has a second connection,
// so the statement runs.
func TestTxNestedOnARaisedPool(t *testing.T) {
	db := connectInvoices(t, filepath.Join(t.TempDir(), "t.db"))
	velox.Pool(db).SetMaxOpenConns(2)
	noHang(t, func() {
		err := db.Tx(ctx, func(context.Context, *velox.DB) error {
			_, err := db.Query[testmodel.Invoice]().Count(ctx)
			return err
		})
		if err != nil {
			t.Errorf("%v", err)
		}
	})
}

// TestTxEndedContextIsNotWaitingForever: database/sql rolls a transaction back when
// its context ends, so the connection is free, and a statement on db in fn would not
// wait forever. It says the context ended, whichever context it runs under.
func TestTxEndedContextIsNotWaitingForever(t *testing.T) {
	db := connectInvoices(t, ":memory:")
	noHang(t, func() {
		cctx, cancel := context.WithCancel(ctx)
		defer cancel()
		_ = db.Tx(cctx, func(fctx context.Context, tx *velox.DB) error {
			cancel()
			for name, c := range map[string]context.Context{"its context": velox.WithoutTx(fctx), "another": ctx} {
				_, err := db.Query[testmodel.Invoice]().Count(c)
				if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "wait forever") {
					t.Errorf("%s: %v", name, err)
				}
			}
			return nil
		})
	})
	if _, err := db.Query[testmodel.Invoice]().Count(ctx); err != nil {
		t.Errorf("after the Tx: %v", err)
	}
}
