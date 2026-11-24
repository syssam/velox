package gqlgraph_test

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
)

// TestNodeType: with WithNodeType or WithFixedNodeType giving an ID's type,
// node(id:) runs no lookup and reads that type alone, under its policy: a
// row that is not there, one the policy hides and a type whose policy
// denies are NOT_FOUND. An ID two types hold is not ambiguous then. An
// empty name falls back to the lookup.
func TestNodeType(t *testing.T) {
	db, types, log := nodeDB(t)
	ctx := context.Background()
	byID := gqlgraph.WithNodeType(func(_ context.Context, id int64) (string, error) {
		switch {
		case id >= 30:
			return "Vault", nil
		case id >= 20:
			return "Badge", nil
		case id == 3:
			return "", nil // unknown: looked up
		}
		return "Person", nil
	})
	log.counts()
	n, err := gqlgraph.ResolveNode(ctx, db, types, int64(2), byID)
	if p, ok := n.(*person); err != nil || !ok || p.Name != "b" {
		t.Errorf("person 2: %#v, %v", n, err)
	}
	if got := log.counts(); !maps.Equal(got, map[string]int{"people": 1}) {
		t.Errorf("person 2: statements by table %v, want people alone", got)
	}
	n, err = gqlgraph.ResolveNode(ctx, db, types, int64(3), gqlgraph.WithFixedNodeType("Badge"))
	if b, ok := n.(*badge); err != nil || !ok || b.ID != 3 {
		t.Errorf("badge 3, fixed: %#v, %v", n, err)
	}
	if got := log.counts(); !maps.Equal(got, map[string]int{"badges": 1}) {
		t.Errorf("badge 3, fixed: statements by table %v, want badges alone", got)
	}
	for _, c := range []struct {
		id   int64
		want map[string]int
	}{
		{21, map[string]int{"badges": 1}}, // the policy hides it
		{30, map[string]int{}},            // the policy denies before SQL
		{29, map[string]int{"badges": 1}}, // no badge 29
		{9, map[string]int{"people": 1}},  // no person 9
	} {
		n, err := gqlgraph.ResolveNode(ctx, db, types, c.id, byID)
		if n != nil || !errors.Is(err, gqlgraph.ErrNodeNotFound) || codeOf(err) != veloxgql.CodeNotFound {
			t.Errorf("%d: %#v, %v; want NOT_FOUND ErrNodeNotFound", c.id, n, err)
		}
		if got := log.counts(); !maps.Equal(got, c.want) {
			t.Errorf("%d: statements by table %v, want %v", c.id, got, c.want)
		}
	}
	// An empty name: the lookup, which finds 3 in two types.
	if _, err := gqlgraph.ResolveNode(ctx, db, types, int64(3), byID); !errors.Is(err, gqlgraph.ErrAmbiguousNodeID) {
		t.Errorf("3, unknown type: %v; want ErrAmbiguousNodeID", err)
	}
	if got := log.counts(); !maps.Equal(got, map[string]int{"": 1, "badges": 1, "people": 1}) {
		t.Errorf("3, unknown type: statements by table %v, want the lookup and both types", got)
	}
	// A later option replaces an earlier one.
	n, err = gqlgraph.ResolveNode(ctx, db, types, int64(3), gqlgraph.WithFixedNodeType("Badge"), gqlgraph.WithFixedNodeType("Person"))
	if p, ok := n.(*person); err != nil || !ok || p.ID != 3 {
		t.Errorf("3, Badge then Person: %#v, %v", n, err)
	}
}

