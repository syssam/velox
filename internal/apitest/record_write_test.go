package apitest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// allColsMy is allCols quoted for MySQL.
var allColsMy = strings.ReplaceAll(allCols, `"`, "`")

// recNewRow is invoiceRow(id) after Total.Add(1) at t1.
func recNewRow(id int64, at time.Time) []any {
	r := invoiceRow(id)
	r[2], r[5] = at, 13.5
	return r
}

// wantLog fails t when e sent other statements than want.
func wantLog(t *testing.T, e *fakeconn.Exec, want []string) {
	t.Helper()
	if !slices.Equal(e.Log, want) {
		t.Errorf("statements:\n%s\nwant:\n%s", strings.Join(e.Log, "\n"), strings.Join(want, "\n"))
	}
}

// TestRecordUpdateSQL: a recorded bulk update reads every column of the rows
// it will change, locked but on SQLite, in key order, sends the update
// narrowed to their keys with RETURNING of every column (on MySQL, a SELECT
// by key after it), all in one transaction, and reports one Change per row.
func TestRecordUpdateSQL(t *testing.T) {
	t1 := t0.Add(time.Hour)
	for _, c := range []struct {
		d    velox.Dialect
		want []string
	}{{velox.Postgres, []string{
		"BEGIN",
		`SELECT ` + allCols + ` FROM "invoices" WHERE "total" < $1 AND "org_id" = $2 ORDER BY "id" FOR UPDATE`,
		`UPDATE "invoices" SET "total" = "total" + $1, "updated_at" = $2 WHERE "total" < $3 AND "org_id" = $4 AND "id" IN ($5, $6) RETURNING ` + allCols,
		"COMMIT",
	}}, {velox.SQLite, []string{
		"BEGIN",
		`SELECT ` + allCols + ` FROM "invoices" WHERE "total" < ? AND "org_id" = ? ORDER BY "id"`,
		`UPDATE "invoices" SET "total" = "total" + ?, "updated_at" = ? WHERE "total" < ? AND "org_id" = ? AND "id" IN (?, ?) RETURNING ` + allCols,
		"COMMIT",
	}}, {velox.MySQL, []string{
		"BEGIN",
		"SELECT " + allColsMy + " FROM `invoices` WHERE `total` < ? AND `org_id` = ? ORDER BY `id` FOR UPDATE",
		"UPDATE `invoices` SET `total` = `total` + ?, `updated_at` = ? WHERE `total` < ? AND `org_id` = ? AND `id` IN (?, ?)",
		"SELECT " + allColsMy + " FROM `invoices` WHERE `id` IN (?, ?)",
		"COMMIT",
	}}} {
		t.Run(c.d.String(), func(t *testing.T) {
			e := &fakeconn.Exec{Results: [][][]any{
				{invoiceRow(1), invoiceRow(2)},
				{recNewRow(1, t1), recNewRow(2, t1)},
			}, Affected: 2}
			var rec velox.Recorder
			rdb := openFake(c.d, e).Record(&rec)
			n, err := rdb.Update[testmodel.Invoice]().Where(I.Total.LT(100), I.OrgID.EQ(1)).Set(I.Total.Add(1)).Exec(ctx)
			if err != nil || n != 2 {
				t.Fatalf("Update: %d, %v", n, err)
			}
			wantLog(t, e, c.want)
			cs := slices.Collect(rec.All())
			if len(cs) != 2 {
				t.Fatalf("%d Changes, want 2", len(cs))
			}
			for i, ch := range cs {
				id := int64(i + 1)
				if ch.Entity != "Invoice" || ch.Op != velox.OpUpdate || ch.Key != id || ch.Soft {
					t.Errorf("change %d: %+v", i, ch)
				}
				got := ch.Diff()
				want := []velox.FieldChange{{Name: "total", Old: 12.5, New: 13.5}} // not updated_at, an UpdateDefault column
				if !reflect.DeepEqual(got, want) {
					t.Errorf("change %d: Diff %+v, want %+v", i, got, want)
				}
				if I.Number.Old(ch) != fmt.Sprintf("INV-%d", id) || I.Total.Old(ch) != 12.5 || I.Total.New(ch) != 13.5 {
					t.Errorf("change %d: number %q, total %v -> %v", i, I.Number.Old(ch), I.Total.Old(ch), I.Total.New(ch))
				}
				if !ch.Changed(I.Total) || ch.Changed(I.Number) || !ch.Changed(I.Number, I.UpdatedAt) {
					t.Errorf("change %d: Changed", i)
				}
				if old, cur, ok := velox.RowsOf[testmodel.Invoice](ch); !ok || old.ID != id || cur.Total != 13.5 || string(old.Data) != "d" {
					t.Errorf("change %d: RowsOf %+v %+v %v", i, old, cur, ok)
				}
			}
		})
	}
}

