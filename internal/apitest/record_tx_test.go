package apitest

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// TestRecordPinnedUpdateInTx: in the caller's transaction a recorded write
// runs under a savepoint that holds its statements and the sink's call.
func TestRecordPinnedUpdateInTx(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{{invoiceRow(1)}, {recNewRow(1, t0.Add(time.Hour))}}, Affected: 1}
	var at []int
	sink := velox.RecordFunc(func(context.Context, *velox.DB, []velox.Change) error {
		at = append(at, len(e.Log))
		return nil
	})
	err := openFake(velox.Postgres, e).Record(sink).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_, err := tx.Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.Total.Set(13.5)).Exec(ctx)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	wantLog(t, e, []string{
		"BEGIN",
		"SAVEPOINT velox_1",
		`SELECT ` + allCols + ` FROM "invoices" WHERE "id" = $1 ORDER BY "id" FOR UPDATE`,
		`UPDATE "invoices" SET "total" = $1, "updated_at" = $2 WHERE "id" = $3 AND "id" = ANY($4) RETURNING ` + allCols,
		"RELEASE SAVEPOINT velox_1",
		"COMMIT",
	})
	if !slices.Equal(at, []int{4}) {
		t.Errorf("the sink ran after %v statements, want once before RELEASE (4)", at)
	}
}

// TestRecordChunkFailureInTx: a recorded write sent in chunks, in the
// caller's transaction, whose second chunk fails rolls back to its
// savepoint: the first chunk does not stay applied.
func TestRecordChunkFailureInTx(t *testing.T) {
	boom := errors.New("boom")
	e := &fakeconn.Exec{
		Results:  [][][]any{{invoiceRow(1), invoiceRow(2)}},
		Affected: 1,
		Errs:     []error{nil, nil, nil, boom}, // SAVEPOINT, SELECT, UPDATE, UPDATE
	}
	var rec velox.Recorder
	rdb := openFake(velox.MySQL, e, engine.WithMaxParams(4)).Record(&rec)
	var werr error
	err := rdb.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_, werr = tx.Update[testmodel.Invoice]().Where(I.OrgID.EQ(1)).Set(I.Total.Add(1)).Exec(ctx)
		return nil // the caller commits anyway
	})
	if err != nil || !errors.Is(werr, boom) {
		t.Fatalf("Tx %v, the write's error %v", err, werr)
	}
	wantLog(t, e, []string{
		"BEGIN",
		"SAVEPOINT velox_1",
		"SELECT " + allColsMy + " FROM `invoices` WHERE `org_id` = ? ORDER BY `id` FOR UPDATE",
		"UPDATE `invoices` SET `total` = `total` + ?, `updated_at` = ? WHERE `org_id` = ? AND `id` IN (?)",
		"UPDATE `invoices` SET `total` = `total` + ?, `updated_at` = ? WHERE `org_id` = ? AND `id` IN (?)",
		"ROLLBACK TO SAVEPOINT velox_1",
		"RELEASE SAVEPOINT velox_1",
		"COMMIT",
	})
	if rec.Len() != 0 {
		t.Errorf("%d Changes", rec.Len())
	}
}

// TestRecordChunkFailureInTxSQLite: on a real database the same: the second
// chunk breaks the unique (org_id, number) index, and after the caller
// commits no row has changed.
func TestRecordChunkFailureInTxSQLite(t *testing.T) {
	db, _ := recInvoices(t, engine.WithMaxParams(4))
	var rec velox.Recorder
	var werr error
	err := db.Record(&rec).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_, werr = tx.Update[testmodel.Invoice]().Where(I.OrgID.EQ(1)).Set(I.Number.Set("X")).Exec(ctx)
		return nil
	})
	if err != nil || werr == nil || !strings.Contains(werr.Error(), "UNIQUE") {
		t.Fatalf("Tx %v, the write's error %v", err, werr)
	}
	got, err := db.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).AllowUnbounded().All(ctx)
	if err != nil || len(got) != 2 || got[0].Number != "N1" || got[1].Number != "N2" {
		t.Errorf("after the commit: %+v, %v", got, err)
	}
	if rec.Len() != 0 {
		t.Errorf("%d Changes", rec.Len())
	}
}

// TestRecordSinkErrorInTxUndoesSinkWrite: a sink's error in the caller's
// transaction rolls the write and the sink's own writes back to the
// savepoint, though the caller then commits.
func TestRecordSinkErrorInTxUndoesSinkWrite(t *testing.T) {
	db, _ := recInvoices(t)
	boom := errors.New("boom")
	sink := velox.RecordFunc(func(ctx context.Context, tx *velox.DB, _ []velox.Change) error {
		audit := testmodel.Invoice{OrgID: 2, Number: "audit", Data: []byte("a")}
		if err := tx.Insert(&audit).Exec(ctx); err != nil {
			return err
		}
		return boom
	})
	var werr error
	err := db.Record(sink).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_, werr = tx.Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.Total.Set(99)).Exec(ctx)
		return nil
	})
	if err != nil || !errors.Is(werr, boom) {
		t.Fatalf("Tx %v, the write's error %v", err, werr)
	}
	if got, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx); err != nil || got.Total != 10 {
		t.Errorf("the write: %+v, %v", got, err)
	}
	if n, err := db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(2)).Count(ctx); err != nil || n != 0 {
		t.Errorf("the sink's audit rows: %d, %v", n, err)
	}
}

