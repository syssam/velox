// Package graphqlgo runs velox's GraphQL field collection under graphql-go
// (github.com/syssam/graphql-go) instead of gqlgen.
//
// The generated Paginate and CollectFields methods read the query's
// selection to project the columns it reads, eager-load the edges it
// traverses and skip COUNT(*) when totalCount is not selected. They read it
// through gqlrelay.SelectedField, and Collect supplies that from graphql-go:
//
//	exec := graphql.NewExecutor(schema, graphqlgo.Collect())
//
// Without it the same resolvers still answer correctly, one query per edge
// per row and a COUNT(*) per connection.
package graphqlgo

import (
	"context"

	graphql "github.com/syssam/graphql-go"

	"github.com/syssam/velox/contrib/graphql/gqlrelay"
)

// Collect returns the executor option that lets velox read every
// operation's selection.
func Collect() graphql.ExecutorOption {
	return graphql.WithOperationInterceptor(interceptor)
}

var interceptor = graphql.OperationInterceptorFunc(
	func(ctx context.Context, oc *graphql.OperationContext, next graphql.OperationHandler) *graphql.Response {
		return next(gqlrelay.WithSelectionSource(ctx, source), oc)
	})

// source answers for the resolver executing in ctx. velox asks only the
// fields beneath the resolved one for their arguments, so the resolved field
// carries none: its own were decoded into the resolver's argument struct.
func source(ctx context.Context) (gqlrelay.SelectedField, bool) {
	fc := graphql.FieldFrom(ctx)
	if fc == nil {
		return nil, false
	}
	var vars map[string]any
	if oc := graphql.OperationFrom(ctx); oc != nil {
		vars = oc.Variables
	}
	return &field{name: fc.Field.Name, sel: fc.Selection(), vars: vars, ctx: ctx}, true
}

// field is gqlrelay.SelectedField over graphql.Selection.
type field struct {
	name string
	args map[string]any
	sel  graphql.Selection
	vars map[string]any
	// ctx is the resolver's, which carries the operation's authorization
	// Decision.
	ctx context.Context
}

func (f *field) FieldName() string         { return f.name }
func (f *field) Arguments() map[string]any { return f.args }

// Fields returns the selection beneath f. With satisfies it is what applies
// to those types: velox passes an object type with the interfaces it
// implements, and graphql-go keys an abstract selection by object type, so
// the interface names find nothing and the object's fields are the answer.
// Two names can yield one response key, and a concrete selection answers
// every name with itself, so each key is kept once.
//
// A field authorization withholds -- Deny, Null or Zero for this request --
// is left out: velox neither selects its column nor loads anything beneath
// it, so rows the caller may not see are never queried.
func (f *field) Fields(satisfies []string) []gqlrelay.SelectedField {
	var (
		backing []field
		seen    map[string]bool
	)
	add := func(s graphql.Selection) {
		for sf := range s.Fields() {
			if sf.Withheld(f.ctx) {
				continue
			}
			if seen != nil {
				if seen[sf.Alias] {
					continue
				}
				seen[sf.Alias] = true
			}
			// A malformed variable fails the field when it resolves, with
			// the error the client should see; planned without arguments,
			// the worst case is an edge loaded that nothing then reads.
			args, _ := sf.ArgumentMap(f.vars)
			backing = append(backing, field{name: sf.Name, args: args, sel: sf.Selection(), vars: f.vars, ctx: f.ctx})
		}
	}
	if len(satisfies) == 0 {
		add(f.sel)
	} else {
		seen = map[string]bool{}
		for _, typ := range satisfies {
			add(f.sel.ForType(typ))
		}
	}
	out := make([]gqlrelay.SelectedField, len(backing))
	for i := range backing {
		out[i] = &backing[i]
	}
	return out
}

// NodeSelects reports whether the connection being resolved in ctx selects
// name on its nodes, edges { node { name } }, under any alias.
//
// A resolver uses it to load what a hand-written field reads and velox
// cannot see. velox leaves an edge the resolver loaded whole, so
//
//	q := client.Order.Query()
//	if graphqlgo.NodeSelects(ctx, "totalCents") {
//		q = q.WithItems() // totalCents sums every item's price
//	}
//
// keeps the price column that a client selecting only items { quantity }
// would otherwise have projected away.
func NodeSelects(ctx context.Context, name string) bool {
	for edges := range graphql.SelectionFrom(ctx).Fields() {
		if edges.Name != "edges" {
			continue
		}
		for node := range edges.Selection().Fields() {
			if node.Name == "node" && node.Selection().Has(name) {
				return true
			}
		}
	}
	return false
}
