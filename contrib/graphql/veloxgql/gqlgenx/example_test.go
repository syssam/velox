package gqlgenx_test

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
)

// An ID is the entity's key. A client may send it as a string or as an
// integer, and gets it back as a string, as the GraphQL specification says.
func ExampleUnmarshalInt64ID() {
	key, err := gqlgenx.UnmarshalInt64ID("42")
	fmt.Println(key, err)
	key, err = gqlgenx.UnmarshalInt64ID(json.Number("42"))
	fmt.Println(key, err)
	_, err = gqlgenx.UnmarshalInt64ID(4.2)
	fmt.Println(err)
	var b strings.Builder
	gqlgenx.MarshalInt64ID(42).MarshalGQL(&b)
	fmt.Println(b.String())
	// Output:
	// 42 <nil>
	// 42 <nil>
	// veloxgql: ID must be a string of an integer, or an integer
	// "42"
}

const exampleSDL = `
type Query { users(first: Int): UserConnection! }
type UserConnection { edges: [UserEdge!]!, pageInfo: PageInfo! }
type UserEdge { node: User!, cursor: String! }
type PageInfo { hasNextPage: Boolean! }
type User { id: ID!, name: String!, friends(first: Int): UserConnection! }
`

// Cost prices a request before it runs: each field is 1, and a connection
// multiplies what it selects by its page size.
func ExampleCost() {
	schema := gqlparser.MustLoadSchema(&ast.Source{Input: exampleSDL})
	doc, errs := gqlparser.LoadQueryWithRules(schema, `{ users(first: 10) { edges { node { name friends(first: 5) { edges { node { name } } } } } } }`, nil)
	if errs != nil {
		fmt.Println(errs)
		return
	}
	l := veloxgql.Limits{MaxPage: 100, MaxList: 100}
	fmt.Println(gqlgenx.Cost(schema, doc.Operations[0], nil, l))
	// Output:
	// 342
}