// TestRecordDeleteSQL: a recorded delete reads every column of its rows,
// locked, then deletes them by key, RETURNING the keys it deleted but on
// MySQL; its Changes have no new values.
func TestRecordDeleteSQL(t *testing.T) {
	for _, c := range []struct {
		d    velox.Dialect
		want []string
	}{{velox.Postgres, []string{
		"BEGIN",
		`SELECT ` + allCols + ` FROM "invoices" WHERE "org_id" = $1 ORDER BY "id" FOR UPDATE`,
		`DELETE FROM "invoices" WHERE "org_id" = $1 AND "id" IN ($2, $3) RETURNING "id"`,
		"COMMIT",
	}}, {velox.SQLite, []string{
		"BEGIN",
		`SELECT ` + allCols + ` FROM "invoices" WHERE "org_id" = ? ORDER BY "id"`,
		`DELETE FROM "invoices" WHERE "org_id" = ? AND "id" IN (?, ?) RETURNING "id"`,
		"COMMIT",
	}}, {velox.MySQL, []string{
		"BEGIN",
		"SELECT " + allColsMy + " FROM `invoices` WHERE `org_id` = ? ORDER BY `id` FOR UPDATE",
		"DELETE FROM `invoices` WHERE `org_id` = ? AND `id` IN (?, ?)",
		"COMMIT",
	}}} {
		t.Run(c.d.String(), func(t *testing.T) {
			e := &fakeconn.Exec{Results: [][][]any{{invoiceRow(1), invoiceRow(2)}, {{int64(1)}, {int64(2)}}}, Affected: 2}
			var rec velox.Recorder
			rdb := openFake(c.d, e).Record(&rec)
			n, err := rdb.Delete[testmodel.Invoice]().Where(I.OrgID.EQ(1)).Exec(ctx)
			if err != nil || n != 2 {
				t.Fatalf("Delete: %d, %v", n, err)
			}
			wantLog(t, e, c.want)
			cs := slices.Collect(rec.All())
			if len(cs) != 2 {
				t.Fatalf("%d Changes, want 2", len(cs))
			}
			for i, ch := range cs {
				id := int64(i + 1)
				if ch.Op != velox.OpDelete || ch.Key != id || ch.NewRow() != nil || ch.Diff() != nil || I.ID.New(ch) != 0 {
					t.Errorf("change %d: %+v new %v", i, ch, ch.NewRow())
				}
				if old, ok := ch.OldRow().(*testmodel.Invoice); !ok || old.ID != id || I.ID.Old(ch) != id || I.Note.Old(ch) != "" {
					t.Errorf("change %d: OldRow %+v", i, ch.OldRow())
				}
			}
		})
	}
}

// TestRecordPinnedUpdateOutsideTx: an update pinned to one row by its key,
// outside a transaction, still runs in one of velox's own, so that the
// locked read and the update are one; the sink runs before its COMMIT.
func TestRecordPinnedUpdateOutsideTx(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{{invoiceRow(1)}, {recNewRow(1, t0.Add(time.Hour))}}, Affected: 1}
	var at []int
	sink := velox.RecordFunc(func(context.Context, *velox.DB, []velox.Change) error {
		at = append(at, len(e.Log))
		return nil
	})
	rdb := openFake(velox.Postgres, e).Record(sink)
	if _, err := rdb.Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.Total.Set(13.5)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	wantLog(t, e, []string{
		"BEGIN",
		`SELECT ` + allCols + ` FROM "invoices" WHERE "id" = $1 ORDER BY "id" FOR UPDATE`,
		`UPDATE "invoices" SET "total" = $1, "updated_at" = $2 WHERE "id" = $3 AND "id" IN ($4) RETURNING ` + allCols,
		"COMMIT",
	})
	if !slices.Equal(at, []int{3}) {
		t.Errorf("the sink ran after %v statements, want once before COMMIT (3)", at)
	}
}

