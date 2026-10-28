package velox_test

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func touch(ctx context.Context, tx *velox.DB) error {
	_, err := tx.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.ID.EQ(1)).Exec(ctx)
	return err
}

const touchSQL = `UPDATE "invoices" SET "paid" = $1, "updated_at" = $2 WHERE "id" = $3`

func TestTxCommits(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	if err := db.Tx(ctx, touch); err != nil {
		t.Fatal(err)
	}
	if want := []string{"BEGIN", touchSQL, "COMMIT"}; !reflect.DeepEqual(e.Log, want) {
		t.Errorf("%q", e.Log)
	}
}

func TestTxRollsBackOnError(t *testing.T) {
	boom := errors.New("boom")
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		if err := touch(ctx, tx); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || e.Log[len(e.Log)-1] != "ROLLBACK" {
		t.Errorf("%v %q", err, e.Log)
	}
}

// TestTxRollsBackOnPanic is half of Review Focus 2.
func TestTxRollsBackOnPanic(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	defer func() {
		if p := recover(); p != "oops" || e.Log[len(e.Log)-1] != "ROLLBACK" {
			t.Errorf("recovered %v, log %q", p, e.Log)
		}
	}()
	_ = db.Tx(ctx, func(context.Context, *velox.DB) error { panic("oops") })
	t.Error("Tx did not re-raise the panic")
}

// TestTxRollsBackWhenTheContextIsCanceled is the other half of Review Focus 2:
// the rollback runs, and runs with a context that is not canceled.
func TestTxRollsBackWhenTheContextIsCanceled(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	c, cancel := context.WithCancel(ctx)
	err := db.Tx(c, func(context.Context, *velox.DB) error {
		cancel()
		return c.Err()
	})
	if !errors.Is(err, context.Canceled) || e.Log[len(e.Log)-1] != "ROLLBACK" {
		t.Errorf("%v %q", err, e.Log)
	}
	if e.RollbackCtxErr != nil {
		t.Errorf("the rollback ran with a canceled context: %v", e.RollbackCtxErr)
	}
}

func TestSavepointRollsBackWhenTheContextIsCanceled(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	c, cancel := context.WithCancel(ctx)
	err := db.Tx(c, func(ctx context.Context, tx *velox.DB) error {
		return tx.Tx(c, func(context.Context, *velox.DB) error {
			cancel()
			return c.Err()
		})
	})
	want := []string{"BEGIN", "SAVEPOINT velox_1", "ROLLBACK TO SAVEPOINT velox_1", "RELEASE SAVEPOINT velox_1", "ROLLBACK"}
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(e.Log, want) {
		t.Errorf("%v\n got %q\nwant %q", err, e.Log, want)
	}
	if e.RollbackCtxErr != nil || e.SavepointCtxErr != nil {
		t.Errorf("rolled back with a canceled context: %v, %v", e.RollbackCtxErr, e.SavepointCtxErr)
	}
}

// goexit runs fn in a goroutine and waits for it to end.
func goexit(fn func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	<-done
}

// TestTxRollsBackOnGoexit: t.Fatal inside fn calls runtime.Goexit, which
// recover does not see.
func TestTxRollsBackOnGoexit(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	goexit(func() {
		_ = db.Tx(ctx, func(context.Context, *velox.DB) error { runtime.Goexit(); return nil })
		t.Error("Tx returned after Goexit")
	})
	if want := []string{"BEGIN", "ROLLBACK"}; !reflect.DeepEqual(e.Log, want) {
		t.Errorf("got %q, want %q", e.Log, want)
	}

	e = &velox.FakeExec{}
	db = velox.OpenFake(velox.Postgres, e)
	goexit(func() {
		_ = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			return tx.Tx(ctx, func(context.Context, *velox.DB) error { runtime.Goexit(); return nil })
		})
		t.Error("Tx returned after Goexit")
	})
	want := []string{"BEGIN", "SAVEPOINT velox_1", "ROLLBACK TO SAVEPOINT velox_1", "RELEASE SAVEPOINT velox_1", "ROLLBACK"}
	if !reflect.DeepEqual(e.Log, want) {
		t.Errorf("in a savepoint:\n got %q\nwant %q", e.Log, want)
	}
}

