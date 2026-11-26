package apitest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

func touch(ctx context.Context, tx *velox.DB) error {
	_, err := tx.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.ID.EQ(1)).Exec(ctx)
	return err
}

const touchSQL = `UPDATE "invoices" SET "paid" = $1, "updated_at" = $2 WHERE "id" = $3`

func TestTxCommits(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	if err := db.Tx(ctx, touch); err != nil {
		t.Fatal(err)
	}
	if want := []string{"BEGIN", touchSQL, "COMMIT"}; !reflect.DeepEqual(e.Log, want) {
		t.Errorf("%q", e.Log)
	}
}

func TestTxRollsBackOnError(t *testing.T) {
	boom := errors.New("boom")
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
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

// TestRollbackOfAnEndedTxIsNotJoined: a ROLLBACK that fails because the driver
// already ended the transaction has nothing to report, so fn's error is
// returned as it is.
func TestRollbackOfAnEndedTxIsNotJoined(t *testing.T) {
	boom := errors.New("boom")
	for _, rollbackErr := range []error{sql.ErrTxDone, pgx.ErrTxClosed} {
		e := &fakeconn.Exec{RollbackErr: rollbackErr}
		db := openFake(velox.Postgres, e)
		err := db.Tx(ctx, func(context.Context, *velox.DB) error { return boom })
		if err == nil || err.Error() != "boom" || !errors.Is(err, boom) || errors.Is(err, rollbackErr) {
			t.Errorf("%v: Tx returned %v", rollbackErr, err)
		}
		if !slices.Contains(e.Log, "ROLLBACK") || slices.Contains(e.Log, "COMMIT") {
			t.Errorf("%v: log %q, want a ROLLBACK and no COMMIT", rollbackErr, e.Log)
		}
	}
}

// TestTxRollsBackOnPanic is half of Review Focus 2.
func TestTxRollsBackOnPanic(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
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
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
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
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
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
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	goexit(func() {
		_ = db.Tx(ctx, func(context.Context, *velox.DB) error { runtime.Goexit(); return nil })
		t.Error("Tx returned after Goexit")
	})
	if want := []string{"BEGIN", "ROLLBACK"}; !reflect.DeepEqual(e.Log, want) {
		t.Errorf("got %q, want %q", e.Log, want)
	}

	e = &fakeconn.Exec{}
	db = openFake(velox.Postgres, e)
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
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
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
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		if err := tx.Tx(ctx, touch); err != nil {
			return err
		}
		if err := tx.Tx(ctx, func(context.Context, *velox.DB) error { return boom }); !errors.Is(err, boom) {
			t.Errorf("inner error %v", err)
		}
		// A savepoint made after a ROLLBACK TO does not reuse a name.
		return tx.Tx(ctx, touch)
	})
	want := []string{"BEGIN", "SAVEPOINT velox_1", touchSQL, "RELEASE SAVEPOINT velox_1",
		"SAVEPOINT velox_2", "ROLLBACK TO SAVEPOINT velox_2", "RELEASE SAVEPOINT velox_2",
		"SAVEPOINT velox_3", touchSQL, "RELEASE SAVEPOINT velox_3", "COMMIT"}
	if err != nil || !reflect.DeepEqual(e.Log, want) {
		t.Errorf("%v\n got %q\nwant %q", err, e.Log, want)
	}
}

// TestSavepointNamesUnique: MySQL replaces a savepoint of the same name, so a
// savepoint made from a shallower DB inside a deeper one must not reuse the
// deeper one's name.
func TestSavepointNamesUnique(t *testing.T) {
	boom := errors.New("boom")
	e := &fakeconn.Exec{}
	db := openFake(velox.MySQL, e)
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

func TestNestedTxDepth(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
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
	e := &fakeconn.Exec{Errs: []error{serialization}}
	db := openFake(velox.Postgres, e)
	runs := 0
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { runs++; return touch(ctx, tx) }, velox.TxRetryRolledBack(2))
	if err != nil || runs != 2 {
		t.Fatalf("%v, runs %d", err, runs)
	}
	want := []string{"BEGIN", touchSQL, "ROLLBACK", "BEGIN", touchSQL, "COMMIT"}
	if !reflect.DeepEqual(e.Log, want) {
		t.Errorf("%q", e.Log)
	}
	e = &fakeconn.Exec{Errs: []error{serialization}}
	db = openFake(velox.Postgres, e)
	if err := db.Tx(ctx, touch); !errors.Is(err, serialization) {
		t.Errorf("without TxRetryRolledBack: %v", err)
	}
	e = &fakeconn.Exec{Errs: []error{errors.New("not retryable")}}
	db = openFake(velox.Postgres, e)
	runs = 0
	if err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { runs++; return touch(ctx, tx) }, velox.TxRetryRolledBack(3)); err == nil || runs != 1 {
		t.Errorf("a non-retryable error: %v, runs %d", err, runs)
	}
}