// TestNodeTypeErrors: a name that is no type of Node fails before any
// statement, with an error that names it; an error of the function is
// returned as it is; an ID of another Go type than WithNodeType's is an
// error.
func TestNodeTypeErrors(t *testing.T) {
	db, types, log := nodeDB(t)
	ctx := context.Background()
	log.counts()
	for _, opt := range []gqlgraph.NodeOption{
		gqlgraph.WithFixedNodeType("Comment"),
		gqlgraph.WithNodeType(func(context.Context, int64) (string, error) { return "Comment", nil }),
	} {
		_, err := gqlgraph.ResolveNode(ctx, db, types, int64(2), opt)
		if err == nil || !strings.Contains(err.Error(), `"Comment"`) || errors.Is(err, gqlgraph.ErrNodeNotFound) {
			t.Errorf("node, unknown type: %v; want an error naming Comment", err)
		}
		_, err = gqlgraph.ResolveNodes(ctx, db, types, []int64{2, 3}, opt)
		if err == nil || !strings.Contains(err.Error(), `"Comment"`) {
			t.Errorf("nodes, unknown type: %v; want an error naming Comment", err)
		}
	}
	bad := errors.New("bad ID")
	failing := gqlgraph.WithNodeType(func(context.Context, int64) (string, error) { return "", bad })
	if _, err := gqlgraph.ResolveNode(ctx, db, types, int64(2), failing); !errors.Is(err, bad) {
		t.Errorf("node, failing function: %v; want its error", err)
	}
	if _, err := gqlgraph.ResolveNodes(ctx, db, types, []int64{2}, failing); !errors.Is(err, bad) {
		t.Errorf("nodes, failing function: %v; want its error", err)
	}
	strs := gqlgraph.WithNodeType(func(context.Context, string) (string, error) { return "Person", nil })
	if _, err := gqlgraph.ResolveNode(ctx, db, types, int64(2), strs); err == nil || !strings.Contains(err.Error(), "IDs of type string") {
		t.Errorf("node, string IDs given an int64: %v", err)
	}
	if got := log.counts(); len(got) != 0 {
		t.Errorf("statements by table %v, want none", got)
	}
}

// TestNodesType: nodes(ids:) reads an ID whose type an option gives in
// that type alone, with one statement per type for all such IDs, and looks
// up only the others; when every ID has a type, there is no lookup.
func TestNodesType(t *testing.T) {
	db, types, log := nodeDB(t)
	ctx := context.Background()
	byID := gqlgraph.WithNodeType(func(_ context.Context, id int64) (string, error) {
		switch {
		case id >= 30:
			return "Vault", nil
		case id >= 20:
			return "Badge", nil
		case id == 3:
			return "", nil
		}
		return "Person", nil
	})
	log.counts()
	ns, err := gqlgraph.ResolveNodes(ctx, db, types, []int64{20, 1, 21, 30, 99, 2}, byID)
	if err != nil || len(ns) != 6 || ns[2] != nil || ns[3] != nil || ns[4] != nil {
		t.Fatalf("nodes: %#v, %v", ns, err)
	}
	if b, ok := ns[0].(*badge); !ok || b.ID != 20 {
		t.Errorf("nodes[0] = %#v, want badge 20", ns[0])
	}
	if p, ok := ns[5].(*person); !ok || p.ID != 2 {
		t.Errorf("nodes[5] = %#v, want person 2", ns[5])
	}
	if got := log.counts(); !maps.Equal(got, map[string]int{"badges": 1, "people": 1}) {
		t.Errorf("nodes, every type given: statements by table %v, want badges and people, no lookup", got)
	}
	// 3's type is unknown: the lookup is of 3 alone, and finds it twice.
	if _, err := gqlgraph.ResolveNodes(ctx, db, types, []int64{1, 3}, byID); !errors.Is(err, gqlgraph.ErrAmbiguousNodeID) {
		t.Errorf("nodes 1, 3: %v; want ErrAmbiguousNodeID", err)
	}
	if got := log.take(""); len(got) != 1 || strings.Count(got[0], "?") != 3 {
		t.Errorf("nodes 1, 3: lookup %q, want one statement of 3 alone", got)
	}
	log.counts()
	ns, err = gqlgraph.ResolveNodes(ctx, db, types, []int64{3, 1}, gqlgraph.WithFixedNodeType("Person"))
	if err != nil || len(ns) != 2 || ns[0].(*person).ID != 3 || ns[1].(*person).ID != 1 {
		t.Errorf("nodes 3, 1 fixed Person: %#v, %v", ns, err)
	}
	if got := log.counts(); !maps.Equal(got, map[string]int{"people": 1}) {
		t.Errorf("nodes, fixed: statements by table %v, want people alone", got)
	}
}
