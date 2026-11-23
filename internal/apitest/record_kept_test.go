package apitest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/internal/testmodel/kind"
)

// keepAll is a Rewrite hook that narrows every DELETE to no row. (An Around
// hook cannot skip a statement: one that returns without calling next fails
// the write.)
var keepAll = velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
	if st.Op == velox.OpDelete {
		st.Where = append(st.Where, velox.Predicate(I.ID.EQ(-1)))
	}
	return nil
})

// TestRecordDeleteKeptByHook: a recorded delete reports only the rows the
// database deleted. On PostgreSQL and SQLite the narrowed DELETE returns
// their keys, and a row it does not return (a Rewrite hook narrowed the
// statement, a trigger kept the row) is not reported. MySQL returns no keys:
// a count of rows short of the locked read's fails the write, rolled back.
func TestRecordDeleteKeptByHook(t *testing.T) {
	for _, d := range []velox.Dialect{velox.Postgres, velox.SQLite, velox.MySQL} {
		t.Run(d.String()+"/narrowed", func(t *testing.T) {
			e := &fakeconn.Exec{Results: [][][]any{{invoiceRow(1), invoiceRow(2)}}}
			var rec velox.Recorder
			n, err := openFake(d, e, keepAll).Record(&rec).Delete[testmodel.Invoice]().Where(I.OrgID.EQ(1)).Exec(ctx)
			if d == velox.MySQL {
				if err == nil || !strings.Contains(err.Error(), "a recorded delete of 2 locked rows deleted 0 by MySQL's count") || e.Log[len(e.Log)-1] != "ROLLBACK" {
					t.Fatalf("Delete: %d, %v; sent %q", n, err, e.Log)
				}
			} else if err != nil || n != 0 || e.Log[len(e.Log)-1] != "COMMIT" {
				t.Fatalf("Delete: %d, %v; sent %q", n, err, e.Log)
			}
			if rec.Len() != 0 {
				t.Errorf("%d Changes for a delete a hook narrowed to no row", rec.Len())
			}
		})
		t.Run(d.String()+"/one kept", func(t *testing.T) {
			// The database deletes 2 alone: it returns 2's key, or counts 1.
			e := &fakeconn.Exec{Results: [][][]any{{invoiceRow(1), invoiceRow(2)}, {{int64(2)}}}, Affected: 1}
			var rec velox.Recorder
			n, err := openFake(d, e).Record(&rec).Delete[testmodel.Invoice]().Where(I.OrgID.EQ(1)).Exec(ctx)
			if d == velox.MySQL {
				if err == nil || !strings.Contains(err.Error(), "deleted 1 by MySQL's count") || e.Log[len(e.Log)-1] != "ROLLBACK" {
					t.Fatalf("Delete: %d, %v; sent %q", n, err, e.Log)
				}
				if rec.Len() != 0 {
					t.Errorf("%d Changes", rec.Len())
				}
				return
			}
			cs := slices.Collect(rec.All())
			if err != nil || n != 1 || len(cs) != 1 || cs[0].Op != velox.OpDelete || cs[0].Key != int64(2) || I.Number.Old(cs[0]) != "INV-2" {
				t.Fatalf("Delete: %d, %v; Changes %+v", n, err, cs)
			}
		})
	}
}

// TestRecordDeleteTriggerKeepsRow: on SQLite a BEFORE DELETE trigger that
// ignores the delete of one row keeps it, and only the row deleted is
// reported.
func TestRecordDeleteTriggerKeepsRow(t *testing.T) {
	db, invs := recInvoices(t)
	if _, err := velox.Pool(db).Exec(`CREATE TRIGGER keep_first BEFORE DELETE ON invoices WHEN old.number = 'N1' BEGIN SELECT RAISE(IGNORE); END`); err != nil {
		t.Fatal(err)
	}
	var rec velox.Recorder
	n, err := db.Record(&rec).Delete[testmodel.Invoice]().Where(I.OrgID.EQ(1)).Exec(ctx)
	cs := slices.Collect(rec.All())
	if err != nil || n != 1 || len(cs) != 1 || cs[0].Key != invs[1].ID || I.Number.Old(cs[0]) != "N2" {
		t.Fatalf("Delete: %d, %v; Changes %+v", n, err, cs)
	}
	if left := invoiceCount(t, db); left != 1 {
		t.Errorf("%d invoices left, want the one the trigger kept", left)
	}
}

