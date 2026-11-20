package apitest

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/internal/testmodel/kind"
)

// TestNullHandlePtrGetters: a Null handle's OldPtr and NewPtr tell NULL
// (nil) from an empty value (a pointer to it), give a copy that does not
// reach the Change, and are nil for a row the Change does not have or a
// Change of another entity.
func TestNullHandlePtrGetters(t *testing.T) {
	S := testmodel.Samples
	before := sampleRow(1, nil) // prev, ratio, maybe_flag, maybe_when NULL
	after := sampleRow(1, nil)
	after[3], after[5], after[7], after[9] = "", 0.0, false, t0
	e := &fakeconn.Exec{Results: [][][]any{{before}, {after}}, Affected: 1}
	var rec velox.Recorder
	if _, err := openFake(velox.Postgres, e).Record(&rec).Update[testmodel.Sample]().Where(S.ID.EQ(1)).
		Set(S.Prev.Set(""), S.Ratio.Set(0), S.MaybeFlag.Set(false), S.MaybeWhen.Set(t0)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	cs := slices.Collect(rec.All())
	if len(cs) != 1 {
		t.Fatalf("%d Changes", len(cs))
	}
	c := cs[0]
	if S.Prev.OldPtr(c) != nil || S.Ratio.OldPtr(c) != nil || S.MaybeFlag.OldPtr(c) != nil || S.MaybeWhen.OldPtr(c) != nil {
		t.Errorf("OldPtr of NULL columns: %v %v %v %v", S.Prev.OldPtr(c), S.Ratio.OldPtr(c), S.MaybeFlag.OldPtr(c), S.MaybeWhen.OldPtr(c))
	}
	prev, ratio, flag, when := S.Prev.NewPtr(c), S.Ratio.NewPtr(c), S.MaybeFlag.NewPtr(c), S.MaybeWhen.NewPtr(c)
	if prev == nil || *prev != kind.Status("") || ratio == nil || *ratio != 0 || flag == nil || *flag || when == nil || !when.Equal(t0) {
		t.Fatalf("NewPtr: %v %v %v %v", prev, ratio, flag, when)
	}
	*prev, *ratio = "x", 3
	if S.Prev.New(c) != "" || *S.Prev.NewPtr(c) != "" || S.Ratio.New(c) != 0 || S.Prev.NewPtr(c) == prev {
		t.Errorf("a NewPtr's value reached the Change: %q, %v", S.Prev.New(c), S.Ratio.New(c))
	}
	if I.PaidAt.OldPtr(c) != nil || I.PaidAt.NewPtr(c) != nil {
		t.Error("an Invoice handle's OldPtr or NewPtr on a Sample's Change is not nil")
	}

	// A delete has no new row.
	e = &fakeconn.Exec{Results: [][][]any{{after}, {{int64(1)}}}, Affected: 1}
	rec.Reset()
	if _, err := openFake(velox.Postgres, e).Record(&rec).Delete[testmodel.Sample]().Where(S.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	cs = slices.Collect(rec.All())
	if len(cs) != 1 || S.Prev.NewPtr(cs[0]) != nil || S.Prev.OldPtr(cs[0]) == nil || S.MaybeWhen.OldPtr(cs[0]) == nil {
		t.Fatalf("delete: %+v", cs)
	}
}

// TestSnapshotTimesUTC: the times of recorded rows are in UTC, without a
// monotonic reading, so that an insert's new row and a later update's old
// row marshal a time in the same form; the entity the caller holds keeps its
// own zone. An update that sets a time equal in instant to the stored one,
// in another zone, does not change the column.
func TestSnapshotTimesUTC(t *testing.T) {
	db, _ := recInvoices(t)
	zone := time.FixedZone("+08", 8*3600)
	created := time.Now() // local zone, with a monotonic reading
	paid := time.Date(2026, 3, 1, 9, 0, 0, 0, zone)
	inv := testmodel.Invoice{OrgID: 1, Number: "N9", Total: 1, Data: []byte("d"), CreatedAt: created, PaidAt: &paid}
	var rec velox.Recorder
	rdb := db.Record(&rec)
	if err := rdb.Insert(&inv).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if inv.CreatedAt != created || inv.PaidAt != &paid || paid.Location() != zone {
		t.Errorf("the caller's entity changed: %v, %v", inv.CreatedAt, inv.PaidAt)
	}
	ins := slices.Collect(rec.All())
	if len(ins) != 1 {
		t.Fatalf("%d Changes", len(ins))
	}
	if got := I.CreatedAt.New(ins[0]); got != created.UTC() {
		t.Errorf("insert's created_at %#v, want %#v", got, created.UTC())
	}
	if got := I.PaidAt.NewPtr(ins[0]); got == nil || *got != paid.UTC() {
		t.Errorf("insert's paid_at %v", got)
	}
	newJSON := marshal(t, ins[0].NewRow())

	rec.Reset()
	if _, err := rdb.Update[testmodel.Invoice]().Where(I.ID.EQ(inv.ID)).
		Set(I.Total.Set(2), I.PaidAt.Set(paid.In(time.FixedZone("-05", -5*3600)))).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	ups := slices.Collect(rec.All())
	if len(ups) != 1 {
		t.Fatalf("%d Changes", len(ups))
	}
	oldJSON, curJSON := marshal(t, ups[0].OldRow()), marshal(t, ups[0].NewRow())
	for _, col := range []string{"created_at", "paid_at"} {
		a, b, c := jsonField(t, newJSON, col), jsonField(t, oldJSON, col), jsonField(t, curJSON, col)
		if a != b || b != c || !strings.HasSuffix(a, `Z"`) {
			t.Errorf("%s: insert's new row %s, update's old row %s and new row %s; want one UTC form", col, a, b, c)
		}
	}
	if ups[0].Changed(I.PaidAt) {
		t.Error("paid_at, set to the same instant in another zone, is reported changed")
	}
	for _, fc := range ups[0].Diff() {
		if fc.Name != "total" {
			t.Errorf("Diff lists %+v", fc)
		}
	}
}

// marshal is v as JSON.
func marshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// jsonField is the raw JSON of the object b's field name.
func jsonField(t *testing.T, b []byte, name string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return string(m[name])
}

// TestMySQLKeyNormaliseFailsClosed: on MySQL a recorded update that assigns
// the key reads its rows again by their new keys; a re-read that finds
// fewer rows than the locked read (the database stored the key in another
// form) fails the write, rolled back, and the sink is not called.
func TestMySQLKeyNormaliseFailsClosed(t *testing.T) {
	// The update assigns 9; the re-read by 9 finds nothing.
	e := &fakeconn.Exec{Results: [][][]any{{invoiceRow(1)}, {}}, Affected: 1}
	called := false
	sink := velox.RecordFunc(func(context.Context, *velox.DB, []velox.Change) error {
		called = true
		return nil
	})
	_, err := openFake(velox.MySQL, e).Record(sink).Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.ID.Set(9)).Exec(ctx)
	if err == nil || !strings.Contains(err.Error(), "found 0 of the 1 rows") || e.Log[len(e.Log)-1] != "ROLLBACK" {
		t.Fatalf("Update: %v; sent %q", err, e.Log)
	}
	if called {
		t.Error("the sink was called")
	}
}
