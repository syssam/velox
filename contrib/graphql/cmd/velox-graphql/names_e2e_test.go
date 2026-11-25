package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// e2eNamesSchema is a sales order with a customer, by the foreign key
// customer_id, and an image_url: names whose schema and Go spellings differ
// in case.
const e2eNamesSchema = `package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type Customer struct{ schema.Schema }

func (Customer) Fields() []schema.Field { return []schema.Field{field.String("name")} }

type SalesOrder struct{ schema.Schema }

func (SalesOrder) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("customer_id").Annotations(graphql.WhereOps()),
		field.String("image_url").Annotations(graphql.WhereOps(graphql.OpsEquality)),
	}
}

func (SalesOrder) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("customer", Customer.Type).Field("customer_id").Unique().Required().Annotations(graphql.WhereEdge()),
	}
}

func (SalesOrder) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.QueryField("salesOrders"), graphql.RelayConnection()}
}
`

// e2eNamesResolvers is the root query's resolver of salesOrders.
const e2eNamesResolvers = `package graph

import (
	"context"

	"example.com/app/gen"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// SalesOrders is the resolver for the salesOrders field.
func (r *queryResolver) SalesOrders(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, where *gen.SalesOrderWhereInput) (*gen.SalesOrderConnection, error) {
	return r.DB.Query[gen.SalesOrder]().Paginate(ctx, after, first, before, last, gen.WithSalesOrderFilter(where))
}

// Query returns QueryResolver implementation.
func (r *Resolver) Query() QueryResolver { return &queryResolver{r} }

type queryResolver struct{ *Resolver }
`

// e2eNamesRun queries with the schema's names, and decodes a kept filter
// document of them with veloxgql.DecodeWhere.
const e2eNamesRun = `package app_test

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	_ "modernc.org/sqlite"

	"example.com/app/gen"
	"example.com/app/graph"
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

func TestSchemaNames(t *testing.T) {
	raw, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	raw.SetMaxOpenConns(1)
	for _, s := range []string{
		"CREATE TABLE customers (id INTEGER PRIMARY KEY, name TEXT NOT NULL)",
		"CREATE TABLE sales_orders (id INTEGER PRIMARY KEY, customer_id INTEGER NOT NULL, image_url TEXT NOT NULL)",
		"INSERT INTO customers (id, name) VALUES (1, 'ada'), (2, 'bob')",
		"INSERT INTO sales_orders (id, customer_id, image_url) VALUES (10, 1, 'a.png'), (11, 2, 'b.png'), (12, 2, 'c.png')",
	} {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	db := velox.Open(velox.SQLite, raw)
	srv := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{DB: db}}))
	srv.AddTransport(transport.POST{})
	q := ` + "`" + `{"query":"{ salesOrders(first: 5, where: {customerId: \"2\", imageUrlNEQ: \"c.png\", hasCustomer: true}) { edges { node { id imageUrl customer { name } } } } }"}` + "`" + `
	req := httptest.NewRequest("POST", "/", strings.NewReader(q))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if got, want := w.Body.String(), ` + "`" + `{"data":{"salesOrders":{"edges":[{"node":{"id":"11","imageUrl":"b.png","customer":{"name":"bob"}}}]}}}` + "`" + `; got != want {
		t.Errorf("salesOrders:\n got %s\nwant %s", got, want)
	}

	// A kept filter decodes by the same names, and selects the same rows.
	var in gen.SalesOrderWhereInput
	if err := veloxgql.DecodeWhere([]byte(` + "`" + `{"customerIdIn": ["2"], "imageUrlNEQ": "c.png", "not": {"idIn": ["99"]}}` + "`" + `), &in); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p, err := in.Predicate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query[gen.SalesOrder]().Where(velox.Pred[gen.SalesOrder](p)).AllowUnbounded().All(ctx)
	if err != nil || len(rows) != 1 || rows[0].ID != 11 {
		t.Errorf("the decoded filter: %v, %v; want order 11", rows, err)
	}
	// Go's spelling is not a name of the input.
	for _, doc := range []string{` + "`" + `{"customerIDIn": ["2"]}` + "`" + `, ` + "`" + `{"imageURL": "a.png"}` + "`" + `} {
		if err := veloxgql.DecodeWhere([]byte(doc), &in); err == nil {
			t.Errorf("%s decodes", doc)
		}
	}
}
`

// TestNamesSchema generates with names=schema alone: fields are named from
// their schema names (imageUrl), the foreign key too, in the type and its
// predicates (customerId, customerIdIn), and the where input's semantics
// are velox's own. A query and a kept
// filter read with veloxgql.DecodeWhere use the same names.
func TestNamesSchema(t *testing.T) {
	t.Parallel() // its own module, in its own directory
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir, run := app(t, map[string]string{
		"schema/schema.go":          e2eNamesSchema,
		"gqlgen.yml":                e2eCollectGqlgen,
		"graph/schema.resolvers.go": e2eNamesResolvers,
		"graph/resolver.go":         e2eResolver,
		"names_test.go":             e2eNamesRun,
	}, true)
	if out, err := run("tool", "velox-gen", "-out", "gen", "-plugin", "velox-graphql:names=schema", "./schema"); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(dir, "gen", "velox.graphql"))
	if err != nil {
		t.Fatal(err)
	}
	sdl := string(b)
	for _, want := range []string{"  imageUrl: String!\n", "  imageUrlNEQ: String\n", "  customerId: ID\n", "  customerIdIn: [ID!]\n", "  hasCustomer: Boolean\n"} {
		if !strings.Contains(sdl, want) {
			t.Errorf("velox.graphql lacks %q:\n%s", want, sdl)
		}
	}
	typ := sdl[strings.Index(sdl, "type SalesOrder {"):]
	if typ = typ[:strings.Index(typ, "}")]; !strings.Contains(typ, "  customerId: ID!\n") {
		t.Errorf("SalesOrder has no customerId:\n%s", sdl)
	}
	for _, not := range []string{"imageURL", "customerID", "interface Node"} {
		if strings.Contains(sdl, not) {
			t.Errorf("velox.graphql has %q:\n%s", not, sdl)
		}
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
	if !strings.Contains(out, "--- PASS: TestSchemaNames ") {
		t.Errorf("no PASS of TestSchemaNames in\n%s", out)
	}
}
