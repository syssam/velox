package veloxgql_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/validator"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
)

// costSDL is a schema of connections, a list and the Node interface.
const costSDL = `
interface Node { id: ID! }
type PageInfo { hasNextPage: Boolean! endCursor: String }
type Query {
	users(first: Int, last: Int): UserConnection!
	node(id: ID!): Node
	nodes(ids: [ID!]!): [Node]!
}
type User implements Node {
	id: ID!
	name: String!
	tags: [String!]!
	friends: [User!]!
	posts(first: Int, last: Int): PostConnection!
}
type Post implements Node {
	id: ID!
	title: String!
	author: User!
}
type UserConnection { edges: [UserEdge!]! pageInfo: PageInfo! totalCount: Int! }
type UserEdge { node: User! cursor: String! }
type PostConnection { edges: [PostEdge!]! pageInfo: PageInfo! totalCount: Int! }
type PostEdge { node: Post! cursor: String! }
`

// costOf parses and validates query, and returns its Cost under l.
func costOf(t *testing.T, query string, vars map[string]any, l veloxgql.Limits) int {
	t.Helper()
	schema := gqlparser.MustLoadSchema(&ast.Source{Input: costSDL})
	doc, errs := gqlparser.LoadQueryWithRules(schema, query, nil)
	if len(errs) > 0 {
		t.Fatalf("%s: %v", query, errs)
	}
	op := doc.Operations[0]
	if vars != nil {
		var err error
		if vars, err = validator.VariableValues(schema, op, vars); err != nil {
			t.Fatal(err)
		}
	}
	return veloxgql.Cost(schema, op, vars, l)
}

func TestCostConnections(t *testing.T) {
	l := veloxgql.DefaultLimits()
	l.MaxPage = 7
	for _, c := range []struct {
		name  string
		query string
		want  int
	}{
		{"first", `{ users(first: 10) { edges { node { name } } } }`, 1 + 10*(1+1+1)},
		{"neither first nor last is MaxPage", `{ users { edges { node { name } } } }`, 1 + 7*3},
		{"last", `{ users(last: 5) { edges { node { name } } } }`, 1 + 5*3},
		{"nested", `{ users(first: 10) { edges { node { posts(first: 2) { totalCount } } } } }`, 1 + 10*(1+1+(1+2*1))},
		{"edges and totalCount", `{ users(first: 4) { totalCount pageInfo { hasNextPage } } }`, 1 + 4*(1+2)},
		{"first 0", `{ users(first: 0) { edges { node { name } } } }`, 1},
	} {
		if got := costOf(t, c.query, nil, l); got != c.want {
			t.Errorf("%s: cost %d, want %d", c.name, got, c.want)
		}
	}
}

func TestCostVariables(t *testing.T) {
	l := veloxgql.DefaultLimits()
	const q = `query ($n: Int) { users(first: $n) { edges { node { name } } } }`
	if got, want := costOf(t, q, map[string]any{"n": 7}, l), 1+7*3; got != want {
		t.Errorf("first: $n = 7: cost %d, want %d", got, want)
	}
	if got, want := costOf(t, q, map[string]any{"n": float64(7)}, l), 1+7*3; got != want {
		t.Errorf("first: $n = 7.0 (JSON): cost %d, want %d", got, want)
	}
	if got, want := costOf(t, q, nil, l), 1+100*3; got != want {
		t.Errorf("first: $n unset: cost %d, want MaxPage's %d", got, want)
	}
	const d = `query ($n: Int = 3) { users(first: $n) { totalCount } }`
	if got, want := costOf(t, d, nil, l), 1+3*1; got != want {
		t.Errorf("first: $n with default 3: cost %d, want %d", got, want)
	}
}

