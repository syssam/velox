package gqlimpl

import (
	"context"
	"slices"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql/internal/shared"
)

// Selected returns the names of the fields selected on the objects of the
// GraphQL type typ that the field of ctx returns: through edges and node for
// a connection field, the field's own selection otherwise, with fragments
// whose type is typ or Node, and without __typename. ok is false outside a
// gqlgen field, and when the field returns neither typ nor a connection to
// it nor Node. The result is computed once per *ast.Field per operation.
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
	f := fc.Field
	return shared.TableFrom(ctx).Selected(f.Field, f.Selections, typ, func() ([]string, bool) {
		return selected(oc, f, typ)
	})
}

// selected is Selected's result, computed.
func selected(oc *graphql.OperationContext, f graphql.CollectedField, typ string) ([]string, bool) {
	var objs []ast.SelectionSet // the selections on the objects of typ
	switch f.Definition.Type.Name() {
	case typ, "Node":
		objs = []ast.SelectionSet{f.Selections}
	case typ + "Connection":
		for _, e := range graphql.CollectFields(oc, f.Selections, []string{typ + "Connection"}) {
			if e.Name != "edges" {
				continue
			}
			for _, n := range graphql.CollectFields(oc, e.Selections, []string{typ + "Edge"}) {
				if n.Name == "node" {
					objs = append(objs, n.Selections)
				}
			}
		}
	default:
		return nil, false
	}
	fields := []string{}
	for _, sels := range objs {
		for _, c := range graphql.CollectFields(oc, sels, []string{typ, "Node"}) {
			if c.Name != "__typename" && !slices.Contains(fields, c.Name) {
				fields = append(fields, c.Name)
			}
		}
	}
	return fields, true
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
