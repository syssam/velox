package integration

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/schema/mixin/softdelete"
)

// changeLog is what a tracking hook saw, safe for concurrent writes.
type changeLog struct {
	mu   sync.Mutex
	runs [][]velox.Change
}

// hook returns the hook that calls Track with names before next on the
// given kinds of write and records Changes after it.
func (l *changeLog) hook(names []string, ops ...velox.Op) velox.Hook {
	return velox.On(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			if err := m.Track(names...); err != nil {
				return err
			}
			if err := next.Mutate(ctx, m); err != nil {
				return err
			}
			l.mu.Lock()
			l.runs = append(l.runs, slices.Collect(m.Changes()))
			l.mu.Unlock()
			return nil
		})
	}, ops...)
}

// last is the Changes of the last write, and resets the log.
func (l *changeLog) last() []velox.Change {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.runs) == 0 {
		return nil
	}
	out := l.runs[len(l.runs)-1]
	l.runs = nil
	return out
}

// field is the FieldChange of the column of the given name in c.
func field(t *testing.T, c velox.Change, name string) velox.FieldChange {
	t.Helper()
	for _, f := range c.Fields {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("change %v has no field %s", c.Key, name)
	return velox.FieldChange{}
}

// TestTrackBulkUpdate: on each database a tracked bulk update reports every
// row it wrote, in key order, with the assigned columns' old and new values,
// the UpdateDefault column included, as the database holds them.
func TestTrackBulkUpdate(t *testing.T) {
	var log changeLog
	each(t, func(t *testing.T, tg target) {
		n, err := tg.db.Update[testmodel.Invoice]().Where(I.OrgID.EQ(1)).Set(I.Total.Add(1), I.Note.Set("a note")).Exec(ctx)
		if err != nil || n != 4 {
			t.Fatalf("Update: %d, %v", n, err)
		}
		cs := log.last()
		if len(cs) != 4 {
			t.Fatalf("changes: %+v", cs)
		}
		for i, c := range cs {
			id := int64(i + 1)
			if len(c.Key) != 1 || c.Key[0] != id {
				t.Errorf("change %d: key %v", i, c.Key)
			}
			total := field(t, c, "total")
			if total.Old != float64(10*id) || total.New != float64(10*id+1) || !total.Changed {
				t.Errorf("invoice %d total: %+v", id, total)
			}
			note := field(t, c, "note")
			if note.New != "a note" || note.Changed != (id%2 == 1) {
				t.Errorf("invoice %d note: %+v", id, note)
			}
			if up := field(t, c, "updated_at"); !up.Changed || !up.Old.(time.Time).Equal(t0) {
				t.Errorf("invoice %d updated_at: %+v", id, up)
			}
			if names := len(c.Fields); names != 3 {
				t.Errorf("invoice %d: %d fields, want total, note and updated_at", id, names)
			}
		}
	}, velox.WithHooks[testmodel.Invoice](log.hook(nil, velox.OpUpdate)))
}

// TestTrackConcurrent: tracked updates of one row from several goroutines
// each see the value the one before left: the old values form one chain,
// none read twice, as the read locks the row (SQLite runs them one at a
// time).
func TestTrackConcurrent(t *testing.T) {
	var log changeLog
	each(t, func(t *testing.T, tg target) {
		const n = 8
		var wg sync.WaitGroup
		errs := make(chan error, n)
		for range n {
			wg.Go(func() {
				// A context of its own: on SQLite's one connection, velox
				// refuses a statement under the context another goroutine's
				// transaction holds.
				c, cancel := context.WithCancel(ctx)
				defer cancel()
				_, err := tg.db.Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.Total.Add(1)).Exec(c)
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
		log.mu.Lock()
		runs := log.runs
		log.runs = nil
		log.mu.Unlock()
		var olds []float64
		for _, r := range runs {
			if len(r) != 1 {
				t.Fatalf("a run's changes: %+v", r)
			}
			f := field(t, r[0], "total")
			if f.New != f.Old.(float64)+1 {
				t.Errorf("change %+v", f)
			}
			olds = append(olds, f.Old.(float64))
		}
		slices.Sort(olds)
		for i, o := range olds {
			if o != float64(10+i) {
				t.Fatalf("old totals %v, want 10 to %d", olds, 10+n-1)
			}
		}
	}, velox.WithHooks[testmodel.Invoice](log.hook([]string{"total"}, velox.OpUpdate)))
}

// TestTrackSoftDelete: a soft delete reaches the entity's update hooks, and
// Track there reports deleted_at set, though the interceptor hides the row
// once it is (MySQL's re-read of the new values is not filtered). A hard
// delete under Skip reports the row's old values.
func TestTrackSoftDelete(t *testing.T) {
	var log changeLog
	each(t, func(t *testing.T, tg target) {
		softTables(t, tg)
		D := fixture.SDocs
		in1 := testmodel.Org.With(context.Background(), int64(1))
		if n, err := tg.pdb.Delete[fixture.SDoc]().Where(D.ID.EQ(2)).Exec(in1); err != nil || n != 1 {
			t.Fatalf("Delete: %d, %v", n, err)
		}
		cs := log.last()
		if len(cs) != 1 || cs[0].Key[0] != int64(2) {
			t.Fatalf("soft delete's changes: %+v", cs)
		}
		if f := field(t, cs[0], "deleted_at"); f.Old != nil || f.New == nil || !f.Changed {
			t.Errorf("deleted_at: %+v", f)
		}
		skip := softdelete.Skip(in1)
		if n, err := tg.pdb.Delete[fixture.SDoc]().Where(D.ID.EQ(1)).Exec(skip); err != nil || n != 1 {
			t.Fatalf("hard Delete: %d, %v", n, err)
		}
		cs = log.last()
		if len(cs) != 1 || len(cs[0].Fields) != 5 || field(t, cs[0], "title").Old != "a" || field(t, cs[0], "title").New != nil {
			t.Errorf("hard delete's changes: %+v", cs)
		}
	}, velox.WithHooks[fixture.SDoc](log.hook(nil, velox.OpUpdate, velox.OpDelete)))
}