func TestPanicInASavepointRollsBackBoth(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	defer func() {
		want := []string{"BEGIN", "SAVEPOINT velox_1", "ROLLBACK TO SAVEPOINT velox_1", "RELEASE SAVEPOINT velox_1", "ROLLBACK"}
		if p := recover(); p != "oops" || !reflect.DeepEqual(e.Log, want) {
			t.Errorf("recovered %v\n got %q\nwant %q", p, e.Log, want)
		}
	}()
	_ = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		return tx.Tx(ctx, func(context.Context, *velox.DB) error { panic("oops") })
	})
	t.Error("Tx did not re-raise the panic")
}

func TestNestedTxUsesSavepoints(t *testing.T) {
	boom := errors.New("boom")
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		if err := tx.Tx(ctx, touch); err != nil {
			return err
		}
		if err := tx.Tx(ctx, func(context.Context, *velox.DB) error { return boom }); !errors.Is(err, boom) {
			t.Errorf("inner error %v", err)
		}
		return nil
	})
	want := []string{"BEGIN", "SAVEPOINT velox_1", touchSQL, "RELEASE SAVEPOINT velox_1",
		"SAVEPOINT velox_2", "ROLLBACK TO SAVEPOINT velox_2", "RELEASE SAVEPOINT velox_2", "COMMIT"}
	if err != nil || !reflect.DeepEqual(e.Log, want) {
		t.Errorf("%v\n got %q\nwant %q", err, e.Log, want)
	}
}

// TestSavepointNamesUnique: MySQL replaces a savepoint of the same name, so a
// savepoint made from a shallower DB inside a deeper one must not reuse the
// deeper one's name.
func TestSavepointNamesUnique(t *testing.T) {
	boom := errors.New("boom")
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.MySQL, e)
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		return tx.Tx(ctx, func(ctx context.Context, sp *velox.DB) error {
			// What Atomic does: a savepoint from the depth-0 DB, inside sp's.
			if err := tx.Tx(ctx, func(context.Context, *velox.DB) error { return nil }); err != nil {
				return err
			}
			return boom
		})
	})
	want := []string{"BEGIN", "SAVEPOINT velox_1", "SAVEPOINT velox_2", "RELEASE SAVEPOINT velox_2",
		"ROLLBACK TO SAVEPOINT velox_1", "RELEASE SAVEPOINT velox_1", "ROLLBACK"}
	if !errors.Is(err, boom) || !reflect.DeepEqual(e.Log, want) {
		t.Errorf("%v\n got %q\nwant %q", err, e.Log, want)
	}
}

// TestSavepointNamesUniqueConcurrently: the counter is the transaction's, and
// savepoints made at once on one transaction (a misuse that races on the
// connection, which the fake does not have) never share a name. Run with
// -race, a plain counter fails here.
func TestSavepointNamesUniqueConcurrently(t *testing.T) {
	const n = 64
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		var wg sync.WaitGroup
		errs := make([]error, n)
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = tx.Tx(ctx, func(context.Context, *velox.DB) error { return nil })
			}()
		}
		wg.Wait()
		return errors.Join(errs...)
	})
	if err != nil {
		t.Fatal(err)
	}
	made := map[string]int{}
	for _, q := range e.Log {
		if name, ok := strings.CutPrefix(q, "SAVEPOINT "); ok {
			made[name]++
		}
	}
	if len(made) != n {
		t.Errorf("%d distinct savepoint names for %d savepoints: %v", len(made), n, made)
	}
	for name, c := range made {
		if c != 1 {
			t.Errorf("savepoint %s made %d times", name, c)
		}
	}
}

func TestNestedTxDepth(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	err := db.Tx(ctx, func(ctx context.Context, a *velox.DB) error {
		return a.Tx(ctx, func(ctx context.Context, b *velox.DB) error {
			return b.Tx(ctx, func(context.Context, *velox.DB) error { return nil })
		})
	})
	want := []string{"BEGIN", "SAVEPOINT velox_1", "SAVEPOINT velox_2",
		"RELEASE SAVEPOINT velox_2", "RELEASE SAVEPOINT velox_1", "COMMIT"}
	if err != nil || !reflect.DeepEqual(e.Log, want) {
		t.Errorf("%v\n got %q\nwant %q", err, e.Log, want)
	}
}