// TestRecordUpdateOne: UpdateQuery.One on a recording handle returns the row
// and records one Change, sending the UPDATE once; inside its own
// transaction the sink runs before the COMMIT.
func TestRecordUpdateOne(t *testing.T) {
	for _, c := range []struct {
		d    velox.Dialect
		want []string
	}{{velox.Postgres, []string{
		"BEGIN",
		`SELECT ` + allCols + ` FROM "invoices" WHERE "id" = $1 ORDER BY "id" FOR UPDATE`,
		`UPDATE "invoices" SET "total" = $1, "updated_at" = $2 WHERE "id" = $3 AND "id" IN ($4) RETURNING ` + allCols,
		`SELECT ` + allCols + ` FROM "invoices" WHERE "id" = $1 LIMIT $2`,
		"COMMIT",
	}}, {velox.SQLite, []string{
		"BEGIN",
		`SELECT ` + allCols + ` FROM "invoices" WHERE "id" = ? ORDER BY "id"`,
		`UPDATE "invoices" SET "total" = ?, "updated_at" = ? WHERE "id" = ? AND "id" IN (?) RETURNING ` + allCols,
		`SELECT ` + allCols + ` FROM "invoices" WHERE "id" = ? LIMIT 2`,
		"COMMIT",
	}}, {velox.MySQL, []string{
		"BEGIN",
		"SELECT " + allColsMy + " FROM `invoices` WHERE `id` = ? ORDER BY `id` FOR UPDATE",
		"UPDATE `invoices` SET `total` = ?, `updated_at` = ? WHERE `id` = ? AND `id` IN (?)",
		"SELECT " + allColsMy + " FROM `invoices` WHERE `id` IN (?)",
		"SELECT " + allColsMy + " FROM `invoices` WHERE `id` = ? LIMIT ?",
		"COMMIT",
	}}} {
		t.Run(c.d.String(), func(t *testing.T) {
			cur := recNewRow(1, t0.Add(time.Hour))
			e := &fakeconn.Exec{Results: [][][]any{{invoiceRow(1)}, {cur}, {cur}}, Affected: 1}
			var rec velox.Recorder
			var at []int
			sink := velox.RecordFunc(func(ctx context.Context, tx *velox.DB, chs []velox.Change) error {
				at = append(at, len(e.Log))
				return rec.Record(ctx, tx, chs)
			})
			rdb := openFake(c.d, e).Record(sink)
			got, err := rdb.Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.Total.Set(13.5)).One(ctx)
			if err != nil || got.Total != 13.5 {
				t.Fatalf("One: %+v, %v", got, err)
			}
			wantLog(t, e, c.want)
			if rec.Len() != 1 || !slices.Equal(at, []int{len(e.Log) - 1}) {
				t.Errorf("%d Changes, sink after %v of %d statements", rec.Len(), at, len(e.Log))
			}
		})
	}

	// Not pinned by key: One reads the key, then the recorded update by it.
	var rec velox.Recorder
	sdb, _ := recInvoices(t)
	got, err := sdb.Record(&rec).Update[testmodel.Invoice]().Where(I.Number.EQ("N2")).Set(I.Total.Set(21)).One(ctx)
	if err != nil || got.ID != 2 || got.Total != 21 {
		t.Fatalf("One by number: %+v, %v", got, err)
	}
	if cs := slices.Collect(rec.All()); len(cs) != 1 || cs[0].Key != int64(2) {
		t.Errorf("One by number: changes %+v", cs)
	}
}

