package velox_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func near(t *testing.T, got time.Time, want time.Duration) {
	t.Helper()
	if d := time.Until(got); got.IsZero() || d > want || d < want-time.Second {
		t.Errorf("deadline in %v, want about %v", d, want)
	}
}

func TestStatementDefaultDeadline(t *testing.T) {
	e := &velox.FakeExec{Block: true}
	db := velox.OpenFake(velox.Postgres, e, velox.WithDeadlines(velox.Deadlines{Statement: 50 * time.Millisecond}))
	start := time.Now()
	_, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx)
	if !errors.Is(err, velox.ErrTimeout) || !errors.Is(err, context.DeadlineExceeded) || !strings.HasPrefix(err.Error(), "velox: testmodel.Invoice: ") {
		t.Errorf("%v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
}

// TestCallersEarlierDeadlineIsKept is Review Focus 2.
func TestCallersEarlierDeadlineIsKept(t *testing.T) {
	e := &velox.FakeExec{Block: true}
	db := velox.OpenFake(velox.Postgres, e, velox.WithDeadlines(velox.Deadlines{Statement: time.Hour, MaxStatement: time.Hour}))
	cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	want, _ := cctx.Deadline()
	_, err := db.Query[testmodel.Invoice]().Limit(1).All(cctx)
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, velox.ErrTimeout) || !e.Deadline.Equal(want) {
		t.Errorf("%v; deadline %v, want the caller's %v", err, e.Deadline, want)
	}
}

func TestMaxStatementBringsADeadlineForward(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e, velox.WithDeadlines(velox.Deadlines{MaxStatement: 2 * time.Second}))
	cctx, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(cctx); err != nil {
		t.Fatal(err)
	}
	near(t, e.Deadline, 2*time.Second)
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	near(t, e.Deadline, 2*time.Second) // no deadline at all is later than the cap
}

func TestNoDeadlinesLeaveTheContextAlone(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); err != nil || !e.Deadline.IsZero() {
		t.Errorf("%v, deadline %v", err, e.Deadline)
	}
}

// TestDeadlineWrapsTheAroundHooks: the hooks see velox's deadline, and a
// hook that retries retries within it.
func TestDeadlineWrapsTheAroundHooks(t *testing.T) {
	e := &velox.FakeExec{Block: true}
	attempts := 0
	retry := velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
		for {
			attempts++
			if err := next(ctx); err == nil || ctx.Err() != nil {
				return err
			}
		}
	})
	db := velox.OpenFake(velox.Postgres, e, retry, velox.WithDeadlines(velox.Deadlines{Statement: 50 * time.Millisecond}))
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); !errors.Is(err, velox.ErrTimeout) || attempts != 1 {
		t.Errorf("%v after %d attempts", err, attempts)
	}
}

func TestTxDeadline(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e, velox.WithDeadlines(velox.Deadlines{Tx: 50 * time.Millisecond}))
	err := db.Tx(ctx, func(_ context.Context, tx *velox.DB) error {
		e.Block = true
		_, err := tx.Query[testmodel.Invoice]().Limit(1).All(ctx) // the caller's ctx: the tx's deadline still applies
		return err
	})
	if !errors.Is(err, velox.ErrTimeout) || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("%v", err)
	}
	if last := e.Log[len(e.Log)-1]; last != "ROLLBACK" {
		t.Errorf("log %v", e.Log)
	}
}

// TestStatementOnTheTxContextTimesOut: a statement run on fn's ctx, which
// carries velox's deadline already, fails past it with ErrTimeout naming its
// entity, as one run on a context without the deadline does; on a DB without
// hooks (plain) and with an Around hook (run). With the caller's deadline
// instead, it is the caller's error.
func TestStatementOnTheTxContextTimesOut(t *testing.T) {
	for _, hooked := range []bool{false, true} {
		var opts []velox.Option
		if hooked {
			opts = append(opts, velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
				return next(ctx)
			}))
		}
		e := &velox.FakeExec{}
		db := velox.OpenFake(velox.Postgres, e, opts...)
		var stmt error
		_ = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			e.Block = true
			_, stmt = tx.Query[testmodel.Invoice]().Limit(1).All(ctx)
			return stmt
		}, velox.TxTimeout(20*time.Millisecond))
		want := "velox: testmodel.Invoice: deadline set by velox passed: context deadline exceeded\nvelox: testmodel.Invoice: context deadline exceeded"
		if !errors.Is(stmt, velox.ErrTimeout) || !errors.Is(stmt, context.DeadlineExceeded) || stmt.Error() != want {
			t.Errorf("hooked=%v: the statement failed with %v, want %q", hooked, stmt, want)
		}

		e = &velox.FakeExec{}
		db = velox.OpenFake(velox.Postgres, e, opts...)
		cctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		_ = db.Tx(cctx, func(ctx context.Context, tx *velox.DB) error {
			e.Block = true
			_, stmt = tx.Query[testmodel.Invoice]().Limit(1).All(ctx)
			return stmt
		})
		cancel()
		if !errors.Is(stmt, context.DeadlineExceeded) || errors.Is(stmt, velox.ErrTimeout) {
			t.Errorf("hooked=%v: past the caller's deadline, the statement failed with %v", hooked, stmt)
		}
	}
}

