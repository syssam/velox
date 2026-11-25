package gogen

import (
	"fmt"

	"github.com/syssam/velox/contrib/graphql/internal/sdl"
	"github.com/syssam/velox/plugin"
)

// writeEdges writes the methods gqlgen binds t's edge fields to.
func (r *renderer) writeEdges(f *file, t *sdl.Type) error {
	for _, ed := range t.Edges {
		if err := r.writeEdge(f, t, ed); err != nil {
			return err
		}
	}
	return nil
}

// nilKey is, for the edge ed of e to tgt when e's row holds its key in a
// pointer, the condition under which the edge has no node, a Go expression
// of x, the owner; "" otherwise. It fails when the key the relation
// compares, the target's or the owner's, is not one column.
func (r *renderer) nilKey(e *plugin.Entity, ed *plugin.Edge, tgt *plugin.Entity) (string, error) {
	where := "schema " + e.Name + ", edge " + ed.Name
	oneKey := func(of *plugin.Entity) error {
		if len(of.PrimaryKey) != 1 {
			return fmt.Errorf("%s: velox-graphql finds its nodes by the key of %s, which is %d columns; skip the edge with graphql.Skip()", where, of.Name, len(of.PrimaryKey))
		}
		return nil
	}
	switch {
	case ed.Field != "" && ed.OwnsKey:
		if err := oneKey(tgt); err != nil {
			return "", err
		}
		for _, fd := range e.Fields {
			if fd.Name == ed.Field {
				if fd.Pointer {
					return recv(e) + "." + fd.GoName + " == nil", nil
				}
				return "", nil
			}
		}
		return "", fmt.Errorf("%s: its foreign key %s is no field of %s", where, ed.Field, e.Name)
	case ed.Field != "":
		return "", oneKey(e)
	}
	if err := oneKey(e); err != nil {
		return "", err
	}
	return "", oneKey(tgt)
}

// writeEdge writes the method of the edge field ed of t: the edge's node or
// nodes, read through gqlgraph's batches (EdgeOne, EdgeList, or Paginate of
// the edge in the target's paginate) on the DB that read or wrote x. Each gets the edge, x's relation, whose field t's gqlgraph.Type
// names in its Edges; that field alone batches it, and a connection's batch
// keeps its arguments. It never reads the relation's field: an
// application's With may have loaded it filtered or limited.
func (r *renderer) writeEdge(f *file, t *sdl.Type, ed sdl.Edge) error {
	e, pe := t.Entity, t.Entity.Edges[ed.Edge]
	tt, tgt := ed.Target, ed.Target.Entity
	if err := r.declare(e.Name+"."+ed.Method, "schema "+e.Name+", edge "+pe.Name); err != nil {
		return err
	}
	nilKey, err := r.nilKey(e, pe, tgt)
	if err != nil {
		return err
	}
	if nilKey != "" && ed.Kind != sdl.EdgeUnique {
		return fmt.Errorf("schema %s, edge %s: a to-many edge whose key is on the owner's row, which velox-gen does not write", e.Name, pe.Name)
	}
	ctx, impl := f.use("context", "context"), f.gqlgraph()
	// The owner's type, the row and the relation's index: the batches are
	// compiled once, on untyped.Table, and gqlgraph takes and gives the rows
	// as *E in an any.
	x := recv(e)
	edge := fmt.Sprintf("%s.Edge(%s, %s)", typeVar(t), x, edgeConst(e, pe))
	switch ed.Kind {
	case sdl.EdgeUnique:
		f.doc("", fmt.Sprintf("%s is the edge %s: the %s it leads to, or nil. It is read in one query with the same edge of every row on %s's page.", ed.Method, pe.Name, tt.Name, x))
		f.p("func (%s *%s) %s(ctx %s.Context) (*%s, error) {", x, e.Name, ed.Method, ctx, tgt.Name)
		if nilKey != "" {
			f.p("\tif %s {", nilKey)
			f.p("\t\treturn nil, nil")
			f.p("\t}")
		}
		f.p("\tv, err := %s.EdgeOne(ctx, &%s, %s)", impl, typeVar(tt), edge)
		f.p("\tn, _ := v.(*%s)", tgt.Name)
		f.p("\treturn n, err")
		f.p("}")
	case sdl.EdgeList:
		f.doc("", fmt.Sprintf("%s is the edge %s: the %s nodes it leads to, in key order. It is read in one query with the same edge of every row on %s's page, and fails on more than MaxList nodes.", ed.Method, pe.Name, tt.Name, x))
		f.p("func (%s *%s) %s(ctx %s.Context) ([]*%s, error) {", x, e.Name, ed.Method, ctx, tgt.Name)
		f.p("\tv, err := %s.EdgeList(ctx, &%s, %s, %d)", impl, typeVar(tt), edge, ed.MaxList)
		f.p("\tn, _ := v.([]*%s)", tgt.Name)
		f.p("\treturn n, err")
		f.p("}")
	case sdl.EdgeConnection:
		f.doc("", fmt.Sprintf("%s is the edge %s: a connection to the %s nodes it leads to. Its queries batch with the same edge of every row on %s's page.", ed.Method, pe.Name, tt.Name, x))
		f.p("%s", signature("func ("+x+" *"+e.Name+") "+ed.Method, "ctx "+ctx+".Context", connParams(f, tt), "(*"+tt.Name+"Connection, error)"))
		f.p("\treturn %s(ctx, %s, %s)", newConnImpl(tt), edge, connArgs(tt))
		f.p("}")
	}
	f.p("")
	return nil
}
