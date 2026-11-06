package gqlimpl_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

// TestRowsAreChecked: a row passes into the untyped runtime as its pointer
// only when it is a row of the Conn's entity; another type panics. With no
// Table, whose statements fail, only its kind is checked. The rows come back
// as *E and []*E of the entity, holding the same pointers.
func TestRowsAreChecked(t *testing.T) {
	people := gqlimpl.TableType[person]("Person")
	x := &person{ID: 7}
	if p := gqlimpl.RowOf(people, x); p == nil || gqlimpl.NodeOf(people, p) != any(x) {
		t.Errorf("RowOf, NodeOf of a person = %v", p)
	}
	if gqlimpl.RowOf(people, nil) != nil || gqlimpl.NodeOf(people, nil) != nil {
		t.Error("RowOf or NodeOf of nil is not nil")
	}
	panics := func(name string, c *gqlimpl.Type, x any) {
		t.Helper()
		defer func() {
			if r := recover(); r == nil || !strings.Contains(r.(string), "is not a row") {
				t.Errorf("%s: recovered %v", name, r)
			}
		}()
		gqlimpl.RowOf(c, x)
	}
	panics("another entity", people, &post{})
	panics("no pointer", &gqlimpl.Type{Name: "None"}, person{})
	if p := gqlimpl.RowOf(&gqlimpl.Type{Name: "None"}, x); p == nil {
		t.Error("RowOf with no Table: nil")
	}
	ps := gqlimpl.NodesOf(people, []any{gqlimpl.RowOf(people, x), gqlimpl.RowOf(people, x)})
	if rows, ok := ps.([]*person); !ok || len(rows) != 2 || rows[0] != x || rows[1] != x {
		t.Errorf("NodesOf = %#v", ps)
	}
	if rows, ok := gqlimpl.NodesOf(people, nil).([]*person); !ok || rows == nil || len(rows) != 0 {
		t.Errorf("NodesOf of none = %#v", rows)
	}
}

// TestEdgeErrors: an edge reads nothing without a DB in the context, and
// returns the error of the owner's statement as it is.
func TestEdgeErrors(t *testing.T) {
	db, _, _ := edgeDB(t)
	x := &child{ID: 1}
	noDB := context.Background()
	if _, err := gqlimpl.EdgeOneOf(noDB, x, children.Owner, "Child.owner"); !errors.Is(err, veloxgql.ErrNoDB) {
		t.Errorf("EdgeOne with no DB: %v", err)
	}
	if _, err := gqlimpl.EdgeListOf(noDB, &owner{ID: 1}, owners.Tags, "Owner.tags", 0); !errors.Is(err, veloxgql.ErrNoDB) {
		t.Errorf("EdgeList with no DB: %v", err)
	}
	kids := gqlimpl.EdgeOf(&owner{ID: 1}, owners.Children, "Owner.children")
	if c, err := gqlimpl.Paginate(noDB, kids, gqlimpl.TableType[child]("Child"), gqlimpl.Args{}, nil); c != nil || !errors.Is(err, veloxgql.ErrNoDB) {
		t.Errorf("Paginate of an edge with no DB = %v, %v", c, err)
	}
	ctx := veloxgql.NewExtension(db).NewContext(context.Background())
	none := &gqlimpl.Type{Name: "None"} // an owner with no Table
	if v, err := gqlimpl.EdgeOne(ctx, gqlimpl.TableType[owner]("Owner"), none.Edge(x, int(children.Owner.I))); v != nil || err == nil {
		t.Errorf("EdgeOne of an owner with no Table = %v, %v", v, err)
	}
	if v, err := gqlimpl.EdgeList(ctx, gqlimpl.TableType[tag]("Tag"), none.Edge(&owner{ID: 1}, int(owners.Tags.I)), 0); v != nil || err == nil {
		t.Errorf("EdgeList of an owner with no Table = %v, %v", v, err)
	}
}

// TestPageNode: a page's row i, as a *E of the node's entity.
func TestPageNode(t *testing.T) {
	db, _ := peopleDB(t)
	ctx := context.Background()
	c, err := gqlimpl.NewConnectionOf(ctx, db.Query[person](), "Person", gqlimpl.Args{First: new(2)}, nil, personKey())
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
	kind := func(typ string) (gqlimpl.NodeKind, bool) {
		return gqlimpl.NewNodeAs[int64, int64](gqlimpl.InputType[post]("Post", int(posts.ID.I), nil), func(k int64) int64 { return k }), typ == "Post"
	}
	n, err := gqlimpl.NodeByID(ctx, veloxgql.EncodeID("Post", int64(2)), kind)
	if p, ok := n.(*post); err != nil || !ok || p.ID != 2 {
		t.Errorf("Node(2) = %v, %v", n, err)
	}
	if _, err := gqlimpl.NodeByID(ctx, veloxgql.EncodeID("Post", "x"), kind); !errors.Is(err, veloxgql.ErrBadID) {
		t.Errorf("a bad key: %v", err)
	}
}

// TestInputWithoutTable: an input's setters checked against a Conn with no
// Table fail, naming no field.
func TestInputWithoutTable(t *testing.T) {
	none := &gqlimpl.Type{Name: "None"}
	set := []velox.Setter[item]{items.Name.Set("x")}
	if _, err := gqlimpl.Checked("CreateNoneInput", none, set); err == nil {
		t.Error("a create input with no Table: no error")
	}
	if _, err := gqlimpl.Checked("UpdateNoneInput", none, set); err == nil {
		t.Error("an update input with no Table: no error")
	}
}
