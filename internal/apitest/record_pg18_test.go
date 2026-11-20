package apitest

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// showVersion is what a recording handle asks PostgreSQL before its first
// recorded write.
const showVersion = "SHOW server_version_num"

// openFake18 is openFake on PostgreSQL whose server is version (as
// server_version_num): e answers SHOW server_version_num with it, ahead of
// its Results. It records as the server's version says.
func openFake18(e *fakeconn.Exec, version int, opts ...velox.Option) *velox.DB {
	e.Answer = func(q string, _ []any) ([][]any, bool) {
		if q == showVersion {
			return [][]any{{strconv.Itoa(version)}}, true
		}
		return nil, false
	}
	return openPolicied(velox.Postgres, e, opts...).WithoutPolicy("velox tests")
}

// oldNew is cols, a quoted column list, as RETURNING old."c", …, new."c", ….
func oldNew(cols string) string { return olds(cols) + ", " + news(cols) }

// olds is cols as old."c", ….
func olds(cols string) string { return prefixCols("old.", cols) }

// news is cols as new."c", ….
func news(cols string) string { return prefixCols("new.", cols) }

func prefixCols(p, cols string) string {
	parts := strings.Split(cols, ", ")
	for i := range parts {
		parts[i] = p + parts[i]
	}
	return strings.Join(parts, ", ")
}

// pair is a row of RETURNING old.…, new.…: before, then after.
func pair(before, after []any) []any { return append(slices.Clone(before), after...) }

// TestRecordPG18UpdateSQL: on PostgreSQL 18 a recorded update that pins one
// row is one statement, sent as it is, RETURNING the row before and after it:
// no locked read, no BEGIN, and the sink runs after it. On 17 it is Task 3's
// locked read, narrowed write and RETURNING, in a transaction. With
// WithoutReturningOld the version is not asked, and the write is 17's.
func TestRecordPG18UpdateSQL(t *testing.T) {
	t1 := t0.Add(time.Hour)
	upd := `UPDATE "invoices" SET "total" = "total" + $1, "updated_at" = $2 WHERE "id" = $3 AND "org_id" = $4`
	locked := []string{
		"BEGIN",
		`SELECT ` + allCols + ` FROM "invoices" WHERE "id" = $1 AND "org_id" = $2 ORDER BY "id" FOR UPDATE`,
		`UPDATE "invoices" SET "total" = "total" + $1, "updated_at" = $2 WHERE "id" = $3 AND "org_id" = $4 AND "id" IN ($5) RETURNING ` + allCols,
		"COMMIT",
	}
	for _, c := range []struct {
		name    string
		version int
		opts    []velox.Option
		results [][][]any
		want    []string
		sinkAt  int // statements sent when the sink ran
	}{
		{"18", 180000, nil, [][][]any{{pair(invoiceRow(1), recNewRow(1, t1))}},
			[]string{showVersion, upd + ` RETURNING ` + oldNew(allCols)}, 2},
		{"17", 170000, nil, [][][]any{{invoiceRow(1)}, {recNewRow(1, t1)}},
			append([]string{showVersion}, locked...), 5},
		{"WithoutReturningOld", 180000, []velox.Option{engine.WithoutReturningOld()}, [][][]any{{invoiceRow(1)}, {recNewRow(1, t1)}},
			locked, 4},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := &fakeconn.Exec{Results: c.results, Affected: 1}
			var at []int
			var cs []velox.Change
			sink := velox.RecordFunc(func(_ context.Context, ch velox.Change) error {
				at = append(at, len(e.Log))
				cs = append(cs, ch)
				return nil
			})
			n, err := openFake18(e, c.version, c.opts...).Record(sink).Update[testmodel.Invoice]().
				Where(I.ID.EQ(1), I.OrgID.EQ(1)).Set(I.Total.Add(1)).Exec(ctx)
			if err != nil || n != 1 {
				t.Fatalf("Update: %d, %v", n, err)
			}
			wantLog(t, e, c.want)
			if !slices.Equal(at, []int{c.sinkAt}) {
				t.Errorf("the sink ran after %v statements, want once after %d", at, c.sinkAt)
			}
			if len(cs) != 1 || cs[0].Op != velox.OpUpdate || cs[0].Key != int64(1) {
				t.Fatalf("Changes %+v", cs)
			}
			want := []velox.FieldChange{{Name: "updated_at", Old: t0, New: t1}, {Name: "total", Old: 12.5, New: 13.5}}
			if got := cs[0].Diff(); !reflect.DeepEqual(got, want) {
				t.Errorf("Diff %+v, want %+v", got, want)
			}
		})
	}
}

