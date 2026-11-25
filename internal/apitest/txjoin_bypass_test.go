package apitest

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// TestBypassBeginnerRowsReadInItsTx: an entity that a WithoutPolicy DB
// inserts, saves or reads under the context of the transaction it began
// reads its edges in that transaction, under the policies, as one the
// transaction's own handle reads does; before, it read them on the DB
// without the reason, which is not the beginner, and failed with
// ErrOutsideTx. Once the transaction has ended it reads them on the DB.
func TestBypassBeginnerRowsReadInItsTx(t *testing.T) {
	db := sourceDB(t).WithoutPolicy("test")
	var kept *testmodel.Sample
	err := db.Tx(ctx, func(ctx context.Context, _ *velox.DB) error {
		s := newSample("in tx")
		if err := db.Insert(&s).Exec(ctx); err != nil {
			return err
		}
		wantLines(t, ctx, "an inserted entity", &s, 0)
		s.Name = "saved"
		if err := db.Save(&s, testmodel.Samples.Name).Exec(ctx); err != nil {
			return err
		}
		wantLines(t, ctx, "a saved entity", &s, 0)
		read, err := db.Query[testmodel.Sample]().Where(testmodel.Samples.Name.EQ("a")).One(ctx)
		if err != nil {
			return err
		}
		wantLines(t, ctx, "a read entity", read, 2)
		for x, err := range db.Query[testmodel.Sample]().Where(testmodel.Samples.Name.EQ("a")).Limit(1).Iter(ctx) {
			if err != nil {
				return err
			}
			wantLines(t, ctx, "an iterated entity", &x, 2)
		}
		kept = read
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	wantLines(t, ctx, "an entity after its transaction", kept, 2)
	if _, err := kept.QueryLines().AllowUnbounded().All(ctx); errors.Is(err, velox.ErrOutsideTx) {
		t.Fatal(err)
	}
}

// TestJoinedWriteIsInTx: an Around hook sees InTx on every statement that
// runs in a transaction its beginner joined under the context, a write
// (insert, save, update, delete) as well as a read; before, a write's
// statement, built on the DB, said it was not.
func TestJoinedWriteIsInTx(t *testing.T) {
	var outside []string
	db := sourceDB(t, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		if !st.InTx {
			outside = append(outside, st.Op.String())
		}
		return next(ctx)
	}))
	outside = nil
	err := db.Tx(ctx, func(ctx context.Context, _ *velox.DB) error {
		s := newSample("in tx")
		if err := db.Insert(&s).Exec(ctx); err != nil {
			return err
		}
		s.Name = "saved"
		if err := db.Save(&s, testmodel.Samples.Name).Exec(ctx); err != nil {
			return err
		}
		if _, err := db.Update[testmodel.Sample]().Where(testmodel.Samples.ID.EQ(s.ID)).Set(testmodel.Samples.Name.Set("updated")).Exec(ctx); err != nil {
			return err
		}
		countSamples(t, ctx, db)
		_, err := db.Delete[testmodel.Sample]().Where(testmodel.Samples.ID.EQ(s.ID)).Exec(ctx)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(outside) != 0 {
		t.Errorf("statements in the joined transaction without InTx: %q", outside)
	}
}

// TestNoReasonCopyFailsInTheBeginnersTx: a WithoutPolicy copy with no reason
// fails its statements and its Tx under the context of a transaction its
// base began, as it does outside one; before, it joined the transaction and
// ran on the transaction's handle, which has no misuse.
func TestNoReasonCopyFailsInTheBeginnersTx(t *testing.T) {
	db := sourceDB(t)
	bad := db.WithoutPolicy("")
	err := db.Tx(ctx, func(ctx context.Context, _ *velox.DB) error {
		if _, err := bad.Query[testmodel.Sample]().Count(ctx); err == nil {
			t.Error("Count on a copy with no reason in the transaction: nil error")
		}
		if err := bad.Tx(ctx, func(context.Context, *velox.DB) error { return nil }); err == nil {
			t.Error("Tx on a copy with no reason in the transaction: nil error")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