func TestRetrySerializable(t *testing.T) {
	serialization := &pgconn.PgError{Code: "40001"}
	e := &velox.FakeExec{Errs: []error{serialization}}
	db := velox.OpenFake(velox.Postgres, e)
	runs := 0
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { runs++; return touch(ctx, tx) }, velox.TxRetrySerializable(2))
	if err != nil || runs != 2 {
		t.Fatalf("%v, runs %d", err, runs)
	}
	want := []string{"BEGIN", touchSQL, "ROLLBACK", "BEGIN", touchSQL, "COMMIT"}
	if !reflect.DeepEqual(e.Log, want) {
		t.Errorf("%q", e.Log)
	}
	e = &velox.FakeExec{Errs: []error{serialization}}
	db = velox.OpenFake(velox.Postgres, e)
	if err := db.Tx(ctx, touch); !errors.Is(err, serialization) {
		t.Errorf("without TxRetrySerializable: %v", err)
	}
	e = &velox.FakeExec{Errs: []error{errors.New("not retryable")}}
	db = velox.OpenFake(velox.Postgres, e)
	runs = 0
	if err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { runs++; return touch(ctx, tx) }, velox.TxRetrySerializable(3)); err == nil || runs != 1 {
		t.Errorf("a non-retryable error: %v, runs %d", err, runs)
	}
}

func TestTxOptionsAndInTx(t *testing.T) {
	var inTx []bool
	spy := func(_ context.Context, st *velox.Statement) error { inTx = append(inTx, st.InTx); return nil }
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(spy))
	if err := db.Tx(ctx, touch, velox.TxIsolation(sql.LevelSerializable), velox.TxReadOnly()); err != nil {
		t.Fatal(err)
	}
	if err := touch(ctx, db); err != nil {
		t.Fatal(err)
	}
	if e.Isolation != sql.LevelSerializable || !e.ReadOnly || !reflect.DeepEqual(inTx, []bool{true, false}) {
		t.Errorf("isolation %v, read-only %v, InTx %v", e.Isolation, e.ReadOnly, inTx)
	}
}

func TestRetryBound(t *testing.T) {
	for _, tc := range []struct{ n, runs int }{{1, 2}, {0, 1}, {-1, 1}, {3, 4}} {
		serialization := &pgconn.PgError{Code: "40001"}
		e := &velox.FakeExec{Err: serialization}
		db := velox.OpenFake(velox.Postgres, e)
		runs := 0
		err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { runs++; return touch(ctx, tx) }, velox.TxRetrySerializable(tc.n))
		if !errors.Is(err, serialization) || runs != tc.runs {
			t.Errorf("TxRetrySerializable(%d): runs %d, want %d; err %v", tc.n, runs, tc.runs, err)
		}
	}
}

func TestFailingCommit(t *testing.T) {
	serialization := &pgconn.PgError{Code: "40001"}
	e := &velox.FakeExec{CommitErr: serialization}
	db := velox.OpenFake(velox.Postgres, e)
	runs := 0
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		runs++
		if runs == 2 {
			e.CommitErr = nil
		}
		return touch(ctx, tx)
	}, velox.TxRetrySerializable(1))
	want := []string{"BEGIN", touchSQL, "COMMIT", "BEGIN", touchSQL, "COMMIT"}
	if err != nil || runs != 2 || !reflect.DeepEqual(e.Log, want) {
		t.Errorf("%v, runs %d\n got %q\nwant %q", err, runs, e.Log, want)
	}

	boom := errors.New("boom")
	e = &velox.FakeExec{CommitErr: boom}
	db = velox.OpenFake(velox.Postgres, e)
	err = db.Tx(ctx, touch, velox.TxRetrySerializable(2))
	want = []string{"BEGIN", touchSQL, "COMMIT"}
	if !errors.Is(err, boom) || !strings.HasPrefix(err.Error(), "velox: commit: ") || !reflect.DeepEqual(e.Log, want) {
		t.Errorf("%v\n got %q\nwant %q", err, e.Log, want)
	}
}

