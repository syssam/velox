package graphqlgen

import "github.com/syssam/velox/contrib/graphql"

// The annotation package keeps its mutation bits unexported; these are the
// values its public constructors set.
var (
	mutCreate = graphql.Mutations(graphql.MutationCreate()).Mutations
	mutUpdate = graphql.Mutations(graphql.MutationUpdate()).Mutations
)