func TestTxOptionsAndInTx(t *testing.T) {
	var inTx []bool
	spy := func(_ context.Context, st *velox.Statement) error { inTx = append(inTx, st.InTx); return nil }
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e, velox.WithRewrite(spy))
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
		e := &fakeconn.Exec{Err: serialization}
		db := openFake(velox.Postgres, e)
		runs := 0
		err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { runs++; return touch(ctx, tx) }, velox.TxRetryRolledBack(tc.n))
		if !errors.Is(err, serialization) || runs != tc.runs {
			t.Errorf("TxRetryRolledBack(%d): runs %d, want %d; err %v", tc.n, runs, tc.runs, err)
		}
	}
}

func TestFailingCommit(t *testing.T) {
	serialization := &pgconn.PgError{Code: "40001"}
	e := &fakeconn.Exec{CommitErr: serialization}
	db := openFake(velox.Postgres, e)
	runs := 0
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		runs++
		if runs == 2 {
			e.CommitErr = nil
		}
		return touch(ctx, tx)
	}, velox.TxRetryRolledBack(1))
	want := []string{"BEGIN", touchSQL, "COMMIT", "BEGIN", touchSQL, "COMMIT"}
	if err != nil || runs != 2 || !reflect.DeepEqual(e.Log, want) {
		t.Errorf("%v, runs %d\n got %q\nwant %q", err, runs, e.Log, want)
	}

	boom := errors.New("boom")
	e = &fakeconn.Exec{CommitErr: boom}
	db = openFake(velox.Postgres, e)
	err = db.Tx(ctx, touch, velox.TxRetryRolledBack(2))
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
	db := openFake(velox.Postgres, &fakeconn.Exec{},
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
	db = openFake(velox.Postgres, &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}},
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
		db := openFake(velox.Postgres, &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}})
		var kept, inner context.Context
		_ = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			kept = ctx
			_ = tx.Tx(ctx, func(ctx context.Context, _ *velox.DB) error { inner = ctx; return nil })
			if err := read(kept, db.WithoutPolicy("another DB")); !errors.Is(err, velox.ErrOutsideTx) {
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
		db := openFake(velox.Postgres, &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}})
		var kept context.Context
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer func() { _ = recover() }()
			_ = db.Tx(ctx, func(ctx context.Context, _ *velox.DB) error {
				kept = ctx
				if err := read(kept, db.WithoutPolicy("another DB")); !errors.Is(err, velox.ErrOutsideTx) {
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
	e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}, Errs: []error{&pgconn.PgError{Code: "40001"}}}
	db := openFake(velox.Postgres, e)
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
	}, velox.TxRetryRolledBack(2))
	if err != nil {
		t.Fatal(err)
	}
}

