package gqlgraph_test

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

// counts returns how many statements l holds of each table, and forgets
// them.
func (l *edgeLog) counts() map[string]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string]int{}
	for table, sql := range l.sql {
		out[table] = len(sql)
	}
	l.sql = map[string][]string{}
	return out
}

// TestResolveNodesBatched: nodes(ids:) reads each type of Node with one
// statement, whatever the number of IDs, and its rows are one batch, so an
// edge of theirs is one statement too.
func TestResolveNodesBatched(t *testing.T) {
	db, _, log := edgeDB(t)
	ctx := operation(t, db, `{ children { edges { node { owner { name } } } } }`)
	key := []gqlgraph.Column{{Name: "id", Index: 0}}
	types := []*gqlgraph.Type{
		{Name: "Child", Table: untyped.TableOf[child], Key: key},
		{Name: "Owner", Table: untyped.TableOf[owner], Key: key},
	}
	// Owners are 1 to 3, so children 4 to 7 are the only rows of their IDs.
	ids := []int64{4, 5, 99, 6, 7, 4}
	log.counts()
	ns, err := gqlgraph.ResolveNodes(field(t, ctx, "children"), db, types, ids)
	if err != nil {
		t.Fatal(err)
	}
	if got := log.counts(); got["children"] != 1 || got["owners"] != 1 || len(got) != 2 {
		t.Errorf("statements by table %v, want one each of children and owners", got)
	}
	if len(ns) != len(ids) || ns[2] != nil {
		t.Fatalf("got %#v", ns)
	}
	for i, id := range ids {
		if i == 2 {
			continue
		}
		if c, ok := ns[i].(*child); !ok || c.ID != id {
			t.Errorf("[%d]: %#v, want child %d", i, ns[i], id)
		}
	}

	octx := field(t, ctx, "children", "edges", "node", "owner")
	names := map[int64]string{}
	for _, n := range ns {
		c, _ := n.(*child)
		if c == nil {
			continue
		}
		o, err := gqlgraph.EdgeOneOf(octx, c, children.Owner, "Child.owner")
		if err != nil {
			t.Fatal(err)
		}
		if o != nil {
			names[c.ID] = o.Name
		}
	}
	if names[4] != "a" || names[5] != "b" || names[6] != "b" || len(names) != 3 {
		t.Errorf("owners %v", names)
	}
	if got := log.counts(); got["owners"] != 1 || len(got) != 1 {
		t.Errorf("statements of the nodes' owners %v, want one", got)
	}
}

// TestResolveNodesCap: more IDs than the MaxPage of the context's limits is
// ErrPageTooLarge, BAD_USER_INPUT, before any statement.
func TestResolveNodesCap(t *testing.T) {
	vdb, _, log := edgeDB(t)
	key := []gqlgraph.Column{{Name: "id", Index: 0}}
	types := []*gqlgraph.Type{{Name: "Owner", Table: untyped.TableOf[owner], Key: key}}

	ctx := gqlgenx.NewExtension(vdb, gqlgenx.WithLimits(veloxgql.Limits{MaxPage: 2})).NewContext(context.Background())
	if ns, err := gqlgraph.ResolveNodes(ctx, vdb, types, []int64{1, 2}); err != nil || len(ns) != 2 {
		t.Errorf("2 IDs under MaxPage 2: %v, %v", ns, err)
	}
	log.counts()
	ns, err := gqlgraph.ResolveNodes(ctx, vdb, types, []int64{1, 2, 3})
	if ns != nil || !errors.Is(err, veloxgql.ErrPageTooLarge) || codeOf(err) != veloxgql.CodeBadUserInput {
		t.Errorf("3 IDs under MaxPage 2: %v, %v; want BAD_USER_INPUT ErrPageTooLarge", ns, err)
	}
	if got := log.counts(); len(got) != 0 {
		t.Errorf("3 IDs under MaxPage 2 ran %v", got)
	}

	many := make([]int64, veloxgql.DefaultLimits().MaxPage+1)
	if _, err := gqlgraph.ResolveNodes(context.Background(), vdb, types, many); !errors.Is(err, veloxgql.ErrPageTooLarge) {
		t.Errorf("%d IDs by default: %v; want ErrPageTooLarge", len(many), err)
	}
}
