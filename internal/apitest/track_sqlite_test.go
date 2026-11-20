package apitest

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
)

// TestTrackSQLite: on a real SQLite database a tracked bulk update returns
// each row's old and new values, a nullable column's as its value or nil,
// and a tracked delete each row's old values; a row the update moves out of
// the Read policy's view is still reported.
func TestTrackSQLite(t *testing.T) {
	_, raw := edgeDB(t)
	exec(t, raw, "INSERT INTO s_posts (id, author_id, title) VALUES (1, 1, 'p')")
	exec(t, raw, "INSERT INTO s_comments (id, post_id) VALUES (1, 1), (2, NULL), (3, 1)")
	var got []velox.Change
	db := velox.Open(velox.SQLite, raw, velox.WithHooks[sComment](tracking(&got, []string{"post_id", "hidden"}, velox.OpUpdate, velox.OpDelete)))
	ID, Post, Hidden := velox.NumberCol[sComment, int64]{I: 0}, velox.NullNumberCol[sComment, int64]{I: 1}, velox.Col[sComment, bool]{I: 2}
	n, err := db.Update[sComment]().Where(ID.In(1, 2)).Set(Post.Set(1), Hidden.Set(true)).Exec(ctx)
	if err != nil || n != 2 {
		t.Fatalf("Update: %d, %v", n, err)
	}
	if s := showChanges(got); s != "[1]: post_id 1->1 hidden false->true*\n[2]: post_id <nil>->1* hidden false->true*\n" {
		t.Errorf("update changes:\n%s", s)
	}
	n, err = db.WithoutPolicy("test: see hidden rows").Delete[sComment]().Where(ID.GT(1)).Exec(ctx)
	if err != nil || n != 2 {
		t.Fatalf("Delete: %d, %v", n, err)
	}
	if s := showChanges(got); s != "[2]: post_id 1-><nil>* hidden true-><nil>*\n[3]: post_id 1-><nil>* hidden false-><nil>*\n" {
		t.Errorf("delete changes:\n%s", s)
	}
}

// TestDiff: Diff lists the columns whose values differ, plain; a nil row is
// all NULL.
func TestDiff(t *testing.T) {
	post := int64(4)
	a := &sComment{ID: 1, PostID: &post}
	b := &sComment{ID: 1, Hidden: true}
	got := velox.Diff(a, b)
	if len(got) != 2 || got[0] != (velox.FieldChange{Name: "post_id", Old: int64(4), New: nil, Changed: true}) ||
		got[1] != (velox.FieldChange{Name: "hidden", Old: false, New: true, Changed: true}) {
		t.Errorf("Diff: %+v", got)
	}
	if got := velox.Diff(a, a); got != nil {
		t.Errorf("Diff of a row with itself: %+v", got)
	}
	got = velox.Diff(nil, b)
	if len(got) != 3 || got[0].Name != "id" || got[1].Name != "hidden" || got[2].Name != "locked" || got[2].New != false {
		t.Errorf("Diff(nil, b): %+v", got)
	}
}

// TestTrackSetsKey: a tracked update that assigns a key column follows the
// row to its new key; on MySQL the re-read asks for the new key. Its Key is
// the one before the write.
func TestTrackSetsKey(t *testing.T) {
	_, raw := pairDB(t)
	exec(t, raw, "INSERT INTO pairs (a, b, price) VALUES (1, 2, 5)")
	var got []velox.Change
	db := velox.Open(velox.SQLite, raw, velox.WithHooks[wgPair](tracking(&got, nil, velox.OpUpdate)))
	if _, err := db.Update[wgPair]().Where(wgPairs.Price.EQ(5)).Set(wgPairs.B.Set(3)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if s := showChanges(got); s != "[1 2]: b 2->3*\n" {
		t.Errorf("changes: %s", s)
	}
	e := &fakeconn.Exec{Results: [][][]any{{{int64(1), int64(2)}}, {{int64(1), int64(3)}}}, Affected: 1}
	fake := openFake(velox.MySQL, e, velox.WithHooks[wgPair](tracking(&got, nil, velox.OpUpdate)))
	if _, err := fake.Update[wgPair]().Where(wgPairs.Price.EQ(5)).Set(wgPairs.B.Set(3)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if e.Log[3] != "SELECT `a`, `b` FROM `pairs` WHERE (`a` = ? AND `b` = ?)" || !slices.Equal(e.ArgLog[3], []any{int64(1), int64(3)}) {
		t.Errorf("re-read: %s %v", e.Log[3], e.ArgLog[3])
	}
	if s := showChanges(got); s != "[1 2]: b 2->3*\n" {
		t.Errorf("MySQL changes: %s", s)
	}
}

// TestTrackRefusals: Track refuses, before anything is sent, an Add on a key
// column, which it could not follow, and an update that sets the key of a
// bidirectional one-to-one edge, whose partners velox re-points itself.
func TestTrackRefusals(t *testing.T) {
	var got []velox.Change
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e, velox.WithHooks[wgPair](tracking(&got, nil, velox.OpUpdate)), velox.WithHooks[bspouse](tracking(&got, nil, velox.OpUpdate)))
	_, err := db.Update[wgPair]().Where(wgPairs.A.EQ(1)).Set(wgPairs.B.Add(1)).Exec(ctx)
	if err == nil || !strings.Contains(err.Error(), "changes by Add or Sub") {
		t.Errorf("Add on a key: %v", err)
	}
	_, err = db.Update[bspouse]().Set(bspouses.SpouseID.Set(3)).Where(bspouses.ID.EQ(1)).Exec(ctx)
	if err == nil || !strings.Contains(err.Error(), "bidirectional one-to-one") {
		t.Errorf("bidirectional key: %v", err)
	}
	if len(e.Log) != 0 {
		t.Errorf("sent %q", e.Log)
	}
}

// TestChangesWithoutTrack: an update no hook tracked yields no Change.
func TestChangesWithoutTrack(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	n := -1
	hook := velox.On(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			err := next.Mutate(ctx, m)
			n = len(slices.Collect(m.Changes()))
			return err
		})
	}, velox.OpUpdate)
	db := openFake(velox.Postgres, e, velox.WithHooks[sPost](hook))
	if _, err := db.Update[sPost]().Where(sPosts.ID.EQ(1)).Set(sPosts.Title.Set("q")).Exec(ctx); err != nil || n != 0 || len(e.Log) != 1 {
		t.Errorf("%v; %d changes; sent %q", err, n, e.Log)
	}
}