// TestTxPassesItsContext: fn's ctx is the one the transaction runs on: it
// has the transaction's deadline, velox's here, and what the caller's
// context and the TxAround hooks put in it. A savepoint's fn gets the context
// given to the nested Tx.
func TestTxPassesItsContext(t *testing.T) {
	type key struct{}
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{},
		velox.WithDeadlines(velox.Deadlines{Tx: time.Hour}),
		velox.WithTxAround(func(ctx context.Context, _ *velox.TxInfo, next func(context.Context) error) error {
			return next(context.WithValue(ctx, hookKey{}, "hook"))
		}))
	outer := context.WithValue(ctx, key{}, "caller")
	before := time.Now()
	err := db.Tx(outer, func(ctx context.Context, tx *velox.DB) error {
		dl, ok := ctx.Deadline()
		if !ok || dl.Before(before.Add(time.Hour)) || dl.After(time.Now().Add(time.Hour)) {
			t.Errorf("fn's ctx has the deadline %v, %v; want velox's, an hour from the start", dl, ok)
		}
		if got := ctx.Value(hookKey{}); got != "hook" {
			t.Errorf("fn's ctx has %v from the TxAround hook", got)
		}
		if got := ctx.Value(key{}); got != "caller" {
			t.Errorf("fn's ctx has %v from the caller's", got)
		}
		inner := context.WithValue(ctx, key{}, "savepoint")
		return tx.Tx(inner, func(ctx context.Context, sp *velox.DB) error {
			if got := ctx.Value(key{}); got != "savepoint" {
				t.Errorf("the savepoint's fn has %v", got)
			}
			if got, _ := ctx.Deadline(); !got.Equal(dl) {
				t.Errorf("the savepoint's fn has the deadline %v, want %v", got, dl)
			}
			return touch(ctx, sp)
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestStatementOutsideItsTx is Review Focus 1: under a context marked as a
// transaction's, a statement on any DB but the transaction's and its
// savepoints' (db, another Open's, another transaction's) fails with
// ErrOutsideTx before it is sent; the transaction's own DB, its savepoints,
// the TxAround hooks' work, a goroutine on the outer context and WithoutTx
// run. Each case runs on a DB without hooks (plain) and on one with an Around
// hook (run).
func TestStatementOutsideItsTx(t *testing.T) {
	read := func(ctx context.Context, db *velox.DB) error {
		_, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx)
		return err
	}
	outer := ctx
	for _, c := range []struct {
		name    string
		fn      func(ctx context.Context, db, tx *velox.DB) error // ctx is fn's
		outside bool
		msg     string // the error's text when outside; the statement's by default
	}{
		{"the transaction with its ctx", func(ctx context.Context, _, tx *velox.DB) error { return read(ctx, tx) }, false, ""},
		{"the transaction with a ctx derived from its", func(ctx context.Context, _, tx *velox.DB) error {
			return read(velox.WithTags(ctx, "k", "v"), tx)
		}, false, ""},
		{"the transaction with the outer ctx", func(_ context.Context, _, tx *velox.DB) error { return read(outer, tx) }, false, ""},
		{"a savepoint with its ctx", func(ctx context.Context, _, tx *velox.DB) error {
			return tx.Tx(ctx, func(ctx context.Context, sp *velox.DB) error { return read(ctx, sp) })
		}, false, ""},
		{"a savepoint with the transaction's ctx", func(ctx context.Context, _, tx *velox.DB) error {
			return tx.Tx(outer, func(_ context.Context, sp *velox.DB) error { return read(ctx, sp) })
		}, false, ""},
		{"the transaction with a savepoint's ctx", func(ctx context.Context, _, tx *velox.DB) error {
			return tx.Tx(ctx, func(ctx context.Context, _ *velox.DB) error { return read(ctx, tx) })
		}, false, ""},
		{"a goroutine on db with the outer ctx", func(_ context.Context, db, _ *velox.DB) error {
			errc := make(chan error)
			go func() { errc <- read(outer, db) }()
			return <-errc
		}, false, ""},
		{"db with the transaction's ctx", func(ctx context.Context, db, _ *velox.DB) error { return read(ctx, db) }, true, ""},
		{"db with a ctx derived from the transaction's", func(ctx context.Context, db, _ *velox.DB) error {
			return read(velox.WithTags(ctx, "k", "v"), db)
		}, true, ""},
		{"db with a savepoint's ctx", func(ctx context.Context, db, tx *velox.DB) error {
			return tx.Tx(outer, func(ctx context.Context, _ *velox.DB) error { return read(ctx, db) })
		}, true, ""},
		{"a write on db", func(ctx context.Context, db, _ *velox.DB) error { return touch(ctx, db) }, true, ""},
		{"a dry run on db", func(ctx context.Context, db, _ *velox.DB) error {
			_, _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).SQL(ctx)
			return err
		}, true, ""},
		{"a transaction on db", func(ctx context.Context, db, _ *velox.DB) error {
			return db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { return read(ctx, tx) })
		}, true, "velox: transaction: statement outside its transaction"},
		{"an InsertAll in batches on db", func(ctx context.Context, db, _ *velox.DB) error {
			defer velox.SetMaxParams(velox.Postgres, 18)()
			return db.InsertAll([]testmodel.Invoice{{OrgID: 1, Number: "A"}, {OrgID: 1, Number: "B"}, {OrgID: 1, Number: "C"}}).Exec(ctx)
		}, true, ""},
		{"an Iter on db", func(ctx context.Context, db, _ *velox.DB) error {
			for _, err := range db.Query[testmodel.Invoice]().Limit(1).Iter(ctx) {
				return err
			}
			return nil
		}, true, ""},
		{"an Insert on db", func(ctx context.Context, db, _ *velox.DB) error {
			return db.Insert(&testmodel.Invoice{OrgID: 1, Number: "A"}).Exec(ctx)
		}, true, ""},
		{"a Save on db", func(ctx context.Context, db, _ *velox.DB) error {
			return db.Save(&testmodel.Invoice{ID: 1, OrgID: 1, Number: "A"}, I.Number).Exec(ctx)
		}, true, ""},
		{"an upsert on db", func(ctx context.Context, db, _ *velox.DB) error {
			return db.Insert(&testmodel.Invoice{OrgID: 1, Number: "A"}).OnConflict(I.OrgID, I.Number).Update(I.Total).Exec(ctx)
		}, true, ""},
		{"a DB of another Open", func(ctx context.Context, _, _ *velox.DB) error {
			return read(ctx, velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}))
		}, true, ""},
		{"another transaction's DB", func(ctx context.Context, db, _ *velox.DB) error {
			var err error
			_ = db.Tx(outer, func(_ context.Context, other *velox.DB) error {
				err = read(ctx, other)
				return nil
			})
			return err
		}, true, ""},
		{"a savepoint of another transaction", func(ctx context.Context, db, _ *velox.DB) error {
			var err error
			_ = db.Tx(outer, func(_ context.Context, other *velox.DB) error {
				err = other.Tx(ctx, func(context.Context, *velox.DB) error { return nil })
				return nil
			})
			return err
		}, true, "velox: transaction: statement outside its transaction"},
		{"another transaction's DB with its ctx", func(_ context.Context, db, _ *velox.DB) error {
			return db.Tx(outer, func(ctx context.Context, other *velox.DB) error { return read(ctx, other) })
		}, false, ""},
		{"db with WithoutTx", func(ctx context.Context, db, _ *velox.DB) error {
			return read(velox.WithoutTx(ctx), db)
		}, false, ""},
		{"the transaction with WithoutTx", func(ctx context.Context, _, tx *velox.DB) error {
			return read(velox.WithoutTx(ctx), tx)
		}, false, ""},
		{"a transaction on db with WithoutTx", func(ctx context.Context, db, _ *velox.DB) error {
			return db.Tx(velox.WithoutTx(ctx), func(ctx context.Context, other *velox.DB) error { return read(ctx, other) })
		}, false, ""},
		{"a transaction on db with the outer ctx", func(_ context.Context, db, _ *velox.DB) error {
			return db.Tx(outer, func(ctx context.Context, tx *velox.DB) error { return read(ctx, tx) })
		}, false, ""},
	} {
		for _, hooked := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/hooked=%v", c.name, hooked), func(t *testing.T) {
				var opts []velox.Option
				if hooked {
					opts = append(opts, velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
						return next(ctx)
					}))
				}
				e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
				db := velox.OpenFake(velox.Postgres, e, opts...)
				var got error
				if err := db.Tx(outer, func(ctx context.Context, tx *velox.DB) error {
					got = c.fn(ctx, db, tx)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				sent := 0 // what the refused statement would have sent: a query, an insert
				for _, s := range e.Log {
					if strings.Contains(s, "invoices") {
						sent++
					}
				}
				switch {
				case !c.outside && got != nil:
					t.Errorf("failed: %v", got)
				case c.outside && !errors.Is(got, velox.ErrOutsideTx):
					t.Errorf("got %v, want ErrOutsideTx", got)
				case c.outside && got.Error() != cmp.Or(c.msg, "velox: testmodel.Invoice: statement outside its transaction"):
					t.Errorf("the error says %q", got)
				case c.outside && sent != 0:
					t.Errorf("sent %q", e.Log)
				}
			})
		}
	}
}