// TestRecordCompositeKey: a composite key narrows the recorded write with
// one AND group per row, and each Change's Key holds both columns.
func TestRecordCompositeKey(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{{int64(1), int64(2), int64(5)}, {int64(1), int64(3), nil}},
		{{int64(1), int64(2), int64(9)}, {int64(1), int64(3), int64(9)}},
	}, Affected: 2}
	var rec velox.Recorder
	rdb := openFake(velox.Postgres, e).Record(&rec)
	if _, err := rdb.Update[wgPair]().Where(wgPairs.A.EQ(1)).Set(wgPairs.Price.Set(9)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	wantLog(t, e, []string{
		"BEGIN",
		`SELECT "a", "b", "price" FROM "pairs" WHERE "a" = $1 ORDER BY "a", "b" FOR UPDATE`,
		`UPDATE "pairs" SET "price" = $1 WHERE "a" = $2 AND (("a" = $3 AND "b" = $4) OR ("a" = $5 AND "b" = $6)) RETURNING "a", "b", "price"`,
		"COMMIT",
	})
	cs := slices.Collect(rec.All())
	if len(cs) != 2 || !reflect.DeepEqual(cs[0].Key, []any{int64(1), int64(2)}) || !reflect.DeepEqual(cs[1].Key, []any{int64(1), int64(3)}) {
		t.Fatalf("changes %+v", cs)
	}
	if f := cs[1].Diff(); len(f) != 1 || f[0].Name != "price" || f[0].Old != nil || f[0].New != wgCents(9) {
		t.Errorf("Diff %+v", f)
	}
}

// setTotal sets invoice id's total on db, a recorded write; it reports a
// failure with t.Error, as it may run on another goroutine.
func setTotal(ctx context.Context, t *testing.T, db *velox.DB, id int64, total float64) {
	t.Helper()
	if _, err := db.Update[testmodel.Invoice]().Where(I.ID.EQ(id)).Set(I.Total.Set(total)).Exec(ctx); err != nil {
		t.Error(err)
	}
}

// TestRecorderTxRollback: a Tx that rolls back leaves its Recorder without
// the Changes its writes recorded.
func TestRecorderTxRollback(t *testing.T) {
	db, _ := recInvoices(t)
	var rec velox.Recorder
	boom := errors.New("boom")
	err := db.Record(&rec).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		setTotal(ctx, t, tx, 1, 11)
		setTotal(ctx, t, tx, 2, 21)
		if rec.Len() != 2 {
			t.Errorf("inside the Tx: %d Changes, want 2", rec.Len())
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if rec.Len() != 0 {
		t.Errorf("after the rollback: %d Changes, want 0", rec.Len())
	}
}

// TestRecorderSavepointRollback: a nested Tx that fails drops its own
// Changes and keeps those recorded before it.
func TestRecorderSavepointRollback(t *testing.T) {
	db, _ := recInvoices(t)
	var rec velox.Recorder
	boom := errors.New("boom")
	err := db.Record(&rec).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		setTotal(ctx, t, tx, 1, 11)
		if err := tx.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			setTotal(ctx, t, tx, 2, 21)
			return boom
		}); !errors.Is(err, boom) {
			t.Errorf("the nested Tx: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got := slices.Collect(rec.All())
	if len(got) != 1 || got[0].Key != int64(1) {
		t.Errorf("after the savepoint's rollback: %+v, want the Change of invoice 1", got)
	}
}

// TestRecorderRetry: an attempt of TxRetryRolledBack that fails drops its
// Changes; the Recorder holds those of the attempt that committed.
func TestRecorderRetry(t *testing.T) {
	db, _ := recInvoices(t)
	var rec velox.Recorder
	attempts := 0
	err := db.Record(&rec).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		attempts++
		if attempts == 1 {
			setTotal(ctx, t, tx, 1, 11)
			setTotal(ctx, t, tx, 2, 21)
			return &pgconn.PgError{Code: "40001"} // a serialization failure
		}
		setTotal(ctx, t, tx, 1, 12)
		return nil
	}, velox.TxRetryRolledBack(2))
	if err != nil || attempts != 2 {
		t.Fatalf("%v, %d attempts", err, attempts)
	}
	got := slices.Collect(rec.All())
	if len(got) != 1 || got[0].Key != int64(1) || I.Total.New(got[0]) != 12 {
		t.Errorf("after the retry: %+v, want the second attempt's one Change", got)
	}
}

