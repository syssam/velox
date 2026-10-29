package veloxgql_test

import (
	"errors"
	"fmt"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox2/contrib/graphql/veloxgql"
)

// An ID names its GraphQL type, so DecodeID refuses an ID of another
// type before anything is looked up. Renaming a type with graphql.Type
// changes every ID and cursor that clients hold.
func ExampleDecodeID() {
	id := veloxgql.EncodeID("User", int64(42))
	fmt.Println(id)
	typ, _ := veloxgql.IDType(id)
	fmt.Println(typ)
	key, err := veloxgql.DecodeID[int64](id, "User")
	fmt.Println(key, err)
	_, err = veloxgql.DecodeID[int64](id, "Account")
	fmt.Println(errors.Is(err, veloxgql.ErrWrongIDType))
	// Output:
	// VXNlcjo0Mg
	// User
	// 42 <nil>
	// true
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
	fmt.Println(veloxgql.Cost(schema, doc.Operations[0], nil, l))
	// Output:
	// 191
}