// TestTxAroundHooksRunOutsideTheTx: a TxAround hook's context is not the
// transaction's, before next and after, so the hook's own statements on db
// run.
func TestTxAroundHooksRunOutsideTheTx(t *testing.T) {
	var db *velox.DB
	var errs []error
	read := func(ctx context.Context) {
		_, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx)
		errs = append(errs, err)
	}
	db = velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{invoiceRow(1)}},
		velox.WithTxAround(func(ctx context.Context, _ *velox.TxInfo, next func(context.Context) error) error {
			read(ctx)
			err := next(ctx)
			read(ctx)
			return err
		}))
	if err := db.Tx(ctx, touch); err != nil {
		t.Fatal(err)
	}
	if len(errs) != 2 || errs[0] != nil || errs[1] != nil {
		t.Errorf("the hook's statements: %v", errs)
	}
}

// TestEndedTxMarksNothing: once its transaction has committed or rolled back,
// the mark on fn's ctx counts as none, on contexts derived from it too.
func TestEndedTxMarksNothing(t *testing.T) {
	read := func(ctx context.Context, db *velox.DB) error {
		_, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx)
		return err
	}
	for _, rollback := range []bool{false, true} {
		db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{invoiceRow(1)}})
		var kept, inner context.Context
		_ = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			kept = ctx
			_ = tx.Tx(ctx, func(ctx context.Context, _ *velox.DB) error { inner = ctx; return nil })
			if err := read(kept, db); !errors.Is(err, velox.ErrOutsideTx) {
				t.Errorf("rollback=%v: inside the transaction: %v, want ErrOutsideTx", rollback, err)
			}
			if rollback {
				return errors.New("undo")
			}
			return nil
		})
		for name, c := range map[string]context.Context{
			"fn's ctx":            kept,
			"a ctx derived":       context.WithoutCancel(velox.WithTags(kept, "k", "v")),
			"the savepoint's ctx": inner,
		} {
			if err := read(c, db); err != nil {
				t.Errorf("rollback=%v: after the transaction, on %s: %v", rollback, name, err)
			}
			if err := db.Tx(c, func(ctx context.Context, tx *velox.DB) error { return read(ctx, tx) }); err != nil {
				t.Errorf("rollback=%v: a Tx after the transaction, on %s: %v", rollback, name, err)
			}
		}
	}
}

