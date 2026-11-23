package apitest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
)

// The handles of sComment, for the UpdateQuery.One tests.
var sComments = struct {
	ID     velox.NumberCol[sComment, int64]
	Hidden velox.Col[sComment, bool]
	Locked velox.Col[sComment, bool]
}{velox.NumberCol[sComment, int64]{I: 0}, velox.Col[sComment, bool]{I: 2}, velox.Col[sComment, bool]{I: 3}}

// TestUpdateOne: One writes the one row it matches and returns it as
// written, the whole row, even when the update moves it out of the Read
// policy's view.
func TestUpdateOne(t *testing.T) {
	db, raw := edgeDB(t)
	exec(t, raw, "INSERT INTO s_posts (id, author_id, title) VALUES (1, 7, 'a'), (2, 7, 'b')")
	exec(t, raw, "INSERT INTO s_comments (id, post_id) VALUES (1, 1), (2, 1)")
	exec(t, raw, "INSERT INTO s_tags (id, name) VALUES (1, 'x')")

	p, err := db.Update[sPost]().Where(sPosts.ID.EQ(1)).Set(sPosts.Title.Set("q"), sPosts.Tags.Add(1)).One(user)
	if err != nil || p.ID != 1 || p.AuthorID != 7 || p.Title != "q" {
		t.Fatalf("One: %+v, %v", p, err)
	}
	want64(t, "tags", postTags(t, raw, 1), 1)

	// The update hides the comment from Read; One still returns it.
	c, err := db.Update[sComment]().Where(sComments.ID.EQ(1)).Set(sComments.Hidden.Set(true)).One(user)
	if err != nil || c.ID != 1 || !c.Hidden || c.PostID == nil || *c.PostID != 1 {
		t.Fatalf("One that hides the row: %+v, %v", c, err)
	}

	// A condition that names the row by a column the update changes.
	p, err = db.Update[sPost]().Where(sPosts.Title.EQ("b")).Set(sPosts.Title.Set("c")).One(user)
	if err != nil || p.ID != 2 || p.Title != "c" {
		t.Fatalf("One by the changed column: %+v, %v", p, err)
	}

	// No setter: the row as it is, read under the policies.
	p, err = db.Update[sPost]().Where(sPosts.ID.EQ(2)).One(user)
	if err != nil || p.Title != "c" {
		t.Fatalf("One with no setter: %+v, %v", p, err)
	}
	if _, err := db.Update[sComment]().Where(sComments.ID.EQ(1)).One(user); !velox.IsNotFound(err) {
		t.Errorf("One with no setter of a hidden row: %v", err)
	}
}

// TestUpdateOneRefused: One writes nothing when its conditions match no
// row the caller may change, or more than one row.
func TestUpdateOneRefused(t *testing.T) {
	db, raw := edgeDB(t)
	exec(t, raw, "INSERT INTO s_posts (id, author_id, title) VALUES (1, 7, 'a'), (2, 7, 'a')")
	exec(t, raw, "INSERT INTO s_comments (id, post_id, hidden, locked) VALUES (1, 1, 1, 0), (2, 1, 0, 1)")
	exec(t, raw, "INSERT INTO s_tags (id, name) VALUES (1, 'x')")

	if _, err := db.Update[sPost]().Where(sPosts.ID.EQ(9)).Set(sPosts.Title.Set("q")).One(user); !velox.IsNotFound(err) {
		t.Errorf("no row: %v", err)
	}
	_, err := db.Update[sPost]().Where(sPosts.Title.EQ("a")).Set(sPosts.Title.Set("q"), sPosts.Tags.Add(1)).One(user)
	if !errors.Is(err, velox.ErrNotSingular) {
		t.Errorf("two rows: %v", err)
	}
	if got := int64s(t, raw, "SELECT count(*) FROM s_posts WHERE title = 'q'"); got[0] != 0 {
		t.Errorf("two rows: %d written", got[0])
	}
	want64(t, "tags after two rows", postTags(t, raw, 1))
	if _, err := db.Update[sComment]().Where(sComments.ID.EQ(1)).Set(sComments.Locked.Set(true)).One(user); !velox.IsNotFound(err) {
		t.Errorf("a row hidden from Read: %v", err)
	}
	if _, err := db.Update[sComment]().Where(sComments.ID.EQ(2)).Set(sComments.Hidden.Set(true)).One(user); !velox.IsNotFound(err) {
		t.Errorf("a row hidden from Write: %v", err)
	}
	if got := int64s(t, raw, "SELECT count(*) FROM s_comments WHERE hidden = locked"); got[0] != 0 {
		t.Errorf("a refused row was written: %d", got[0])
	}
	if _, err := db.Update[sPost]().Set(sPosts.Title.Set("q")).One(user); !errors.Is(err, velox.ErrNoWhere) {
		t.Errorf("no Where: %v", err)
	}
	if _, err := db.Update[sPost]().Where(sPosts.ID.EQ(1)).Set(sPosts.Title.Set("q")).One(context.WithValue(user, sSkip{}, 1)); err != nil {
		t.Errorf("a hook that does not call next: %v", err)
	}
}

// TestUpdateOnePostgresStatements: on PostgreSQL, One locks the row it
// reads the key of, narrows the UPDATE to that key and reads the row back
// by it, in one transaction.
func TestUpdateOnePostgresStatements(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{{{int64(1)}}, {{int64(1), int64(7), "q"}}}, Affected: 1}
	db := openFake(velox.Postgres, e)
	p, err := db.Update[sPost]().Where(sPosts.Title.EQ("a")).Set(sPosts.Title.Set("q")).One(user)
	if err != nil || p.ID != 1 || p.Title != "q" {
		t.Fatalf("One: %+v, %v", p, err)
	}
	want := []string{
		`SELECT "id" FROM "s_posts" WHERE "title" = $1 LIMIT 2 FOR UPDATE`,
		`UPDATE "s_posts" SET "title" = $1 WHERE "id" = $2`,
		`SELECT "id", "author_id", "title" FROM "s_posts" WHERE "id" = $1 LIMIT 2`,
	}
	if got := slices.DeleteFunc(slices.Clone(e.Log), func(s string) bool { return !strings.HasPrefix(s, "SELECT") && !strings.HasPrefix(s, "UPDATE") }); !slices.Equal(got, want) {
		t.Errorf("statements:\n%s\nwant:\n%s\nall: %q", strings.Join(got, "\n"), strings.Join(want, "\n"), e.Log)
	}
}
