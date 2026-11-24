package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// e2eNodeSchema is a customer, with an image_url whose Go name is ImageURL,
// and a memo whose Read policy hides the memos titled hidden. No type has a
// QueryField, so the root query holds node and nodes alone.
const e2eNodeSchema = `package schema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

type Customer struct{ schema.Schema }

func (Customer) Fields() []schema.Field {
	return []schema.Field{field.String("name"), field.String("image_url")}
}

type Memo struct{ schema.Schema }

func (Memo) Fields() []schema.Field { return []schema.Field{field.String("title")} }

// Policy hides the memos titled hidden.
func (Memo) Policy() velox.Policy {
	return velox.Policy{
		Read: func(context.Context, *velox.DB) (velox.Predicate, error) { return velox.FieldNEQ("title", "hidden"), nil },
	}
}
`

// e2eNodeType is a type hint: an ID of 1000 or more is a memo's, as a
// snowflake's bits would say; any other is looked up. It is a file of its
// own, as gqlgen keeps only resolvers in schema.resolvers.go.
const e2eNodeType = `package graph

import (
	"context"

	"example.com/app/gen"
)

// nodeType is the type of an ID of 1000 or more, Memo; "" for any other.
var nodeType = gen.WithNodeType(func(_ context.Context, id int64) (string, error) {
	if id >= 1000 {
		return "Memo", nil
	}
	return "", nil
})
`

// e2eNodeResolvers answers node and nodes with the generated ResolveNode and
// ResolveNodes, with the type hint.
const e2eNodeResolvers = `package graph

import (
	"context"

	"example.com/app/gen"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// Node is the resolver for the node field.
func (r *queryResolver) Node(ctx context.Context, id int64) (gen.Noder, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return gen.ResolveNode(ctx, db, id, nodeType)
}

// Nodes is the resolver for the nodes field.
func (r *queryResolver) Nodes(ctx context.Context, ids []int64) ([]gen.Noder, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return gen.ResolveNodes(ctx, db, ids, nodeType)
}

// Query returns QueryResolver implementation.
func (r *Resolver) Query() QueryResolver { return &queryResolver{r} }

type queryResolver struct{ *Resolver }
`

// e2eNodeRun serves node and nodes, and counts the statements of each
// table: a memo's ID, whose type the hint gives, runs no lookup.
const e2eNodeRun = `package app_test

import (
	"context"
	"database/sql"
	"maps"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	_ "modernc.org/sqlite"

	"example.com/app/gen"
	"example.com/app/graph"
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
)

func TestNode(t *testing.T) {
	raw, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	raw.SetMaxOpenConns(1)
	for _, s := range []string{
		"CREATE TABLE customers (id INTEGER PRIMARY KEY, name TEXT NOT NULL, image_url TEXT NOT NULL)",
		"CREATE TABLE memos (id INTEGER PRIMARY KEY, title TEXT NOT NULL)",
		"INSERT INTO customers (id, name, image_url) VALUES (1, 'ada', 'a.png'), (2, 'bob', 'b.png')",
		"INSERT INTO memos (id, title) VALUES (1000, 'seen'), (1001, 'hidden')",
	} {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	var mu sync.Mutex
	tables := map[string]int{} // statements by table; "" is the lookup
	around := func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		mu.Lock()
		tables[st.Entity.Table]++
		mu.Unlock()
		return next(ctx)
	}
	counts := func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		out := maps.Clone(tables)
		clear(tables)
		return out
	}
	db := velox.Open(velox.SQLite, raw, velox.WithAround(around))
	srv := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{}}))
	srv.Use(gqlgenx.NewExtension(db))
	srv.SetErrorPresenter(gqlgenx.ErrorPresenter(nil))
	srv.AddTransport(transport.POST{})
	query := func(q string) string {
		req := httptest.NewRequest("POST", "/", strings.NewReader(q))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w.Body.String()
	}
	for _, c := range []struct {
		q, want string
		tables  map[string]int
	}{
		{"{\"query\":\"{ node(id: \\\"2\\\") { id ... on Customer { name imageURL } } }\"}",
			"{\"data\":{\"node\":{\"id\":\"2\",\"name\":\"bob\",\"imageURL\":\"b.png\"}}}",
			map[string]int{"": 1, "customers": 1}},
		{"{\"query\":\"{ node(id: \\\"1000\\\") { id ... on Memo { title } } }\"}",
			"{\"data\":{\"node\":{\"id\":\"1000\",\"title\":\"seen\"}}}",
			map[string]int{"memos": 1}},
		{"{\"query\":\"{ nodes(ids: [\\\"1000\\\", \\\"1001\\\"]) { id } }\"}",
			"{\"data\":{\"nodes\":[{\"id\":\"1000\"},null]}}",
			map[string]int{"memos": 1}},
		{"{\"query\":\"{ nodes(ids: [\\\"1\\\", \\\"99\\\", \\\"1000\\\"]) { id ... on Customer { name } } }\"}",
			"{\"data\":{\"nodes\":[{\"id\":\"1\",\"name\":\"ada\"},null,{\"id\":\"1000\"}]}}",
			map[string]int{"": 1, "customers": 1, "memos": 1}},
	} {
		if got := query(c.q); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.q, got, c.want)
		}
		if got := counts(); !maps.Equal(got, c.tables) {
			t.Errorf("%s: statements by table %v, want %v", c.q, got, c.tables)
		}
	}
	// A hidden memo and a missing ID are NOT_FOUND.
	for _, q := range []string{"{\"query\":\"{ node(id: \\\"1001\\\") { id } }\"}", "{\"query\":\"{ node(id: \\\"99\\\") { id } }\"}"} {
		if got := query(q); !strings.Contains(got, "\"code\":\"NOT_FOUND\"") {
			t.Errorf("%s: %s; want NOT_FOUND", q, got)
		}
	}
	// WithFixedNodeType reads one type; a name that is no type of Node is an
	// error that names it, before any statement.
	ctx := context.Background()
	counts()
	n, err := gen.ResolveNode(ctx, db, 1, gen.WithFixedNodeType("Customer"))
	if c, ok := n.(*gen.Customer); err != nil || !ok || c.Name != "ada" {
		t.Errorf("customer 1, fixed: %#v, %v", n, err)
	}
	if _, err := gen.ResolveNodes(ctx, db, []int64{1}, gen.WithFixedNodeType("Invoice")); err == nil || !strings.Contains(err.Error(), "\"Invoice\"") {
		t.Errorf("an unknown type: %v", err)
	}
	if got := counts(); !maps.Equal(got, map[string]int{"customers": 1}) {
		t.Errorf("fixed types: statements by table %v, want one of customers", got)
	}
}
`

