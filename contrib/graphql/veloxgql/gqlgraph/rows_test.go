package gqlgraph_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
)

// TestRowsAreChecked: a row passes into the untyped runtime as its pointer
// only when it is a row of the Conn's entity; another type panics. With no
// Table, whose statements fail, only its kind is checked. The rows come back
// as *E and []*E of the entity, holding the same pointers.
func TestRowsAreChecked(t *testing.T) {
	people := gqlgraph.TableType[person]("Person")
	x := &person{ID: 7}
	if p := gqlgraph.RowOf(people, x); p == nil || gqlgraph.NodeOf(people, p) != any(x) {
		t.Errorf("RowOf, NodeOf of a person = %v", p)
	}
	if gqlgraph.RowOf(people, nil) != nil || gqlgraph.NodeOf(people, nil) != nil {
		t.Error("RowOf or NodeOf of nil is not nil")
	}
	panics := func(name string, c *gqlgraph.Type, x any) {
		t.Helper()
		defer func() {
			if r := recover(); r == nil || !strings.Contains(r.(string), "is not a row") {
				t.Errorf("%s: recovered %v", name, r)
			}
		}()
		gqlgraph.RowOf(c, x)
	}
	panics("another entity", people, &post{})
	panics("no pointer", &gqlgraph.Type{Name: "None"}, person{})
	if p := gqlgraph.RowOf(&gqlgraph.Type{Name: "None"}, x); p == nil {
		t.Error("RowOf with no Table: nil")
	}
	ps := gqlgraph.NodesOf(people, []any{gqlgraph.RowOf(people, x), gqlgraph.RowOf(people, x)})
	if rows, ok := ps.([]*person); !ok || len(rows) != 2 || rows[0] != x || rows[1] != x {
		t.Errorf("NodesOf = %#v", ps)
	}
	if rows, ok := gqlgraph.NodesOf(people, nil).([]*person); !ok || rows == nil || len(rows) != 0 {
		t.Errorf("NodesOf of none = %#v", rows)
	}
}

// TestEdgeErrors: an edge reads nothing without a DB in the context, and
// returns the error of the owner's statement as it is.
func TestEdgeErrors(t *testing.T) {
	db, _, _ := edgeDB(t)
	x := &child{ID: 1}
	noDB := context.Background()
	if _, err := gqlgraph.EdgeOneOf(noDB, x, children.Owner, "Child.owner"); !errors.Is(err, veloxgql.ErrNoDB) {
		t.Errorf("EdgeOne with no DB: %v", err)
	}
	if _, err := gqlgraph.EdgeListOf(noDB, &owner{ID: 1}, owners.Tags, "Owner.tags", 0); !errors.Is(err, veloxgql.ErrNoDB) {
		t.Errorf("EdgeList with no DB: %v", err)
	}
	kids := gqlgraph.EdgeOf(&owner{ID: 1}, owners.Children, "Owner.children")
	if c, err := gqlgraph.Paginate(noDB, kids, gqlgraph.TableType[child]("Child"), gqlgraph.Args{}); c != nil || !errors.Is(err, veloxgql.ErrNoDB) {
		t.Errorf("Paginate of an edge with no DB = %v, %v", c, err)
	}
	ctx := veloxgql.NewExtension(db).NewContext(context.Background())
	none := &gqlgraph.Type{Name: "None"} // an owner with no Table
	if v, err := gqlgraph.EdgeOne(ctx, gqlgraph.TableType[owner]("Owner"), none.Edge(x, int(children.Owner.I))); v != nil || err == nil {
		t.Errorf("EdgeOne of an owner with no Table = %v, %v", v, err)
	}
	if v, err := gqlgraph.EdgeList(ctx, gqlgraph.TableType[tag]("Tag"), none.Edge(&owner{ID: 1}, int(owners.Tags.I)), 0); v != nil || err == nil {
		t.Errorf("EdgeList of an owner with no Table = %v, %v", v, err)
	}
}

// TestPageNode: a page's row i, as a *E of the node's entity.
func TestPageNode(t *testing.T) {
	db, _ := peopleDB(t)
	ctx := context.Background()
	c, err := gqlgraph.NewConnectionOf(ctx, db.Query[person](), "Person", gqlgraph.Args{First: new(2)}, nil, personKey())
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Connection.Page(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := range p.Rows {
		if x, ok := p.Node(i).(*person); !ok || x.ID != int64(i+1) {
			t.Errorf("Node(%d) = %#v", i, p.Node(i))
		}
	}
}

// TestNodeAs: a Node type whose key is a named type decodes an ID into the
// base type and converts it.
func TestNodeAs(t *testing.T) {
	db, r := mutationDB(t)
	ctx := tenantKey.With(veloxgql.NewExtension(db).NewContext(context.Background()), "ann")
	seedPosts(ctx, t, db)
	r.take()
	kind := func(typ string) (gqlgraph.NodeKind, bool) {
		return gqlgraph.NewNodeAs[int64, int64](gqlgraph.InputType[post]("Post", int(posts.ID.I), nil), func(k int64) int64 { return k }), typ == "Post"
	}
	n, err := gqlgraph.NodeByID(ctx, veloxgql.EncodeID("Post", int64(2)), kind)
	if p, ok := n.(*post); err != nil || !ok || p.ID != 2 {
		t.Errorf("Node(2) = %v, %v", n, err)
	}
	if _, err := gqlgraph.NodeByID(ctx, veloxgql.EncodeID("Post", "x"), kind); !errors.Is(err, veloxgql.ErrBadID) {
		t.Errorf("a bad key: %v", err)
	}
}

// TestInputWithoutTable: an input's setters checked against a Conn with no
// Table fail, naming no field.
func TestInputWithoutTable(t *testing.T) {
	none := &gqlgraph.Type{Name: "None"}
	set := []velox.Setter[item]{items.Name.Set("x")}
	if _, err := gqlgraph.Validate("CreateNoneInput", none, set); err == nil {
		t.Error("a create input with no Table: no error")
	}
	if _, err := gqlgraph.Validate("UpdateNoneInput", none, set); err == nil {
		t.Error("an update input with no Table: no error")
	}
}