func TestTxPastItsDeadlineIsNotCommitted(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	err := db.Tx(ctx, func(context.Context, *velox.DB) error {
		time.Sleep(60 * time.Millisecond)
		return nil
	}, velox.TxTimeout(20*time.Millisecond))
	if !errors.Is(err, velox.ErrTimeout) || !strings.HasPrefix(err.Error(), "velox: transaction: ") {
		t.Errorf("%v", err)
	}
	notCommitted(t, e)
}

// notCommitted fails t unless e's transaction ended with ROLLBACK.
func notCommitted(t *testing.T, e *velox.FakeExec) {
	t.Helper()
	for _, s := range e.Log {
		if s == "COMMIT" {
			t.Errorf("committed: %v", e.Log)
		}
	}
	if len(e.Log) == 0 || e.Log[len(e.Log)-1] != "ROLLBACK" {
		t.Errorf("log %v, want ROLLBACK last", e.Log)
	}
}

// TestTimedOutTxReportsNoRollbackError: once the transaction's deadline has
// passed, a rollback that fails (pgx closes the connection whose statement
// the deadline cut off) is not reported; before it, it is.
func TestTimedOutTxReportsNoRollbackError(t *testing.T) {
	conn := errors.New("conn closed")
	e := &velox.FakeExec{RollbackErr: conn}
	db := velox.OpenFake(velox.Postgres, e)
	err := db.Tx(ctx, func(_ context.Context, tx *velox.DB) error {
		e.Block = true
		_, err := tx.Query[testmodel.Invoice]().Limit(1).All(ctx)
		return err
	}, velox.TxTimeout(50*time.Millisecond))
	if !errors.Is(err, velox.ErrTimeout) || strings.Contains(err.Error(), "rollback") {
		t.Errorf("transaction: %v", err)
	}

	e = &velox.FakeExec{}
	db = velox.OpenFake(velox.Postgres, e)
	err = db.Tx(ctx, func(_ context.Context, tx *velox.DB) error {
		return tx.Tx(ctx, func(_ context.Context, nested *velox.DB) error {
			e.Block = true
			_, err := nested.Query[testmodel.Invoice]().Limit(1).All(ctx)
			e.Block, e.Err = false, conn // ROLLBACK TO SAVEPOINT fails
			return err
		})
	}, velox.TxTimeout(50*time.Millisecond))
	if !errors.Is(err, velox.ErrTimeout) || strings.Contains(err.Error(), "rollback") {
		t.Errorf("savepoint: %v", err)
	}

	e = &velox.FakeExec{RollbackErr: conn}
	db = velox.OpenFake(velox.Postgres, e)
	err = db.Tx(ctx, func(context.Context, *velox.DB) error { return errors.New("boom") })
	if !errors.Is(err, conn) || !strings.Contains(err.Error(), "velox: rollback: conn closed") {
		t.Errorf("in time: %v", err)
	}

	e = &velox.FakeExec{}
	db = velox.OpenFake(velox.Postgres, e)
	err = db.Tx(ctx, func(_ context.Context, tx *velox.DB) error {
		return tx.Tx(ctx, func(context.Context, *velox.DB) error {
			e.Err = conn
			return errors.New("boom")
		})
	}, velox.TxTimeout(time.Minute))
	if !errors.Is(err, conn) || !strings.Contains(err.Error(), "velox: rollback to savepoint: conn closed") {
		t.Errorf("savepoint in time: %v", err)
	}
}

// TestCallersDeadlineBoundsTheTx: a deadline on the transaction's context
// that is earlier than velox's bounds the statements inside it, whatever
// context they are given, and the transaction is not committed past it. It
// is the caller's, so the error is not ErrTimeout.
func TestCallersDeadlineBoundsTheTx(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e, velox.WithDeadlines(velox.Deadlines{MaxTx: time.Hour}))
	cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- db.Tx(cctx, func(_ context.Context, tx *velox.DB) error {
			e.Block = true
			_, err := tx.Query[testmodel.Invoice]().Limit(1).All(ctx) // ctx has no deadline
			return err
		})
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, velox.ErrTimeout) {
			t.Errorf("blocked statement: %v", err)
		}
		notCommitted(t, e)
	case <-time.After(2 * time.Second):
		t.Fatal("the statement inside the transaction ignored the caller's deadline")
	}
}

