package gqlgraph_test

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
)

// TestRowsAreChecked: a row passes through the untyped runtime as a *E in an
// any, and a nil pointer is no row. The rows come back as []*E of the
// entity, holding the same pointers; with no Table there is no slice.
func TestRowsAreChecked(t *testing.T) {
	people := gqlgraph.TableType[person]("Person")
	x := &person{ID: 7}
	if gqlgraph.RowOf(x) != any(x) || gqlgraph.RowOf(nil) != nil || gqlgraph.RowOf((*person)(nil)) != nil {
		t.Error("RowOf")
	}
	ps := gqlgraph.NodesOf(people, []any{x, x})
	if rows, ok := ps.([]*person); !ok || len(rows) != 2 || rows[0] != x || rows[1] != x {
		t.Errorf("NodesOf = %#v", ps)
	}
	if rows, ok := gqlgraph.NodesOf(people, nil).([]*person); !ok || rows == nil || len(rows) != 0 {
		t.Errorf("NodesOf of none = %#v", rows)
	}
	if v := gqlgraph.NodesOf(&gqlgraph.Type{Name: "None"}, nil); v != nil {
		t.Errorf("NodesOf of none with no Table = %#v", v)
	}
}

// TestEdgeErrors: an edge of a row no statement made reads nothing, and an
// edge returns the error of the owner's statement as it is.
func TestEdgeErrors(t *testing.T) {
	db, _, _ := edgeDB(t)
	x := &child{ID: 1}
	noDB := context.Background()
	if _, err := gqlgraph.EdgeOneOf(noDB, x, children.Owner, "Child.owner"); !errors.Is(err, velox.ErrNoSource) {
		t.Errorf("EdgeOne of a row with no source: %v", err)
	}
	if _, err := gqlgraph.EdgeListOf(noDB, &owner{ID: 1}, owners.Tags, "Owner.tags", 0); !errors.Is(err, velox.ErrNoSource) {
		t.Errorf("EdgeList of a row with no source: %v", err)
	}
	kids := gqlgraph.EdgeOf(&owner{ID: 1}, owners.Children, "Owner.children")
	if c, err := gqlgraph.Paginate(noDB, kids, gqlgraph.TableType[child]("Child"), gqlgraph.Args{}); c != nil || !errors.Is(err, velox.ErrNoSource) {
		t.Errorf("Paginate of an edge of a row with no source = %v, %v", c, err)
	}
	ctx := context.Background()
	// Rows a statement read have a DB, so the edge reaches its checks.
	o, err := db.Query[owner]().Where(owners.ID.EQ(1)).First(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c, err := db.Query[child]().Where(children.ID.EQ(5)).First(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// A row of another entity than the owner's fails the edge's read, with
	// an error and no panic: the row has no source of the owner's entity.
	wrong := gqlgraph.TableType[child]("Child").Edge(o, int(children.Owner.I))
	if v, err := gqlgraph.EdgeOne(ctx, gqlgraph.TableType[owner]("Owner"), wrong); v != nil || !errors.Is(err, velox.ErrNoSource) {
		t.Errorf("EdgeOne of a row of another entity = %v, %v", v, err)
	}
	none := &gqlgraph.Type{Name: "None"} // an owner with no Table
	if v, err := gqlgraph.EdgeOne(ctx, gqlgraph.TableType[owner]("Owner"), none.Edge(c, int(children.Owner.I))); v != nil || err == nil {
		t.Errorf("EdgeOne of an owner with no Table = %v, %v", v, err)
	}
	if v, err := gqlgraph.EdgeList(ctx, gqlgraph.TableType[tag]("Tag"), none.Edge(o, int(owners.Tags.I)), 0); v != nil || err == nil {
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