// TestRecordHookedWrites: a Rewrite hook that narrows a recorded update or
// delete to one of the rows its locked read found, under an Around hook that
// passes each statement on, gets that one row reported; the other, which
// the write left alone, is not.
func TestRecordHookedWrites(t *testing.T) {
	var arounds int
	var keep int64
	narrow := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		if st.Op == velox.OpUpdate || st.Op == velox.OpDelete {
			st.Where = append(st.Where, velox.Predicate(I.ID.NEQ(keep)))
		}
		return nil
	})
	pass := velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		arounds++
		return next(ctx)
	})
	db, invs := recInvoices(t, narrow, pass)
	keep = invs[0].ID
	var rec velox.Recorder
	rdb := db.Record(&rec)
	n, err := rdb.Update[testmodel.Invoice]().Where(I.OrgID.EQ(1)).Set(I.Total.Add(1)).Exec(ctx)
	cs := slices.Collect(rec.All())
	if err != nil || n != 1 || len(cs) != 1 || cs[0].Op != velox.OpUpdate || cs[0].Key != invs[1].ID || I.Total.Old(cs[0]) != 20 || I.Total.New(cs[0]) != 21 {
		t.Fatalf("Update: %d, %v; Changes %+v", n, err, cs)
	}
	rec.Reset()
	n, err = rdb.Delete[testmodel.Invoice]().Where(I.OrgID.EQ(1)).Exec(ctx)
	cs = slices.Collect(rec.All())
	if err != nil || n != 1 || len(cs) != 1 || cs[0].Op != velox.OpDelete || cs[0].Key != invs[1].ID {
		t.Fatalf("Delete: %d, %v; Changes %+v", n, err, cs)
	}
	if arounds == 0 {
		t.Error("the Around hook saw no statement")
	}
	if got, err := db.Query[testmodel.Invoice]().AllowUnbounded().All(ctx); err != nil || len(got) != 1 || got[0].ID != keep || got[0].Total != 10 {
		t.Errorf("left: %+v, %v", got, err)
	}
}

// TestRecordKeyStoredOtherwise: an update that assigns the key reports its
// row although the database returns the key in another form than the value
// assigned (a truncated time, a padded CHAR): the chunk's one row takes it.
// Rows that cannot be placed fail the write, rolled back, rather than go
// unreported.
func TestRecordKeyStoredOtherwise(t *testing.T) {
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL} {
		t.Run(d.String(), func(t *testing.T) {
			// The update assigns 9; the database holds 10.
			e := &fakeconn.Exec{Results: [][][]any{{invoiceRow(1)}, {invoiceRow(10)}}, Affected: 1}
			var rec velox.Recorder
			_, err := openFake(d, e).Record(&rec).Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.ID.Set(9)).Exec(ctx)
			cs := slices.Collect(rec.All())
			if err != nil || len(cs) != 1 || cs[0].Key != int64(10) || I.ID.Old(cs[0]) != 1 {
				t.Fatalf("Update: %v; Changes %+v", err, cs)
			}
		})
	}
	t.Run("unplaceable", func(t *testing.T) {
		e := &fakeconn.Exec{Results: [][][]any{{invoiceRow(1), invoiceRow(2)}, {invoiceRow(10), invoiceRow(11)}}, Affected: 2}
		var rec velox.Recorder
		_, err := openFake(velox.Postgres, e).Record(&rec).Update[testmodel.Invoice]().Where(I.OrgID.EQ(1)).Set(I.ID.Set(9)).Exec(ctx)
		if err == nil || !strings.Contains(err.Error(), "found 2 rows whose key matches none it was sent for") || e.Log[len(e.Log)-1] != "ROLLBACK" {
			t.Fatalf("Update: %v; sent %q", err, e.Log)
		}
		if rec.Len() != 0 {
			t.Errorf("%d Changes", rec.Len())
		}
	})
}

// TestDeleteReturningOnlyWhenRecorded: a DELETE renders RETURNING only on
// the recording path; a Returning a Rewrite hook sets on any other delete
// is ignored, as it always was, and on a recorded delete it is refused.
func TestDeleteReturningOnlyWhenRecorded(t *testing.T) {
	ret := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		if st.Op == velox.OpDelete {
			st.Returning = []int{0, 1}
		}
		return nil
	})
	for _, d := range []velox.Dialect{velox.Postgres, velox.SQLite} {
		e := &fakeconn.Exec{Affected: 1}
		if _, err := openFake(d, e, ret).Delete[testmodel.Invoice]().Where(I.ID.EQ(1)).Exec(ctx); err != nil || len(e.Log) != 1 || strings.Contains(e.Log[0], "RETURNING") {
			t.Errorf("%s: %v, sent %q", d, err, e.Log)
		}
		e = &fakeconn.Exec{Results: [][][]any{{invoiceRow(1)}}, Affected: 1}
		var rec velox.Recorder
		_, err := openFake(d, e, ret).Record(&rec).Delete[testmodel.Invoice]().Where(I.ID.EQ(1)).Exec(ctx)
		if err == nil || !strings.Contains(err.Error(), "a Rewrite hook changed the RETURNING of a recorded write") || rec.Len() != 0 {
			t.Errorf("%s recorded: %v, %d Changes", d, err, rec.Len())
		}
	}
}

