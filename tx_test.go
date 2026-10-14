package velox_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func touch(tx *velox.DB) error {
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
	err := db.Tx(ctx, func(tx *velox.DB) error {
		if err := touch(tx); err != nil {
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
	_ = db.Tx(ctx, func(*velox.DB) error { panic("oops") })
	t.Error("Tx did not re-raise the panic")
}

// TestTxRollsBackWhenTheContextIsCanceled is the other half of Review Focus 2:
// the rollback runs, and runs with a context that is not canceled.
func TestTxRollsBackWhenTheContextIsCanceled(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	c, cancel := context.WithCancel(ctx)
	err := db.Tx(c, func(*velox.DB) error {
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
	err := db.Tx(c, func(tx *velox.DB) error {
		return tx.Tx(c, func(*velox.DB) error {
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
		_ = db.Tx(ctx, func(*velox.DB) error { runtime.Goexit(); return nil })
		t.Error("Tx returned after Goexit")
	})
	if want := []string{"BEGIN", "ROLLBACK"}; !reflect.DeepEqual(e.Log, want) {
		t.Errorf("got %q, want %q", e.Log, want)
	}

	e = &velox.FakeExec{}
	db = velox.OpenFake(velox.Postgres, e)
	goexit(func() {
		_ = db.Tx(ctx, func(tx *velox.DB) error {
			return tx.Tx(ctx, func(*velox.DB) error { runtime.Goexit(); return nil })
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
	_ = db.Tx(ctx, func(tx *velox.DB) error {
		return tx.Tx(ctx, func(*velox.DB) error { panic("oops") })
	})
	t.Error("Tx did not re-raise the panic")
}

func TestNestedTxUsesSavepoints(t *testing.T) {
	boom := errors.New("boom")
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	err := db.Tx(ctx, func(tx *velox.DB) error {
		if err := tx.Tx(ctx, touch); err != nil {
			return err
		}
		if err := tx.Tx(ctx, func(*velox.DB) error { return boom }); !errors.Is(err, boom) {
			t.Errorf("inner error %v", err)
		}
		return nil
	})
	want := []string{"BEGIN", "SAVEPOINT velox_1", touchSQL, "RELEASE SAVEPOINT velox_1",
		"SAVEPOINT velox_1", "ROLLBACK TO SAVEPOINT velox_1", "RELEASE SAVEPOINT velox_1", "COMMIT"}
	if err != nil || !reflect.DeepEqual(e.Log, want) {
		t.Errorf("%v\n got %q\nwant %q", err, e.Log, want)
	}
}

func TestNestedTxDepth(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	err := db.Tx(ctx, func(a *velox.DB) error {
		return a.Tx(ctx, func(b *velox.DB) error {
			return b.Tx(ctx, func(*velox.DB) error { return nil })
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
	err := db.Tx(ctx, func(tx *velox.DB) error { runs++; return touch(tx) }, velox.RetrySerializable(2))
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
		t.Errorf("without RetrySerializable: %v", err)
	}
	e = &velox.FakeExec{Errs: []error{errors.New("not retryable")}}
	db = velox.OpenFake(velox.Postgres, e)
	runs = 0
	if err := db.Tx(ctx, func(tx *velox.DB) error { runs++; return touch(tx) }, velox.RetrySerializable(3)); err == nil || runs != 1 {
		t.Errorf("a non-retryable error: %v, runs %d", err, runs)
	}
}

func TestTxOptionsAndInTx(t *testing.T) {
	var inTx []bool
	spy := func(_ context.Context, st *velox.Statement) error { inTx = append(inTx, st.InTx); return nil }
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(spy))
	if err := db.Tx(ctx, touch, velox.Isolation(sql.LevelSerializable), velox.ReadOnly()); err != nil {
		t.Fatal(err)
	}
	if err := touch(db); err != nil {
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
		err := db.Tx(ctx, func(tx *velox.DB) error { runs++; return touch(tx) }, velox.RetrySerializable(tc.n))
		if !errors.Is(err, serialization) || runs != tc.runs {
			t.Errorf("RetrySerializable(%d): runs %d, want %d; err %v", tc.n, runs, tc.runs, err)
		}
	}
}

func TestFailingCommit(t *testing.T) {
	serialization := &pgconn.PgError{Code: "40001"}
	e := &velox.FakeExec{CommitErr: serialization}
	db := velox.OpenFake(velox.Postgres, e)
	runs := 0
	err := db.Tx(ctx, func(tx *velox.DB) error {
		runs++
		if runs == 2 {
			e.CommitErr = nil
		}
		return touch(tx)
	}, velox.RetrySerializable(1))
	want := []string{"BEGIN", touchSQL, "COMMIT", "BEGIN", touchSQL, "COMMIT"}
	if err != nil || runs != 2 || !reflect.DeepEqual(e.Log, want) {
		t.Errorf("%v, runs %d\n got %q\nwant %q", err, runs, e.Log, want)
	}

	boom := errors.New("boom")
	e = &velox.FakeExec{CommitErr: boom}
	db = velox.OpenFake(velox.Postgres, e)
	err = db.Tx(ctx, touch, velox.RetrySerializable(2))
	want = []string{"BEGIN", touchSQL, "COMMIT"}
	if !errors.Is(err, boom) || !strings.HasPrefix(err.Error(), "velox: commit: ") || !reflect.DeepEqual(e.Log, want) {
		t.Errorf("%v\n got %q\nwant %q", err, e.Log, want)
	}
}