// TestNodeOn generates with node=on alone: velox's own API, names from Go
// names, with Relay's Node, every
// type implementing it, and node and nodes in a root query that has no
// other field. gqlgen serves them through the generated ResolveNode and
// ResolveNodes, and a type hint skips the lookup statement.
func TestNodeOn(t *testing.T) {
	t.Parallel() // its own module, in its own directory
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir, run := app(t, map[string]string{
		"schema/schema.go":          e2eNodeSchema,
		"gqlgen.yml":                e2eCollectGqlgen,
		"graph/schema.resolvers.go": e2eNodeResolvers,
		"graph/nodetype.go":         e2eNodeType,
		"node_test.go":              e2eNodeRun,
	}, true)
	if out, err := run("tool", "velox-gen", "-out", "gen", "-plugin", "velox-graphql:node=on", "./schema"); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(dir, "gen", "velox.graphql"))
	if err != nil {
		t.Fatal(err)
	}
	sdl := string(b)
	for _, want := range []string{
		"interface Node @goModel(model: \"example.com/app/gen.Noder\") {",
		"type Customer implements Node {", "type Memo implements Node {", "  imageURL: String!\n",
		"type Query {\n  \"\"\"Fetches an object given its ID.\"\"\"\n  node(id: ID!): Node\n  \"\"\"Lookup nodes by a list of IDs.\"\"\"\n  nodes(ids: [ID!]!): [Node]!\n}\n",
	} {
		if !strings.Contains(sdl, want) {
			t.Errorf("velox.graphql lacks %q:\n%s", want, sdl)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "gen", "noder_gql.go")); err != nil {
		t.Errorf("no noder_gql.go: %v", err)
	}
	if out, err := run("tool", "gqlgen", "generate"); err != nil {
		t.Fatalf("gqlgen generate: %v\n%s", err, out)
	}
	if out, err := run("mod", "tidy", "-e"); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
	if out, err := run("vet", "./..."); err != nil {
		t.Fatalf("go vet: %v\n%s", err, out)
	}
	out, err := run("test", "-count=1", "-v", ".")
	if err != nil {
		t.Fatalf("go test: %v\n%s", err, out)
	}
	if !strings.Contains(out, "--- PASS: TestNode ") {
		t.Errorf("no PASS of TestNode in\n%s", out)
	}
}
