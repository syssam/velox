package apitest

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

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
	sink := velox.RecordFunc(func(context.Context, velox.Change) error {
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
		`UPDATE "invoices" SET "total" = $1, "updated_at" = $2 WHERE "id" = $3 AND "id" IN ($4) RETURNING ` + allCols,
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
	var tx *velox.DB
	sink := velox.RecordFunc(func(ctx context.Context, c velox.Change) error {
		audit := testmodel.Invoice{OrgID: 2, Number: "audit", Data: []byte("a")}
		if err := tx.Insert(&audit).Exec(ctx); err != nil {
			return err
		}
		return boom
	})
	var werr error
	err := db.Record(sink).Tx(ctx, func(ctx context.Context, t *velox.DB) error {
		tx = t
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