// TestEndedTxMarksNothingOnEveryEnd: a transaction ended by a panic in fn,
// by runtime.Goexit in fn, or by a retry (the first attempt's transaction
// rolled back) leaves no mark either. A begin that fails makes no frame, so
// there is no mark to end.
func TestEndedTxMarksNothingOnEveryEnd(t *testing.T) {
	read := func(ctx context.Context, db *velox.DB) error {
		_, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx)
		return err
	}
	for _, end := range []string{"panic", "Goexit"} {
		db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{invoiceRow(1)}})
		var kept context.Context
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer func() { _ = recover() }()
			_ = db.Tx(ctx, func(ctx context.Context, _ *velox.DB) error {
				kept = ctx
				if err := read(kept, db); !errors.Is(err, velox.ErrOutsideTx) {
					t.Errorf("%s: inside the transaction: %v, want ErrOutsideTx", end, err)
				}
				if end == "panic" {
					panic("fn failed")
				}
				runtime.Goexit()
				return nil
			})
		}()
		<-done
		if err := read(kept, db); err != nil {
			t.Errorf("%s: after the transaction: %v", end, err)
		}
	}

	// The retried attempt runs while the first attempt's transaction has
	// ended: the first attempt's ctx is unmarked by then.
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}, Errs: []error{&pgconn.PgError{Code: "40001"}}}
	db := velox.OpenFake(velox.Postgres, e)
	var first context.Context
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		if first == nil {
			first = ctx
			return touch(ctx, tx) // fails, to be retried
		}
		if err := read(first, db); err != nil {
			t.Errorf("the first attempt's ctx, during the retry: %v", err)
		}
		return nil
	}, velox.TxRetrySerializable(2))
	if err != nil {
		t.Fatal(err)
	}
}

