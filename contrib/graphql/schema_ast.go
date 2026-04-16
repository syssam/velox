package graphql

import (
	"fmt"
	"strings"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
)

// SchemaOutputWriter is a function that receives the final *ast.Schema for custom output.
// Use WithOutputWriter to set this on the extension.
type SchemaOutputWriter func(*ast.Schema) error

// BuildSchema constructs a typed *ast.Schema from the graph.
// This is the equivalent of Ent's entgql.BuildSchema — schema hooks receive
// the typed AST for structural modifications (add/remove types, fields, directives).
//
// The schema is built by generating SDL, then parsing it into a validated AST
// using gqlparser. This reuses all existing SDL generators while providing
// typed AST access to hooks.
func (g *Generator) BuildSchema() (*ast.Schema, error) {
	sdl := g.genFullSchema()
	return parseSchemaSDL(sdl)
}

// parseSchemaSDL parses a GraphQL SDL string into a validated *ast.Schema.
// Uses gqlparser.LoadSchema which includes the built-in prelude types
// (String, Int, Float, Boolean, ID, etc.) and validates the schema.
func parseSchemaSDL(sdl string) (*ast.Schema, error) {
	source := &ast.Source{
		Name:  "velox.graphql",
		Input: sdl,
	}
	schema, err := gqlparser.LoadSchema(source)
	if err != nil {
		return nil, fmt.Errorf("parse generated schema: %w", err)
	}
	return schema, nil
}

// printSchema renders an *ast.Schema to SDL string using gqlparser's formatter.
// This matches Ent's printSchema function.
func printSchema(schema *ast.Schema) string {
	sb := &strings.Builder{}
	f := formatter.NewFormatter(sb, formatter.WithIndent("  "))
	f.FormatSchema(schema)
	return sb.String()
}

// applySchemaHooks runs schema hooks on the given AST schema.
func (g *Generator) applySchemaHooks(schema *ast.Schema) error {
	for _, hook := range g.config.schemaHooks {
		if err := hook(g.config.graph, schema); err != nil {
			return fmt.Errorf("schema hook: %w", err)
		}
	}
	return nil
}