func TestTxRetries(t *testing.T) {
	for _, c := range []struct {
		name string
		opts []velox.TxOption
		want int
	}{
		{"none", nil, 0},
		{"timeout then retry", []velox.TxOption{velox.TxTimeout(time.Second), velox.TxRetryRolledBack(3)}, 3},
		{"the last wins", []velox.TxOption{velox.TxRetryRolledBack(3), velox.TxRetryRolledBack(0)}, 0},
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
		"retry0":    velox.TxRetryRolledBack(0),
	} {
		e := &fakeconn.Exec{}
		db := openFake(velox.Postgres, e)
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
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
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

// TestBrokenTxReportsItsRollback: a transaction a savepoint broke reports
// the ROLLBACK's own failure too, as a transaction whose fn failed does.
func TestBrokenTxReportsItsRollback(t *testing.T) {
	gone, refused := errors.New("the connection is gone"), errors.New("rollback refused")
	e := &fakeconn.Exec{Errs: []error{nil, gone}, RollbackErr: refused} // SAVEPOINT, then ROLLBACK TO
	db := openFake(velox.Postgres, e)
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
	e := &fakeconn.Exec{Errs: []error{nil, gone}} // SAVEPOINT, then ROLLBACK TO
	db := openFake(velox.Postgres, e)
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

// TestNestedTxReleaseFails: when a savepoint's RELEASE fails, the nested Tx
// rolls back to the savepoint and returns the error. velox goes on, and sends
// COMMIT, without the nested work, when the enclosing fn returns nil.
func TestNestedTxReleaseFails(t *testing.T) {
	// BEGIN consumes nothing: SAVEPOINT, the UPDATE, then RELEASE.
	e := &fakeconn.Exec{Errs: []error{nil, nil, errors.New("release failed")}}
	db := openFake(velox.Postgres, e)
	var nested error
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		nested = tx.Tx(ctx, touch)
		return nil
	})
	if err != nil {
		t.Errorf("outer Tx: %v, want nil", err)
	}
	if nested == nil || nested.Error() != "velox: release savepoint: release failed" {
		t.Errorf("nested Tx: %v, want velox: release savepoint: release failed", nested)
	}
	want := []string{"BEGIN", "SAVEPOINT velox_1", touchSQL, "RELEASE SAVEPOINT velox_1",
		"ROLLBACK TO SAVEPOINT velox_1", "RELEASE SAVEPOINT velox_1", "COMMIT"}
	if !reflect.DeepEqual(e.Log, want) {
		t.Errorf("\n got %q\nwant %q", e.Log, want)
	}
}

// TestNestedTxReleaseAndRollbackFail: when the rollback that follows a failed
// RELEASE fails too, the nested work may be in the transaction, which then
// cannot commit, whatever the outer fn returns.
func TestNestedTxReleaseAndRollbackFail(t *testing.T) {
	release, undo := errors.New("release failed"), errors.New("rollback failed")
	e := &fakeconn.Exec{Errs: []error{nil, nil, release, undo}}
	db := openFake(velox.Postgres, e)
	var nested, after error
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		nested = tx.Tx(ctx, touch)
		after = touch(ctx, tx)
		return nil
	})
	if !errors.Is(nested, release) || !errors.Is(nested, undo) {
		t.Errorf("nested Tx: %v", nested)
	}
	if !errors.Is(after, undo) {
		t.Errorf("a statement after: %v", after)
	}
	if !errors.Is(err, undo) {
		t.Errorf("outer Tx: %v", err)
	}
	notCommitted(t, e)
}

// TestNestedTxCanceledBeforeReleaseDoesNotCommit: on a real database, a nested
// Tx whose context ends before RELEASE returns an error, and its row must not
// commit when the outer fn logs the error and returns nil.
func TestNestedTxCanceledBeforeReleaseDoesNotCommit(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	raw.SetMaxOpenConns(1)
	if _, err := raw.Exec("CREATE TABLE t (id integer PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	db := velox.Open(velox.SQLite, raw)
	var nested error
	err = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		sub, cancel := context.WithCancel(ctx)
		defer cancel()
		nested = tx.Tx(sub, func(ctx context.Context, tx *velox.DB) error {
			if _, err := tx.Exec(ctx, "test", "INSERT INTO t (id) VALUES (1)"); err != nil {
				return err
			}
			cancel()
			return nil
		})
		return nil
	})
	if err != nil {
		t.Errorf("outer Tx: %v", err)
	}
	if !errors.Is(nested, context.Canceled) {
		t.Errorf("nested Tx: %v", nested)
	}
	var n int
	if err := raw.QueryRow("SELECT COUNT(*) FROM t").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("a nested Tx that returned %v committed %d rows", nested, n)
	}
}

// TestLateStatementUnderACanceledFnCtxFails: a statement that a goroutine
// sends, after the transaction ended, under a ctx fn cancelled on return,
// fails with context.Canceled and writes nothing, where under fn's own ctx it
// runs on its own; the commit is not affected. On SQLite, as the fake
// connection ignores a context that ended.
func TestLateStatementUnderACanceledFnCtxFails(t *testing.T) {
	db := sqliteTable(t)
	var late, own context.Context
	err := db.Tx(ctx, func(txCtx context.Context, tx *velox.DB) error {
		own = txCtx
		c, cancel := context.WithCancel(txCtx)
		defer cancel()
		late = c
		return insertT(c, db, "1") // joins the transaction
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := insertT(late, db, "2"); !errors.Is(err, context.Canceled) {
		t.Errorf("late statement under the canceled ctx: %v, want context.Canceled", err)
	}
	if n := tableRows(t, db); n != 1 {
		t.Errorf("%d rows, want 1", n)
	}
	if err := insertT(own, db, "3"); err != nil {
		t.Errorf("under fn's own ctx: %v", err)
	}
	if n := tableRows(t, db); n != 2 {
		t.Errorf("%d rows, want 2: the late statement under fn's own ctx runs on its own", n)
	}
}
