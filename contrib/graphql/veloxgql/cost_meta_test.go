package veloxgql_test

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/contrib/graphql/internal/sdl"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/plugin"
)

// graphiQL is the query GraphiQL and most clients send for the schema.
const graphiQL = `query IntrospectionQuery {
	__schema {
		queryType { name }
		mutationType { name }
		subscriptionType { name }
		types { ...FullType }
		directives { name description locations args { ...InputValue } }
	}
}
fragment FullType on __Type {
	kind name description
	fields(includeDeprecated: true) { name description args { ...InputValue } type { ...TypeRef } isDeprecated deprecationReason }
	inputFields { ...InputValue }
	interfaces { ...TypeRef }
	enumValues(includeDeprecated: true) { name description isDeprecated deprecationReason }
	possibleTypes { ...TypeRef }
}
fragment InputValue on __InputValue { name description type { ...TypeRef } defaultValue }
fragment TypeRef on __Type {
	kind name
	ofType { kind name ofType { kind name ofType { kind name ofType { kind name ofType { kind name ofType { kind name ofType { kind name } } } } } } }
}`

// richSchema is the schema velox-graphql writes for n entities, each with 18
// fields, three edges with their where inputs, an order and all the
// mutations.
func richSchema(t testing.TB, n int) *ast.Schema {
	t.Helper()
	return richSchemaOf(t, n)
}

// richSchemaOf is richSchema with these annotations on every field but the key.
func richSchemaOf(t testing.TB, n int, field ...graphql.Annotation) *ast.Schema {
	t.Helper()
	enc := func(as ...graphql.Annotation) map[string]json.RawMessage {
		var a graphql.Annotation
		for _, x := range as {
			a = x.Merge(a).(graphql.Annotation)
		}
		b, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		return map[string]json.RawMessage{"graphql": b}
	}
	f := func(name, typ string) *plugin.Field {
		return &plugin.Field{
			Name: name, GoName: strings.ToUpper(name[:1]) + name[1:], Column: name, Type: typ, ElemType: map[string]string{"time": "time.Time"}[typ], PkgPath: map[string]string{"time": "time"}[typ],
			Annotations: enc(append(field, graphql.OrderField(strings.ToUpper(name)))...),
		}
	}
	m := &plugin.Model{Version: plugin.ModelVersion}
	for i := range n {
		name, next := fmt.Sprintf("E%d", i), fmt.Sprintf("E%d", (i+1)%n)
		nullable := f("note", "string")
		nullable.Annotations = nil
		nullable.Nullable, nullable.Pointer = true, true
		m.Entities = append(m.Entities, &plugin.Entity{
			Name: name, PrimaryKey: []int{0},
			Fields: []*plugin.Field{
				{Name: "id", GoName: "ID", Column: "id", Type: "int64", AutoID: true},
				f("title", "string"), f("slug", "string"), f("summary", "string"), nullable,
				f("views", "int"), f("score", "int"), f("rank", "int"),
				f("createdAt", "time"), f("updatedAt", "time"), f("price", "int"), f("stock", "int"), f("weight", "int"),
				f("code", "string"), f("label", "string"), f("kind", "string"), f("region", "string"), f("publishedAt", "time"), f("expiresAt", "time"),
			},
			Edges: []*plugin.Edge{
				{Name: "items", GoName: "Items", Kind: plugin.M2M, Target: next, Annotations: enc(graphql.WhereEdge())},
				{Name: "peers", GoName: "Peers", Kind: plugin.M2M, Target: next, Annotations: enc(graphql.WhereEdge())},
				{Name: "links", GoName: "Links", Kind: plugin.M2M, Target: name, Annotations: enc(graphql.WhereEdge())},
			},
			Annotations: enc(graphql.QueryField(fmt.Sprintf("q%d", i)), graphql.MultiOrder(), graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate)),
		})
	}
	src, err := sdl.Render(m)
	if err != nil {
		t.Fatal(err)
	}
	s, err := sdl.Check(src)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// metaCost is the cost of query on schema under l.
