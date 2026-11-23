package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
)

// errNoteFilter is the error of noteFilter under a context it fails.
var errNoteFilter = errors.New("note filter failed")

type noteFilterFails struct{}

// noteFilter hides the note "a" from every statement, and fails under
// noteFilterFails; it restricts no other entity.
func noteFilter(ctx context.Context, _ *velox.DB, entity string, _ velox.Op) (velox.Predicate, error) {
	if entity != "INote" {
		return velox.Allow(), nil
	}
	if ctx.Value(noteFilterFails{}) != nil {
		return velox.Predicate{}, errNoteFilter
	}
	return velox.FieldNEQ("body", "a"), nil
}

// TestFilterOnRealDatabases: a WithFilter filter, with the interceptor and
// the Read policy, filters reads, relations and writes on each database;
// WithoutPolicy keeps it; its error fails a Has and a With load; and MySQL
// refuses an upsert that updates an entity it restricts, and not one it
// leaves alone.
func TestFilterOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		interceptorTables(t, tg)
		bg := context.Background()
		q := tg.pdb.Query[fixture.INote]().OrderBy(fixture.INotes.ID.Asc()).AllowUnbounded()
		ns, err := q.All(bg)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, inoteIDs(ns), []int64{4})
		if ns, err = tg.db.Query[fixture.INote]().OrderBy(fixture.INotes.ID.Asc()).AllowUnbounded().All(bg); err != nil {
			t.Fatal(err)
		}
		wantIDs(t, inoteIDs(ns), []int64{3, 4})

		os, err := tg.pdb.Query[fixture.IOwner]().With(fixture.IOwners.Notes).With(fixture.IOwners.Tagged).
			OrderBy(fixture.IOwners.ID.Asc()).AllowUnbounded().All(bg)
		if err != nil {
			t.Fatal(err)
		}
		notes, _ := os[0].Notes.Get()
		tagged, _ := os[0].Tagged.Get()
		wantIDs(t, inoteIDs(notes), nil)
		wantIDs(t, inoteIDs(tagged), nil)
		if n, err := tg.pdb.Query[fixture.IOwner]().Where(fixture.IOwners.Notes.Has()).AllowUnbounded().Count(bg); err != nil || n != 1 {
			t.Errorf("Notes.Has: %d, %v", n, err)
		}
		if joined, err := tg.pdb.Query[fixture.IOwner]().Join(fixture.IOwners.Pinned).AllowUnbounded().All(bg); err != nil || len(joined) != 0 {
			t.Errorf("Join(Pinned): %v, %v", joined, err)
		}
		fails := context.WithValue(bg, noteFilterFails{}, true)
		if _, err := tg.pdb.Query[fixture.IOwner]().Where(fixture.IOwners.Notes.Has()).AllowUnbounded().All(fails); !errors.Is(err, errNoteFilter) {
			t.Errorf("Has under a failing filter: %v", err)
		}
		if _, err := tg.pdb.Query[fixture.IOwner]().With(fixture.IOwners.Notes).AllowUnbounded().All(fails); !errors.Is(err, errNoteFilter) {
			t.Errorf("With under a failing filter: %v", err)
		}

		owner := func(ctx context.Context) error {
			return tg.db.Insert(&fixture.IOwner{ID: 1, Name: "o9"}).OnConflict(fixture.IOwners.ID).Update(fixture.IOwners.Name).Exec(ctx)
		}
		if err := owner(bg); err != nil {
			t.Errorf("upsert of an owner, which the filter leaves alone: %v", err)
		}
		// On MySQL the interceptor refuses INote's upsert before the filter
		// is asked; elsewhere the filter hides the note "a" from it.
		if tg.d != velox.MySQL {
			err := tg.db.Insert(&fixture.INote{ID: 9, OwnerID: 7, Body: "a"}).OnConflict(fixture.INotes.Body).Update(fixture.INotes.OwnerID).Exec(bg)
			if err == nil {
				t.Errorf("the upsert of the filtered note succeeded")
			}
		}

		if n, err := tg.pdb.Update[fixture.INote]().Set(fixture.INotes.OwnerID.Set(5)).All().Exec(bg); err != nil || n != 1 {
			t.Errorf("Update all: %d, %v; want 1", n, err)
		}
		if n, err := tg.pdb.Delete[fixture.INote]().Where(fixture.INotes.ID.In(1, 4)).Exec(bg); err != nil || n != 1 {
			t.Errorf("Delete 1 and 4: %d, %v; want 1", n, err)
		}
	}, velox.WithFilter(noteFilter))
}
