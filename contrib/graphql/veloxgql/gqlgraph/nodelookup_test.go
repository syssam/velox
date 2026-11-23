package gqlgraph_test

import (
	"context"
	"errors"
	"maps"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

// TestNodeLookup: node(id:) and nodes(ids:) find the types that hold the
// IDs with one statement, then read only those types, under their
// policies: a hidden or denied row is still none, and an ID that two types
// show is still ambiguous.
func TestNodeLookup(t *testing.T) {
	db, types, log := nodeDB(t)
	ctx := context.Background()
	log.counts()
	cases := []struct {
		id   int64
		want map[string]int
	}{
		{2, map[string]int{"": 1, "people": 1}},              // a person
		{99, map[string]int{"": 1}},                          // none: no read
		{21, map[string]int{"": 1, "badges": 1}},             // a badge the policy hides
		{30, map[string]int{"": 1}},                          // a vault, whose policy denies before SQL
		{3, map[string]int{"": 1, "badges": 1, "people": 1}}, // a badge and a person
		{20, map[string]int{"": 1, "badges": 1}},             // a badge
	}
	for _, c := range cases {
		_, _ = gqlgraph.ResolveNode(ctx, db, types, c.id)
		if got := log.counts(); !maps.Equal(got, c.want) {
			t.Errorf("node %d: statements by table %v, want %v", c.id, got, c.want)
		}
	}
	_, _ = gqlgraph.ResolveNode(ctx, db, types, int64(2))
	want := `SELECT 0 FROM "badges" WHERE "id" = ? UNION ALL SELECT 1 FROM "people" WHERE "id" = ? UNION ALL SELECT 2 FROM "vaults" WHERE "id" = ?`
	if got := log.take(""); len(got) != 1 || got[0] != want {
		t.Errorf("lookup %q, want %q", got, want)
	}

	ns, err := gqlgraph.ResolveNodes(ctx, db, types, []int64{1, 2, 99})
	if err != nil || len(ns) != 3 || ns[2] != nil {
		t.Fatalf("nodes: %#v, %v", ns, err)
	}
	if got := log.counts(); !maps.Equal(got, map[string]int{"": 1, "people": 1}) {
		t.Errorf("nodes 1, 2, 99: statements by table %v, want the lookup and one of people", got)
	}
	if _, err := gqlgraph.ResolveNodes(ctx, db, types, []int64{20, 21, 30, 3}); !errors.Is(err, gqlgraph.ErrAmbiguousNodeID) {
		t.Errorf("nodes with 3: %v; want ErrAmbiguousNodeID", err)
	}
	if got := log.counts(); !maps.Equal(got, map[string]int{"": 1, "badges": 1, "people": 1}) {
		t.Errorf("nodes 20, 21, 30, 3: statements by table %v", got)
	}

	// A hook that denies raw SQL denies the lookup; every type is read as
	// before. vaults' policy denies its reads before they reach the hooks,
	// so the log never holds them.
	log.mu.Lock()
	log.fail = func(st *velox.Statement) error {
		if st.Entity.Table == "" {
			return velox.ErrDenied
		}
		return nil
	}
	log.mu.Unlock()
	n, err := gqlgraph.ResolveNode(ctx, db, types, int64(2))
	if p, ok := n.(*person); err != nil || !ok || p.ID != 2 {
		t.Errorf("node 2, lookup denied: %#v, %v", n, err)
	}
	if got := log.counts(); !maps.Equal(got, map[string]int{"": 1, "badges": 1, "people": 1}) {
		t.Errorf("node 2, lookup denied: statements by table %v, want every type", got)
	}
}

// TestNodeLookupFallback: a type whose key the lookup cannot read, a []byte
// here, is read whatever the lookup finds, beside types it can read.
func TestNodeLookupFallback(t *testing.T) {
	db, raw, log := edgeDB(t)
	for _, s := range []string{
		`CREATE TABLE blobs (id BLOB PRIMARY KEY, label TEXT NOT NULL)`,
		`INSERT INTO blobs (id, label) VALUES (x'01', 'one')`,
	} {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	key := []gqlgraph.Column{{Name: "id", Index: 0}}
	types := append(nodeTypes(), &gqlgraph.Type{Name: "Blob", Table: untyped.TableOf[blob], Key: key})
	log.counts()
	n, err := gqlgraph.ResolveNode(context.Background(), db, types, int64(5))
	if c, ok := n.(*child); err != nil || !ok || c.ID != 5 {
		t.Fatalf("node 5: %#v, %v", n, err)
	}
	if got := log.counts(); !maps.Equal(got, map[string]int{"": 1, "children": 1}) {
		t.Errorf("node 5: statements by table %v, want the lookup and children (blobs is read, but no blob key holds 5, so with no SQL)", got)
	}
	n, err = gqlgraph.ResolveNode(context.Background(), db, types, []byte{1})
	if b, ok := n.(*blob); err != nil || !ok || b.Label != "one" {
		t.Fatalf("blob 1: %#v, %v", n, err)
	}
	if got := log.counts(); !maps.Equal(got, map[string]int{"blobs": 1}) {
		t.Errorf("blob 1: statements by table %v, want blobs alone (no integer key holds it)", got)
	}
}
