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
		field.Bool("vip").Default(false), field.Time("joined_at"),
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
		field.String("note").Optional().Default(""),
	}
}

func (SalesOrder) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("customer", Customer.Type).Field("customer_id").Unique().Required().Annotations(graphql.WhereEdge()),
		edge.To("referrer", Customer.Type).Field("referrer_id").Unique().Annotations(graphql.Skip(graphql.SkipAll)),
	}
}

func (SalesOrder) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.QueryField("salesOrders"), graphql.RelayConnection(), graphql.Mutations(graphql.MutationCreate)}
}

// Settings and Assignee are the Go types of json fields, which veloxe named
// in the SDL by their names and the application declares.
type Settings struct {
	Theme string ` + "`json:\"theme\"`" + `
}

type Assignee struct {
	Name string ` + "`json:\"name\"`" + `
}

// Workspace has a QueryField and no RelayConnection: veloxe's root field of
// it is a list, and it has no connection.
type Workspace struct{ schema.Schema }

func (Workspace) Fields() []schema.Field {
	return []schema.Field{
		field.String("slug"),
		field.JSON("settings", &Settings{}).Optional(),
		field.JSON("assignees", []Assignee{}),
	}
}

func (Workspace) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.QueryField()}
}
`

// e2eVeloxeResolvers are the root query's resolvers: salesOrders, and node
// and nodes through the generated ResolveNode and ResolveNodes.
const e2eVeloxeResolvers = `package graph

import (
	"context"

	"example.com/app/gen"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// Customers is the resolver for the customers field, which compat=veloxe
// gives every type.
func (r *queryResolver) Customers(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy *gen.CustomerOrder, where *gen.CustomerWhereInput) (*gen.CustomerConnection, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return db.Query[gen.Customer]().Paginate(ctx, after, first, before, last, gen.WithCustomerOrder(orderBy), gen.WithCustomerFilter(where))
}

// SalesOrders is the resolver for the salesOrders field.
func (r *queryResolver) SalesOrders(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy *gen.SalesOrderOrder, where *gen.SalesOrderWhereInput) (*gen.SalesOrderConnection, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return db.Query[gen.SalesOrder]().Paginate(ctx, after, first, before, last, gen.WithSalesOrderOrder(orderBy), gen.WithSalesOrderFilter(where))
}

// Workspaces is the resolver for the workspaces field: a list, as veloxe
// gave a type with a QueryField and no RelayConnection.
func (r *queryResolver) Workspaces(ctx context.Context) ([]*gen.Workspace, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query[gen.Workspace]().AllowUnbounded().All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*gen.Workspace, len(rows))
	for i := range rows {
		out[i] = &rows[i]
	}
	return out, nil
}

// Node is the resolver for the node field.
func (r *queryResolver) Node(ctx context.Context, id int64) (gen.Noder, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return gen.ResolveNode(ctx, db, id)
}

// Nodes is the resolver for the nodes field.
func (r *queryResolver) Nodes(ctx context.Context, ids []int64) ([]gen.Noder, error) {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return gen.ResolveNodes(ctx, db, ids)
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
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	_ "modernc.org/sqlite"

	"example.com/app/graph"
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
)

func TestVeloxeNames(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	for _, s := range []string{
		"CREATE TABLE customers (id INTEGER PRIMARY KEY, name TEXT NOT NULL, tier TEXT NOT NULL, vip BOOLEAN NOT NULL, joined_at DATETIME NOT NULL)",
		"CREATE TABLE sales_orders (id INTEGER PRIMARY KEY, organization_id INTEGER NOT NULL, customer_id INTEGER NOT NULL, image_url TEXT NOT NULL, referrer_id INTEGER, total INTEGER NOT NULL, custom_fields TEXT NOT NULL, status TEXT NOT NULL, note TEXT NOT NULL DEFAULT '')",
		"CREATE TABLE workspaces (id INTEGER PRIMARY KEY, slug TEXT NOT NULL, settings TEXT NOT NULL, assignees TEXT NOT NULL)",
		"INSERT INTO customers (id, name, tier, vip, joined_at) VALUES (1, 'ada', 'gold', 1, '2026-01-02T00:00:00Z'), (2, 'bob', 'silver', 0, '2026-01-03T00:00:00Z'), (3, 'cy', 'gold', 0, '2026-01-01T00:00:00Z')",
		"INSERT INTO sales_orders (id, organization_id, customer_id, image_url, referrer_id, total, custom_fields, status) VALUES (10, 7, 1, 'a.png', 2, 10, '{}', 'open'), (11, 7, 2, 'b.png', NULL, 20, '{}', 'open'), (12, 8, 2, 'c.png', 1, 30, '{\"a\":1}', 'shipped')",
		"INSERT INTO workspaces (id, slug, settings, assignees) VALUES (1, 'acme', '{\"theme\":\"dark\"}', '[{\"name\":\"ada\"},{\"name\":\"bob\"}]')",
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	srv := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{}}))
	// The cost charges a list of objects MaxList rows for each parent: the root
	// list and the json list of Assignee are lists of objects.
	srv.Use(gqlgenx.NewExtension(velox.Open(velox.SQLite, db), veloxgql.WithLimits(veloxgql.Limits{MaxList: 10})))
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
		// Cursors veloxe wrote, the parity probe's of keys 1 and 3, page on,
		// with veloxe's page flags (ADR 0110).
		` + "`" + `{"query":"{ customers(first: 2, after: \"gqFpAaF2wA\") { edges { node { name } } pageInfo { hasNextPage hasPreviousPage } } }"}` + "`" + `: ` +
	"`" + `{"data":{"customers":{"edges":[{"node":{"name":"bob"}},{"node":{"name":"cy"}}],"pageInfo":{"hasNextPage":false,"hasPreviousPage":true}}}}` + "`" + `,
		` + "`" + `{"query":"{ customers(last: 1, before: \"gqFpA6F2wA\") { edges { node { name } } pageInfo { hasNextPage hasPreviousPage } } }"}` + "`" + `: ` +
	"`" + `{"data":{"customers":{"edges":[{"node":{"name":"bob"}}],"pageInfo":{"hasNextPage":true,"hasPreviousPage":true}}}}` + "`" + `,
		` + "`" + `{"query":"{ customers(first: 5, orderBy: {field: JOINED_AT, direction: DESC}) { edges { node { name } } } }"}` + "`" + `: ` +
	"`" + `{"data":{"customers":{"edges":[{"node":{"name":"bob"}},{"node":{"name":"ada"}},{"node":{"name":"cy"}}]}}}` + "`" + `,
		` + "`" + `{"query":"{ customers(first: 5, orderBy: {field: TIER}) { edges { node { name } } } }"}` + "`" + `: ` +
	"`" + `{"data":{"customers":{"edges":[{"node":{"name":"ada"}},{"node":{"name":"cy"}},{"node":{"name":"bob"}}]}}}` + "`" + `,
		` + "`" + `{"query":"{ workspaces { slug settings { theme } assignees { name } } }"}` + "`" + `: ` +
	"`" + `{"data":{"workspaces":[{"slug":"acme","settings":{"theme":"dark"},"assignees":[{"name":"ada"},{"name":"bob"}]}]}}` + "`" + `,
		` + "`" + `{"query":"{ salesOrders(first: 1) { edges { node { note customer { name } } } } }"}` + "`" + `: ` +
	"`" + `{"data":{"salesOrders":{"edges":[{"node":{"note":"","customer":{"name":"ada"}}}]}}}` + "`" + `,
	} {
		if got := query(q); got != want {
			t.Errorf("%s:\n got %s\nwant %s", q, got, want)
		}
	}
	// A bool order pages on: the cursor holds the bool. bob and cy tie, and
	// the key breaks the tie DESC, as veloxe did (ADR 0110): cy, then bob.
	var page struct {
		Data struct {
			Customers struct {
				Edges    []struct{ Node struct{ Name string } }
				PageInfo struct {
					EndCursor   string
					HasNextPage bool
				}
			}
		}
	}
	got := query("{\"query\":\"{ customers(first: 2, orderBy: {field: VIP, direction: DESC}) { edges { node { name } } pageInfo { endCursor hasNextPage } } }\"}")
	if err := json.Unmarshal([]byte(got), &page); err != nil || len(page.Data.Customers.Edges) != 2 || page.Data.Customers.Edges[0].Node.Name != "ada" || !page.Data.Customers.PageInfo.HasNextPage {
		t.Fatalf("the first page by vip: %v\n%s", err, got)
	}
	got = query("{\"query\":\"query($c: Cursor) { customers(first: 2, after: $c, orderBy: {field: VIP, direction: DESC}) { edges { node { name } } } }\",\"variables\":{\"c\":\"" + page.Data.Customers.PageInfo.EndCursor + "\"}}")
	if want := "{\"data\":{\"customers\":{\"edges\":[{\"node\":{\"name\":\"bob\"}}]}}}"; got != want {
		t.Errorf("the next page by vip:\n got %s\nwant %s", got, want)
	}
}
`

// e2eVeloxeApp is the application's own SDL, as a veloxe application had it:
// its scalars, and the types of its json fields, which veloxe's SDL named.
const e2eVeloxeApp = `scalar Map
scalar Amount
scalar JSON

type Settings @goModel(model: "example.com/app/schema.Settings") {
  theme: String!
}

type Assignee @goModel(model: "example.com/app/schema.Assignee") {
  name: String!
}
`

// e2eVeloxeMemo is a schema whose Read policy hides the memos titled
// hidden, for node and nodes.
const e2eVeloxeMemo = `package schema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

type Memo struct{ schema.Schema }

func (Memo) Fields() []schema.Field { return []schema.Field{field.String("title")} }

// Policy hides the memos titled hidden.
func (Memo) Policy() velox.Policy {
	return velox.Policy{
		Read: func(context.Context) (velox.Predicate, error) { return velox.FieldNEQ("title", "hidden"), nil },
	}
}
`

// e2eVeloxeNodeRun queries node and nodes, with veloxe's fragments on the
// types, and the where inputs veloxe read otherwise than velox does: an
// empty In, a false IsNil, an empty or, an In over velox's width cap, and a
// not of nothing, which veloxe refused.
const e2eVeloxeNodeRun = `package app_test

import (
	"database/sql"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	_ "modernc.org/sqlite"

	"example.com/app/graph"
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
)

func TestVeloxeNodeAndFilters(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	for _, s := range []string{
		"CREATE TABLE customers (id INTEGER PRIMARY KEY, name TEXT NOT NULL, tier TEXT NOT NULL, vip BOOLEAN NOT NULL DEFAULT 0, joined_at DATETIME NOT NULL DEFAULT '2020-01-01 00:00:00')",
		"CREATE TABLE sales_orders (id INTEGER PRIMARY KEY, organization_id INTEGER NOT NULL, customer_id INTEGER NOT NULL, image_url TEXT NOT NULL, referrer_id INTEGER, total INTEGER NOT NULL, custom_fields TEXT NOT NULL, status TEXT NOT NULL, note TEXT NOT NULL DEFAULT '')",
		"CREATE TABLE workspaces (id INTEGER PRIMARY KEY, slug TEXT NOT NULL, settings TEXT NOT NULL, assignees TEXT NOT NULL)",
		"CREATE TABLE memos (id INTEGER PRIMARY KEY, title TEXT NOT NULL)",
		"INSERT INTO customers (id, name, tier) VALUES (1, 'ada', 'gold'), (2, 'bob', 'silver')",
		"INSERT INTO sales_orders (id, organization_id, customer_id, image_url, referrer_id, total, custom_fields, status) VALUES (10, 7, 1, 'a.png', 2, 10, '{}', 'open'), (11, 7, 2, 'b.png', NULL, 20, '{}', 'open'), (12, 8, 2, 'c.png', 1, 30, '{}', 'shipped')",
		"INSERT INTO memos (id, title) VALUES (20, 'seen'), (21, 'hidden')",
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	srv := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{}}))
	srv.Use(gqlgenx.NewExtension(velox.Open(velox.SQLite, db)))
	srv.SetErrorPresenter(gqlgenx.ErrorPresenter(nil))
	srv.AddTransport(transport.POST{})
	query := func(q string) string {
		req := httptest.NewRequest("POST", "/", strings.NewReader(q))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w.Body.String()
	}
	var wide []string // 151 organization IDs, over velox's MaxFilterWidth of 100
	for i := 100; i < 250; i++ {
		wide = append(wide, fmt.Sprintf("\\\"%d\\\"", i))
	}
	wide = append(wide, "\\\"8\\\"")
	all := "{\"data\":{\"salesOrders\":{\"edges\":[{\"node\":{\"id\":\"10\"}},{\"node\":{\"id\":\"11\"}},{\"node\":{\"id\":\"12\"}}]}}}"
	orders := func(where string) string {
		return "{\"query\":\"{ salesOrders(first: 5, where: " + where + ") { edges { node { id } } } }\"}"
	}
	for _, c := range []struct{ q, want string }{
		{"{\"query\":\"{ node(id: \\\"11\\\") { id ... on SalesOrder { imageUrl } } }\"}",
			"{\"data\":{\"node\":{\"id\":\"11\",\"imageUrl\":\"b.png\"}}}"},
		{"{\"query\":\"{ node(id: \\\"20\\\") { id ... on Memo { title } } }\"}",
			"{\"data\":{\"node\":{\"id\":\"20\",\"title\":\"seen\"}}}"},
		{"{\"query\":\"{ nodes(ids: [\\\"10\\\", \\\"1\\\", \\\"99\\\", \\\"21\\\", \\\"20\\\"]) { id ... on SalesOrder { imageUrl } ... on Customer { name } } }\"}",
			"{\"data\":{\"nodes\":[{\"id\":\"10\",\"imageUrl\":\"a.png\"},{\"id\":\"1\",\"name\":\"ada\"},null,null,{\"id\":\"20\"}]}}"},
		{orders("{organizationIdIn: []}"), all},
		{orders("{referrerIdIsNil: false}"), all},
		{orders("{referrerIdNotNil: false, organizationIdNotIn: []}"), all},
		{orders("{or: []}"), all},
		{orders("{and: [{}], organizationIdIn: [" + strings.Join(wide, ", ") + "]}"),
			"{\"data\":{\"salesOrders\":{\"edges\":[{\"node\":{\"id\":\"12\"}}]}}}"},
	} {
		if got := query(c.q); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.q, got, c.want)
		}
	}
	// A missing ID, a hidden one and a not of nothing are errors, as in veloxe.
	for _, c := range []struct{ q, want string }{
		{"{\"query\":\"{ node(id: \\\"99\\\") { id } }\"}", "\"code\":\"NOT_FOUND\""},
		{"{\"query\":\"{ node(id: \\\"21\\\") { id } }\"}", "could not resolve to a node with the global ID"},
		{orders("{not: {}}"), "empty predicate SalesOrderWhereInput"},
		{orders("{or: [{}]}"), "\"code\":\"BAD_USER_INPUT\""},
	} {
		got := query(c.q)
		if !strings.Contains(got, c.want) || !strings.Contains(got, "\"errors\"") {
			t.Errorf("%s:\n got %s\nwant an error with %s", c.q, got, c.want)
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
		"schema/memo.go":            e2eVeloxeMemo,
		"veloxe_node_test.go":       e2eVeloxeNodeRun,
		"gqlgen.yml":                e2eCollectGqlgen + "  Map:\n    model: github.com/99designs/gqlgen/graphql.Map\n  Amount:\n    model: example.com/app/schema.Amount\n",
		"graph/app.graphql":         e2eVeloxeApp,
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
	for _, want := range []string{"  customerId: ID!\n", "  organizationId: ID!\n", "  imageUrl: String!\n", "  customerIdNotIn: [ID!]\n", "  hasCustomer: Boolean\n", "  customFields: Map!\n", "  status: SalesOrderEntryStatus!\n", "enum SalesOrderEntryStatus", "enum SalesOrderStatus @goModel(model: \"example.com/app/schema.SalesOrderStatus\")", "  totalGT: Amount\n", "  totalNotIn: [Amount!]\n",
		"interface Node @goModel(model: \"example.com/app/gen.Noder\") {", "type SalesOrder implements Node", "type Memo implements Node", "  nodes(ids: [ID!]!): [Node]!\n"} {
		if !strings.Contains(string(sdl), want) {
			t.Errorf("velox.graphql lacks %q:\n%s", want, sdl)
		}
	}
	if strings.Contains(string(sdl), "customerID") {
		t.Errorf("velox.graphql names an edge's ID customerID:\n%s", sdl)
	}
	// The rest of veloxe's surface: every orderable field in the order, a
	// root field for every type, a list for Workspace, Optional nullable, a
	// Required edge non-null, veloxe's connections and scalars, and the json
	// fields' Go types by name.
	for _, want := range []string{
		"enum CustomerOrderField {\n  NAME\n  TIER\n  VIP\n  JOINED_AT\n}",
		"enum SalesOrderOrderField {\n  ORGANIZATION_ID\n  CUSTOMER_ID\n  IMAGE_URL\n  REFERRER_ID\n  STATUS\n  NOTE\n}",
		"  customers(after: Cursor, first: Int, before: Cursor, last: Int, orderBy: CustomerOrder, where: CustomerWhereInput): CustomerConnection!\n",
		"  workspaces: [Workspace!]!\n", "  note: String\n", "  customer: Customer!\n",
		"  edges: [SalesOrderEdge!]\n", "  node: SalesOrder\n",
		"  settings: Settings\n", "  assignees: [Assignee!]!\n",
	} {
		if !strings.Contains(string(sdl), want) {
			t.Errorf("velox.graphql lacks %q:\n%s", want, sdl)
		}
	}
	for _, not := range []string{"WorkspaceConnection", "WorkspaceOrder", "scalar JSON", "scalar UUID"} {
		if strings.Contains(string(sdl), not) {
			t.Errorf("velox.graphql has %q:\n%s", not, sdl)
		}
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
	if !strings.Contains(out, "--- PASS: TestVeloxeNames ") || !strings.Contains(out, "--- PASS: TestVeloxeNodeAndFilters ") {
		t.Errorf("no PASS of TestVeloxeNames or TestVeloxeNodeAndFilters in\n%s", out)
	}
}
