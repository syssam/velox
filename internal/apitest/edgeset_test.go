package apitest

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
)

// The entities of the edge setter tests: an sPost has an author (a key on
// its own row), tags (many-to-many), comments (a key on the comment's row)
// and a cover (one-to-one, the key on the cover's row). A tag, a comment and
// a cover the context does not reveal are hidden from Read; a locked
// comment or cover is hidden from Write.

type sUser struct {
	ID   int64
	Name string
}

var sUserMeta velox.MetaOnce[sUser]

func (*sUser) VeloxMeta() *velox.Meta[sUser] { return sUserMeta.Load(sUserDescriptor) }

// Policy hides the user named "hidden" from Read.
func (*sUser) Policy() velox.Policy {
	return velox.Policy{Read: func(context.Context) (velox.Predicate, error) { return velox.FieldNEQ("name", "hidden"), nil }}
}

func sUserDescriptor() velox.Descriptor[sUser] {
	return velox.Descriptor[sUser]{
		Schema:     (*sUser)(nil),
		Table:      "s_users",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}},
		PrimaryKey: []int{0},
		FieldPtr: func(x *sUser, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.Name
		},
	}
}

type sPost struct {
	ID       int64
	AuthorID int64
	Title    string
	Author   velox.One[sUser]
	Tags     velox.Many[sTag]
	Comments velox.Many[sComment]
	Cover    velox.One[sCover]
	Spine    velox.One[sSpine]
}

var sPosts = struct {
	ID       velox.NumberCol[sPost, int64]
	AuthorID velox.NumberCol[sPost, int64]
	Title    velox.StringCol[sPost, string]
	Author   velox.Rel[sPost, sUser]
	Tags     velox.Rel[sPost, sTag]
	Comments velox.Rel[sPost, sComment]
	Cover    velox.Rel[sPost, sCover]
	Spine    velox.Rel[sPost, sSpine]
}{
	velox.NumberCol[sPost, int64]{I: 0}, velox.NumberCol[sPost, int64]{I: 1}, velox.StringCol[sPost, string]{I: 2},
	velox.Rel[sPost, sUser]{I: 0}, velox.Rel[sPost, sTag]{I: 1}, velox.Rel[sPost, sComment]{I: 2}, velox.Rel[sPost, sCover]{I: 3},
	velox.Rel[sPost, sSpine]{I: 4},
}

var sPostMeta velox.MetaOnce[sPost]

// sSkip is the context key under which sPost's hook does not call next.
type sSkip struct{}

func (*sPost) Hooks() []velox.Hook {
	return []velox.Hook{func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			if ctx.Value(sSkip{}) != nil {
				return nil
			}
			return next.Mutate(ctx, m)
		})
	}}
}

func (*sPost) VeloxMeta() *velox.Meta[sPost] { return sPostMeta.Load(sPostDescriptor) }

func sPostDescriptor() velox.Descriptor[sPost] {
	return velox.Descriptor[sPost]{
		Schema:     (*sPost)(nil),
		Table:      "s_posts",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "AuthorID", Name: "author_id"}, {Field: "Title", Name: "title"}},
		PrimaryKey: []int{0},
		Edges: []velox.EdgeDescriptor{
			velox.EdgeTo[sUser]("author", 1, 0),
			velox.EdgeThrough[sPostTag, sTag]("tags", 0, 0, 1, 0),
			velox.EdgeTo[sComment]("comments", 0, 1),
			velox.EdgeTo[sCover]("cover", 0, 1),
			velox.EdgeTo[sSpine]("spine", 0, 1),
		},
		FieldPtr: func(x *sPost, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.AuthorID
			}
			return &x.Title
		},
		EdgeFieldPtr: func(x *sPost, i int) any {
			switch i {
			case 0:
				return &x.Author
			case 1:
				return &x.Tags
			case 2:
				return &x.Comments
			case 3:
				return &x.Cover
			}
			return &x.Spine
		},
	}
}

type sTag struct {
	ID     int64
	Name   string
	Hidden bool
}

var sTagMeta velox.MetaOnce[sTag]

func (*sTag) VeloxMeta() *velox.Meta[sTag] { return sTagMeta.Load(sTagDescriptor) }

func (*sTag) Policy() velox.Policy { return sHiddenPolicy(false) }

func sTagDescriptor() velox.Descriptor[sTag] {
	return velox.Descriptor[sTag]{
		Schema:     (*sTag)(nil),
		Table:      "s_tags",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}, {Field: "Hidden", Name: "hidden"}},
		PrimaryKey: []int{0},
		FieldPtr: func(x *sTag, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.Name
			}
			return &x.Hidden
		},
	}
}

type sPostTag struct {
	PostID int64
	TagID  int64
}

var sPostTagMeta velox.MetaOnce[sPostTag]

func (*sPostTag) VeloxMeta() *velox.Meta[sPostTag] { return sPostTagMeta.Load(sPostTagDescriptor) }

