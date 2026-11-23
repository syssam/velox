package apitest

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox"
)

// uPost is s_posts with an UpdateDefault on title, standing in for an
// updated_at column, and the comments edge (the key on the comment).
type uPost struct {
	ID       int64
	AuthorID int64
	Title    string
	Comments velox.Many[sComment]
}

var uPosts = struct {
	ID       velox.NumberCol[uPost, int64]
	Title    velox.StringCol[uPost, string]
	Comments velox.Rel[uPost, sComment]
}{velox.NumberCol[uPost, int64]{I: 0}, velox.StringCol[uPost, string]{I: 2}, velox.Rel[uPost, sComment]{I: 0}}

var uPostMeta velox.MetaOnce[uPost]

func (*uPost) VeloxMeta() *velox.Meta[uPost] {
	return uPostMeta.Load(func() velox.Descriptor[uPost] {
		return velox.Descriptor[uPost]{
			Table: "s_posts",
			Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "AuthorID", Name: "author_id"},
				{Field: "Title", Name: "title", UpdateDefault: func() any { return "touched" }}},
			PrimaryKey: []int{0},
			Edges:      []velox.EdgeDescriptor{velox.EdgeTo[sComment]("comments", 0, 1)},
			FieldPtr: func(x *uPost, i int) any {
				return [...]any{&x.ID, &x.AuthorID, &x.Title}[i]
			},
			EdgeFieldPtr: func(x *uPost, _ int) any { return &x.Comments },
		}
	})
}

// TestEdgeOnlyUpdateRunsHooksAndUpdateDefaults: an update of only edge
// setters runs its hooks and its UpdateDefaults, as ent's does when a
// relation changes, so the title's UpdateDefault is written, through Exec,
// SQL and One; Exec returns the rows whose edges it wrote (ADR 0136).
func TestEdgeOnlyUpdateRunsHooksAndUpdateDefaults(t *testing.T) {
	ran := 0
	count := velox.Before(func(_ context.Context, m *velox.Mutation) error {
		if m.Entity() == "uPost" {
			ran++
		}
		return nil
	}, velox.OpUpdate)
	_, raw := edgeDB(t)
	db := velox.Open(velox.SQLite, raw, velox.WithHooksAll(count))
	exec(t, raw, "INSERT INTO s_posts (id, author_id, title) VALUES (1, 1, 'p')")
	exec(t, raw, "INSERT INTO s_comments (id, post_id) VALUES (1, NULL), (2, NULL)")
	title := func() string {
		var s string
		if err := raw.QueryRow("SELECT title FROM s_posts WHERE id = 1").Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	n, err := db.Update[uPost]().Set(uPosts.Comments.Add(1)).Where(uPosts.ID.EQ(1)).Exec(user)
	if err != nil || n != 1 || ran != 1 || title() != "touched" {
		t.Errorf("Exec: %d, %v, %d hook runs, title %q", n, err, ran, title())
	}
	want64(t, "linked", owners(t, raw, "s_comments"), 1, -1)
	q, _, err := db.Update[uPost]().Set(uPosts.Comments.Add(2)).Where(uPosts.ID.EQ(1)).SQL(user)
	if want := `UPDATE "s_posts" SET "title" = ? WHERE "id" = ?`; err != nil || q != want || ran != 2 {
		t.Errorf("SQL: %q, %v, %d hook runs; want %s", q, err, ran, want)
	}
	exec(t, raw, "UPDATE s_posts SET title = 'p'")
	got, err := db.Update[uPost]().Set(uPosts.Comments.Add(2)).Where(uPosts.ID.EQ(1)).One(user)
	if err != nil || got.Title != "touched" || ran != 3 {
		t.Errorf("One: %+v, %v, %d hook runs", got, err, ran)
	}
	want64(t, "linked by One", owners(t, raw, "s_comments"), 1, 1)
}

// TestEmptyPatchOneHidden: an empty patch sends nothing and asks no
// policy about a write, but its One reads the row as Writable, so a row the
// Write policy hides (a locked comment) is ErrNotFound, and one it lets the
// caller change is returned (ADR 0136).
func TestEmptyPatchOneHidden(t *testing.T) {
	db, raw := edgeDB(t)
	exec(t, raw, "INSERT INTO s_comments (id, post_id, locked) VALUES (1, NULL, 1), (2, NULL, 0)")
	id := velox.NumberCol[sComment, int64]{I: 0}
	locked := velox.Col[sComment, bool]{I: 3}
	if _, err := db.Update[sComment]().Set(locked.SetPtr(nil)).Where(id.EQ(1)).One(user); !errors.Is(err, velox.ErrNotFound) {
		t.Errorf("One of a row the Write policy hides: %v", err)
	}
	got, err := db.Update[sComment]().Set(locked.SetPtr(nil)).Where(id.EQ(2)).One(user)
	if err != nil || got.ID != 2 {
		t.Errorf("One of a writable row: %+v, %v", got, err)
	}
}
