package gqlimpl_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
	"github.com/syssam/velox/scope"
)

func (*post) IsNode() {}

func (x *post) GlobalID() string { return veloxgql.EncodeID("Post", x.ID) }

// postKind is post as a Node type, as the generated code writes it.
func postKind() gqlimpl.NodeKind {
	return gqlimpl.NewNode[int64](gqlimpl.InputType[post]("Post", int(posts.ID.I), nil))
}

func postKinds(typ string) (gqlimpl.NodeKind, bool) {
	if typ == "Post" {
		return postKind(), true
	}
	return gqlimpl.NodeKind{}, false
}

func seedPosts(ctx context.Context, t *testing.T, db *velox.DB) {
	t.Helper()
	for _, p := range []post{{ID: 1, Title: "a"}, {ID: 2, Title: "b"}, {ID: 4, Title: "hidden"}} {
		if err := db.Insert(&p).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

// TestNodeByID: the node of an ID, nil (an untyped nil) for a key no row has
// or the caller may not read, and the errors of an ID before any query.
func TestNodeByID(t *testing.T) {
	db, r := mutationDB(t)
	ctx := tenantKey.With(veloxgql.NewExtension(db).NewContext(context.Background()), "ann")
	seedPosts(ctx, t, db)
	r.take()

	n, err := gqlimpl.NodeByID(ctx, veloxgql.EncodeID("Post", int64(2)), postKinds)
	if p, ok := n.(*post); err != nil || !ok || p.ID != 2 || p.Title != "b" {
		t.Errorf("Node(2) = %v, %v", n, err)
	}
	for name, id := range map[string]int64{"missing": 3, "hidden by policy": 4} {
		n, err := gqlimpl.NodeByID(ctx, veloxgql.EncodeID("Post", id), postKinds)
		if err != nil || n != nil {
			t.Errorf("Node of a %s row = %#v, %v; want an untyped nil", name, n, err)
		}
	}
	r.take()
	for name, c := range map[string]struct {
		id   string
		want error
	}{
		"another type": {veloxgql.EncodeID("User", int64(1)), veloxgql.ErrWrongIDType},
		"malformed":    {"???", veloxgql.ErrBadID},
		"bad key":      {veloxgql.EncodeID("Post", "x"), veloxgql.ErrBadID},
	} {
		if _, err := gqlimpl.NodeByID(ctx, c.id, postKinds); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	if _, stmts := r.take(); len(stmts) != 0 {
		t.Errorf("an ID that fails sent %v", stmts)
	}
	if _, err := gqlimpl.NodeByID(veloxgql.NewExtension(db).NewContext(context.Background()), veloxgql.EncodeID("Post", int64(1)), postKinds); !errors.Is(err, scope.ErrNoScope) {
		t.Errorf("no scope: %v, want ErrNoScope", err)
	}
}

// TestNodesByID: the nodes of IDs in their order with nil for a row none, one
// query for the IDs of a type however often one repeats, every ID decoded
// before any query, and no more IDs than MaxPage.
func TestNodesByID(t *testing.T) {
	db, r := mutationDB(t)
	ctx := tenantKey.With(veloxgql.NewExtension(db, veloxgql.WithLimits(veloxgql.Limits{MaxPage: 4})).NewContext(context.Background()), "ann")
	seedPosts(ctx, t, db)
	id := func(k int64) string { return veloxgql.EncodeID("Post", k) }
	r.take()

	got, err := gqlimpl.NodesByID(ctx, []string{id(2), id(3), id(1), id(2)}, postKinds)
	if err != nil || len(got) != 4 {
		t.Fatalf("Nodes = %v, %v", got, err)
	}
	var keys []int64
	for _, n := range got {
		if p, ok := n.(*post); ok {
			keys = append(keys, p.ID)
		} else if n != nil {
			t.Errorf("a node that is not a post: %#v", n)
		} else {
			keys = append(keys, 0)
		}
	}
	if !slices.Equal(keys, []int64{2, 0, 1, 2}) {
		t.Errorf("Nodes keys = %v, want [2 0 1 2]", keys)
	}
	if _, stmts := r.take(); len(stmts) != 1 {
		t.Errorf("Nodes of one type sent %v, want one statement", stmts)
	}

	if got, err := gqlimpl.NodesByID(ctx, nil, postKinds); err != nil || len(got) != 0 {
		t.Errorf("Nodes of no IDs = %v, %v", got, err)
	}
	for name, c := range map[string]struct {
		ids  []string
		want error
	}{
		"another type": {[]string{id(1), veloxgql.EncodeID("User", int64(1))}, veloxgql.ErrWrongIDType},
		"malformed":    {[]string{id(1), "???"}, veloxgql.ErrBadID},
		"bad key":      {[]string{id(1), veloxgql.EncodeID("Post", "x")}, veloxgql.ErrBadID},
		"too many":     {[]string{id(1), id(1), id(1), id(1), id(1)}, veloxgql.ErrPageTooLarge},
	} {
		r.take()
		if _, err := gqlimpl.NodesByID(ctx, c.ids, postKinds); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
		if _, stmts := r.take(); len(stmts) != 0 {
			t.Errorf("%s: sent %v before the error", name, stmts)
		}
	}
}

// TestShow: what an input's String prints for a pointer field.
func TestShow(t *testing.T) {
	s, j := "x", veloxgql.JSON(`{"a":1}`)
	if gqlimpl.Show[string](nil) != nil || gqlimpl.Show(&s) != "x" {
		t.Errorf("Show = %v, %v", gqlimpl.Show[string](nil), gqlimpl.Show(&s))
	}
	if gqlimpl.ShowJSON(nil) != nil || gqlimpl.ShowJSON(&j) != `{"a":1}` {
		t.Errorf("ShowJSON = %v, %v", gqlimpl.ShowJSON(nil), gqlimpl.ShowJSON(&j))
	}
}
