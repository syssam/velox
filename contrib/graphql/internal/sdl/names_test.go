package sdl

import (
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/plugin"
)

// namesModel is a sales order with a customer, by the foreign key
// customer_id, and an image_url, whose schema and Go names differ in case.
func namesModel(t *testing.T) *plugin.Model {
	ck := &plugin.Field{Name: "customer_id", GoName: "CustomerID", Type: "int64", Annotations: ann(t, graphql.WhereOps())}
	img := &plugin.Field{Name: "image_url", GoName: "ImageURL", Type: "string", Annotations: ann(t, graphql.WhereOps(graphql.OpsEquality))}
	order := entity("SalesOrder", ck, img)
	order.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate))
	cust := edge("customer", plugin.M2O, "Customer")
	cust.Field, cust.FieldOn, cust.OwnsKey, cust.Required = "customer_id", "SalesOrder", true, true
	order.Edges = []*plugin.Edge{cust}
	return model(order, entity("Customer"))
}

// names=schema names a field by its schema name in plain mode, and keeps
// velox's design otherwise: the foreign key stays out of the type, and its
// predicates and input are named by the edge, customerId. Under
// compat=veloxe, names=go names by the Go names and keeps veloxe's surface.
func TestNamesOption(t *testing.T) {
	for _, c := range []struct {
		name string
		cfg  Config
		want map[[2]string]string // type and field: GraphQL type, "" for none
	}{
		{"names=schema", Config{Names: "schema"}, map[[2]string]string{
			{"SalesOrder", "imageUrl"}:               "String!",
			{"SalesOrder", "imageURL"}:               "",
			{"SalesOrder", "customerId"}:             "",
			{"SalesOrder", "customer"}:               "Customer",
			{"SalesOrderWhereInput", "imageUrl"}:     "String",
			{"SalesOrderWhereInput", "imageUrlNEQ"}:  "String",
			{"SalesOrderWhereInput", "customerId"}:   "ID",
			{"SalesOrderWhereInput", "customerIdIn"}: "[ID!]",
			{"SalesOrderWhereInput", "customerID"}:   "",
			{"CreateSalesOrderInput", "imageUrl"}:    "String!",
			{"CreateSalesOrderInput", "customerId"}:  "ID!",
		}},
		{"names=go", Config{Names: "go"}, map[[2]string]string{
			{"SalesOrder", "imageURL"}:               "String!",
			{"SalesOrderWhereInput", "customerIDIn"}: "[ID!]",
			{"CreateSalesOrderInput", "customerID"}:  "ID!",
		}},
		{"compat=veloxe,names=go", Config{Veloxe: true, Names: "go"}, map[[2]string]string{
			{"SalesOrder", "imageURL"}:                "String!",
			{"SalesOrder", "customerID"}:              "ID!",
			{"SalesOrder", "customerId"}:              "",
			{"SalesOrderWhereInput", "customerIDNEQ"}: "ID",
			{"SalesOrderWhereInput", "customerIdNEQ"}: "",
			{"CreateSalesOrderInput", "customerID"}:   "ID!",
		}},
		{"compat=veloxe", Config{Veloxe: true}, map[[2]string]string{
			{"SalesOrder", "imageUrl"}:                "String!",
			{"SalesOrder", "customerId"}:              "ID!",
			{"SalesOrderWhereInput", "customerIdNEQ"}: "ID",
			{"CreateSalesOrderInput", "customerId"}:   "ID!",
		}},
	} {
		sdl, err := RenderConfig(namesModel(t), c.cfg)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		s, err := Check(sdl)
		if err != nil {
			t.Fatalf("%s: %v\n%s", c.name, err, sdl)
		}
		for k, want := range c.want {
			if got := typeOf(s, k[0], k[1]); got != want {
				t.Errorf("%s: %s.%s is %q, want %q", c.name, k[0], k[1], got, want)
			}
		}
	}
	for _, c := range []struct {
		cfg  Config
		want string
	}{
		{Config{Names: "snake"}, "names=snake: names is go"},
		{Config{Veloxe: true, Filters: "loose"}, "filters=loose: filters is strict"},
	} {
		if _, err := RenderConfig(namesModel(t), c.cfg); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want an error with %q", c.cfg, err, c.want)
		}
	}
	for _, c := range []struct {
		cfg              Config
		schema, veloxeFs bool
	}{
		{Config{}, false, false},
		{Config{Veloxe: true}, true, true},
		{Config{Veloxe: true, Names: "go", Filters: "strict"}, false, false},
		{Config{Names: "schema", Filters: "veloxe"}, true, true},
	} {
		if c.cfg.SchemaNames() != c.schema || c.cfg.VeloxeFilters() != c.veloxeFs {
			t.Errorf("%+v: SchemaNames %v, VeloxeFilters %v; want %v, %v", c.cfg, c.cfg.SchemaNames(), c.cfg.VeloxeFilters(), c.schema, c.veloxeFs)
		}
	}
}