// TestCostDodges: the ways a client might make a request look cheaper than it
// is are each counted (Review Focus 4).
func TestCostDodges(t *testing.T) {
	l := veloxgql.DefaultLimits()
	const one = `users(first: 10) { edges { node { name } } }`
	single := costOf(t, `{ `+one+` }`, nil, l)

	// Aliases.
	var aliases strings.Builder
	for i := range 50 {
		fmt.Fprintf(&aliases, "a%d: %s ", i, one)
	}
	if got := costOf(t, `{ `+aliases.String()+` }`, nil, l); got != 50*single {
		t.Errorf("50 aliases: cost %d, want %d", got, 50*single)
	}

	// A fragment spread at 30 places costs 30 times the fragment.
	var spreads strings.Builder
	for i := range 30 {
		fmt.Fprintf(&spreads, "a%d: users(first: 1) { edges { node { ...F } } } ", i)
	}
	frag := `fragment F on User { name posts(first: 10) { totalCount } }`
	perSpread := 1 + 1*(1+1+(1+1+10*1)) // users(first: 1) { edges { node { name posts } } }
	if got := costOf(t, `{ `+spreads.String()+` } `+frag, nil, l); got != 30*perSpread {
		t.Errorf("30 spreads: cost %d, want %d", got, 30*perSpread)
	}

	// A fragment DAG: each fragment spreads the next twice, so costing every
	// spread again would take 2^40 steps. Each is costed once.
	var dag strings.Builder
	for i := range 40 {
		fmt.Fprintf(&dag, "fragment F%d on User { name ...F%d ...F%d } ", i, i+1, i+1)
	}
	dag.WriteString(`fragment F40 on User { name }`)
	got := costOf(t, `{ users(first: 1) { edges { node { ...F0 } } } } `+dag.String(), nil, l)
	if want := 1 + 1*(1+1+(int64Pow2(41)-1)); got != want {
		t.Errorf("fragment DAG: cost %d, want %d", got, want)
	}

	// Huge first at five levels saturates, and does not wrap.
	const huge = 1000000
	deep := fmt.Sprintf(`{ users(first: %[1]d) { edges { node { posts(first: %[1]d) { edges { node { author { posts(first: %[1]d) { edges { node { author { posts(first: %[1]d) { edges { node { author { posts(first: %[1]d) { totalCount } } } } } } } } } } } } } } } } }`, huge)
	if got := costOf(t, deep, nil, l); got != 1<<62 {
		t.Errorf("five levels of first: %d: cost %d, want 1<<62", huge, got)
	}
	if got := costOf(t, `query ($n: Int) { users(first: $n) { totalCount } }`, map[string]any{"n": 1 << 30}, l); got != 1+1<<30 {
		t.Errorf("first: 1<<30: cost %d", got)
	}

	// Inline fragments on node: every branch counts.
	const node = `{ node(id: "x") { ... on User { posts { totalCount } } ... on Post { title } } }`
	if got, want := costOf(t, node, nil, l), 1+(1+100*1)+1; got != want {
		t.Errorf("node with two branches: cost %d, want %d", got, want)
	}
	// @skip and @include are not read.
	if got, want := costOf(t, `{ users(first: 2) @skip(if: true) { totalCount } }`, nil, l), 1+2; got != want {
		t.Errorf("@skip: cost %d, want %d", got, want)
	}
}

func int64Pow2(n int) int { return 1 << n }

func TestCostNodesAndLists(t *testing.T) {
	l := veloxgql.DefaultLimits()
	l.MaxList = 20
	for _, c := range []struct {
		name  string
		query string
		vars  map[string]any
		want  int
	}{
		{"nodes by its ids", `{ nodes(ids: ["a", "b", "c"]) { id } }`, nil, 1 + 3*1},
		{"nodes by variable ids", `query ($ids: [ID!]!) { nodes(ids: $ids) { id } }`, map[string]any{"ids": []any{"a", "b"}}, 1 + 2*1},
		{"a list of objects", `{ users(first: 1) { edges { node { friends { name } } } } }`, nil, 1 + 1*(1+1+(1+20*1))},
		{"a list of scalars", `{ users(first: 1) { edges { node { tags } } } }`, nil, 1 + 1*(1+1+1)},
		{"__typename", `{ __typename }`, nil, 1},
		{"__schema", `{ __schema { types { name fields { name type { name ofType { name } } } } } }`, nil, 0},
		{"__type", `{ __type(name: "User") { name fields { name } } }`, nil, 0},
	} {
		if got := costOf(t, c.query, c.vars, l); got != c.want {
			t.Errorf("%s: cost %d, want %d", c.name, got, c.want)
		}
	}
}

// saturating is a query whose cost saturates by itself.
const saturating = `users(first: 1000000) { edges { node { posts(first: 1000000) { edges { node { author { posts(first: 1000000) { edges { node { author { posts(first: 1000000) { edges { node { author { posts(first: 1000000) { totalCount } } } } } } } } } } } } } } } }`