// TestRecorderPanicAndCommitFailure: a Tx whose fn panics, and one whose
// COMMIT fails, drop their Changes.
func TestRecorderPanicAndCommitFailure(t *testing.T) {
	db, _ := recInvoices(t)
	var rec velox.Recorder
	func() {
		defer func() { _ = recover() }()
		_ = db.Record(&rec).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			setTotal(ctx, t, tx, 1, 11)
			panic("boom")
		})
	}()
	if rec.Len() != 0 {
		t.Errorf("after a panic: %d Changes", rec.Len())
	}
	commit := errors.New("commit failed")
	e := &fakeconn.Exec{Results: [][][]any{{invoiceRow(1)}, {recNewRow(1, t0.Add(time.Hour))}}, Affected: 1, CommitErr: commit}
	err := openFake(velox.Postgres, e).Record(&rec).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_, err := tx.Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.Total.Set(13.5)).Exec(ctx)
		return err
	})
	if !errors.Is(err, commit) {
		t.Fatal(err)
	}
	if rec.Len() != 0 {
		t.Errorf("after a failed COMMIT: %d Changes", rec.Len())
	}
}

// TestRecordFuncSeesRollback: a RecordFunc is called for the changes of a
// savepoint that then rolls back; it sees changes as they happen (R5).
func TestRecordFuncSeesRollback(t *testing.T) {
	db, _ := recInvoices(t)
	var keys []any
	sink := velox.RecordFunc(func(_ context.Context, _ *velox.DB, cs []velox.Change) error {
		for _, c := range cs {
			keys = append(keys, c.Key)
		}
		return nil
	})
	err := db.Record(sink).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_ = tx.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			setTotal(ctx, t, tx, 2, 21)
			return errors.New("boom")
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(keys, []any{int64(2)}) {
		t.Errorf("the RecordFunc saw %v, want the rolled-back Change of invoice 2", keys)
	}
}

// TestRecorderSharedConcurrentTx: one Recorder shared by two transactions
// on two goroutines, their Changes interleaved: the one that rolls back
// drops only its own, and the other's survive.
func TestRecorderSharedConcurrentTx(t *testing.T) {
	dbA, _ := recInvoices(t)
	dbB, _ := recInvoices(t)
	var rec velox.Recorder
	aWrote, bWrote := make(chan struct{}), make(chan struct{})
	boom := errors.New("boom")
	errs := make(chan error, 2)
	go func() {
		errs <- dbA.Record(&rec).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			setTotal(ctx, t, tx, 1, 11)
			close(aWrote)
			<-bWrote
			setTotal(ctx, t, tx, 2, 21)
			return boom
		})
	}()
	go func() {
		errs <- dbB.Record(&rec).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			<-aWrote
			setTotal(ctx, t, tx, 1, 99)
			close(bWrote)
			return nil
		})
	}()
	for range 2 {
		if err := <-errs; err != nil && !errors.Is(err, boom) {
			t.Fatal(err)
		}
	}
	got := slices.Collect(rec.All())
	if len(got) != 1 || got[0].Key != int64(1) || I.Total.New(got[0]) != 99 {
		t.Errorf("after A's rollback: %+v, want B's one Change", got)
	}
}

// TestRecordOnTxHandle: Record on a transaction's handle replaces its sink
// for that handle. Writes through it go to the new sink, the outer sink
// keeps the writes made before, and a savepoint or the transaction that
// rolls back drops the new Recorder's rows, by id.
func TestRecordOnTxHandle(t *testing.T) {
	db, _ := recInvoices(t)
	boom := errors.New("boom")
	keys := func(r *velox.Recorder) []any {
		var ks []any
		for c := range r.All() {
			ks = append(ks, c.Key)
		}
		return ks
	}
	var outer, inner velox.Recorder
	err := db.Record(&outer).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		if _, err := tx.Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.Total.Set(11)).Exec(ctx); err != nil {
			return err
		}
		itx := tx.Record(&inner)
		if _, err := itx.Update[testmodel.Invoice]().Where(I.ID.EQ(2)).Set(I.Total.Set(22)).Exec(ctx); err != nil {
			return err
		}
		if err := itx.Tx(ctx, func(ctx context.Context, sp *velox.DB) error {
			if _, err := sp.Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.Total.Set(33)).Exec(ctx); err != nil {
				return err
			}
			return boom
		}); !errors.Is(err, boom) {
			t.Errorf("savepoint: %v, want boom", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := keys(&outer); !slices.Equal(got, []any{int64(1)}) {
		t.Errorf("outer Recorder: keys %v, want [1]", got)
	}
	if got := keys(&inner); !slices.Equal(got, []any{int64(2)}) {
		t.Errorf("inner Recorder: keys %v, want [2]: the savepoint's row is dropped", got)
	}
	for c := range inner.All() {
		if I.Total.Old(c) != 20 || I.Total.New(c) != 22 {
			t.Errorf("inner Change: total %v -> %v", I.Total.Old(c), I.Total.New(c))
		}
	}

	var rolled velox.Recorder
	err = db.Record(&outer).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		if _, err := tx.Record(&rolled).Update[testmodel.Invoice]().Where(I.ID.EQ(2)).Set(I.Total.Set(44)).Exec(ctx); err != nil {
			return err
		}
		if rolled.Len() != 1 {
			t.Errorf("before the rollback: %d Changes, want 1", rolled.Len())
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Tx: %v, want boom", err)
	}
	if rolled.Len() != 0 {
		t.Errorf("after the rollback: %d Changes, want 0", rolled.Len())
	}
}
