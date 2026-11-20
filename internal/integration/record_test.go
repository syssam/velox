package integration

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// changed is the FieldChange of the column of the given name in c's
// ChangedFields, and whether there is one.
func changed(c velox.Change, name string) (velox.FieldChange, bool) {
	for _, f := range c.ChangedFields() {
		if f.Name == name {
			return f, true
		}
	}
	return velox.FieldChange{}, false
}

// TestRecordBulkUpdate: on each database a recorded bulk update reports
// every row it changed, in key order, with the old and new values the
// database holds; updated_at, the UpdateDefault column, is a changed field
// unless RecordIgnore names it, and invoice 2, whose note was already "a
// note", lists only total and updated_at.
func TestRecordBulkUpdate(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		var rec, ign velox.Recorder
		n, err := tg.db.Record(&rec).Update[testmodel.Invoice]().Where(I.OrgID.EQ(1), I.ID.LT(3)).Set(I.Total.Add(1), I.Note.Set("a note")).Exec(ctx)
		if err != nil || n != 2 {
			t.Fatalf("Update: %d, %v", n, err)
		}
		cs := slices.Collect(rec.All())
		if len(cs) != 2 {
			t.Fatalf("%d Changes, want 2", len(cs))
		}
		for i, c := range cs {
			id := int64(i + 1)
			if c.Key != id || c.Op != velox.OpUpdate {
				t.Errorf("change %d: %+v", i, c)
			}
			if f, ok := changed(c, "total"); !ok || f.Old != float64(10*id) || f.New != float64(10*id+1) {
				t.Errorf("invoice %d total: %+v", id, f)
			}
			if _, ok := changed(c, "note"); ok != (id == 1) {
				t.Errorf("invoice %d: note changed %v", id, ok)
			}
			if f, ok := changed(c, "updated_at"); !ok || !f.Old.(time.Time).Equal(t0) {
				t.Errorf("invoice %d updated_at: %+v", id, f)
			}
			if c.OldValues()["number"] == nil || len(c.NewValues()) != len(c.OldValues()) {
				t.Errorf("invoice %d: snapshots %v %v", id, c.OldValues(), c.NewValues())
			}
		}
		if _, err := tg.db.Record(&ign, velox.RecordIgnore("updated_at")).Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.Total.Add(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		cs = slices.Collect(ign.All())
		if len(cs) != 1 || len(cs[0].ChangedFields()) != 1 {
			t.Fatalf("ignoring updated_at: %+v", cs)
		}
		if _, ok := changed(cs[0], "total"); !ok || cs[0].NewValues()["updated_at"] == nil {
			t.Errorf("ignoring updated_at: %+v %v", cs[0].ChangedFields(), cs[0].NewValues())
		}
	})
}

// TestRecordConcurrent: recorded updates of one row from several goroutines
// each see the value the one before left: the old totals form one chain,
// none read twice, as the read locks the row (SQLite runs them one at a
// time).
func TestRecordConcurrent(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		const n = 8
		var rec velox.Recorder
		rdb := tg.db.Record(&rec)
		var wg sync.WaitGroup
		errs := make(chan error, n)
		for range n {
			wg.Go(func() {
				// A context of its own: on SQLite's one connection, velox
				// refuses a statement under the context another goroutine's
				// transaction holds.
				c, cancel := context.WithCancel(ctx)
				defer cancel()
				_, err := rdb.Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.Total.Add(1)).Exec(c)
				errs <- err
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		var olds []float64
		news := map[float64]bool{}
		for c := range rec.All() {
			f, ok := changed(c, "total")
			if !ok || f.New != f.Old.(float64)+1 {
				t.Fatalf("change %+v", c.ChangedFields())
			}
			olds = append(olds, f.Old.(float64))
			news[f.New.(float64)] = true
		}
		if len(olds) != n {
			t.Fatalf("%d Changes, want %d", len(olds), n)
		}
		slices.Sort(olds)
		for i, o := range olds {
			if o != float64(10+i) || (i > 0 && !news[o]) {
				t.Fatalf("old totals %v, want 10 to %d, each the new total of another", olds, 10+n-1)
			}
		}
	})
}

// TestRecordHardDelete: a recorded delete reports each row's old values and
// no new ones.
func TestRecordHardDelete(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		tg.exec(t, "DELETE FROM tickets")
		tg.exec(t, "DELETE FROM items")
		var rec velox.Recorder
		n, err := tg.db.Record(&rec).Delete[testmodel.Invoice]().Where(I.ID.In(2, 4)).Exec(ctx)
		if err != nil || n != 2 {
			t.Fatalf("Delete: %d, %v", n, err)
		}
		cs := slices.Collect(rec.All())
		if len(cs) != 2 || cs[0].Key != int64(2) || cs[1].Key != int64(4) {
			t.Fatalf("changes %+v", cs)
		}
		for _, c := range cs {
			old := c.OldValues()
			if c.Op != velox.OpDelete || c.NewValues() != nil || old["note"] != "a note" || old["total"] != float64(10*c.Key.(int64)) {
				t.Errorf("change %v: %+v old %v", c.Key, c, old)
			}
		}
		wantIDs(t, query(t, tg), []int64{1, 3, 5})
	})
}