// TestRecordNamedTypeGetters: the handles' Old and New return the field's
// own type, a named string or integer, with the value the row holds.
func TestRecordNamedTypeGetters(t *testing.T) {
	S := testmodel.Samples
	before := sampleRow(1, nil)
	after := sampleRow(1, nil)
	after[2], after[3], after[4] = "t", "s", int64(5)
	e := &fakeconn.Exec{Results: [][][]any{{before}, {after}}, Affected: 1}
	var rec velox.Recorder
	if _, err := openFake(velox.Postgres, e).Record(&rec).Update[testmodel.Sample]().Where(S.ID.EQ(1)).Set(S.Status.Set("t"), S.Prev.Set("s"), S.Price.Set(5)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	cs := slices.Collect(rec.All())
	if len(cs) != 1 {
		t.Fatalf("%d Changes", len(cs))
	}
	// Typed constants: the comparisons compile only for the fields' types.
	status, prev, price := S.Status.Old(cs[0]), S.Prev.New(cs[0]), S.Price.New(cs[0])
	if status != kind.Status("s") || S.Status.New(cs[0]) != kind.Status("t") || prev != kind.Status("s") || S.Prev.Old(cs[0]) != kind.Status("") ||
		price != kind.Cents(5) || S.Price.Old(cs[0]) != kind.Cents(0) {
		t.Errorf("status %q -> %q, prev %q -> %q, price %d -> %d", status, S.Status.New(cs[0]), S.Prev.Old(cs[0]), prev, S.Price.Old(cs[0]), price)
	}
}

// TestRecordBidiSinkErrorInTx: in the caller's transaction a sink's error on
// a re-point of a bidirectional one-to-one key rolls back to the write's
// savepoint, the sink's own write with it, and the transaction goes on.
func TestRecordBidiSinkErrorInTx(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1, Results: [][][]any{
		{spouse(5, 1)}, {spouse(5, 2)},
		{spouse(1, 5)}, {spouse(1, 0)},
		{spouse(2, 0)}, {spouse(2, 5)},
	}}
	boom := errors.New("boom")
	sink := velox.RecordFunc(func(ctx context.Context, tx *velox.DB, _ []velox.Change) error {
		if _, err := tx.Delete[testmodel.Invoice]().Where(I.ID.EQ(99)).Exec(ctx); err != nil { // the sink's write
			return err
		}
		return boom
	})
	err := openFake(velox.Postgres, e).Record(sink).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		if err := tx.Save(&bspouse{ID: 5, SpouseID: new(int64(2))}, bspouses.SpouseID).Exec(ctx); !errors.Is(err, boom) {
			t.Errorf("Save: %v, want the sink's error", err)
		}
		_, err := tx.Delete[testmodel.Invoice]().Where(I.ID.EQ(7)).Exec(ctx) // the transaction goes on
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// The sink's DELETE, then the rollback to the write's savepoint; the
	// transaction's next write runs and it commits.
	sp := `SELECT "id", "spouse_id" FROM "bspouses" WHERE `
	wantLog(t, e, []string{
		"BEGIN",
		"SAVEPOINT velox_1",
		sp + `"id" = $1 ORDER BY "id" FOR UPDATE`,
		`UPDATE "bspouses" SET "spouse_id" = $1 WHERE "id" = $2 AND "id" = ANY($3) RETURNING "id", "spouse_id"`,
		sp + `"spouse_id" = $1 ORDER BY "id" FOR UPDATE`,
		`UPDATE "bspouses" SET "spouse_id" = $1 WHERE "spouse_id" = $2 AND "id" = ANY($3) RETURNING "id", "spouse_id"`,
		sp + `"id" = $1 AND "spouse_id" IS NULL ORDER BY "id" FOR UPDATE`,
		`UPDATE "bspouses" SET "spouse_id" = $1 WHERE "id" = $2 AND "spouse_id" IS NULL AND "id" = ANY($3) RETURNING "id", "spouse_id"`,
		`DELETE FROM "invoices" WHERE "id" = $1`,
		"ROLLBACK TO SAVEPOINT velox_1",
		"RELEASE SAVEPOINT velox_1",
		"SAVEPOINT velox_2",
		`SELECT ` + allCols + ` FROM "invoices" WHERE "id" = $1 ORDER BY "id" FOR UPDATE`,
		"RELEASE SAVEPOINT velox_2",
		"COMMIT",
	})
}

// TestRecordUseTxKeepsRolledBack: velox never sees the end of a transaction
// the caller began (UseTx), so a Recorder used through it holds every
// Change delivered, those of a transaction that then rolls back too.
func TestRecordUseTxKeepsRolledBack(t *testing.T) {
	db, invs := recInvoices(t)
	tx, err := velox.Pool(db).BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var rec velox.Recorder
	vtx, err := velox.UseTx(db.Record(&rec), tx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vtx.Update[testmodel.Invoice]().Where(I.ID.EQ(invs[0].ID)).Set(I.Total.Set(99)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if rec.Len() != 1 {
		t.Errorf("%d Changes after the rollback, want the 1 delivered", rec.Len())
	}
	if got, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(invs[0].ID)).One(ctx); err != nil || got.Total != 10 {
		t.Errorf("after the rollback: %+v, %v", got, err)
	}
}