func sPostTagDescriptor() velox.Descriptor[sPostTag] {
	return velox.Descriptor[sPostTag]{
		Table:      "s_post_tags",
		Columns:    []velox.ColumnDescriptor{{Field: "PostID", Name: "post_id"}, {Field: "TagID", Name: "tag_id"}},
		PrimaryKey: []int{0, 1},
		FieldPtr: func(x *sPostTag, i int) any {
			if i == 0 {
				return &x.PostID
			}
			return &x.TagID
		},
	}
}

type sComment struct {
	ID     int64
	PostID *int64
	Hidden bool
	Locked bool
}

var sCommentMeta velox.MetaOnce[sComment]

func (*sComment) VeloxMeta() *velox.Meta[sComment] { return sCommentMeta.Load(sCommentDescriptor) }

func (*sComment) Policy() velox.Policy { return sHiddenPolicy(true) }

func sCommentDescriptor() velox.Descriptor[sComment] {
	return velox.Descriptor[sComment]{
		Schema:     (*sComment)(nil),
		Table:      "s_comments",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "PostID", Name: "post_id"}, {Field: "Hidden", Name: "hidden"}, {Field: "Locked", Name: "locked"}},
		PrimaryKey: []int{0},
		FieldPtr: func(x *sComment, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.PostID
			case 2:
				return &x.Hidden
			}
			return &x.Locked
		},
	}
}

type sCover struct {
	ID     int64
	PostID *int64
	Hidden bool
	Locked bool
}

var sCoverMeta velox.MetaOnce[sCover]

func (*sCover) VeloxMeta() *velox.Meta[sCover] { return sCoverMeta.Load(sCoverDescriptor) }

func (*sCover) Policy() velox.Policy { return sHiddenPolicy(true) }

func sCoverDescriptor() velox.Descriptor[sCover] {
	return velox.Descriptor[sCover]{
		Schema:     (*sCover)(nil),
		Table:      "s_covers",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "PostID", Name: "post_id"}, {Field: "Hidden", Name: "hidden"}, {Field: "Locked", Name: "locked"}},
		PrimaryKey: []int{0},
		FieldPtr: func(x *sCover, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.PostID
			case 2:
				return &x.Hidden
			}
			return &x.Locked
		},
	}
}

// sSpine is the other side of sPost's one-to-one edge spine, whose
// NULL-able key, post_id, is of the Required edge post: velox refuses it
// set to NULL.
type sSpine struct {
	ID     int64
	PostID *int64
}

var sSpineMeta velox.MetaOnce[sSpine]

func (*sSpine) VeloxMeta() *velox.Meta[sSpine] { return sSpineMeta.Load(sSpineDescriptor) }

func sSpineDescriptor() velox.Descriptor[sSpine] {
	return velox.Descriptor[sSpine]{
		Table:      "s_spines",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "PostID", Name: "post_id", RequiredEdge: "post"}},
		PrimaryKey: []int{0},
		FieldPtr: func(x *sSpine, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.PostID
		},
	}
}

// sHiddenPolicy hides a hidden row from Read and, when locks, a locked row
// from Write.
func sHiddenPolicy(locks bool) velox.Policy {
	p := velox.Policy{Read: func(context.Context) (velox.Predicate, error) { return velox.FieldEQ("hidden", false), nil }}
	if locks {
		p.Write = func(context.Context) (velox.Predicate, error) { return velox.FieldEQ("locked", false), nil }
	}
	return p
}

var sDDL = []string{
	`CREATE TABLE s_users (id integer PRIMARY KEY, name text NOT NULL)`,
	`CREATE TABLE s_posts (id integer PRIMARY KEY, author_id integer NOT NULL, title text NOT NULL)`,
	`CREATE TABLE s_tags (id integer PRIMARY KEY, name text NOT NULL, hidden boolean NOT NULL DEFAULT 0)`,
	`CREATE TABLE s_post_tags (post_id integer NOT NULL REFERENCES s_posts (id), tag_id integer NOT NULL REFERENCES s_tags (id), PRIMARY KEY (post_id, tag_id))`,
	`CREATE TABLE s_comments (id integer PRIMARY KEY, post_id integer REFERENCES s_posts (id), hidden boolean NOT NULL DEFAULT 0, locked boolean NOT NULL DEFAULT 0)`,
	`CREATE TABLE s_covers (id integer PRIMARY KEY, post_id integer UNIQUE REFERENCES s_posts (id), hidden boolean NOT NULL DEFAULT 0, locked boolean NOT NULL DEFAULT 0)`,
	`CREATE TABLE s_spines (id integer PRIMARY KEY, post_id integer UNIQUE REFERENCES s_posts (id))`,
}

// edgeDB opens a SQLite database with the tables of the edge setter tests,
// and returns it with its pool for inspection.
func edgeDB(t *testing.T) (*velox.DB, *sql.DB) {
	t.Helper()
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "edges.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	for _, s := range sDDL {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return velox.Open(velox.SQLite, raw), raw
}

