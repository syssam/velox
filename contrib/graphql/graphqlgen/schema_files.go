package graphqlgen

import (
	"fmt"
	"path"
	"strings"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/contrib/graphql"
)

// OrderField is one value of an entity's <Type>OrderField enum and what it
// orders by: a field, or the number of neighbors on an edge.
type OrderField struct {
	Name  string
	Field *gen.Field
	Edge  *gen.Edge
}

// OrderFields returns the values of t's OrderField enum in the order the
// schema lists them. The SDL is written from it, so the two cannot differ.
func (g *Generator) OrderFields(t *gen.Type) []OrderField {
	var out []OrderField
	// Filter by SkipOrderField annotation, then exclude non-orderable fields.
	// Text fields (field.Text(), Size >= MaxInt32) are excluded because ordering
	// by unbounded text columns is inefficient and rarely useful in practice.
	// Orderable types: String (non-Text), Int, Int64, Float, Bool, Enum, Time.
	for _, f := range g.filterFields(t.Fields, graphql.SkipOrderField) {
		if g.isOrderableField(f) {
			out = append(out, OrderField{Name: g.getOrderFieldName(f), Field: f})
		}
	}
	// Edge count ordering - only when explicitly annotated with OrderField (like Ent)
	for _, e := range t.Edges {
		if !e.Unique {
			if name := g.getEdgeAnnotation(e).GetOrderField(); name != "" {
				out = append(out, OrderField{Name: name, Edge: e})
			}
		}
	}
	return out
}

// WhereField is one field of an entity's <Type>WhereInput and what it filters
// on: a field with an operator, or an edge.
type WhereField struct {
	// Name is the field's name in the schema: nameContains, hasPosts.
	Name string
	// Field and Op are the field filtered and the operator ("EQ", "NotIn",
	// "ContainsFold", ...), for a filter on a field.
	Field *gen.Field
	Op    string
	// Edge is the edge, for hasPosts and hasPostsWith; With tells the two
	// apart.
	Edge *gen.Edge
	With bool
}

// WhereFields returns the fields of t's where input after not, and and or,
// in the order the schema lists them: what each one filters on, so that a
// generator of its own Go does not have to read that back out of a name
// (nameIn is "name" with In, or a field called name_in).
func (g *Generator) WhereFields(t *gen.Type) []WhereField {
	var out []WhereField
	add := func(f *gen.Field) {
		name := camel(f.Name)
		for _, op := range g.getWhereInputFieldOps(f) {
			n := name
			if op.Name != "EQ" {
				n += op.Name
			}
			out = append(out, WhereField{Name: n, Field: f, Op: op.Name})
		}
	}
	if t.ID != nil && !g.skipFieldInWhereInput(t, t.ID) {
		add(t.ID)
	}
	for _, f := range t.Fields {
		if !g.skipFieldInWhereInput(t, f) {
			add(f)
		}
	}
	for _, e := range t.Edges {
		if e.Type == nil || e.Type.HasCompositeID() || g.skipEdgeInWhereInput(t, e) {
			continue
		}
		out = append(out,
			WhereField{Name: "has" + pascal(e.Name), Edge: e},
			WhereField{Name: "has" + pascal(e.Name) + "With", Edge: e, With: true})
	}
	return out
}

// SchemaFiles returns the GraphQL schema Generate would write, as SDL text by
// file path relative to the schema directory, and writes nothing: no schema
// file and no Go code.
//
// It is for a generator that produces its own Go for the same schema (vx):
// the SDL is the contract a GraphQL engine binds Go types against, and it
// should not differ between the two. The schema is validated as Generate
// validates it; schema hooks and the output writer, which belong to the files
// Generate writes, are not applied.
func (g *Generator) SchemaFiles() (map[string]string, error) {
	if g.config.WhereInputs {
		if err := g.validateWhereInputAnnotations(); err != nil {
			return nil, err
		}
	}
	for _, t := range g.filterNodes(g.graph.Nodes, graphql.SkipType) {
		if err := g.validateResolverMappings(t); err != nil {
			return nil, err
		}
		if err := g.validateUnimplementedEdgeAnnotations(t); err != nil {
			return nil, err
		}
	}
	if err := g.validateEnumNames(); err != nil {
		return nil, err
	}
	if _, err := g.validateUnions(); err != nil {
		return nil, err
	}
	if err := g.validateInterfaceFields(); err != nil {
		return nil, err
	}

	root := g.config.SchemaFilename
	if root == "" {
		root = "schema.graphql"
	}
	files := map[string]string{}
	switch g.config.SchemaSplitMode {
	case SchemaSplitByCategory:
		files[path.Join("schema", "types.graphql")] = g.genTypesSchema()
		files[path.Join("schema", "inputs.graphql")] = g.genInputsSchema()
		files[path.Join("schema", "connections.graphql")] = g.genConnectionsSchema()
		files[path.Join("schema", "scalars.graphql")] = g.genScalarsSchema()
		files[root] = g.genRootSchema()
	case SchemaSplitPerEntity:
		files[root] = g.genPerEntityRootSchema()
		for _, t := range g.filterNodes(g.graph.Nodes, graphql.SkipType) {
			files[fmt.Sprintf("velox_%s.graphql", strings.ToLower(t.Name))] = g.genEntitySchemaFile(t)
		}
	default:
		files[root] = g.genFullSchema()
	}
	for name, content := range files {
		if err := checkSDLSyntax(name, content); err != nil {
			return nil, err
		}
	}
	return files, nil
}