// saturatingUser is a selection on a User whose cost saturates by itself.
const saturatingUser = `posts(first: 1000000) { edges { node { author { posts(first: 1000000) { edges { node { author { posts(first: 1000000) { edges { node { author { posts(first: 1000000) { totalCount } } } } } } } } } } } } }`

// TestCostSaturationAdds: sums of saturated costs stay saturated and never
// wrap to a negative cost, which would pass any MaxCost.
func TestCostSaturationAdds(t *testing.T) {
	l := veloxgql.DefaultLimits()
	for _, c := range []struct{ name, query string }{
		{"two aliases", `{ a: ` + saturating + ` b: ` + saturating + ` }`},
		{"three aliases", `{ a: ` + saturating + ` b: ` + saturating + ` c: ` + saturating + ` }`},
		{"four aliases", `{ a: ` + saturating + ` b: ` + saturating + ` c: ` + saturating + ` d: ` + saturating + ` }`},
		{"under a multiplier", `{ users(first: 100) { edges { node { posts(first: 100) { edges { node { author { a: ` + saturatingUser + ` b: ` + saturatingUser + ` } } } } } } } }`},
		{"a saturated fragment spread twice", `{ ...F ...F } fragment F on Query { ` + saturating + ` }`},
	} {
		if got := costOf(t, c.query, nil, l); got != 1<<62 {
			t.Errorf("%s: cost %d, want 1<<62", c.name, got)
		}
	}
}

// TestCostJSONNumber: variables as gqlgen decodes them over HTTP are
// json.Number, and are read.
func TestCostJSONNumber(t *testing.T) {
	l := veloxgql.DefaultLimits()
	const q = `query ($n: Int) { users(first: $n) { edges { node { name } } } }`
	if got, want := costOf(t, q, map[string]any{"n": json.Number("7")}, l), 1+7*3; got != want {
		t.Errorf("json.Number 7: cost %d, want %d", got, want)
	}
}

// TestCostNegativeFirst: a negative first is refused by the resolver, and
// counts as 0 here.
func TestCostNegativeFirst(t *testing.T) {
	l := veloxgql.DefaultLimits()
	if got := costOf(t, `{ users(first: -5) { edges { node { name } } } }`, nil, l); got != 1 {
		t.Errorf("first: -5: cost %d, want 1", got)
	}
}

// TestCostConnectionWithoutArguments: a field of a connection type that has
// no first and last arguments costs MaxPage, the safer value.
func TestCostConnectionWithoutArguments(t *testing.T) {
	schema := gqlparser.MustLoadSchema(&ast.Source{Input: costSDL + `extend type User { allPosts: PostConnection! }`})
	doc, errs := gqlparser.LoadQueryWithRules(schema, `{ users(first: 1) { edges { node { allPosts { totalCount } } } } }`, nil)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	l := veloxgql.DefaultLimits()
	if got, want := veloxgql.Cost(schema, doc.Operations[0], nil, l), 1+1*(1+1+(1+100*1)); got != want {
		t.Errorf("cost %d, want %d", got, want)
	}
}

// TestCostSelectsOperation: the Extension costs the operation gqlgen
// selected, not the first of the document, and refuses a saturated one.
func TestCostSelectsOperation(t *testing.T) {
	ext := veloxgql.NewExtension(&velox.DB{}, veloxgql.WithLimits(veloxgql.Limits{MaxCost: 50}))
	schema := gqlparser.MustLoadSchema(&ast.Source{Input: costSDL})
	if err := ext.Validate(&graphql.ExecutableSchemaMock{SchemaFunc: func() *ast.Schema { return schema }}); err != nil {
		t.Fatal(err)
	}
	doc, errs := gqlparser.LoadQueryWithRules(schema, `query Cheap { users(first: 1) { totalCount } } query Dear { users(first: 100) { totalCount } } query Huge { a: `+saturating+` b: `+saturating+` }`, nil)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	for name, refused := range map[string]bool{"Cheap": false, "Dear": true, "Huge": true} {
		op := doc.Operations.ForName(name)
		err := ext.MutateOperationContext(context.Background(), &graphql.OperationContext{Operation: op, Doc: doc, OperationName: name})
		if (err != nil) != refused {
			t.Errorf("%s: error %v, refused want %v", name, err, refused)
		}
	}
}
