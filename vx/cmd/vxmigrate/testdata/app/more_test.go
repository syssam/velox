package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unheard is a test whose failures are the result looked at, and not
// reported.
type unheard struct{}

func (unheard) Errorf(string, ...any) {}

func TestReviewed(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	ann, err := s.AddUser(ctx, "ann", "ann@example.com", 30, nil)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := s.AddUser(ctx, "bob", "bob@example.com", 40, nil)
	if err != nil {
		t.Fatal(err)
	}

	// The tests of an error, by the generated names and by the library's.
	_, missing := s.Find(ctx, 9999)
	_, several := s.TheOnly(ctx)
	_, invalid := s.AddUser(ctx, "", "x@example.com", 1, nil)
	_, taken := s.AddUser(ctx, "ann2", "ann@example.com", 1, nil)
	for what, err := range map[string]error{"not found": missing, "not singular": several, "validation": invalid, "constraint": taken, "ok": nil} {
		eq(t, "Classify", Classify(err), what)
		eq(t, "ClassifyByLibrary", ClassifyByLibrary(err), what)
	}
	eq(t, "the field of a validation error", FieldOf(invalid), "name")
	// A condition asked whether it is one: a test that a restricted caller
	// is narrowed must not pass for every caller afterwards.
	require.Nil(t, Scope(true, "ann"))
	require.NotNil(t, Scope(false, "ann"))
	checks := assert.New(t)
	checks.Nil(Scope(true, "ann"), "sees all")
	checks.NotNilf(Scope(false, "ann"), "narrowed to %s", "ann")
	// And the opposite of each has to fail.
	eq(t, "NotNil of no condition", assert.NotNil(unheard{}, Scope(true, "ann")), false)
	eq(t, "Nil of a condition", assert.Nil(unheard{}, Scope(false, "ann")), false)
	// An error a test makes for a missing row is one, wrapped as well.
	eq(t, "a made-up missing row", Classify(Missing("user")), "not found")
	eq(t, "by the library's names", ClassifyByLibrary(fmt.Errorf("loading: %w", Missing("user"))), "not found")

	// A deferred update runs when the function returns.
	eq(t, "the age before the deferred update", s.BumpLater(ctx, ann.ID), 30)
	u, err := s.Find(ctx, ann.ID)
	eq(t, "the age after it", show(u.Age, err), "31 <nil>")

	// A hook is told an update of one row from an update of many.
	one, many := s.CountOps()
	eq(t, "rename", s.Rename(ctx, bob.ID, "rob"), nil)
	eq(t, "promote", show(Classify(func() error { _, err := s.Promote(ctx, bob.ID); return err }())), "ok")
	if _, err := s.AddPost(ctx, ann, "a1"); err != nil {
		t.Fatal(err)
	}
	n, err := s.Publish(ctx)
	eq(t, "published", show(n, err), "1 <nil>")
	eq(t, "updates of one row, and of many", show(*one, *many), "2 1")

	// Builders completed after the bulk was made.
	late, err := s.AddPostsLate(ctx, bob, "b1", "b2")
	if err != nil || len(late) != 2 {
		t.Fatal(late, err)
	}
	titles, err := s.Titles(ctx, bob.ID)
	eq(t, "the posts are the author's", show(titles, err), "[rob:b1 rob:b2] <nil>")
	if _, err := s.Adopt(ctx, *ann, "a2"); err != nil {
		t.Fatal(err)
	}
	titles, err = s.Titles(ctx, ann.ID)
	eq(t, "adopted", show(titles, err), "[ann:a1 ann:a2] <nil>")

	// Of two orders the last is the order.
	asc := byTitle(t)
	desc := byTitle(t)
	desc.Direction = descending
	got, err := s.TitlesByLast(ctx, asc, desc)
	eq(t, "the last order", show(got, err), "[b2 b1 a2 a1] <nil>")
	got, err = s.TitlesByLast(ctx, desc, asc)
	eq(t, "the last order, the other way", show(got, err), "[a1 a2 b1 b2] <nil>")

	// An eager load of some columns.
	posts, err := s.AuthorsNamed(ctx)
	if err != nil || len(posts) != 4 {
		t.Fatal(len(posts), err)
	}
	for _, p := range posts {
		if p.Edges.Author == nil || p.Edges.Author.Name == "" || p.Edges.Author.Email != "" {
			t.Errorf("the author of %s was loaded as %+v", p.Title, p.Edges.Author)
		}
	}

	// Orders that are not a column's Asc or Desc.
	us, err := s.Oldest(ctx)
	eq(t, "by a function", show(names(us), err), "[rob ann] <nil>")
	us, err = s.ByColumn(ctx, "name")
	eq(t, "by a column named at run time", show(names(us), err), "[rob ann] <nil>")
	if _, err := s.ByColumn(ctx, "no_such_column"); err == nil {
		t.Error("ordering by a column that does not exist did not fail")
	}

	// A value set next to "no stamp of the update's own" is written; without
	// a value, the column stays as it was.
	pinned := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	eq(t, "backdate", s.Backdate(ctx, ann.ID, pinned), nil)
	u, err = s.Find(ctx, ann.ID)
	eq(t, "updated_at was set", show(u.UpdatedAt.Equal(pinned), err), "true <nil>")
	eq(t, "touch", s.Touch(ctx, ann.ID), nil)
	u, err = s.Find(ctx, ann.ID)
	eq(t, "updated_at was kept", show(u.UpdatedAt.Equal(pinned), u.Age, err), "true 32 <nil>")

	// A function that only queries, given a transaction.
	inside, after, err := s.AddAndCount(ctx, "cy", "cy@example.com")
	eq(t, "inside the transaction, and after it was rolled back", show(inside, after, err), "3 2 <nil>")
}
