package graphqlgo

import (
	"github.com/syssam/graphql-go"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// Limits returns the graphql-go executor options that hold a request to
// veloxgql's limits of a whole request, each zero field of l taken from
// veloxgql.DefaultLimits(): MaxDepth (graphql.WithMaxDepth), MaxTokens
// (graphql.WithMaxTokens), MaxErrors (graphql.WithMaxErrors),
// MaxResponseBytes (graphql.WithMaxResponseBytes) and Timeout
// (graphql.WithOperationTimeout). graphql-go enforces them, with its own
// codes and its own semantics: its depth does not count introspection, and
// its response size is the data's bytes. The other limits are velox's
// alone: MaxPage, MaxList, MaxOrderTerms and the filter limits, which
// gqlgraph reads from the state With puts in each operation's context,
// MaxCost and MaxIntrospectionCost, which WithCost enforces, and the
// document walks of gqlgenx (MaxFields, MaxFragments, MaxNameBytes,
// MaxLiteralBytes, MaxVariableNodes), and MaxBodyBytes, which a transport
// of graphql-go bounds. Give them after With's, so that they replace any
// earlier option of graphql-go's that sets the same limit:
//
//	exec := graphql.NewExecutor(s, append(graphqlgo.With(db, veloxgql.WithLimits(l)), graphqlgo.Limits(l)...)...)
func Limits(l veloxgql.Limits) []graphql.ExecutorOption {
	l = veloxgql.NewConfig(veloxgql.WithLimits(l)).Limits
	return []graphql.ExecutorOption{
		graphql.WithMaxDepth(l.MaxDepth),
		graphql.WithMaxTokens(l.MaxTokens),
		graphql.WithMaxErrors(l.MaxErrors),
		graphql.WithMaxResponseBytes(int64(l.MaxResponseBytes)),
		graphql.WithOperationTimeout(l.Timeout),
	}
}