// exec runs SQL on raw.
func exec(t *testing.T, raw *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := raw.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// int64s reads one integer column with raw: NULL is -1.
func int64s(t *testing.T, raw *sql.DB, q string, args ...any) []int64 {
	t.Helper()
	rows, err := raw.Query(q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer func() { _ = rows.Close() }()
	out := []int64{}
	for rows.Next() {
		var v sql.NullInt64
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		if !v.Valid {
			v.Int64 = -1
		}
		out = append(out, v.Int64)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func want64(t *testing.T, what string, got []int64, want ...int64) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s: %v, want %v", what, got, want)
	}
}

// postTags are the tags of post id, by the join rows.
func postTags(t *testing.T, raw *sql.DB, id int64) []int64 {
	t.Helper()
	return int64s(t, raw, "SELECT tag_id FROM s_post_tags WHERE post_id = ? ORDER BY tag_id", id)
}

// owners are the post_id of each row of table, in id order.
func owners(t *testing.T, raw *sql.DB, table string) []int64 {
	t.Helper()
	return int64s(t, raw, "SELECT post_id FROM "+table+" ORDER BY id")
}

// edgeErr returns err's *EdgeError, failing unless it is one of edge, of
// keys and matching target.
func edgeErr(t *testing.T, err error, edge string, target error, keys ...any) {
	t.Helper()
	var ee *velox.EdgeError
	if !errors.As(err, &ee) {
		t.Fatalf("error %v (%T), want an *EdgeError", err, err)
	}
	if ee.Edge != edge || !errors.Is(err, target) || !reflect.DeepEqual(ee.Keys, keys) {
		t.Errorf("EdgeError %+v (%v), want edge %s, %v, keys %v", ee, err, edge, target, keys)
	}
}

// user is a context under the policies.
var user = context.Background()

// TestEdgeSetterThrough: a many-to-many edge's Add links on insert and
// update, Remove and Clear unlink, in that order whatever the setters'
// order, and an update of only edge setters sends no UPDATE.
func TestEdgeSetterThrough(t *testing.T) {
	db, raw := edgeDB(t)
	exec(t, raw, "INSERT INTO s_tags (id, name) VALUES (1, 'a'), (2, 'b'), (3, 'c')")
	p := sPost{AuthorID: 1, Title: "p"}
	two := int64(2)
	if err := db.Insert(&p).Set(sPosts.Tags.Add(1, &two, int32(2))).Exec(user); err != nil {
		t.Fatal(err)
	}
	want64(t, "after insert", postTags(t, raw, p.ID), 1, 2)

	n, err := db.Update[sPost]().Set(sPosts.Tags.Add(3, 2), sPosts.Tags.Remove(1)).Where(sPosts.ID.EQ(p.ID)).Exec(user)
	if err != nil || n != 1 {
		t.Fatalf("update: %d, %v", n, err)
	}
	want64(t, "after add and remove", postTags(t, raw, p.ID), 2, 3)

	// Clear then Add, whatever the order given.
	if _, err := db.Update[sPost]().Set(sPosts.Tags.Add(1), sPosts.Tags.Clear()).Where(sPosts.ID.EQ(p.ID)).Exec(user); err != nil {
		t.Fatal(err)
	}
	want64(t, "after clear and add", postTags(t, raw, p.ID), 1)

	// A title and an edge in one update.
	if _, err := db.Update[sPost]().Set(sPosts.Title.Set("q"), sPosts.Tags.Clear()).Where(sPosts.ID.EQ(p.ID)).Exec(user); err != nil {
		t.Fatal(err)
	}
	want64(t, "after clear", postTags(t, raw, p.ID))
	got, err := db.Query[sPost]().Where(sPosts.ID.EQ(p.ID)).One(ctx)
	if err != nil || got.Title != "q" {
		t.Errorf("title: %+v, %v", got, err)
	}

	// An update of edges alone renders nothing, and needs a Where.
	q := db.Update[sPost]().Set(sPosts.Tags.Add(1)).Where(sPosts.ID.EQ(p.ID))
	if s, args, err := q.SQL(user); s != "" || args != nil || err != nil {
		t.Errorf("SQL of edges alone: %q %v %v", s, args, err)
	}
	if _, err := db.Update[sPost]().Set(sPosts.Tags.Add(1)).Exec(user); !errors.Is(err, velox.ErrNoWhere) {
		t.Errorf("no Where: %v", err)
	}
	if _, _, err := db.Update[sPost]().Set(sPosts.Tags.Add(1)).SQL(user); !errors.Is(err, velox.ErrNoWhere) {
		t.Errorf("SQL without Where: %v", err)
	}
	if _, err := db.Update[sPost]().Where(sPosts.ID.EQ(p.ID)).Exec(user); err == nil || !strings.Contains(err.Error(), "Update has no Set") {
		t.Errorf("no setter: %v", err)
	}
	if c := sPosts.Tags.Add(1).Column(); c != -1 {
		t.Errorf("an edge setter's Column: %d", c)
	}
}

// TestEdgeSetterThroughHidden: Add of a tag the caller may not read fails,
// and the insert with it is undone; Remove and Clear leave a link to a
// hidden tag.
func TestEdgeSetterThroughHidden(t *testing.T) {
	db, raw := edgeDB(t)
	exec(t, raw, "INSERT INTO s_tags (id, name, hidden) VALUES (1, 'a', 0), (2, 'b', 1)")
	p := sPost{AuthorID: 1, Title: "p"}
	err := db.Insert(&p).Set(sPosts.Tags.Add(1, 2, 9)).Exec(user)
	edgeErr(t, err, "tags", velox.ErrNotFound, int64(2), int64(9))
	if n := int64s(t, raw, "SELECT COUNT(*) FROM s_posts"); n[0] != 0 {
		t.Errorf("the insert was kept: %v posts", n)
	}
	if n := int64s(t, raw, "SELECT COUNT(*) FROM s_post_tags"); n[0] != 0 {
		t.Errorf("join rows were kept: %v", n)
	}

	p = sPost{AuthorID: 1, Title: "p"}
	if err := db.Insert(&p).Set(sPosts.Tags.Add(1)).Exec(user); err != nil {
		t.Fatal(err)
	}
	exec(t, raw, "INSERT INTO s_post_tags (post_id, tag_id) VALUES (?, 2)", p.ID)
	if _, err := db.Update[sPost]().Set(sPosts.Tags.Remove(1, 2)).Where(sPosts.ID.EQ(p.ID)).Exec(user); err != nil {
		t.Fatal(err)
	}
	want64(t, "after remove", postTags(t, raw, p.ID), 2)
	exec(t, raw, "INSERT INTO s_post_tags (post_id, tag_id) VALUES (?, 1)", p.ID)
	if _, err := db.Update[sPost]().Set(sPosts.Tags.Clear()).Where(sPosts.ID.EQ(p.ID)).Exec(user); err != nil {
		t.Fatal(err)
	}
	want64(t, "after clear", postTags(t, raw, p.ID), 2)
}

// TestEdgeSetterThroughClearPages: Clear reads the links a page at a time,
// and a page of hidden targets does not stop it.
func TestEdgeSetterThroughClearPages(t *testing.T) {
	db, raw := edgeDB(t)
	exec(t, raw, "INSERT INTO s_posts (id, author_id, title) VALUES (1, 1, 'p')")
	n := velox.EdgePage*2 + 7
	tx, err := raw.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		hidden := i <= velox.EdgePage // the first page is all hidden
		if _, err := tx.Exec("INSERT INTO s_tags (id, name, hidden) VALUES (?, 't', ?)", i, hidden); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec("INSERT INTO s_post_tags (post_id, tag_id) VALUES (1, ?)", i); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Update[sPost]().Set(sPosts.Tags.Clear()).Where(sPosts.ID.EQ(1)).Exec(user); err != nil {
		t.Fatal(err)
	}
	if got := len(postTags(t, raw, 1)); got != velox.EdgePage {
		t.Errorf("%d links left, want the %d hidden ones", got, velox.EdgePage)
	}
}

// TestEdgeSetterManyOwners: an update's edges are written for every row it
// matches; Add of an edge whose key is on the target fails for several.
func TestEdgeSetterManyOwners(t *testing.T) {
	db, raw := edgeDB(t)
	exec(t, raw, "INSERT INTO s_tags (id, name) VALUES (1, 'a')")
	exec(t, raw, "INSERT INTO s_posts (id, author_id, title) VALUES (1, 1, 'p'), (2, 1, 'p'), (3, 1, 'other')")
	exec(t, raw, "INSERT INTO s_comments (id) VALUES (1)")
	n, err := db.Update[sPost]().Set(sPosts.Tags.Add(1)).Where(sPosts.Title.EQ("p")).Exec(user)
	if err != nil || n != 2 {
		t.Fatalf("update: %d, %v", n, err)
	}
	want64(t, "post 1", postTags(t, raw, 1), 1)
	want64(t, "post 2", postTags(t, raw, 2), 1)
	want64(t, "post 3", postTags(t, raw, 3))

	_, err = db.Update[sPost]().Set(sPosts.Comments.Add(1)).Where(sPosts.Title.EQ("p")).Exec(user)
	if err == nil || !strings.Contains(err.Error(), "Add for 2 owners at once") {
		t.Errorf("Add to many owners: %v", err)
	}
	want64(t, "comment", owners(t, raw, "s_comments"), -1)

	// Clear and Remove for several owners are allowed.
	exec(t, raw, "UPDATE s_comments SET post_id = 2")
	if _, err := db.Update[sPost]().Set(sPosts.Comments.Clear()).Where(sPosts.Title.EQ("p")).Exec(user); err != nil {
		t.Fatal(err)
	}
	want64(t, "comment after clear", owners(t, raw, "s_comments"), -1)

	xs := []sPost{{ID: 10, AuthorID: 1, Title: "a"}, {ID: 11, AuthorID: 1, Title: "b"}}
	if err := db.InsertAll(xs).Set(sPosts.Comments.Add(1)).Exec(user); err == nil || !strings.Contains(err.Error(), "Add for 2 owners at once") {
		t.Errorf("InsertAll Add to many owners: %v", err)
	}
	if err := db.InsertAll(xs).Set(sPosts.Tags.Add(1)).Exec(user); err != nil {
		t.Fatal(err)
	}
	want64(t, "post 10", postTags(t, raw, 10), 1)
	want64(t, "post 11", postTags(t, raw, 11), 1)
}

// TestEdgeSetterChildren: an edge whose key is on the target re-points the
// children no owner holds; Remove and Clear set NULL on those the caller
// may change.
func TestEdgeSetterChildren(t *testing.T) {
	db, raw := edgeDB(t)
	exec(t, raw, "INSERT INTO s_posts (id, author_id, title) VALUES (100, 1, 'other')")
	exec(t, raw, "INSERT INTO s_comments (id, post_id, hidden, locked) VALUES (1, NULL, 0, 0), (2, NULL, 0, 0), (3, NULL, 0, 0), (4, 100, 0, 0), (5, NULL, 1, 0), (6, NULL, 0, 1)")
	p := sPost{AuthorID: 1, Title: "p"}
	if err := db.Insert(&p).Set(sPosts.Comments.Add(1, 2)).Exec(user); err != nil {
		t.Fatal(err)
	}
	want64(t, "after insert", owners(t, raw, "s_comments"), p.ID, p.ID, -1, 100, -1, -1)

	// One already linked here is a link that exists.
	if _, err := db.Update[sPost]().Set(sPosts.Comments.Remove(1), sPosts.Comments.Add(2, 3)).Where(sPosts.ID.EQ(p.ID)).Exec(user); err != nil {
		t.Fatal(err)
	}
	want64(t, "after remove and add", owners(t, raw, "s_comments"), -1, p.ID, p.ID, 100, -1, -1)

	add := func(k int64) error {
		_, err := db.Update[sPost]().Set(sPosts.Comments.Add(k)).Where(sPosts.ID.EQ(p.ID)).Exec(user)
		return err
	}
	edgeErr(t, add(4), "comments", velox.ErrLinked, int64(4))
	edgeErr(t, add(5), "comments", velox.ErrNotFound, int64(5)) // hidden from Read
	edgeErr(t, add(6), "comments", velox.ErrNotFound, int64(6)) // hidden from Write
	edgeErr(t, add(9), "comments", velox.ErrNotFound, int64(9))
	want64(t, "after the refused adds", owners(t, raw, "s_comments"), -1, p.ID, p.ID, 100, -1, -1)

	// Remove leaves another owner's child; Clear leaves a locked one.
	if _, err := db.Update[sPost]().Set(sPosts.Comments.Remove(4)).Where(sPosts.ID.EQ(p.ID)).Exec(user); err != nil {
		t.Fatal(err)
	}
	exec(t, raw, "UPDATE s_comments SET post_id = ? WHERE id = 6", p.ID)
	if _, err := db.Update[sPost]().Set(sPosts.Comments.Clear()).Where(sPosts.ID.EQ(p.ID)).Exec(user); err != nil {
		t.Fatal(err)
	}
	want64(t, "after clear", owners(t, raw, "s_comments"), -1, -1, -1, 100, -1, p.ID)
}

// TestEdgeSetterRequiredOneChild: a one-to-one edge whose key is of a
// Required edge links a child no owner holds: with no child held, nothing
// is let go, so the key is never set to NULL. Letting go of a held child is
// refused, as it would leave the child's key NULL, and the link stays.
func TestEdgeSetterRequiredOneChild(t *testing.T) {
	db, raw := edgeDB(t)
	exec(t, raw, "INSERT INTO s_spines (id, post_id) VALUES (1, NULL), (2, NULL)")
	p := sPost{AuthorID: 1, Title: "p"}
	if err := db.Insert(&p).Set(sPosts.Spine.Add(1)).Exec(user); err != nil {
		t.Fatal(err)
	}
	want64(t, "after insert", owners(t, raw, "s_spines"), p.ID, -1)
	_, err := db.Update[sPost]().Set(sPosts.Spine.Add(2)).Where(sPosts.ID.EQ(p.ID)).Exec(user)
	if err == nil || !strings.Contains(err.Error(), `missing required edge "sSpine.post"`) {
		t.Errorf("replace: %v", err)
	}
	want64(t, "after the refused replace", owners(t, raw, "s_spines"), p.ID, -1)
}

// TestEdgeSetterOneChild: a one-to-one edge's Add lets go of the child the
// owner holds and links the new one; it takes one key; a held child the
// caller may not change fails with ErrLinked.
func TestEdgeSetterOneChild(t *testing.T) {
	db, raw := edgeDB(t)
	exec(t, raw, "INSERT INTO s_covers (id, post_id, hidden, locked) VALUES (1, NULL, 0, 0), (2, NULL, 0, 0), (3, NULL, 0, 0), (4, NULL, 1, 0)")
	p := sPost{AuthorID: 1, Title: "p"}
	if err := db.Insert(&p).Set(sPosts.Cover.Add(1)).Exec(user); err != nil {
		t.Fatal(err)
	}
	want64(t, "after insert", owners(t, raw, "s_covers"), p.ID, -1, -1, -1)
	set := func(ss ...velox.Setter[sPost]) error {
		_, err := db.Update[sPost]().Set(ss...).Where(sPosts.ID.EQ(p.ID)).Exec(user)
		return err
	}
	if err := set(sPosts.Cover.Add(2)); err != nil {
		t.Fatal(err)
	}
	want64(t, "after replace", owners(t, raw, "s_covers"), -1, p.ID, -1, -1)
	if err := set(sPosts.Cover.Add(2)); err != nil {
		t.Fatal(err)
	}
	want64(t, "after the same", owners(t, raw, "s_covers"), -1, p.ID, -1, -1)
	if err := set(sPosts.Cover.Add(1, 3)); err == nil || !strings.Contains(err.Error(), "one-to-one") {
		t.Errorf("two keys: %v", err)
	}
	edgeErr(t, set(sPosts.Cover.Add(4)), "cover", velox.ErrNotFound, int64(4))

	exec(t, raw, "UPDATE s_covers SET locked = 1 WHERE id = 2")
	edgeErr(t, set(sPosts.Cover.Add(3)), "cover", velox.ErrLinked, int64(3))
	want64(t, "after the refused replace", owners(t, raw, "s_covers"), -1, p.ID, -1, -1)

	exec(t, raw, "UPDATE s_covers SET locked = 0 WHERE id = 2")
	if err := set(sPosts.Cover.Clear(), sPosts.Cover.Add(3)); err != nil {
		t.Fatal(err)
	}
	want64(t, "after clear and add", owners(t, raw, "s_covers"), -1, -1, p.ID, -1)
	if err := set(sPosts.Cover.Remove(3)); err != nil {
		t.Fatal(err)
	}
	want64(t, "after remove", owners(t, raw, "s_covers"), -1, -1, -1, -1)
}

// TestEdgeSetterSetKey: Set of an edge whose key is on the owner's row sets
// that key once its target is one the caller may read, on insert and on
// update, for every row the update matches; a hidden or missing target is an
// *EdgeError of ErrNotFound, and nothing is written. Set of another edge, or
// of a nil key, fails before anything is sent, and Set with its column's own
// setter on update assigns the column twice.
func TestEdgeSetterSetKey(t *testing.T) {
	db, raw := edgeDB(t)
	exec(t, raw, "INSERT INTO s_users (id, name) VALUES (1, 'ann'), (2, 'bob'), (3, 'hidden')")
	p := sPost{Title: "p"}
	if err := db.Insert(&p).Set(sPosts.Author.Set(2)).Exec(user); err != nil {
		t.Fatal(err)
	}
	if p.AuthorID != 2 {
		t.Errorf("AuthorID %d, want 2", p.AuthorID)
	}
	want64(t, "after insert", int64s(t, raw, "SELECT author_id FROM s_posts"), 2)
	edgeErr(t, db.Insert(&sPost{Title: "q"}).Set(sPosts.Author.Set(int32(3))).Exec(user), "author", velox.ErrNotFound, int64(3))
	edgeErr(t, db.Insert(&sPost{Title: "q"}).Set(sPosts.Author.Set(9)).Exec(user), "author", velox.ErrNotFound, int64(9))
	want64(t, "after the refused inserts", int64s(t, raw, "SELECT COUNT(*) FROM s_posts"), 1)

	exec(t, raw, "INSERT INTO s_posts (id, author_id, title) VALUES (10, 2, 'r')")
	both := sPosts.ID.In(p.ID, 10)
	n, err := db.Update[sPost]().Set(sPosts.Author.Set(1)).Where(both).Exec(user)
	if err != nil || n != 2 {
		t.Fatalf("update: %d, %v", n, err)
	}
	want64(t, "after update", int64s(t, raw, "SELECT author_id FROM s_posts ORDER BY id"), 1, 1)
	_, err = db.Update[sPost]().Set(sPosts.Title.Set("x"), sPosts.Author.Set(3)).Where(both).Exec(user)
	edgeErr(t, err, "author", velox.ErrNotFound, int64(3))
	want64(t, "after the refused update", int64s(t, raw, "SELECT author_id FROM s_posts ORDER BY id"), 1, 1)
	if titles := int64s(t, raw, "SELECT COUNT(*) FROM s_posts WHERE title = 'x'"); titles[0] != 0 {
		t.Errorf("the refused update wrote %d titles", titles[0])
	}

	for name, s := range map[string]velox.Setter[sPost]{
		"a many-to-many edge":                sPosts.Tags.Set(1),
		"an edge whose key the target holds": sPosts.Cover.Set(1),
		"a nil key":                          sPosts.Author.Set(nil),
		"a nil pointer":                      sPosts.Author.Set((*int64)(nil)),
	} {
		if err := db.Insert(&sPost{Title: "s"}).Set(s).Exec(user); err == nil {
			t.Errorf("Set of %s: nil", name)
		}
		if _, err := db.Update[sPost]().Set(s).Where(both).Exec(user); err == nil {
			t.Errorf("update with Set of %s: nil", name)
		}
	}
	if _, err := db.Update[sPost]().Set(sPosts.Author.Set(1), sPosts.AuthorID.Set(2)).Where(both).Exec(user); err == nil || !strings.Contains(err.Error(), "assigned twice") {
		t.Errorf("Set with its column's setter: %v", err)
	}
	if c, want := sPosts.Author.Set(1).Column(), sPosts.AuthorID.Set(1).Column(); c != want {
		t.Errorf("Set's Column %d, want its key's, %d", c, want)
	}
	q, _, err := db.Update[sPost]().Set(sPosts.Author.Set(1)).Where(both).SQL(user)
	if err != nil || !strings.Contains(q, `"author_id" = ?`) {
		t.Errorf("SQL: %q, %v", q, err)
	}
	want64(t, "at the end", int64s(t, raw, "SELECT author_id FROM s_posts ORDER BY id"), 1, 1)

	// Set writes nothing after the rows, so an InsertAll of generated keys
	// takes it, as Add would not.
	if err := db.InsertAll([]sPost{{Title: "a"}, {Title: "b"}}).Set(sPosts.Author.Set(2)).Exec(user); err != nil {
		t.Fatal(err)
	}
	want64(t, "after InsertAll", int64s(t, raw, "SELECT author_id FROM s_posts ORDER BY id"), 1, 1, 2, 2)
}

// TestEdgeSetterRefused: an insert's Add or Sub, an edge whose key is on the
// owner's row, an upsert or an InsertAll of generated keys with edge
// setters, fail before anything is sent.
func TestEdgeSetterRefused(t *testing.T) {
	db, raw := edgeDB(t)
	p := sPost{AuthorID: 1, Title: "p"}
	if err := db.Insert(&p).Set(sPosts.AuthorID.Add(1)).Exec(user); err == nil || !strings.Contains(err.Error(), "Add or Sub") {
		t.Errorf("Add on insert: %v", err)
	}
	for name, s := range map[string]velox.Setter[sPost]{
		"Add": sPosts.Author.Add(1), "Remove": sPosts.Author.Remove(1), "Clear": sPosts.Author.Clear(),
	} {
		err := db.Insert(&p).Set(s).Exec(user)
		if err == nil || !strings.Contains(err.Error(), "set it with Set, or that column's handle") {
			t.Errorf("%s of a key on the row: %v", name, err)
		}
		if _, _, err := db.Insert(&p).Set(s).SQL(user); err == nil {
			t.Errorf("SQL of %s of a key on the row: nil", name)
		}
	}
	one := int64(1)
	for name, k := range map[string]any{"nil": nil, "nil pointer": (*int64)(nil), "text": "x"} {
		if err := db.Insert(&p).Set(sPosts.Tags.Add(one, k)).Exec(user); err == nil {
			t.Errorf("a %s key: nil", name)
		}
		if err := db.Insert(&p).Set(sPosts.Tags.Remove(k)).Exec(user); err == nil {
			t.Errorf("a %s key to remove: nil", name)
		}
	}
	if err := db.Insert(&p).Set(sPosts.Tags.Add(1)).OnConflict(sPosts.ID).DoNothing().Exec(user); err == nil || !strings.Contains(err.Error(), "upsert") {
		t.Errorf("upsert: %v", err)
	}
	xs := []sPost{{AuthorID: 1, Title: "a"}}
	if err := db.InsertAll(xs).Set(sPosts.Tags.Add(1)).Exec(user); err == nil || !strings.Contains(err.Error(), "every entity's key") {
		t.Errorf("InsertAll of generated keys: %v", err)
	}
	if n := int64s(t, raw, "SELECT COUNT(*) FROM s_posts"); n[0] != 0 {
		t.Errorf("%v posts inserted", n)
	}
}

// TestEdgeSetterInsertColumns: an insert's column setter wins over the
// entity's field, and is stored in it; SQL renders the INSERT alone.
func TestEdgeSetterInsertColumns(t *testing.T) {
	db, raw := edgeDB(t)
	exec(t, raw, "INSERT INTO s_tags (id, name) VALUES (1, 'a')")
	p := sPost{AuthorID: 1, Title: "struct"}
	s, _, err := db.Insert(&p).Set(sPosts.Title.Set("setter"), sPosts.Tags.Add(1)).SQL(user)
	if err != nil || !strings.HasPrefix(s, `INSERT INTO "s_posts"`) || strings.Contains(s, "s_post_tags") {
		t.Errorf("SQL: %q, %v", s, err)
	}
	p.Title = "struct"
	if err := db.Insert(&p).Set(sPosts.Title.Set("setter")).Exec(user); err != nil {
		t.Fatal(err)
	}
	if p.Title != "setter" {
		t.Errorf("the entity's title: %q", p.Title)
	}
	got, err := db.Query[sPost]().Where(sPosts.ID.EQ(p.ID)).One(ctx)
	if err != nil || got.Title != "setter" {
		t.Errorf("stored: %+v, %v", got, err)
	}
	xs := []sPost{{ID: 5, AuthorID: 1, Title: "a"}, {ID: 6, AuthorID: 1, Title: "b"}}
	if err := db.InsertAll(xs).Set(sPosts.AuthorID.Set(2)).Exec(user); err != nil {
		t.Fatal(err)
	}
	want64(t, "authors", int64s(t, raw, "SELECT author_id FROM s_posts WHERE id IN (5, 6) ORDER BY id"), 2, 2)
}

// TestEdgeSetterInTx: in a transaction the write is a savepoint, which an
// edge's error rolls back alone.
func TestEdgeSetterInTx(t *testing.T) {
	db, raw := edgeDB(t)
	exec(t, raw, "INSERT INTO s_tags (id, name) VALUES (1, 'a')")
	err := db.Tx(user, func(ctx context.Context, tx *velox.DB) error {
		keep := sPost{AuthorID: 1, Title: "keep"}
		if err := tx.Insert(&keep).Set(sPosts.Tags.Add(1)).Exec(ctx); err != nil {
			return err
		}
		lost := sPost{AuthorID: 1, Title: "lost"}
		if err := tx.Insert(&lost).Set(sPosts.Tags.Add(2)).Exec(ctx); !errors.Is(err, velox.ErrNotFound) {
			t.Errorf("hidden add: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want64(t, "posts", int64s(t, raw, "SELECT COUNT(*) FROM s_posts"), 1)
	want64(t, "links", int64s(t, raw, "SELECT tag_id FROM s_post_tags"), 1)
}

// TestEdgeSetterRefusedKinds: a bidirectional one-to-one edge, and an edge
// whose foreign key is not nullable, take no edge setter; nothing is sent.
func TestEdgeSetterRefusedKinds(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	spouse := velox.Rel[bspouse, bspouse]{I: 0}
	_, err := db.Update[bspouse]().Set(spouse.Add(int64(2))).Where(bspouses.ID.EQ(1)).Exec(ctx)
	if err == nil || !strings.Contains(err.Error(), "bidirectional") {
		t.Errorf("bidirectional: %v", err)
	}
	_, err = db.Update[fixture.DAuthor]().Set(fixture.DAuthors.Books.Remove(int64(2))).Where(fixture.DAuthors.ID.EQ(1)).Exec(ctx)
	if err == nil || !strings.Contains(err.Error(), "not nullable") {
		t.Errorf("not nullable: %v", err)
	}
	if len(e.Log) != 0 {
		t.Errorf("sent %q", e.Log)
	}
}

// TestEdgeSetterHookSkips: an insert a hook does not let through writes no
// edge.
func TestEdgeSetterHookSkips(t *testing.T) {
	db, raw := edgeDB(t)
	exec(t, raw, "INSERT INTO s_tags (id, name) VALUES (1, 'a')")
	p := sPost{ID: 3, AuthorID: 1, Title: "p"}
	if err := db.Insert(&p).Set(sPosts.Tags.Add(1)).Exec(context.WithValue(user, sSkip{}, true)); err != nil {
		t.Fatal(err)
	}
	want64(t, "links", int64s(t, raw, "SELECT COUNT(*) FROM s_post_tags"), 0)
}

// TestEdgeSetterPostgresStatements: on PostgreSQL an update of edges alone
// locks the rows it matches, checks the targets, and inserts the join rows
// ON CONFLICT DO NOTHING, in a transaction.
func TestEdgeSetterPostgresStatements(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{{{int64(1)}}, {{int64(7)}}}, Affected: 1}
	db := openPolicied(velox.Postgres, e)
	n, err := db.Update[sPost]().Set(sPosts.Tags.Add(7)).Where(sPosts.ID.EQ(1)).Exec(user)
	if err != nil || n != 1 {
		t.Fatalf("update: %d, %v", n, err)
	}
	want := []string{
		`SELECT "id" FROM "s_posts" WHERE "id" = $1 FOR UPDATE`,
		`SELECT "id" FROM "s_tags" WHERE "id" IN ($1) AND "hidden" = $2 LIMIT $3`,
		`INSERT INTO "s_post_tags" ("post_id", "tag_id") VALUES ($1, $2) ON CONFLICT ("post_id", "tag_id") DO NOTHING`,
	}
	if got := slices.DeleteFunc(slices.Clone(e.Log), func(s string) bool { return !strings.HasPrefix(s, "SELECT") && !strings.HasPrefix(s, "INSERT") }); !slices.Equal(got, want) {
		t.Errorf("statements:\n%s\nwant:\n%s\nall: %q", strings.Join(got, "\n"), strings.Join(want, "\n"), e.Log)
	}
}