// TestRecordPG18UpdateOne: on PostgreSQL 18 UpdateQuery.One, in its own
// transaction, sends the update once, RETURNING old and new, then reads the
// row; the sink waits for the COMMIT.
func TestRecordPG18UpdateOne(t *testing.T) {
	cur := recNewRow(1, t0.Add(time.Hour))
	e := &fakeconn.Exec{Results: [][][]any{{pair(invoiceRow(1), cur)}, {cur}}}
	var rec velox.Recorder
	var at []int
	sink := velox.RecordFunc(func(ctx context.Context, ch velox.Change) error {
		at = append(at, len(e.Log))
		return rec.Record(ctx, ch)
	})
	got, err := openFake18(e, 180000).Record(sink).Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.Total.Set(13.5)).One(ctx)
	if err != nil || got.Total != 13.5 {
		t.Fatalf("One: %+v, %v", got, err)
	}
	wantLog(t, e, []string{
		"BEGIN",
		showVersion,
		`UPDATE "invoices" SET "total" = $1, "updated_at" = $2 WHERE "id" = $3 RETURNING ` + oldNew(allCols),
		`SELECT ` + allCols + ` FROM "invoices" WHERE "id" = $1 LIMIT $2`,
		"COMMIT",
	})
	if rec.Len() != 1 || !slices.Equal(at, []int{len(e.Log)}) {
		t.Errorf("%d Changes, sink after %v of %d statements", rec.Len(), at, len(e.Log))
	}
}

// TestRecordPG18BulkUpdate: on PostgreSQL 18 an update that does not pin one
// row runs in a transaction of velox's own, one statement; the rows it
// returns, in the order the database found them, are reported in key order,
// and a row it left as it was is not reported. In the caller's transaction
// it runs in a savepoint.
func TestRecordPG18BulkUpdate(t *testing.T) {
	t1 := t0.Add(time.Hour)
	upd := `UPDATE "invoices" SET "total" = "total" + $1, "updated_at" = $2 WHERE "total" < $3 AND "org_id" = $4 RETURNING ` + oldNew(allCols)
	same := invoiceRow(3)
	results := func() [][][]any {
		return [][][]any{{pair(invoiceRow(2), recNewRow(2, t1)), pair(same, same), pair(invoiceRow(1), recNewRow(1, t1))}}
	}
	check := func(t *testing.T, rec *velox.Recorder) {
		t.Helper()
		var keys []any
		for ch := range rec.All() {
			keys = append(keys, ch.Key)
			if ch.Op != velox.OpUpdate || I.Total.Old(ch) != 12.5 || I.Total.New(ch) != 13.5 {
				t.Errorf("change %+v", ch)
			}
		}
		if !reflect.DeepEqual(keys, []any{int64(1), int64(2)}) {
			t.Errorf("keys %v, want 1 and 2, in key order", keys)
		}
	}
	t.Run("own tx", func(t *testing.T) {
		e := &fakeconn.Exec{Results: results()}
		var rec velox.Recorder
		n, err := openFake18(e, 180000).Record(&rec).Update[testmodel.Invoice]().Where(I.Total.LT(100), I.OrgID.EQ(1)).Set(I.Total.Add(1)).Exec(ctx)
		if err != nil || n != 3 {
			t.Fatalf("Update: %d, %v", n, err)
		}
		wantLog(t, e, []string{showVersion, "BEGIN", upd, "COMMIT"})
		check(t, &rec)
	})
	t.Run("caller's tx", func(t *testing.T) {
		e := &fakeconn.Exec{Results: results()}
		var rec velox.Recorder
		err := openFake18(e, 180000).Record(&rec).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			_, err := tx.Update[testmodel.Invoice]().Where(I.Total.LT(100), I.OrgID.EQ(1)).Set(I.Total.Add(1)).Exec(ctx)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		wantLog(t, e, []string{"BEGIN", showVersion, "SAVEPOINT velox_1", upd, "RELEASE SAVEPOINT velox_1", "COMMIT"})
		check(t, &rec)
	})
}

