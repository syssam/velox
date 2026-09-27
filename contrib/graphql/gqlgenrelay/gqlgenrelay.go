// Package gqlgenrelay connects velox's GraphQL runtime to gqlgen. Importing
// it makes field collection read the selection from gqlgen's request
// context, and it holds the gqlgen marshalers of the Bytes scalar.
//
// Code velox generates imports it unless it was generated with
// graphqlgen.WithoutGQLGen(), so a gqlgen server needs nothing more, and a
// server on another engine -- graphql-go, with contrib/graphqlgo -- links no
// gqlgen at all.
package gqlgenrelay

import (
	"context"

	"github.com/99designs/gqlgen/graphql"

	"github.com/syssam/velox/contrib/graphql/gqlrelay"
)

func init() { gqlrelay.SetDefaultSelectionSource(Source) }

// Source is the field gqlgen is resolving in ctx.
func Source(ctx context.Context) (gqlrelay.SelectedField, bool) {
	fc := graphql.GetFieldContext(ctx)
	if fc == nil || !graphql.HasOperationContext(ctx) {
		return nil, false
	}
	return &field{oc: graphql.GetOperationContext(ctx), f: fc.Field}, true
}

// field is gqlrelay.SelectedField over gqlgen's collected fields.
type field struct {
	oc *graphql.OperationContext
	f  graphql.CollectedField
}

func (g *field) FieldName() string { return g.f.Name }

func (g *field) Arguments() map[string]any {
	if g.f.Field == nil {
		return nil
	}
	return g.f.ArgumentMap(g.oc.Variables)
}

// Fields wraps every collected field in one backing array: a field boxed on
// its own is an allocation per selected field, on a path that runs for every
// resolver that collects.
func (g *field) Fields(satisfies []string) []gqlrelay.SelectedField {
	collected := graphql.CollectFields(g.oc, g.f.Selections, satisfies)
	if len(collected) == 0 {
		return nil
	}
	backing := make([]field, len(collected))
	out := make([]gqlrelay.SelectedField, len(collected))
	for i, f := range collected {
		backing[i] = field{oc: g.oc, f: f}
		out[i] = &backing[i]
	}
	return out
}
