package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// e2eVeloxeSchema is a schema as a veloxe application declares it: foreign
// keys marked graphql.Type("ID"), one bound to an edge (customer_id) and one
// with none (organization_id), a name with an initialism (image_url), a
// field of the application's scalar Map, an enum graphql.Type renames, and
// an Other field of the application's scalar Amount, compared in SQL.
const e2eVeloxeSchema = `package schema

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Amount is a sum in cents, stored as an integer: an Other field's type, as
// a decimal is, and the application's GraphQL scalar Amount.
type Amount struct{ Cents int64 }

func (a Amount) Value() (driver.Value, error) { return a.Cents, nil }

func (a *Amount) Scan(v any) error {
	n, ok := v.(int64)
	if !ok {
		return fmt.Errorf("amount: %T", v)
	}
	a.Cents = n
	return nil
}

func (a Amount) MarshalGQL(w io.Writer) { fmt.Fprint(w, a.Cents) }

func (a *Amount) UnmarshalGQL(v any) error {
	switch n := v.(type) {
	case int64:
		a.Cents = n
	case int:
		a.Cents = int64(n)
	case json.Number:
		c, err := n.Int64()
		a.Cents = c
		return err
	default:
		return fmt.Errorf("amount: %T", v)
	}
	return nil
}

// SalesOrderStatus is the application's enum type, of the name velox-gen
// gives SalesOrder.status's own (which graphql.Type renames), with its own
// marshalers, as nextapp's schematype.ExchangeRateSource has.
type SalesOrderStatus string

func (SalesOrderStatus) Values() []string { return []string{"gold", "silver"} }

func (s SalesOrderStatus) MarshalGQL(w io.Writer) { fmt.Fprintf(w, "%q", strings.ToUpper(string(s))) }

func (s *SalesOrderStatus) UnmarshalGQL(v any) error {
	str, ok := v.(string)
	if !ok {
		return fmt.Errorf("status: %T", v)
	}
	*s = SalesOrderStatus(strings.ToLower(str))
	return nil
}

type Customer struct{ schema.Schema }

func (Customer) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("id").Annotations(graphql.Type("ID")), field.String("name"),
		field.Enum("tier").GoType(SalesOrderStatus("")).Default("gold").Annotations(graphql.Type("SalesOrderStatus"), graphql.WhereOps()),
	}
}

type SalesOrder struct{ schema.Schema }

func (SalesOrder) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("organization_id").Annotations(graphql.Type("ID"), graphql.WhereOps()),
		field.Int64("customer_id").Annotations(graphql.Type("ID"), graphql.WhereOps()),
		field.String("image_url"),
		field.Int64("referrer_id").Optional().Nillable().Annotations(graphql.WhereOps()),
		field.Other("total", Amount{}).SchemaType(map[string]string{field.SQLite: "integer", field.Postgres: "bigint", field.MySQL: "bigint"}).
			Annotations(graphql.Type("Amount"), graphql.WhereOps(graphql.OpsComparison)),
		field.JSON("custom_fields", map[string]any{}).Annotations(graphql.Type("Map")),
		field.Enum("status").Values("open", "shipped").Annotations(graphql.Type("SalesOrderEntryStatus"), graphql.WhereOps()),
	}
}

func (SalesOrder) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("customer", Customer.Type).Field("customer_id").Unique().Required().Annotations(graphql.WhereEdge()),
		edge.To("referrer", Customer.Type).Field("referrer_id").Unique().Annotations(graphql.Skip(graphql.SkipAll)),
	}
}

func (SalesOrder) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.QueryField("salesOrders"), graphql.Mutations(graphql.MutationCreate)}
}
`

// e2eVeloxeResolvers is the root query's resolver.
const e2eVeloxeResolvers = `package graph

import (
	"context"

	"example.com/app/gen"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// SalesOrders is the resolver for the salesOrders field.
func (r *queryResolver) SalesOrders(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, where *gen.SalesOrderWhereInput) (*gen.SalesOrderConnection, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return db.Query[gen.SalesOrder]().Paginate(ctx, after, first, before, last, gen.WithSalesOrderFilter(where))
}

// Query returns QueryResolver implementation.
func (r *Resolver) Query() QueryResolver { return &queryResolver{r} }

type queryResolver struct{ *Resolver }
`