// TestRecordPG18TooManyRows: on PostgreSQL 18 an update that returns more
// rows than WithMaxRows fails with ErrTooManyRows, naming WithMaxRows, and
// its transaction rolls back: nothing is kept or reported.
func TestRecordPG18TooManyRows(t *testing.T) {
	t1 := t0.Add(time.Hour)
	e := &fakeconn.Exec{Results: [][][]any{{
		pair(invoiceRow(1), recNewRow(1, t1)), pair(invoiceRow(2), recNewRow(2, t1)), pair(invoiceRow(3), recNewRow(3, t1)),
	}}}
	var rec velox.Recorder
	_, err := openFake18(e, 180000, velox.WithMaxRows(2)).Record(&rec).Update[testmodel.Invoice]().Where(I.OrgID.EQ(1)).Set(I.Total.Add(1)).Exec(ctx)
	want := "velox: testmodel.Invoice: a recorded update wrote 3 rows, more than 2, the cap WithMaxRows sets on the rows it records; the write is rolled back: read more rows than the cap"
	if !errors.Is(err, velox.ErrTooManyRows) || err.Error() != want {
		t.Fatalf("got %v, want %s", err, want)
	}
	wantLog(t, e, []string{
		showVersion,
		"BEGIN",
		`UPDATE "invoices" SET "total" = "total" + $1, "updated_at" = $2 WHERE "org_id" = $3 RETURNING ` + oldNew(allCols),
		"ROLLBACK",
	})
	if rec.Len() != 0 {
		t.Errorf("%d Changes", rec.Len())
	}
}