// TestWithoutTx: WithoutTx removes the mark and keeps the context's values
// and deadline; on a context without the mark it returns ctx.
func TestWithoutTx(t *testing.T) {
	if got := velox.WithoutTx(ctx); got != ctx {
		t.Error("WithoutTx changed a context without the mark")
	}
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{invoiceRow(1)}})
	tagged := velox.WithTags(ctx, "route", "POST /pay")
	err := db.Tx(tagged, func(ctx context.Context, tx *velox.DB) error {
		free := velox.WithoutTx(ctx)
		if got, want := velox.TagsOf(free), velox.TagsOf(ctx); !reflect.DeepEqual(got, want) || len(got) == 0 {
			t.Errorf("tags %v, want %v", got, want)
		}
		if id, all := velox.TenantOf(free); id != nil || all == "" {
			t.Errorf("tenant %v, %q: the WithAllTenants of the caller's ctx is lost", id, all)
		}
		if a, _ := free.Deadline(); !a.Equal(func() time.Time { d, _ := ctx.Deadline(); return d }()) {
			t.Errorf("deadline %v", a)
		}
		_, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(free)
		return err
	}, velox.TxTimeout(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	// Cancelling the parent cancels the WithoutTx ctx.
	parent, cancel := context.WithCancel(ctx)
	_ = db.Tx(parent, func(ctx context.Context, _ *velox.DB) error {
		free := velox.WithoutTx(ctx)
		cancel()
		select {
		case <-free.Done():
		case <-time.After(time.Second):
			t.Error("cancelling the parent did not cancel the WithoutTx ctx")
		}
		if !errors.Is(free.Err(), context.Canceled) {
			t.Errorf("Err %v, want context.Canceled", free.Err())
		}
		return nil
	})
}

func TestTxRetries(t *testing.T) {
	for _, c := range []struct {
		name string
		opts []velox.TxOption
		want int
	}{
		{"none", nil, 0},
		{"timeout then retry", []velox.TxOption{velox.TxTimeout(time.Second), velox.TxRetrySerializable(3)}, 3},
		{"the last wins", []velox.TxOption{velox.TxRetrySerializable(3), velox.TxRetrySerializable(0)}, 0},
		{"no retry option", []velox.TxOption{velox.TxReadOnly(), velox.TxIsolation(sql.LevelSerializable)}, 0},
	} {
		if got := velox.TxRetries(c.opts...); got != c.want {
			t.Errorf("%s: velox.TxRetries = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestNestedTxRefusesOptions(t *testing.T) {
	const want = "velox: transaction: a nested Tx (a savepoint) takes no options; give them to the outermost Tx"
	for name, opt := range map[string]velox.TxOption{
		"readonly":  velox.TxReadOnly(),
		"isolation": velox.TxIsolation(sql.LevelSerializable),
		"timeout":   velox.TxTimeout(time.Second),
		"retry0":    velox.TxRetrySerializable(0),
	} {
		e := &velox.FakeExec{}
		db := velox.OpenFake(velox.Postgres, e)
		ran := false
		err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			err := tx.Tx(ctx, func(context.Context, *velox.DB) error { ran = true; return nil }, opt)
			if err == nil || err.Error() != want {
				t.Errorf("%s: got %v, want %q", name, err, want)
			}
			return nil
		})
		if err != nil || ran {
			t.Errorf("%s: err %v, nested fn ran %v", name, err, ran)
		}
		for _, q := range e.Log {
			if strings.Contains(q, "SAVEPOINT") {
				t.Errorf("%s: sent %q", name, q)
			}
		}
	}
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		return tx.Tx(ctx, func(context.Context, *velox.DB) error { return nil })
	})
	if want := []string{"BEGIN", "SAVEPOINT velox_1", "RELEASE SAVEPOINT velox_1", "COMMIT"}; err != nil || !reflect.DeepEqual(e.Log, want) {
		t.Errorf("%v %q", err, e.Log)
	}
}

// TestZeroTxOptionPanics: a zero TxOption is not valid, as a zero Option is
// not; TxRetries, like DB.Tx, panics on it.
func TestZeroTxOptionPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("TxRetries(TxOption{}) did not panic")
		}
	}()
	velox.TxRetries(velox.TxOption{})
}

