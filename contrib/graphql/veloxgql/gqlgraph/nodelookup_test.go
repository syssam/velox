package gqlgraph_test

import (
	"context"
	"database/sql"
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

// TestNodeLookupRewritten: a Rewrite hook may point an entity's statements
// at another table, which the lookup's raw SQL never sees. On such a DB
// every type is read, so an ID whose row is only in the rewritten table is
// found, as LoadNode finds it.
func TestNodeLookupRewritten(t *testing.T) {
	_, types, _ := nodeDB(t)
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	for _, s := range []string{
		"CREATE TABLE people (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER NOT NULL)",
		"CREATE TABLE people_t1 (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER NOT NULL)",
		"CREATE TABLE badges (id INTEGER PRIMARY KEY)",
		"CREATE TABLE vaults (id INTEGER PRIMARY KEY)",
		"INSERT INTO people_t1 (id, name, age) VALUES (50, 'tenant', 5)",
	} {
		if _, err := sqlDB.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	tenant := func(_ context.Context, st *velox.Statement) error {
		if st.Entity != nil && st.Entity.Table == "people" {
			c := *st.Entity
			c.Table = "people_t1"
			st.Entity = &c
		}
		return nil
	}
	db := velox.Open(velox.SQLite, sqlDB, velox.WithRewrite(tenant))
	ctx := context.Background()
	n, err := gqlgraph.ResolveNode(ctx, db, types, int64(50))
	if p, ok := n.(*person); err != nil || !ok || p.Name != "tenant" {
		t.Errorf("node 50: %#v, %v; want the tenant's person", n, err)
	}
	ns, err := gqlgraph.ResolveNodes(ctx, db, types, []int64{50, 99})
	if err != nil || len(ns) != 2 || ns[0] == nil || ns[1] != nil {
		t.Errorf("nodes 50, 99: %#v, %v; want the tenant's person and nil", ns, err)
	}
}