func metaCost(t testing.TB, schema *ast.Schema, query string, l veloxgql.Limits) int {
	t.Helper()
	doc, errs := gqlparser.LoadQueryWithRules(schema, query, nil)
	if len(errs) > 0 {
		t.Fatalf("%v", errs)
	}
	return veloxgql.Cost(schema, doc.Operations[0], nil, l)
}

// TestGraphiQLOnARichSchema: the default MaxIntrospectionCost admits GraphiQL's
// query on 150 rich entities (decision 0027: 500 cost 688046, which is why a
// larger schema raises the limit).
func TestGraphiQLOnARichSchema(t *testing.T) {
	if testing.Short() {
		t.Skip("renders 150 entities")
	}
	l := veloxgql.DefaultLimits()
	if l.MaxIntrospectionCost <= l.MaxCost {
		t.Fatalf("MaxIntrospectionCost %d, MaxCost %d", l.MaxIntrospectionCost, l.MaxCost)
	}
	for _, n := range []int{30, 80, 150} {
		schema := richSchema(t, n)
		got := metaCost(t, schema, graphiQL, l)
		t.Logf("%d entities: %d", n, got)
		if got > l.MaxIntrospectionCost {
			t.Errorf("%d entities: GraphiQL's query costs %d, over MaxIntrospectionCost %d", n, got, l.MaxIntrospectionCost)
		}
		// 39984 since the plugin writes no Mutation type (decision 0033).
		if n == 30 && got < 35000 {
			t.Errorf("30 entities cost %d: the schema is not rich", got)
		}
	}
}

// TestGraphiQLWithWhereOps: a where input of every field's predicates costs more: with
// WhereOps on each of the 18 fields the default admits GraphiQL's query on fewer entities
// than richSchema's 150, which limits.go, doc.go and decision 0027 say.
func TestGraphiQLWithWhereOps(t *testing.T) {
	if testing.Short() {
		t.Skip("renders 100 entities")
	}
	l := veloxgql.DefaultLimits()
	for n, fits := range map[int]bool{95: true, 105: false} {
		got := metaCost(t, richSchemaOf(t, n, graphql.WhereOps()), graphiQL, veloxgql.Limits{MaxIntrospectionCost: 1 << 30})
		t.Logf("%d entities: %d", n, got)
		if (got <= l.MaxIntrospectionCost) != fits {
			t.Errorf("%d entities cost %d, and the limit is %d", n, got, l.MaxIntrospectionCost)
		}
	}
}

// TestIntrospectionBudgetIsItsOwn: raising one limit does not loosen the other,
// and the walk's work stops at MaxIntrospectionCost.
func TestIntrospectionBudgetIsItsOwn(t *testing.T) {
	schema := richSchema(t, 30)
	l := veloxgql.DefaultLimits()
	l.MaxIntrospectionCost = 5
	if got := metaCost(t, schema, `{ __schema { types { name } } }`, l); got < veloxgql.DefaultLimits().MaxCost {
		t.Errorf("over its limit: cost %d", got)
	}
	var before, after runtime.MemStats
	var q strings.Builder
	q.WriteString("{ ")
	for i := range 1500 {
		fmt.Fprintf(&q, "a%d: __schema { types { fields { args { type { name } } type { name ofType { name } } } } } ", i)
	}
	q.WriteString("}")
	doc, errs := gqlparser.LoadQueryWithRules(schema, q.String(), nil)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	l = veloxgql.DefaultLimits()
	l.MaxIntrospectionCost = 10000
	runtime.ReadMemStats(&before)
	got := veloxgql.Cost(schema, doc.Operations[0], nil, l)
	runtime.ReadMemStats(&after)
	if got <= l.MaxIntrospectionCost {
		t.Errorf("cost %d", got)
	}
	if n := after.TotalAlloc - before.TotalAlloc; n > 32<<20 {
		t.Errorf("a walk stopped at 10000 allocated %d bytes", n)
	}
}
