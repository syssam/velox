package integration

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/testmodel"
)

// TestFieldInSelectRaw runs ADR 0157's case on every dialect: owners' names,
// text, hold ids of notes and docs, integers. FieldInSelectRaw matches a
// name whose row INote's interceptor (note 2) or Read policy (note 3) hides,
// or that is soft-deleted (doc 1) or in another org (doc 3); FieldInSelect
// does not. An Around hook sees the reason, and an UPDATE and a DELETE take
// the condition.
func TestFieldInSelectRaw(t *testing.T) {
	var mu sync.Mutex
	var reasons []string
	around := velox.WithAround(func(c context.Context, st *velox.Statement, next func(context.Context) error) error {
		mu.Lock()
		reasons = append(reasons, st.RawReasons()...)
		mu.Unlock()
		return next(c)
	})
	each(t, func(t *testing.T, tg target) {
		interceptorTables(t, tg)
		softTables(t, tg)
		tg.exec(t, "UPDATE owners SET name = ? WHERE id = ?", "3", 1)
		tg.exec(t, "UPDATE owners SET name = ? WHERE id = ?", "2", 2)
		tg.exec(t, "INSERT INTO owners (id, name, pinned_id) VALUES (?, ?, ?)", 3, "1", nil)
		in1 := testmodel.Org.With(context.Background(), int64(1))
		if n, err := tg.pdb.Delete[fixture.SDoc]().Where(fixture.SDocs.ID.EQ(1)).Exec(in1); err != nil || n != 1 {
			t.Fatalf("soft delete: %d, %v", n, err)
		}
		O := fixture.IOwners
		owners := func(c context.Context, p velox.Predicate) []int64 {
			t.Helper()
			os, err := tg.pdb.Query[fixture.IOwner]().Where(velox.Pred[fixture.IOwner](p)).OrderBy(O.ID.Asc()).AllowUnbounded().All(c)
			if err != nil {
				t.Fatal(err)
			}
			return ownerIDs(os)
		}
		const why = "a deleted row keeps its history"
		for _, c := range []struct {
			entity      string
			plain, want []int64
		}{
			{"INote", []int64{3}, []int64{1, 2, 3}},
			{"SDoc", []int64{2}, []int64{1, 2, 3}},
		} {
			wantIDs(t, owners(in1, velox.FieldInSelect("name", c.entity, "id")), c.plain)
			mu.Lock()
			reasons = nil
			mu.Unlock()
			wantIDs(t, owners(in1, velox.FieldInSelectRaw(why, "name", c.entity, "id")), c.want)
			mu.Lock()
			if !slices.Equal(reasons, []string{why}) {
				t.Errorf("%s: RawReasons %q", c.entity, reasons)
			}
			mu.Unlock()
		}
		// In an Or and a Not, with a condition of its own.
		hidden := velox.FieldInSelectRaw(why, "name", "INote", "id", velox.FieldEQ("hidden", true))
		wantIDs(t, owners(in1, velox.Or(velox.FieldEQ("id", int64(3)), velox.Not(hidden))), []int64{1, 3})

		upd, err := tg.pdb.Update[fixture.IOwner]().Set(O.PinnedID.Set(4)).Where(velox.Pred[fixture.IOwner](hidden)).Exec(in1)
		if err != nil || upd != 1 {
			t.Errorf("update: %d, %v", upd, err)
		}
		del, err := tg.pdb.Delete[fixture.IOwner]().Where(velox.Pred[fixture.IOwner](hidden)).Exec(in1)
		if err != nil || del != 1 {
			t.Errorf("delete: %d, %v", del, err)
		}
		wantIDs(t, owners(in1, velox.Allow()), []int64{1, 3})
	}, around)
}