// TestRecordPG18Delete: on PostgreSQL 18 a recorded delete is one statement,
// RETURNING the rows before it; its Changes have no new values. On 17 it is
// Task 3's.
func TestRecordPG18Delete(t *testing.T) {
	for _, c := range []struct {
		version int
		results [][][]any
		want    []string
	}{{180000, [][][]any{{invoiceRow(2), invoiceRow(1)}}, []string{
		showVersion,
		"BEGIN",
		`DELETE FROM "invoices" WHERE "org_id" = $1 RETURNING ` + olds(allCols),
		"COMMIT",
	}}, {170000, [][][]any{{invoiceRow(1), invoiceRow(2)}}, []string{
		showVersion,
		"BEGIN",
		`SELECT ` + allCols + ` FROM "invoices" WHERE "org_id" = $1 ORDER BY "id" FOR UPDATE`,
		`DELETE FROM "invoices" WHERE "org_id" = $1 AND "id" IN ($2, $3)`,
		"COMMIT",
	}}} {
		t.Run(strconv.Itoa(c.version), func(t *testing.T) {
			e := &fakeconn.Exec{Results: c.results, Affected: 2}
			var rec velox.Recorder
			n, err := openFake18(e, c.version).Record(&rec).Delete[testmodel.Invoice]().Where(I.OrgID.EQ(1)).Exec(ctx)
			if err != nil || n != 2 {
				t.Fatalf("Delete: %d, %v", n, err)
			}
			wantLog(t, e, c.want)
			cs := slices.Collect(rec.All())
			if len(cs) != 2 {
				t.Fatalf("%d Changes, want 2", len(cs))
			}
			for i, ch := range cs {
				if ch.Op != velox.OpDelete || ch.Key != int64(i+1) || ch.NewRow() != nil || I.Total.Old(ch) != 12.5 {
					t.Errorf("change %d: %+v", i, ch)
				}
			}
		})
	}
	t.Run("pinned", func(t *testing.T) {
		e := &fakeconn.Exec{Results: [][][]any{{invoiceRow(1)}}}
		var rec velox.Recorder
		if _, err := openFake18(e, 180000).Record(&rec).Delete[testmodel.Invoice]().Where(I.ID.EQ(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		wantLog(t, e, []string{showVersion, `DELETE FROM "invoices" WHERE "id" = $1 RETURNING ` + olds(allCols)})
		if rec.Len() != 1 {
			t.Errorf("%d Changes, want 1", rec.Len())
		}
	})
}

// nullTicket is a tickets row of RETURNING old.… for a row the upsert
// inserted: every column NULL.
var nullTicket = []any{nil, nil, nil, nil, nil, nil, nil, nil}

// TestRecordPG18Upsert: on PostgreSQL 18 a recorded upsert is one INSERT …
// ON CONFLICT, RETURNING every column before and after it, with no read
// first: a row whose old key is NULL is an insert, any other an update with
// the old values the conflict found. The caller's entities get the keys as
// without recording. A row DoNothing kept is not returned, nor reported. On
// 17 it is Task 4's locked read by the conflict columns.
func TestRecordPG18Upsert(t *testing.T) {
	ins := `INSERT INTO "tickets" (` + ticketInsertCols + `) VALUES ($1, $2, $3, $4, $5, $6), ($7, $8, $9, $10, $11, $12) ON CONFLICT ("org_id", "title") `
	for _, c := range []struct {
		version int
		results [][][]any
		want    []string
	}{{180000, [][][]any{{
		pair(recTicket(1, "bob", "a", false), recTicket(1, "bob", "a", true)),
		pair(nullTicket, recTicket(2, "ann", "b", true)),
	}}, []string{
		showVersion,
		ins + `DO UPDATE SET "done" = EXCLUDED."done" RETURNING ` + oldNew(ticketCols),
	}}, {170000, [][][]any{
		{recTicket(1, "bob", "a", false)},
		{recTicket(1, "bob", "a", true), recTicket(2, "ann", "b", true)},
	}, []string{
		showVersion,
		"BEGIN",
		`SELECT ` + ticketCols + ` FROM "tickets" WHERE ("org_id", "title") IN (($1, $2), ($3, $4)) FOR UPDATE`,
		ins + `DO UPDATE SET "done" = EXCLUDED."done" RETURNING ` + ticketCols,
		"COMMIT",
	}}} {
		t.Run(strconv.Itoa(c.version), func(t *testing.T) {
			e := &fakeconn.Exec{Results: c.results}
			var rec velox.Recorder
			xs := []testmodel.Ticket{{OrgID: 7, Owner: "ann", Title: "a", Done: true}, {OrgID: 7, Owner: "ann", Title: "b", Done: true}}
			if err := openFake18(e, c.version).Record(&rec).InsertAll(xs).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7)); err != nil {
				t.Fatal(err)
			}
			wantLog(t, e, c.want)
			cs := slices.Collect(rec.All())
			if len(cs) != 2 {
				t.Fatalf("%d Changes, want 2: %+v", len(cs), cs)
			}
			up, in := cs[0], cs[1]
			if up.Op != velox.OpUpdate || up.Key != int64(1) || Tk.Done.Old(up) || !Tk.Done.New(up) || Tk.Owner.New(up) != "bob" || !up.Changed(Tk.Done) || up.Changed(Tk.Owner) {
				t.Errorf("update: %+v, done %v -> %v, owner %q", up, Tk.Done.Old(up), Tk.Done.New(up), Tk.Owner.New(up))
			}
			if in.Op != velox.OpInsert || in.Key != int64(2) || in.OldRow() != nil || Tk.Title.New(in) != "b" {
				t.Errorf("insert: %+v", in)
			}
			if xs[0].ID != 1 || xs[1].ID != 2 {
				t.Errorf("the entities' keys %d, %d: want what RETURNING gave, as without recording", xs[0].ID, xs[1].ID)
			}
		})
	}
	t.Run("DoNothing", func(t *testing.T) {
		e := &fakeconn.Exec{Results: [][][]any{{pair(nullTicket, recTicket(2, "ann", "b", true))}}}
		var rec velox.Recorder
		xs := []testmodel.Ticket{{OrgID: 7, Owner: "ann", Title: "a", Done: true}, {OrgID: 7, Owner: "ann", Title: "b", Done: true}}
		if err := openFake18(e, 180000).Record(&rec).InsertAll(xs).OnConflict(Tk.OrgID, Tk.Title).DoNothing().Exec(as("ann", 7)); err != nil {
			t.Fatal(err)
		}
		wantLog(t, e, []string{showVersion, ins + `DO NOTHING RETURNING ` + oldNew(ticketCols)})
		cs := slices.Collect(rec.All())
		if len(cs) != 1 || cs[0].Op != velox.OpInsert || cs[0].Key != int64(2) {
			t.Fatalf("Changes %+v, want the insert of b", cs)
		}
	})
	t.Run("chunks", func(t *testing.T) {
		// A statement per row, under WithMaxParams: each one statement, all
		// in a transaction of velox's own.
		e := &fakeconn.Exec{Results: [][][]any{
			{pair(recTicket(1, "bob", "a", false), recTicket(1, "bob", "a", true))},
			{pair(nullTicket, recTicket(2, "ann", "b", true))},
		}}
		var rec velox.Recorder
		xs := []testmodel.Ticket{{OrgID: 7, Owner: "ann", Title: "a", Done: true}, {OrgID: 7, Owner: "ann", Title: "b", Done: true}}
		if err := openFake18(e, 180000, engine.WithMaxParams(6)).Record(&rec).InsertAll(xs).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7)); err != nil {
			t.Fatal(err)
		}
		one := `INSERT INTO "tickets" (` + ticketInsertCols + `) VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT ("org_id", "title") DO UPDATE SET "done" = EXCLUDED."done" RETURNING ` + oldNew(ticketCols)
		wantLog(t, e, []string{showVersion, "BEGIN", one, one, "COMMIT"})
		cs := slices.Collect(rec.All())
		if len(cs) != 2 || cs[0].Op != velox.OpUpdate || cs[1].Op != velox.OpInsert || xs[0].ID != 1 || xs[1].ID != 2 {
			t.Fatalf("Changes %+v, keys %d %d", cs, xs[0].ID, xs[1].ID)
		}
	})
	t.Run("single Insert", func(t *testing.T) {
		e := &fakeconn.Exec{Results: [][][]any{{pair(recTicket(1, "bob", "a", false), recTicket(1, "bob", "a", true))}}}
		var rec velox.Recorder
		tk := testmodel.Ticket{OrgID: 7, Owner: "ann", Title: "a", Done: true}
		if err := openFake18(e, 180000).Record(&rec).Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7)); err != nil {
			t.Fatal(err)
		}
		wantLog(t, e, []string{showVersion,
			`INSERT INTO "tickets" (` + ticketInsertCols + `) VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT ("org_id", "title") DO UPDATE SET "done" = EXCLUDED."done" RETURNING ` + oldNew(ticketCols)})
		cs := slices.Collect(rec.All())
		if len(cs) != 1 || cs[0].Op != velox.OpUpdate || tk.ID != 1 {
			t.Fatalf("Changes %+v, key %d", cs, tk.ID)
		}
	})
}