// TestTxPastCallersDeadlineIsNotCommitted: a transaction whose context ended,
// by the caller's deadline or cancellation, is rolled back and its error says
// which.
func TestTxPastCallersDeadlineIsNotCommitted(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	cctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	err := db.Tx(cctx, func(context.Context, *velox.DB) error {
		time.Sleep(60 * time.Millisecond)
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, velox.ErrTimeout) || !strings.HasPrefix(err.Error(), "velox: transaction: ") {
		t.Errorf("past the deadline: %v", err)
	}
	notCommitted(t, e)

	e = &velox.FakeExec{}
	db = velox.OpenFake(velox.Postgres, e)
	cctx, cancel = context.WithCancel(ctx)
	err = db.Tx(cctx, func(context.Context, *velox.DB) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || !strings.HasPrefix(err.Error(), "velox: transaction: ") {
		t.Errorf("canceled: %v", err)
	}
	notCommitted(t, e)
}

// TestSavepointGetsCallersDeadline: the caller's deadline on a transaction
// reaches the statements of a nested Tx.
func TestSavepointGetsCallersDeadline(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	want, _ := cctx.Deadline()
	var got time.Time
	err := db.Tx(cctx, func(_ context.Context, tx *velox.DB) error {
		return tx.Tx(ctx, func(_ context.Context, nested *velox.DB) error {
			_, err := nested.Query[testmodel.Invoice]().Limit(1).All(ctx) // ctx has no deadline
			got = e.Deadline
			return err
		})
	})
	if err != nil || !got.Equal(want) {
		t.Errorf("%v; savepoint statement's deadline %v, want the caller's %v", err, got, want)
	}
}

// TestRetriesStopAtTheTxDeadline is Review Focus 3.
func TestRetriesStopAtTheTxDeadline(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	runs := 0
	err := db.Tx(ctx, func(context.Context, *velox.DB) error {
		runs++
		time.Sleep(20 * time.Millisecond)
		return &pgconn.PgError{Code: "40001"}
	}, velox.TxRetrySerializable(1000), velox.TxTimeout(100*time.Millisecond))
	if !errors.Is(err, velox.ErrTimeout) || runs < 2 || runs > 10 {
		t.Errorf("%v after %d runs", err, runs)
	}
}

func TestTimeoutReplacesTheTxDefault(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, velox.WithDeadlines(velox.Deadlines{Tx: 10 * time.Millisecond, MaxTx: time.Hour}))
	err := db.Tx(ctx, func(context.Context, *velox.DB) error {
		time.Sleep(40 * time.Millisecond)
		return nil
	}, velox.TxTimeout(time.Minute))
	if err != nil {
		t.Errorf("%v", err)
	}
}

func TestStatementsInATxGetItsDeadline(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	var got time.Time
	err := db.Tx(ctx, func(_ context.Context, tx *velox.DB) error {
		return tx.Tx(ctx, func(_ context.Context, nested *velox.DB) error { // a savepoint inherits it too
			_, err := nested.Query[testmodel.Invoice]().Limit(1).All(ctx)
			got = e.Deadline // before RELEASE SAVEPOINT records its own
			return err
		})
	}, velox.TxTimeout(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	near(t, got, 3*time.Second)
}

// TestSavepointIsBoundedByTheTxDeadline: SAVEPOINT and RELEASE run within
// the transaction's deadline whatever context the nested Tx is given.
func TestSavepointIsBoundedByTheTxDeadline(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	var dl, savepoint, release time.Time
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		dl, _ = ctx.Deadline()
		err := tx.Tx(context.Background(), func(context.Context, *velox.DB) error {
			savepoint = e.Deadline
			return nil
		})
		release = e.Deadline
		return err
	}, velox.TxTimeout(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if dl.IsZero() || !savepoint.Equal(dl) || !release.Equal(dl) {
		t.Errorf("SAVEPOINT's deadline %v, RELEASE's %v, want the transaction's %v", savepoint, release, dl)
	}

	e = &velox.FakeExec{}
	db = velox.OpenFake(velox.Postgres, e)
	var nested error
	_ = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		e.Block = true
		nested = tx.Tx(context.Background(), func(context.Context, *velox.DB) error { return nil })
		return nested
	}, velox.TxTimeout(20*time.Millisecond))
	if !errors.Is(nested, velox.ErrTimeout) || !errors.Is(nested, context.DeadlineExceeded) || !strings.Contains(fmt.Sprint(nested), "velox: savepoint: ") {
		t.Errorf("a SAVEPOINT past the transaction's deadline: %v", nested)
	}
}
