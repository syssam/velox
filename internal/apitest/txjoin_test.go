package apitest

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// countSamples is the number of samples db reads under ctx.
func countSamples(t *testing.T, ctx context.Context, db *velox.DB) int64 {
	t.Helper()
	n, err := db.Query[testmodel.Sample]().Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// TestBeginnerJoinsItsTx: under the context of a transaction db began, a
// statement on db runs in that transaction, on SQLite's one connection, and
// rolls back with it; db.Tx there is a savepoint, and under a savepoint's
// context db runs in the savepoint. Another DB (a WithoutPolicy copy, a
// recording copy, another Open) still fails with ErrOutsideTx, and outside
// any transaction nothing changes. An entity db reads in the transaction
// reads its edges on db once it has ended (ADR 0171).
func TestBeginnerJoinsItsTx(t *testing.T) {
	db := sourceDB(t)
	other := sourceDB(t)
	stop := errors.New("roll back")
	var read *testmodel.Sample
	err := db.Tx(ctx, func(ctx context.Context, _ *velox.DB) error {
		s := newSample("in tx")
		if err := db.Insert(&s).Exec(ctx); err != nil {
			t.Fatalf("insert on db in its transaction: %v", err)
		}
		if n := countSamples(t, ctx, db); n != 2 {
			t.Errorf("db in its transaction counts %d samples, want 2, its own insert included", n)
		}
		var err error
		if read, err = db.Query[testmodel.Sample]().Where(testmodel.Samples.ID.EQ(s.ID)).One(ctx); err != nil {
			t.Fatal(err)
		}
		wantLines(t, ctx, "an entity read in the joined transaction", read, 0)
		// A savepoint: its error undoes only its work.
		if err := db.Tx(ctx, func(ctx context.Context, _ *velox.DB) error {
			s := newSample("in savepoint")
			if err := db.Insert(&s).Exec(ctx); err != nil { // db under the savepoint's ctx
				t.Fatalf("insert on db in a savepoint: %v", err)
			}
			if n := countSamples(t, ctx, db); n != 3 {
				t.Errorf("db in the savepoint counts %d samples, want 3", n)
			}
			return stop
		}); !errors.Is(err, stop) {
			t.Fatalf("savepoint: %v", err)
		}
		if n := countSamples(t, ctx, db); n != 2 {
			t.Errorf("after the savepoint rolled back db counts %d samples, want 2", n)
		}
		for name, d := range map[string]*velox.DB{
			"a WithoutPolicy copy": db.WithoutPolicy("another DB"),
			"a recording copy":     db.Record(&velox.Recorder{}),
			"another Open":         other,
		} {
			if _, err := d.Query[testmodel.Sample]().Count(ctx); !errors.Is(err, velox.ErrOutsideTx) {
				t.Errorf("%s in the transaction: %v, want ErrOutsideTx", name, err)
			}
			if err := d.Tx(ctx, func(context.Context, *velox.DB) error { return nil }); !errors.Is(err, velox.ErrOutsideTx) {
				t.Errorf("Tx of %s in the transaction: %v, want ErrOutsideTx", name, err)
			}
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}
	if n := countSamples(t, ctx, db); n != 1 {
		t.Errorf("after the rollback db counts %d samples, want 1: the insert on db rolled back", n)
	}
	// Outside any transaction db runs as before, and a Tx is a transaction.
	if err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		return tx.Tx(ctx, func(context.Context, *velox.DB) error { return nil })
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := read.QueryLines().AllowUnbounded().All(ctx); err != nil {
		t.Errorf("an entity read in the ended transaction: QueryLines on db: %v", err)
	}
}