// e2eVeloxeRun queries veloxe's names: the keys as IDs, beside the edge, and
// the predicates named from the field.
const e2eVeloxeRun = `package app_test

import (
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	_ "modernc.org/sqlite"

	"example.com/app/graph"
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

func TestVeloxeNames(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	for _, s := range []string{
		"CREATE TABLE customers (id INTEGER PRIMARY KEY, name TEXT NOT NULL, tier TEXT NOT NULL)",
		"CREATE TABLE sales_orders (id INTEGER PRIMARY KEY, organization_id INTEGER NOT NULL, customer_id INTEGER NOT NULL, image_url TEXT NOT NULL, referrer_id INTEGER, total INTEGER NOT NULL, custom_fields TEXT NOT NULL, status TEXT NOT NULL)",
		"INSERT INTO customers (id, name, tier) VALUES (1, 'ada', 'gold'), (2, 'bob', 'silver')",
		"INSERT INTO sales_orders (id, organization_id, customer_id, image_url, referrer_id, total, custom_fields, status) VALUES (10, 7, 1, 'a.png', 2, 10, '{}', 'open'), (11, 7, 2, 'b.png', NULL, 20, '{}', 'open'), (12, 8, 2, 'c.png', 1, 30, '{\"a\":1}', 'shipped')",
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	srv := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{}}))
	srv.Use(veloxgql.NewExtension(velox.Open(velox.SQLite, db)))
	srv.AddTransport(transport.POST{})
	query := func(q string) string {
		req := httptest.NewRequest("POST", "/", strings.NewReader(q))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w.Body.String()
	}
	for q, want := range map[string]string{
		` + "`" + `{"query":"{ salesOrders(first: 5, where: {customerId: \"2\"}) { edges { node { id organizationId customerId imageUrl customer { name } } } } }"}` + "`" + `: ` +
	"`" + `{"data":{"salesOrders":{"edges":[{"node":{"id":"11","organizationId":"7","customerId":"2","imageUrl":"b.png","customer":{"name":"bob"}}},{"node":{"id":"12","organizationId":"8","customerId":"2","imageUrl":"c.png","customer":{"name":"bob"}}}]}}}` + "`" + `,
		` + "`" + `{"query":"{ salesOrders(first: 5, where: {organizationIdIn: [\"8\"], customerIdNEQ: \"1\", hasCustomer: true, status: SHIPPED}) { edges { node { id customFields status } } } }"}` + "`" + `: ` +
	"`" + `{"data":{"salesOrders":{"edges":[{"node":{"id":"12","customFields":{"a":1},"status":"SHIPPED"}}]}}}` + "`" + `,
		` + "`" + `{"query":"{ salesOrders(first: 5, where: {totalGT: 15, totalLTE: 20}) { edges { node { id total } } } }"}` + "`" + `: ` +
	"`" + `{"data":{"salesOrders":{"edges":[{"node":{"id":"11","total":20}}]}}}` + "`" + `,
		` + "`" + `{"query":"{ salesOrders(first: 5, where: {referrerIdIn: [\"1\", \"2\"], referrerIdNEQ: \"1\"}) { edges { node { id referrerId } } } }"}` + "`" + `: ` +
	"`" + `{"data":{"salesOrders":{"edges":[{"node":{"id":"10","referrerId":"2"}}]}}}` + "`" + `,
		` + "`" + `{"query":"{ salesOrders(first: 5, where: {hasCustomerWith: [{tier: SILVER}], status: OPEN}) { edges { node { id customer { tier } } } } }"}` + "`" + `: ` +
	"`" + `{"data":{"salesOrders":{"edges":[{"node":{"id":"11","customer":{"tier":"SILVER"}}}]}}}` + "`" + `,
		` + "`" + `{"query":"{ salesOrders(first: 5, where: {referrerIdIsNil: true}) { edges { node { id } } } }"}` + "`" + `: ` +
	"`" + `{"data":{"salesOrders":{"edges":[{"node":{"id":"11"}}]}}}` + "`" + `,
	} {
		if got := query(q); got != want {
			t.Errorf("%s:\n got %s\nwant %s", q, got, want)
		}
	}
}
`

// TestVeloxeCompat generates with compat=veloxe and serves veloxe's API:
// customerId and organizationId are IDs in the type, imageUrl is named as
// veloxe named it, and the where input filters by customerId, customerIdNEQ
// and organizationIdIn.
func TestVeloxeCompat(t *testing.T) {
	t.Parallel() // its own module, in its own directory
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir, run := app(t, map[string]string{
		"schema/schema.go":          e2eVeloxeSchema,
		"gqlgen.yml":                e2eCollectGqlgen + "  Map:\n    model: github.com/99designs/gqlgen/graphql.Map\n  Amount:\n    model: example.com/app/schema.Amount\n",
		"graph/app.graphql":         "scalar Map\nscalar Amount\n",
		"graph/schema.resolvers.go": e2eVeloxeResolvers,
		"veloxe_test.go":            e2eVeloxeRun,
	}, true)
	if out, err := run("tool", "velox-gen", "-out", "gen", "-plugin", "velox-graphql:compat=veloxe", "./schema"); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	sdl, err := os.ReadFile(filepath.Join(dir, "gen", "velox.graphql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"  customerId: ID!\n", "  organizationId: ID!\n", "  imageUrl: String!\n", "  customerIdNotIn: [ID!]\n", "  hasCustomer: Boolean\n", "  customFields: Map!\n", "  status: SalesOrderEntryStatus!\n", "enum SalesOrderEntryStatus", "enum SalesOrderStatus @goModel(model: \"example.com/app/schema.SalesOrderStatus\")", "  totalGT: Amount\n", "  totalNotIn: [Amount!]\n"} {
		if !strings.Contains(string(sdl), want) {
			t.Errorf("velox.graphql lacks %q:\n%s", want, sdl)
		}
	}
	if strings.Contains(string(sdl), "customerID") {
		t.Errorf("velox.graphql names an edge's ID customerID:\n%s", sdl)
	}
	in := string(sdl[strings.Index(string(sdl), "input CreateSalesOrderInput"):])
	if !strings.Contains(in[:strings.Index(in, "}")], "customFields: Map!") {
		t.Errorf("CreateSalesOrderInput takes no Map:\n%s", in)
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
	if !strings.Contains(out, "--- PASS: TestVeloxeNames ") {
		t.Errorf("no PASS of TestVeloxeNames in\n%s", out)
	}
}
