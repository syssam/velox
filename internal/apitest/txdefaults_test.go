package apitest

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/internal/fakeconn"
)

// TestTxDefaultsRetry: a DB's TxRetrySerializable default retries every Tx,
// and a call's TxRetrySerializable(0) overrides it.
func TestTxDefaultsRetry(t *testing.T) {
	serialization := &pgconn.PgError{Code: "40001"}
	e := &fakeconn.Exec{Errs: []error{serialization}}
	db := openFake(velox.Postgres, e, velox.WithTxDefaults(velox.TxRetrySerializable(2)))
	runs := 0
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { runs++; return touch(ctx, tx) })
	if err != nil || runs != 2 {
		t.Fatalf("default retry: %v, runs %d", err, runs)
	}
	if want := []string{"BEGIN", touchSQL, "ROLLBACK", "BEGIN", touchSQL, "COMMIT"}; !slices.Equal(e.Log, want) {
		t.Errorf("default retry:\n got %q\nwant %q", e.Log, want)
	}

	e = &fakeconn.Exec{Errs: []error{serialization}}
	db = openFake(velox.Postgres, e, velox.WithTxDefaults(velox.TxRetrySerializable(2)))
	runs = 0
	err = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error { runs++; return touch(ctx, tx) }, velox.TxRetrySerializable(0))
	if !errors.Is(err, serialization) || runs != 1 {
		t.Errorf("overridden: %v, runs %d", err, runs)
	}
}

// TestTxDefaultsNested: a savepoint does not retry on its own. Its failure
// fails the outermost Tx, whose default retry runs both fns again.
func TestTxDefaultsNested(t *testing.T) {
	serialization := &pgconn.PgError{Code: "40001"}
	e := &fakeconn.Exec{Errs: []error{nil, serialization}} // SAVEPOINT, then touch
	db := openFake(velox.Postgres, e, velox.WithTxDefaults(velox.TxRetrySerializable(3)))
	outer, inner := 0, 0
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		outer++
		return tx.Tx(ctx, func(ctx context.Context, sp *velox.DB) error { inner++; return touch(ctx, sp) })
	})
	if err != nil || outer != 2 || inner != 2 {
		t.Errorf("%v: outer runs %d, inner runs %d, want 2 and 2\n%q", err, outer, inner, e.Log)
	}
}

// TestTxDefaultsTimeoutAndIsolation: a default TxTimeout bounds fn's ctx, a
// call's TxTimeout replaces it, its TxTimeout(0) lifts it, and a default isolation reaches the
// transaction velox begins itself for a chunked insert.
func TestTxDefaultsTimeoutAndIsolation(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{}, velox.WithTxDefaults(velox.TxTimeout(time.Hour)))
	left := func(opts ...velox.TxOption) time.Duration {
		t.Helper()
		var d time.Duration
		if err := db.Tx(ctx, func(ctx context.Context, _ *velox.DB) error {
			dl, ok := ctx.Deadline()
			if !ok {
				return errors.New("no deadline")
			}
			d = time.Until(dl)
			return nil
		}, opts...); err != nil {
			t.Fatal(err)
		}
		return d
	}
	if d := left(); d < 59*time.Minute || d > time.Hour {
		t.Errorf("default timeout: %v left", d)
	}
	if d := left(velox.TxTimeout(time.Minute)); d < 59*time.Second || d > time.Minute {
		t.Errorf("call's timeout: %v left", d)
	}
	err := db.Tx(ctx, func(ctx context.Context, _ *velox.DB) error {
		if _, ok := ctx.Deadline(); ok {
			return errors.New("a deadline")
		}
		return nil
	}, velox.TxTimeout(0))
	if err != nil {
		t.Errorf("TxTimeout(0) lifts the default: %v", err)
	}

	ret := [][]any{
		{int64(1), "new", int64(1), "n"}, {int64(2), "new", int64(7), "n"},
		{int64(3), "new", int64(3), "n"}, {int64(4), "new", int64(7), "n"},
	}
	e := &fakeconn.Exec{Results: [][][]any{ret[:2], ret[2:]}}
	db = openFake(velox.Postgres, e, engine.WithMaxParams(6), velox.WithTxDefaults(velox.TxIsolation(sql.LevelSerializable)))
	xs := make([]dDef, 4)
	for i := range xs {
		xs[i].Name = "x"
		if i%2 == 0 {
			xs[i].Score = int64(i + 1)
		}
	}
	if err := db.InsertAll(xs).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.Log) == 0 || e.Log[0] != "BEGIN" || e.Isolation != sql.LevelSerializable {
		t.Errorf("velox's own transaction: isolation %v\n%q", e.Isolation, e.Log)
	}
}

func TestTxDefaultsZeroOptionPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("WithTxDefaults(TxOption{}) did not panic")
		}
	}()
	velox.WithTxDefaults(velox.TxOption{})
}
