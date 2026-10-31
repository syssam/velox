package gqlimpl

import (
	"context"
	"slices"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// Selected returns the names of the fields selected on the objects of the
// GraphQL type typ that the field of ctx returns: through edges and node for
// a connection field, the field's own selection otherwise, with fragments
// whose type is typ or Node, and without __typename. ok is false outside a
// gqlgen field, and when the field returns neither typ nor a connection to
// it nor Node. The result is computed once per *ast.Field per operation.
//
// Fragments on every interface and union typ is a member of count, read
// from the server's schema. ok is false too where Selected cannot see what
// the objects' resolvers read, so that the caller reads every column:
//   - a field of the connection other than edges, pageInfo, totalCount and
//     __typename, or of an edge other than node, cursor and __typename (an
//     application's own, which may resolve from the nodes);
//   - without the server's schema (a context outside a gqlgen server), a
//     fragment on an interface or a union other than Node.
//
// A field merged from several selections of one response key (a named
// fragment spread where the field is also selected) is computed once per
// merged selection. The result is shared: the caller must not change it.
func Selected(ctx context.Context, typ string) (fields []string, ok bool) {
	fc := graphql.GetFieldContext(ctx)
	if fc == nil || fc.Field.Field == nil || fc.Field.Definition == nil || !graphql.HasOperationContext(ctx) {
		return nil, false
	}
	oc := graphql.GetOperationContext(ctx)
	f, t := fc.Field, gqlcore.TableFromContext(ctx)
	return t.Selected(f.Field, f.Selections, typ, func() ([]string, bool) {
		return selected(oc, t.Schema(), f, typ)
	})
}

// selected is Selected's result, computed; schema may be nil.
func selected(oc *graphql.OperationContext, schema *ast.Schema, f graphql.CollectedField, typ string) ([]string, bool) {
	var objs []ast.SelectionSet // the selections on the objects of typ
	switch f.Definition.Type.Name() {
	case typ, "Node":
		objs = []ast.SelectionSet{f.Selections}
	case typ + "Connection":
		conn, ok := collect(oc, schema, f.Selections, typ+"Connection")
		if !ok {
			return nil, false
		}
		for _, e := range conn {
			switch e.Name {
			case "pageInfo", "totalCount", "__typename":
			case "edges":
				edges, ok := collect(oc, schema, e.Selections, typ+"Edge")
				if !ok {
					return nil, false
				}
				for _, n := range edges {
					switch n.Name {
					case "cursor", "__typename":
					case "node":
						objs = append(objs, n.Selections)
					default:
						return nil, false
					}
				}
			default:
				return nil, false
			}
		}
	default:
		return nil, false
	}
	fields := []string{}
	for _, sels := range objs {
		cs, ok := collect(oc, schema, sels, typ)
		if !ok {
			return nil, false
		}
		for _, c := range cs {
			if c.Name != "__typename" && !slices.Contains(fields, c.Name) {
				fields = append(fields, c.Name)
			}
		}
	}
	return fields, true
}

// collect is the fields sels selects on an object of the type typ, through
// its fragments on typ and on each interface and union typ is a member of,
// as schema says. Without a schema, those are typ and Node, and a fragment on
// another interface or union fails it.
func collect(oc *graphql.OperationContext, schema *ast.Schema, sels ast.SelectionSet, typ string) ([]graphql.CollectedField, bool) {
	sat := []string{typ, "Node"}
	if d := schemaType(schema, typ); d != nil {
		sat = []string{typ}
		for _, i := range schema.GetImplements(d) {
			sat = append(sat, i.Name)
		}
	} else if abstractFragment(oc, sels, sat, map[string]bool{}) {
		return nil, false
	}
	return graphql.CollectFields(oc, sels, sat), true
}

// schemaType is typ's definition in schema; nil when schema is nil or has
// none.
func schemaType(schema *ast.Schema, typ string) *ast.Definition {
	if schema == nil {
		return nil
	}
	return schema.Types[typ]
}

// abstractFragment reports whether sels holds, at any depth of fragments, a
// fragment on an interface or a union not in sat, or on a type it cannot
// tell is an object type.
func abstractFragment(oc *graphql.OperationContext, sels ast.SelectionSet, sat []string, seen map[string]bool) bool {
	abstract := func(cond string, d *ast.Definition) bool {
		return cond != "" && !slices.Contains(sat, cond) && (d == nil || d.Kind != ast.Object)
	}
	for _, sel := range sels {
		switch sel := sel.(type) {
		case *ast.InlineFragment:
			// The validator gives an inline fragment its parent's definition;
			// the fields in it have the type condition's.
			var d *ast.Definition
			for _, c := range sel.SelectionSet {
				if f, ok := c.(*ast.Field); ok && f.ObjectDefinition != nil {
					d = f.ObjectDefinition
					break
				}
			}
			if abstract(sel.TypeCondition, d) || abstractFragment(oc, sel.SelectionSet, sat, seen) {
				return true
			}
		case *ast.FragmentSpread:
			if seen[sel.Name] {
				continue
			}
			seen[sel.Name] = true
			d := oc.Doc.Fragments.ForName(sel.Name)
			if d == nil {
				continue
			}
			if abstract(d.TypeCondition, d.Definition) || abstractFragment(oc, d.SelectionSet, sat, seen) {
				return true
			}
		}
	}
	return false
}

// Narrow returns q reading only those of cols that q selects; nil cols
// leave q as it is.
//
// It never adds a column q leaves out (Query.Selects). When q selects none
// of cols, q is left as it is too.
func Narrow[E any](q velox.Query[E], cols []velox.Column[E]) velox.Query[E] {
	if cols == nil {
		return q
	}
	keep := make([]velox.Column[E], 0, len(cols))
	for _, c := range cols {
		if q.Selects(c) {
			keep = append(keep, c)
		}
	}
	if len(keep) == 0 {
		return q
	}
	return q.Columns(keep...)
}