// TestRecordJSONValues: a JSON column's values are the decoded field values.
func TestRecordJSONValues(t *testing.T) {
	cur := jRow(1)
	cur[2] = []byte(`["a","b"]`)
	e := &fakeconn.Exec{Results: [][][]any{{jRow(1)}, {cur}}, Affected: 1}
	var rec velox.Recorder
	rdb := openFake(velox.Postgres, e).Record(&rec)
	J := fixture.JDocs
	if _, err := rdb.Update[fixture.JDoc]().Where(J.ID.EQ(1)).Set(J.Tags.Set([]string{"a", "b"})).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	cs := slices.Collect(rec.All())
	if len(cs) != 1 {
		t.Fatalf("%d Changes", len(cs))
	}
	want := []velox.FieldChange{{Name: "tags", Old: []string{"a"}, New: []string{"a", "b"}}}
	if got := cs[0].Diff(); !reflect.DeepEqual(got, want) {
		t.Errorf("Diff %#v, want %#v", got, want)
	}
	c := cs[0]
	if !reflect.DeepEqual(J.Attrs.Old(c), map[string]any{"k": "v"}) || J.Shape.Old(c) != (fixture.JShape{W: 1, H: 2}) || J.Nums.Old(c) != nil ||
		!reflect.DeepEqual(J.Tags.New(c), []string{"a", "b"}) {
		t.Errorf("values: attrs %#v, shape %#v, nums %#v, tags %#v", J.Attrs.Old(c), J.Shape.Old(c), J.Nums.Old(c), J.Tags.New(c))
	}
}

// TestRecordKeyChange: an update that assigns the key reports the row by
// its new key, and its old values hold the old one.
func TestRecordKeyChange(t *testing.T) {
	db, _ := recInvoices(t)
	var rec velox.Recorder
	if _, err := db.Record(&rec).Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.ID.Set(9)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	cs := slices.Collect(rec.All())
	if len(cs) != 1 || cs[0].Key != int64(9) || I.ID.Old(cs[0]) != 1 || I.ID.New(cs[0]) != 9 {
		t.Fatalf("changes %+v", cs)
	}
}

// TestRecordUnchangedRowsAreSkipped: an update that leaves every column as
// it was reports no Change.
func TestRecordUnchangedRowsAreSkipped(t *testing.T) {
	db, _ := recInvoices(t)
	var rec velox.Recorder
	rdb := db.Record(&rec, velox.RecordKeep(I.UpdatedAt))
	one, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx) // as stored
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rdb.Update[testmodel.Invoice]().Where(I.ID.EQ(one.ID)).Set(I.Total.Set(one.Total), I.UpdatedAt.Set(one.UpdatedAt)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.Len() != 0 {
		t.Errorf("an update to the current values: %d Changes", rec.Len())
	}
}

