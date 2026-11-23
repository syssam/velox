package gqlgraph_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox"
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
	// The lookup, a raw statement of no table, finds no owner of the IDs,
	// so owners is not read.
	if got := log.counts(); got[""] != 1 || got["children"] != 1 || len(got) != 2 {
		t.Errorf("statements by table %v, want the lookup and one of children", got)
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

// nodeTypes is the Child and the Owner types, keyed by their id.
func nodeTypes() []*gqlgraph.Type {
	key := []gqlgraph.Column{{Name: "id", Index: 0}}
	return []*gqlgraph.Type{
		{Name: "Child", Table: untyped.TableOf[child], Key: key},
		{Name: "Owner", Table: untyped.TableOf[owner], Key: key},
	}
}

// twoKeys is a type whose key is two columns, which a Node cannot have.
func twoKeys() *gqlgraph.Type {
	return &gqlgraph.Type{Name: "Pair", Table: untyped.TableOf[child], Key: []gqlgraph.Column{{Name: "id", Index: 0}, {Name: "title", Index: 2}}}
}

// TestNodeKeyColumns: a type of two key columns is an error from LoadNode,
// ResolveNode and ResolveNodes, and not a probe that finds nothing.
func TestNodeKeyColumns(t *testing.T) {
	db, _, log := edgeDB(t)
	ctx := context.Background()
	log.counts()
	if n, err := gqlgraph.LoadNode(ctx, db, twoKeys(), int64(1)); n != nil || err == nil || !strings.Contains(err.Error(), "2 key columns") {
		t.Errorf("LoadNode: %v, %v", n, err)
	}
	if n, err := gqlgraph.ResolveNode(ctx, db, []*gqlgraph.Type{twoKeys()}, int64(1)); n != nil || err == nil {
		t.Errorf("ResolveNode: %v, %v", n, err)
	}
	if ns, err := gqlgraph.ResolveNodes(ctx, db, []*gqlgraph.Type{twoKeys()}, []int64{1}); ns != nil || err == nil || !strings.Contains(err.Error(), "2 key columns") {
		t.Errorf("ResolveNodes: %v, %v", ns, err)
	}
	if got := log.counts(); len(got) != 0 {
		t.Errorf("a type with two keys ran %v", got)
	}
}

// TestNodeUnholdableIDs: an ID the key column cannot hold names no row, so
// ResolveNodes gives nil for it, with no statement when no ID can be held.
func TestNodeUnholdableIDs(t *testing.T) {
	db, _, log := edgeDB(t)
	ctx := context.Background()
	types := nodeTypes()[1:]
	log.counts()
	ns, err := gqlgraph.ResolveNodes(ctx, db, types, []string{"x", "y"})
	if err != nil || len(ns) != 2 || ns[0] != nil || ns[1] != nil {
		t.Errorf("IDs no key holds: %v, %v", ns, err)
	}
	if got := log.counts(); len(got) != 0 {
		t.Errorf("IDs no key holds ran %v", got)
	}
	if n, err := gqlgraph.LoadNode(ctx, db, types[0], "x"); n != nil || err != nil {
		t.Errorf("LoadNode of an ID no key holds: %v, %v", n, err)
	}
	ns, err = gqlgraph.ResolveNodes(ctx, db, types, []any{int64(2), "x", int64(3)})
	if err != nil || len(ns) != 3 || ns[1] != nil {
		t.Fatalf("mixed IDs: %v, %v", ns, err)
	}
	if o, ok := ns[0].(*owner); !ok || o.ID != 2 {
		t.Errorf("[0]: %#v, want owner 2", ns[0])
	}
	if o, ok := ns[2].(*owner); !ok || o.ID != 3 {
		t.Errorf("[2]: %#v, want owner 3", ns[2])
	}
}

// TestNodeStatements: a denied read is nil, as a row that is not there is,
// and any other statement error is returned, by each of the readers.
func TestNodeStatements(t *testing.T) {
	db, _, log := edgeDB(t)
	ctx := context.Background()
	types := nodeTypes()
	boom := errors.New("boom")
	fail := func(err error) {
		log.mu.Lock()
		defer log.mu.Unlock()
		log.fail = func(*velox.Statement) error { return err }
	}

	fail(velox.ErrDenied)
	if n, err := gqlgraph.LoadNode(ctx, db, types[1], int64(1)); n != nil || err != nil {
		t.Errorf("LoadNode of a denied read: %v, %v", n, err)
	}
	ns, err := gqlgraph.ResolveNodes(ctx, db, types, []int64{1, 4})
	if err != nil || len(ns) != 2 || ns[0] != nil || ns[1] != nil {
		t.Errorf("ResolveNodes of a denied read: %v, %v", ns, err)
	}

	fail(boom)
	if n, err := gqlgraph.LoadNode(ctx, db, types[1], int64(1)); n != nil || !errors.Is(err, boom) {
		t.Errorf("LoadNode: %v, %v", n, err)
	}
	if n, err := gqlgraph.ResolveNode(ctx, db, types, int64(1)); n != nil || !errors.Is(err, boom) {
		t.Errorf("ResolveNode: %v, %v", n, err)
	}
	if ns, err := gqlgraph.ResolveNodes(ctx, db, types, []int64{1}); ns != nil || !errors.Is(err, boom) {
		t.Errorf("ResolveNodes: %v, %v", ns, err)
	}
}

// blob is an entity keyed by bytes, whose Go type a map cannot hold.
type blob struct {
	ID    []byte
	Label string
}

var blobMeta velox.MetaOnce[blob]

func (*blob) VeloxMeta() *velox.Meta[blob] { return blobMeta.Load(blobDescriptor) }

func blobDescriptor() velox.Descriptor[blob] {
	return velox.Descriptor[blob]{
		Table:      "blobs",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Label", Name: "label"}},
		PrimaryKey: []int{0},
		FieldPtr:   func(x *blob, i int) any { return [...]any{&x.ID, &x.Label}[i] },
	}
}

// TestResolveNodesUncomparableKey: a key whose Go type is not comparable is
// read by LoadNode of each ID, one statement each, in the IDs' order.
func TestResolveNodesUncomparableKey(t *testing.T) {
	db, raw, log := edgeDB(t)
	for _, s := range []string{
		`CREATE TABLE blobs (id BLOB PRIMARY KEY, label TEXT NOT NULL)`,
		`INSERT INTO blobs (id, label) VALUES (x'01', 'one'), (x'02', 'two')`,
	} {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	types := []*gqlgraph.Type{{Name: "Blob", Table: untyped.TableOf[blob], Key: []gqlgraph.Column{{Name: "id", Index: 0}}}}
	log.counts()
	ns, err := gqlgraph.ResolveNodes(context.Background(), db, types, [][]byte{{2}, {9}, {1}})
	if err != nil || len(ns) != 3 || ns[1] != nil {
		t.Fatalf("got %#v, %v", ns, err)
	}
	for i, want := range map[int]string{0: "two", 2: "one"} {
		if b, ok := ns[i].(*blob); !ok || b.Label != want {
			t.Errorf("[%d]: %#v, want %s", i, ns[i], want)
		}
	}
	if got := log.counts(); got["blobs"] != 3 {
		t.Errorf("statements by table %v, want 3 of blobs", got)
	}

	boom := errors.New("boom")
	log.mu.Lock()
	log.fail = func(*velox.Statement) error { return boom }
	log.mu.Unlock()
	if ns, err := gqlgraph.ResolveNodes(context.Background(), db, types, [][]byte{{1}}); ns != nil || !errors.Is(err, boom) {
		t.Errorf("a statement error: %v, %v", ns, err)
	}
}
