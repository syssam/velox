package integration

import (
	"errors"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// paperVersion reads the version of the paper with id's key.
func paperVersion(t *testing.T, db *velox.DB, p *testmodel.Paper) int64 {
	t.Helper()
	got, err := db.Query[testmodel.Paper]().Where(P.ID.EQ(p.ID)).Limit(1).All(ctx)
	if err != nil || len(got) != 1 {
		t.Fatalf("paper %v: %+v, %v", p.ID, got, err)
	}
	return got[0].Version
}

// TestOptimisticLocking: Paper's Version column. Save writes only the
// version it read and moves it on, and a second writer of the same version
// fails with ErrStale, which matches ErrNotFound, leaving its value as it
// was; an update, its One and an upsert's update branch add 1; an upsert's
// insert keeps the given version; an update of edges only leaves it; and a
// write that assigns the version is refused.
func TestOptimisticLocking(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		keysTables(t, tg)
		db := tg.db
		p := testmodel.Paper{Title: "draft"}
		if err := db.Insert(&p).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if p.Version != 0 || paperVersion(t, db, &p) != 0 {
			t.Fatalf("a new paper's version: %d", p.Version)
		}

		first, second := p, p // two readers of version 0
		first.Title, second.Title = "first", "second"
		if err := db.Save(&first, P.Title).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if first.Version != 1 || paperVersion(t, db, &p) != 1 {
			t.Errorf("after the first Save: *x at %d, the row at %d, want 1", first.Version, paperVersion(t, db, &p))
		}
		err := db.Save(&second, P.Title).Exec(ctx)
		if !errors.Is(err, velox.ErrStale) || !errors.Is(err, velox.ErrNotFound) {
			t.Fatalf("the second Save of version 0: %v, want ErrStale", err)
		}
		if second.Version != 0 || second.Title != "second" {
			t.Errorf("after ErrStale, *x is %+v", second)
		}
		got, err := db.Query[testmodel.Paper]().Where(P.ID.EQ(p.ID)).Limit(1).All(ctx)
		if err != nil || got[0].Title != "first" {
			t.Errorf("the stale Save wrote: %+v, %v", got, err)
		}

		// A Save of a deleted row is stale too.
		gone := testmodel.Paper{Title: "gone"}
		if err := db.Insert(&gone).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Delete[testmodel.Paper]().Where(P.ID.EQ(gone.ID)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if err := db.Save(&gone, P.Title).Exec(ctx); !errors.Is(err, velox.ErrStale) {
			t.Errorf("a Save of a deleted row: %v, want ErrStale", err)
		}

		// Updates, One and an upsert's update branch each add 1.
		if n, err := db.Update[testmodel.Paper]().Set(P.Title.Set("bulk")).Where(P.ID.EQ(p.ID)).Exec(ctx); err != nil || n != 1 {
			t.Fatalf("update: %d, %v", n, err)
		}
		one, err := db.Update[testmodel.Paper]().Set(P.Title.Set("one")).Where(P.ID.EQ(p.ID)).One(ctx)
		if err != nil || one.Version != 3 {
			t.Fatalf("UpdateQuery.One: %+v, %v, want version 3", one, err)
		}
		up := testmodel.Paper{ID: p.ID, Title: "upserted", Version: 99}
		if err := db.Insert(&up).OnConflict(P.ID).Update(P.Title).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if v := paperVersion(t, db, &p); v != 4 {
			t.Errorf("after the upsert's update branch: version %d, want 4", v)
		}
		fresh := testmodel.Paper{Title: "fresh", Version: 7}
		if err := db.Insert(&fresh).OnConflict(P.ID).Update(P.Title).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if v := paperVersion(t, db, &fresh); v != 7 {
			t.Errorf("an upsert's insert: version %d, want the given 7", v)
		}
		keep := testmodel.Paper{ID: p.ID, Title: "kept"}
		if err := db.Insert(&keep).OnConflict(P.ID).DoNothing().Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if v := paperVersion(t, db, &p); v != 4 {
			t.Errorf("after DoNothing: version %d, want 4", v)
		}

		// Linking a topic writes the join table, not the paper's row.
		if err := db.Insert(&testmodel.Topic{ID: "t1", Name: "T1"}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Update[testmodel.Paper]().Set(P.Topics.Add("t1")).Where(P.ID.EQ(p.ID)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if v := paperVersion(t, db, &p); v != 4 {
			t.Errorf("after an update of edges only: version %d, want 4", v)
		}

		// The version is velox's to set.
		for name, err := range map[string]error{
			"Update.Set": func() error {
				_, err := db.Update[testmodel.Paper]().Set(P.Version.Set(9)).Where(P.ID.EQ(p.ID)).Exec(ctx)
				return err
			}(),
			"Save":   db.Save(&first, P.Version).Exec(ctx),
			"upsert": db.Insert(&up).OnConflict(P.ID).Update(P.Version).Exec(ctx),
		} {
			if !errors.Is(err, velox.ErrVersionSet) {
				t.Errorf("%s of the version: %v, want ErrVersionSet", name, err)
			}
		}
		if v := paperVersion(t, db, &p); v != 4 {
			t.Errorf("after the refused writes: version %d, want 4", v)
		}
	})
}