// TestRecordVersionReadOnce: a pool asks the server its version once, at
// its first recorded write; a handle that does not record never asks. A
// read that fails is not kept: that write takes the locked read, and the
// next asks again.
func TestRecordVersionReadOnce(t *testing.T) {
	t1 := t0.Add(time.Hour)
	shows := func(e *fakeconn.Exec) int {
		n := 0
		for _, s := range e.Log {
			if s == showVersion {
				n++
			}
		}
		return n
	}
	e := &fakeconn.Exec{Affected: 1, Results: [][][]any{
		{pair(invoiceRow(1), recNewRow(1, t1))},
		{pair(invoiceRow(1), recNewRow(1, t1))},
	}}
	db := openFake18(e, 180000)
	if _, err := db.Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.Total.Add(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if n := shows(e); n != 0 {
		t.Fatalf("a handle that does not record asked the version %d times", n)
	}
	var rec velox.Recorder
	for range 2 {
		if _, err := db.Record(&rec).Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.Total.Add(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := shows(e); n != 1 {
		t.Errorf("two recorded writes asked the version %d times, want 1:\n%s", n, strings.Join(e.Log, "\n"))
	}
	if rec.Len() != 2 {
		t.Errorf("%d Changes, want 2", rec.Len())
	}

	// The first read fails: its write takes the locked read; the second
	// asks again, and gets 18.
	boom := errors.New("boom")
	e = &fakeconn.Exec{Affected: 1, Errs: []error{boom}, Results: [][][]any{
		{invoiceRow(1)}, {recNewRow(1, t1)},
		{pair(invoiceRow(1), recNewRow(1, t1))},
	}}
	rdb := openFake18(e, 180000).Record(&rec)
	for range 2 {
		if _, err := rdb.Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.Total.Add(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	upd := `UPDATE "invoices" SET "total" = "total" + $1, "updated_at" = $2 WHERE "id" = $3`
	wantLog(t, e, []string{
		showVersion,
		"BEGIN",
		`SELECT ` + allCols + ` FROM "invoices" WHERE "id" = $1 ORDER BY "id" FOR UPDATE`,
		upd + ` AND "id" IN ($4) RETURNING ` + allCols,
		"COMMIT",
		showVersion,
		upd + ` RETURNING ` + oldNew(allCols),
	})
}

// TestRecordPG18SoftDelete: on PostgreSQL 18 softdelete's update takes the
// one-statement path, and reports a delete, Soft, with both rows.
func TestRecordPG18SoftDelete(t *testing.T) {
	const cols = `"id", "org_id", "owner_id", "title", "deleted_at"`
	D := fixture.SDocs
	e := &fakeconn.Exec{Results: [][][]any{{pair([]any{int64(2), int64(1), int64(1), "b", nil}, []any{int64(2), int64(1), int64(1), "b", t0})}}}
	var rec velox.Recorder
	n, err := openFake18(e, 180000).Record(&rec).Delete[fixture.SDoc]().Where(D.ID.EQ(2)).Exec(ctx)
	if err != nil || n != 1 {
		t.Fatalf("Delete: %d, %v", n, err)
	}
	wantLog(t, e, []string{
		showVersion,
		`UPDATE "s_docs" SET "deleted_at" = $1 WHERE "id" = $2 AND "deleted_at" IS NULL RETURNING ` + oldNew(cols),
	})
	cs := slices.Collect(rec.All())
	if len(cs) != 1 || cs[0].Op != velox.OpDelete || !cs[0].Soft || cs[0].Key != int64(2) {
		t.Fatalf("Changes %+v, want one soft delete of 2", cs)
	}
	if old, cur, ok := velox.RowsOf[fixture.SDoc](cs[0]); !ok || old.DeletedAt != nil || cur == nil || cur.DeletedAt == nil {
		t.Errorf("RowsOf: %+v %+v %v", old, cur, ok)
	}
}

// TestRecordPG18BidiRepoint: on PostgreSQL 18 each statement of a
// bidirectional one-to-one re-point is one statement, RETURNING old and
// new, and every row whose key changed is reported once.
func TestRecordPG18BidiRepoint(t *testing.T) {
	const cols = `"id", "spouse_id"`
	e := &fakeconn.Exec{Results: [][][]any{
		{pair(spouse(5, 1), spouse(5, 2))},
		{pair(spouse(1, 5), spouse(1, 0))},
		{pair(spouse(2, 0), spouse(2, 5))},
	}}
	var rec velox.Recorder
	if err := openFake18(e, 180000).Record(&rec).Save(&bspouse{ID: 5, SpouseID: new(int64(2))}, bspouses.SpouseID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	wantLog(t, e, []string{
		"BEGIN",
		showVersion,
		`UPDATE "bspouses" SET "spouse_id" = $1 WHERE "id" = $2 RETURNING ` + oldNew(cols),
		`UPDATE "bspouses" SET "spouse_id" = $1 WHERE "spouse_id" = $2 RETURNING ` + oldNew(cols),
		`UPDATE "bspouses" SET "spouse_id" = $1 WHERE "id" = $2 AND "spouse_id" IS NULL RETURNING ` + oldNew(cols),
		"COMMIT",
	})
	if got, want := spouses(&rec), []string{"update 5 1->2", "update 1 5->0", "update 2 0->5"}; !slices.Equal(got, want) {
		t.Errorf("Changes %q, want %q", got, want)
	}
}