// TestRollbackIsBounded: a rollback velox sends on a context it detached
// from the caller's is bounded, so a dead connection cannot hang Tx: after fn
// fails, after a nested fn fails, and after fn's ctx was canceled.
func TestRollbackIsBounded(t *testing.T) {
	defer velox.SetRollbackTimeout(50 * time.Millisecond)()
	boom := errors.New("boom")
	bounded := func(t *testing.T, what string, start, dl time.Time) {
		t.Helper()
		if dl.IsZero() || dl.After(start.Add(70*time.Millisecond)) {
			t.Errorf("%s: its deadline is %v after the start, want at most 70ms", what, dl.Sub(start))
		}
	}
	unbounded := func(t *testing.T, what string, err error, start time.Time) {
		t.Helper()
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%s: Tx returned after %v", what, d)
		}
		if !errors.Is(err, boom) || !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, velox.ErrTimeout) {
			t.Errorf("%s: %v, want boom and context.DeadlineExceeded, not ErrTimeout", what, err)
		}
	}

	e := &velox.FakeExec{BlockRollback: true}
	db := velox.OpenFake(velox.Postgres, e)
	start := time.Now()
	err := db.Tx(ctx, func(context.Context, *velox.DB) error { return boom })
	unbounded(t, "ROLLBACK", err, start)
	bounded(t, "ROLLBACK", start, e.RollbackDeadline)

	e = &velox.FakeExec{BlockRollback: true}
	db = velox.OpenFake(velox.Postgres, e)
	var nested error
	var nestedStart, nestedDeadline time.Time
	_ = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		nestedStart = time.Now()
		nested = tx.Tx(ctx, func(context.Context, *velox.DB) error { return boom })
		nestedDeadline = e.RollbackDeadline
		return nested
	})
	unbounded(t, "ROLLBACK TO", nested, nestedStart)
	bounded(t, "ROLLBACK TO", nestedStart, nestedDeadline)
	if !strings.Contains(fmt.Sprint(nested), "velox: rollback to savepoint: ") {
		t.Errorf("ROLLBACK TO: %v, want it to say so", nested)
	}

	e = &velox.FakeExec{BlockRollback: true}
	db = velox.OpenFake(velox.Postgres, e)
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	start = time.Now()
	err = db.Tx(cctx, func(context.Context, *velox.DB) error { cancel(); return nil })
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("canceled: Tx returned after %v", d)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: %v", err)
	}
	bounded(t, "canceled", start, e.RollbackDeadline)
	if !errors.Is(e.RollbackCtxErr, context.DeadlineExceeded) {
		t.Errorf("canceled: the ROLLBACK's context ended with %v, want DeadlineExceeded: detached from the cancellation, then bounded", e.RollbackCtxErr)
	}
}

// TestBrokenTxReportsItsRollback: a transaction a savepoint broke reports
// the ROLLBACK's own failure too, as a transaction whose fn failed does.
func TestBrokenTxReportsItsRollback(t *testing.T) {
	gone, refused := errors.New("the connection is gone"), errors.New("rollback refused")
	e := &velox.FakeExec{Errs: []error{nil, gone}, RollbackErr: refused} // SAVEPOINT, then ROLLBACK TO
	db := velox.OpenFake(velox.Postgres, e)
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_ = tx.Tx(ctx, func(context.Context, *velox.DB) error { return errors.New("boom") })
		return nil
	})
	if !errors.Is(err, gone) || !errors.Is(err, refused) || !strings.Contains(fmt.Sprint(err), "velox: rollback: rollback refused") {
		t.Errorf("Tx: %v, want the ROLLBACK TO's error and the ROLLBACK's", err)
	}
	notCommitted(t, e)
}

// TestFailedSavepointRollbackAbortsTheTx: once a savepoint's ROLLBACK TO has
// failed, its work may still be in the transaction, so the transaction does
// not commit even when fn swallows the error, and its statements and nested
// Tx fail before anything is sent.
func TestFailedSavepointRollbackAbortsTheTx(t *testing.T) {
	boom, gone := errors.New("boom"), errors.New("the connection is gone")
	e := &velox.FakeExec{Errs: []error{nil, gone}} // SAVEPOINT, then ROLLBACK TO
	db := velox.OpenFake(velox.Postgres, e)
	var nested, stmt, again error
	var sent int
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		nested = tx.Tx(ctx, func(context.Context, *velox.DB) error { return boom })
		sent = len(e.Log)
		stmt = touch(ctx, tx)
		again = tx.Tx(ctx, func(context.Context, *velox.DB) error { t.Error("a nested Tx ran on a broken transaction"); return nil })
		if len(e.Log) != sent {
			t.Errorf("sent %q on a broken transaction", e.Log[sent:])
		}
		return nil // swallows nested
	})
	if !errors.Is(nested, boom) || !errors.Is(nested, gone) {
		t.Errorf("nested Tx: %v, want boom and the ROLLBACK TO's error", nested)
	}
	if !errors.Is(stmt, gone) || !strings.HasPrefix(fmt.Sprint(stmt), "velox: testmodel.Invoice: ") {
		t.Errorf("a statement after: %v, want the ROLLBACK TO's error", stmt)
	}
	if !errors.Is(again, gone) || !strings.HasPrefix(fmt.Sprint(again), "velox: savepoint: ") {
		t.Errorf("a nested Tx after: %v, want the ROLLBACK TO's error", again)
	}
	if !errors.Is(err, gone) || !strings.HasPrefix(fmt.Sprint(err), "velox: transaction: ") {
		t.Errorf("Tx: %v, want the ROLLBACK TO's error", err)
	}
	notCommitted(t, e)
}
