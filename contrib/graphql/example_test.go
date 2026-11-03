package graphql_test

import (
	"fmt"

	"github.com/syssam/velox/contrib/graphql"
)

// An entity's Annotations choose what velox-graphql generates for it: here
// only CreateXInput, whose Create a resolver the application declares calls.
func ExampleMutations() {
	a := graphql.Mutations(graphql.MutationCreate)
	fmt.Println(a.Mutations)
	// Output: [create]
}

// Skip leaves generated parts out, and WhereOps narrows a field's filters.
func ExampleSkip() {
	skip := graphql.Skip(graphql.SkipMutationUpdateInput)
	ops := graphql.WhereOps(graphql.OpEQ, graphql.OpIn)
	fmt.Println(skip.Skip == graphql.SkipMutationUpdateInput, ops.WhereOps == graphql.OpEQ|graphql.OpIn)
	// Output: true true
}
