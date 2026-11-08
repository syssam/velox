package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
)

// interceptorTables creates the fixture's owners, notes and owner_notes on
// tg, with the rows of apitest's interceptorDB: owners 1 (pinned note 2) and
// 2 (pinned note 1); notes 1 "a" and 2 "b", hidden, and 3 "secret", which
// the Read policy hides, of owner 1, and 4 "c" of owner 2; links (1, 1),
// (1, 2) and (2, 2).
func interceptorTables(t *testing.T, tg target) {
	t.Helper()
	text, boolean := "text", "boolean"
	if tg.d == velox.MySQL {
		text = "VARCHAR(64)"
	}
	for _, s := range []string{
		"DROP TABLE IF EXISTS owner_notes", "DROP TABLE IF EXISTS notes", "DROP TABLE IF EXISTS owners",
		"CREATE TABLE owners (id bigint PRIMARY KEY, name " + text + " NOT NULL, pinned_id bigint)",
		"CREATE TABLE notes (id bigint PRIMARY KEY, owner_id bigint NOT NULL, body " + text + " NOT NULL UNIQUE, hidden " + boolean + " NOT NULL)",
		"CREATE TABLE owner_notes (owner_id bigint NOT NULL, note_id bigint NOT NULL, PRIMARY KEY (owner_id, note_id))",
	} {
		tg.exec(t, s)
	}
	tg.exec(t, "INSERT INTO owners (id, name, pinned_id) VALUES (?, ?, ?), (?, ?, ?)", 1, "o1", 2, 2, "o2", 1)
	tg.exec(t, "INSERT INTO notes (id, owner_id, body, hidden) VALUES (?, ?, ?, ?), (?, ?, ?, ?), (?, ?, ?, ?), (?, ?, ?, ?)",
		1, 1, "a", false, 2, 1, "b", true, 3, 1, "secret", false, 4, 2, "c", false)
	tg.exec(t, "INSERT INTO owner_notes (owner_id, note_id) VALUES (?, ?), (?, ?), (?, ?)", 1, 1, 1, 2, 2, 2)
}

func inoteIDs(ns []fixture.INote) []int64 {
	out := []int64{}
	for _, n := range ns {
		out = append(out, n.ID)
	}
	return out
}

// TestInterceptorOnRealDatabases: an interceptor filters reads, relations
// and writes on each database; WithoutPolicy keeps it, and MySQL refuses an
// upsert that updates its entity.
func TestInterceptorOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		interceptorTables(t, tg)
		bg := context.Background()
		q := tg.pdb.Query[fixture.INote]().OrderBy(fixture.INotes.ID.Asc()).Unbounded()
		ns, err := q.All(bg)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, inoteIDs(ns), []int64{1, 4})
		if ns, err = tg.db.Query[fixture.INote]().OrderBy(fixture.INotes.ID.Asc()).Unbounded().All(bg); err != nil {
			t.Fatal(err)
		}
		wantIDs(t, inoteIDs(ns), []int64{1, 3, 4})
		if n, err := q.Count(bg); err != nil || n != 2 {
			t.Errorf("Count: %d, %v", n, err)
		}

		os, err := tg.pdb.Query[fixture.IOwner]().With(fixture.IOwners.Notes.OrderBy(fixture.INotes.ID.Asc())).With(fixture.IOwners.Tagged).
			OrderBy(fixture.IOwners.ID.Asc()).Unbounded().All(bg)
		if err != nil {
			t.Fatal(err)
		}
		notes, _ := os[0].Notes.Get()
		tagged, _ := os[0].Tagged.Get()
		wantIDs(t, inoteIDs(notes), []int64{1})
		wantIDs(t, inoteIDs(tagged), []int64{1})
		has, err := tg.pdb.Query[fixture.IOwner]().Where(fixture.IOwners.Tagged.Has()).Unbounded().Count(bg)
		if err != nil || has != 1 {
			t.Errorf("Tagged.Has: %d, %v", has, err)
		}
		joined, err := tg.pdb.Query[fixture.IOwner]().Join(fixture.IOwners.Pinned).Unbounded().All(bg)
		if err != nil || len(joined) != 1 || joined[0].ID != 2 {
			t.Errorf("Join(Pinned): %v, %v", joined, err)
		}

		up := func(ctx context.Context) error {
			return tg.db.Insert(&fixture.INote{ID: 9, OwnerID: 7, Body: "b"}).OnConflict(fixture.INotes.Body).Update(fixture.INotes.OwnerID).Exec(ctx)
		}
		err = up(bg)
		if tg.d == velox.MySQL {
			if !errors.Is(err, velox.ErrPolicyUpsert) {
				t.Errorf("MySQL upsert: %v", err)
			}
		} else if err == nil {
			t.Errorf("the upsert of the hidden note succeeded")
		}
		skip := fixture.WithFilterMode(bg, fixture.FilterSkip)
		if n, err := tg.pdb.Query[fixture.INote]().Where(fixture.INotes.OwnerID.EQ(7)).Count(skip); err != nil || n != 0 {
			t.Errorf("the hidden note was updated: %d, %v", n, err)
		}

		if n, err := tg.pdb.Update[fixture.INote]().Set(fixture.INotes.OwnerID.Set(5)).All().Exec(bg); err != nil || n != 2 {
			t.Errorf("Update all: %d, %v; want 2", n, err)
		}
		if n, err := tg.pdb.Delete[fixture.INote]().Where(fixture.INotes.ID.In(1, 2)).Exec(bg); err != nil || n != 1 {
			t.Errorf("Delete 1 and 2: %d, %v; want 1", n, err)
		}
		if n, err := tg.db.Query[fixture.INote]().Count(skip); err != nil || n != 3 {
			t.Errorf("rows left: %d, %v; want 3", n, err)
		}
	})
}