// TestUpdateDefaultIgnored: a column an UpdateDefault fills (updated_at) is
// left out of the comparison by default: an update that only bumps it
// reports nothing, and Diff leaves it out, while the getters and the rows
// still hold it. RecordKeep takes it back in.
func TestUpdateDefaultIgnored(t *testing.T) {
	db, _ := recInvoices(t)
	var rec velox.Recorder
	rdb := db.Record(&rec)
	one, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rdb.Update[testmodel.Invoice]().Where(I.ID.EQ(one.ID)).Set(I.Total.Set(one.Total)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.Len() != 0 {
		t.Errorf("an update that only bumps updated_at: %d Changes", rec.Len())
	}
	if _, err := rdb.Update[testmodel.Invoice]().Where(I.ID.EQ(one.ID)).Set(I.Total.Add(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	cs := slices.Collect(rec.All())
	if len(cs) != 1 {
		t.Fatalf("%d Changes, want 1", len(cs))
	}
	if f := cs[0].Diff(); len(f) != 1 || f[0].Name != "total" {
		t.Errorf("Diff %+v: updated_at is ignored", f)
	}
	if I.UpdatedAt.Old(cs[0]).IsZero() || !I.UpdatedAt.New(cs[0]).After(I.UpdatedAt.Old(cs[0])) || !cs[0].Changed(I.UpdatedAt) {
		t.Error("the getters lost an ignored column")
	}
	if cur, ok := cs[0].NewRow().(*testmodel.Invoice); !ok || cur.UpdatedAt.IsZero() {
		t.Errorf("NewRow lost an ignored column: %+v", cs[0].NewRow())
	}

	// RecordKeep: updated_at alone is a change, and Diff lists it.
	rec.Reset()
	keep := db.Record(&rec, velox.RecordKeep(I.UpdatedAt))
	if _, err := keep.Update[testmodel.Invoice]().Where(I.ID.EQ(one.ID)).Set(I.Total.Set(one.Total + 1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if cs := slices.Collect(rec.All()); len(cs) != 1 || len(cs[0].Diff()) != 1 || cs[0].Diff()[0].Name != "updated_at" {
		t.Errorf("an update of updated_at alone, kept: %+v", cs)
	}
	// RecordIgnore wins over RecordKeep.
	rec.Reset()
	both := db.Record(&rec, velox.RecordKeep(I.UpdatedAt), velox.RecordIgnore(I.UpdatedAt))
	if _, err := both.Update[testmodel.Invoice]().Where(I.ID.EQ(one.ID)).Set(I.Total.Set(one.Total + 1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.Len() != 0 {
		t.Errorf("updated_at kept and ignored: %d Changes", rec.Len())
	}
}

// TestRecordIgnoreTyped: RecordIgnore takes typed columns, and the options
// of several entities combine, one RecordIgnore per entity: an update that
// changes only ignored columns reports nothing, and Diff leaves them out.
func TestRecordIgnoreTyped(t *testing.T) {
	db, _ := recInvoices(t)
	var rec velox.Recorder
	rdb := db.Record(&rec, velox.RecordIgnore(I.Note, I.Paid), velox.RecordIgnore(testmodel.Items.Qty))
	if _, err := rdb.Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.Note.Set("n"), I.Paid.Set(true)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.Len() != 0 {
		t.Errorf("an update of ignored columns: %d Changes", rec.Len())
	}
	if _, err := rdb.Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.Note.Set("m"), I.Total.Add(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	cs := slices.Collect(rec.All())
	if len(cs) != 1 {
		t.Fatalf("%d Changes, want 1", len(cs))
	}
	if f := cs[0].Diff(); len(f) != 1 || f[0].Name != "total" {
		t.Errorf("Diff %+v, want total alone", f)
	}
	if I.Note.New(cs[0]) != "m" || !cs[0].Changed(I.Note) {
		t.Error("the getters lost an ignored column")
	}
	rc := recEngine(rdb).Recording()
	if len(rc.Ignore) != 2 {
		t.Errorf("RecordIgnore of two entities: %d entities", len(rc.Ignore))
	}
}

// TestSinkGetsTx: a sink set up once gets the write's transaction: the audit
// rows it writes with tx.InsertAll commit with the write, outside a
// transaction and in the caller's, are not recorded themselves, and roll
// back with the caller's transaction.
func TestSinkGetsTx(t *testing.T) {
	db, _ := recInvoices(t)
	var calls int
	var keys []any
	sink := velox.RecordFunc(func(ctx context.Context, tx *velox.DB, cs []velox.Change) error {
		calls++
		audits := make([]testmodel.Invoice, len(cs))
		for i, c := range cs {
			keys = append(keys, c.Key)
			audits[i] = testmodel.Invoice{OrgID: 2, Number: fmt.Sprintf("audit-%d-%v", calls, c.Key), Note: "audit", Data: []byte("a")}
		}
		return tx.InsertAll(audits).Exec(ctx)
	})
	rdb := db.Record(sink)
	countAudits := func() int {
		t.Helper()
		n, err := db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(2), I.Note.EQ("audit")).Count(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return int(n)
	}
	if _, err := rdb.Update[testmodel.Invoice]().Where(I.OrgID.EQ(1)).Set(I.Paid.Set(true)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !slices.Equal(keys, []any{int64(1), int64(2)}) || countAudits() != 2 {
		t.Errorf("outside a transaction: %d calls, keys %v, %d audit rows; want one call, 1 and 2, 2 rows", calls, keys, countAudits())
	}

	// In the caller's transaction, which rolls back: the audit rows go too.
	boom := errors.New("boom")
	err := rdb.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		if _, err := tx.Update[testmodel.Invoice]().Where(I.OrgID.EQ(1)).Set(I.Total.Add(1)).Exec(ctx); err != nil {
			return err
		}
		if got, err := tx.Query[testmodel.Invoice]().Where(I.OrgID.EQ(2), I.Note.EQ("audit")).Count(ctx); err != nil || got != 4 {
			t.Errorf("audit rows in the transaction: %d, %v; want 4", got, err)
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Tx: %v", err)
	}
	if calls != 2 || countAudits() != 2 {
		t.Errorf("after the rollback: %d calls, %d audit rows; want 2 calls, 2 rows", calls, countAudits())
	}

	// In the caller's transaction, which commits, with a Recorder beside:
	// the audit rows are not recorded.
	var rec velox.Recorder
	both := db.Record(velox.RecordFunc(func(ctx context.Context, tx *velox.DB, cs []velox.Change) error {
		if err := rec.Record(ctx, tx, cs); err != nil {
			return err
		}
		return sink(ctx, tx, cs)
	}))
	err = both.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_, err := tx.Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.Total.Add(1)).Exec(ctx)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Len() != 1 || countAudits() != 3 {
		t.Errorf("%d Changes, %d audit rows; want 1 (the audit row is not recorded) and 3", rec.Len(), countAudits())
	}
}

// TestSinkOneCallPerWrite: a bulk update of three rows calls the sink once,
// with the three Changes in key order; a write that changes nothing calls
// it not at all.
func TestSinkOneCallPerWrite(t *testing.T) {
	db, _ := recInvoices(t)
	if err := db.Insert(&testmodel.Invoice{OrgID: 1, Number: "N3", Total: 30, Data: []byte("d")}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var calls [][]any
	rdb := db.Record(velox.RecordFunc(func(_ context.Context, _ *velox.DB, cs []velox.Change) error {
		var keys []any
		for _, c := range cs {
			keys = append(keys, c.Key)
		}
		calls = append(calls, keys)
		return nil
	}))
	if _, err := rdb.Update[testmodel.Invoice]().Where(I.OrgID.EQ(1)).Set(I.Total.Add(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, [][]any{{int64(1), int64(2), int64(3)}}) {
		t.Errorf("the sink got %v, want one call with 1, 2 and 3", calls)
	}
	calls = nil
	if _, err := rdb.Update[testmodel.Invoice]().Where(I.OrgID.EQ(9)).Set(I.Total.Add(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != nil {
		t.Errorf("a write that changed nothing called the sink: %v", calls)
	}
}

// TestRecordSinkErrorInTx: inside a transaction the sink's error is the
// write's, and the transaction that returns it rolls the change back.
func TestRecordSinkErrorInTx(t *testing.T) {
	db, _ := recInvoices(t)
	boom := errors.New("boom")
	rdb := db.Record(velox.RecordFunc(func(context.Context, *velox.DB, []velox.Change) error { return boom }))
	err := rdb.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_, err := tx.Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.Total.Set(99)).Exec(ctx)
		return err
	})
	if !errors.Is(err, boom) {
		t.Fatalf("got %v, want the sink's error", err)
	}
	if got, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx); err != nil || got.Total != 10 {
		t.Errorf("after the rollback: %+v, %v", got, err)
	}
}

// TestSinkErrorOutsideTxRollsBack: outside a transaction the sink runs
// before the COMMIT of the transaction velox opened: its error rolls the
// write back, and nothing is committed, an insert's or an update's.
func TestSinkErrorOutsideTxRollsBack(t *testing.T) {
	db, _ := recInvoices(t)
	boom := errors.New("boom")
	rdb := db.Record(velox.RecordFunc(func(ctx context.Context, tx *velox.DB, _ []velox.Change) error {
		if err := tx.Insert(&testmodel.Invoice{OrgID: 2, Number: "audit", Data: []byte("a")}).Exec(ctx); err != nil {
			return err
		}
		return boom
	}))
	if _, err := rdb.Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.Total.Set(99)).Exec(ctx); !errors.Is(err, boom) {
		t.Fatalf("Update: got %v, want the sink's error", err)
	}
	if err := rdb.Insert(&testmodel.Invoice{OrgID: 1, Number: "N9", Total: 9, Data: []byte("d")}).Exec(ctx); !errors.Is(err, boom) {
		t.Fatalf("Insert: got %v, want the sink's error", err)
	}
	if got, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx); err != nil || got.Total != 10 {
		t.Errorf("after the sink's error: %+v, %v", got, err)
	}
	if n, err := db.Query[testmodel.Invoice]().Count(ctx); err != nil || n != 2 {
		t.Errorf("rows after the sink's errors: %d, %v; want the 2 seeded, no insert and no audit row", n, err)
	}
}

// TestRecordTooManyRows: a recorded write whose locked read matches more
// rows than WithMaxRows allows fails with ErrTooManyRows and sends no write.
func TestRecordTooManyRows(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{{invoiceRow(1), invoiceRow(2), invoiceRow(3)}}, Affected: 3}
	var rec velox.Recorder
	rdb := openFake(velox.Postgres, e, velox.WithMaxRows(2)).Record(&rec)
	_, err := rdb.Update[testmodel.Invoice]().Where(I.OrgID.EQ(1)).Set(I.Total.Add(1)).Exec(ctx)
	if want := "velox: testmodel.Invoice: a recorded update matches more than 2 rows, the cap WithMaxRows sets on its read of them: read more rows than the cap"; !errors.Is(err, velox.ErrTooManyRows) || err.Error() != want {
		t.Fatalf("got %v, want %s", err, want)
	}
	for _, s := range e.Log {
		if strings.HasPrefix(s, "UPDATE") || s == "COMMIT" {
			t.Errorf("sent %s", s)
		}
	}
	if rec.Len() != 0 {
		t.Errorf("%d Changes", rec.Len())
	}
}

// recInvoices opens a SQLite database with invoices 1 (total 10) and 2
// (total 20) of org 1, without the policies, opened with opts (the rows are
// inserted without them).
func recInvoices(t *testing.T, opts ...velox.Option) (*velox.DB, []testmodel.Invoice) {
	t.Helper()
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "record.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	for _, s := range policyDDL {
		exec(t, raw, s)
	}
	seed := velox.Open(velox.SQLite, raw).WithoutPolicy("record tests: seed")
	invs := []testmodel.Invoice{
		{OrgID: 1, Number: "N1", Total: 10, Data: []byte("d")},
		{OrgID: 1, Number: "N2", Total: 20, Data: []byte("d")},
	}
	for i := range invs {
		if err := seed.Insert(&invs[i]).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return velox.Open(velox.SQLite, raw, opts...).WithoutPolicy("record tests"), invs
}

// TestRecordDryRun: SQL(ctx) on a recording handle renders the plain
// statement, sends nothing and records nothing.
func TestRecordDryRun(t *testing.T) {
	e := &fakeconn.Exec{}
	var rec velox.Recorder
	rdb := openFake(velox.Postgres, e).Record(&rec)
	q, _, err := rdb.Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.Total.Set(2)).SQL(ctx)
	if want := `UPDATE "invoices" SET "total" = $1, "updated_at" = $2 WHERE "id" = $3`; err != nil || q != want {
		t.Errorf("SQL: %s, %v; want %s", q, err, want)
	}
	if len(e.Log) != 0 || rec.Len() != 0 {
		t.Errorf("sent %q, %d Changes", e.Log, rec.Len())
	}
}

// TestRecordSinkInTx: inside the caller's transaction the sink runs in it,
// and the writes it makes through the transaction are sent there and not
// recorded.
func TestRecordSinkInTx(t *testing.T) {
	db, _ := recInvoices(t)
	var rec velox.Recorder
	sink := velox.RecordFunc(func(ctx context.Context, tx *velox.DB, cs []velox.Change) error {
		if err := rec.Record(ctx, tx, cs); err != nil {
			return err
		}
		_, err := tx.Update[testmodel.Invoice]().Where(I.ID.EQ(2)).Set(I.Note.Set("audited")).Exec(ctx)
		return err
	})
	err := db.Record(sink).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_, err := tx.Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.Total.Set(11)).Exec(ctx)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Len() != 1 {
		t.Errorf("%d Changes, want 1: the sink's own update is not recorded", rec.Len())
	}
	if got, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(2)).One(ctx); err != nil || got.Note != "audited" {
		t.Errorf("the sink's write: %+v, %v", got, err)
	}
}
