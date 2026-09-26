package graphqlgen

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vektah/gqlparser/v2/parser"
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

// checkSDLSyntax parses one generated SDL file and reports a syntax error
// with the file name, position and offending line.
//
// The SDL is assembled by string concatenation, so a malformed fragment
// (an unescaped description, a stray brace) used to reach disk unnoticed and
// surface later as a gqlgen error far from its cause. writeSchema calls this
// for every file it writes.
//
// It checks syntax only, NOT type resolution: split modes write fragments
// that reference types in sibling files, and a single-file schema may
// reference types the application declares in its own .graphql files
// (ResolverMapping return types, Implements interfaces). Full validation
// (parseSchemaSDL) still runs whenever hooks or an output writer need the AST.
func checkSDLSyntax(name, sdl string) error {
	_, err := parser.ParseSchema(&ast.Source{Name: name, Input: sdl})
	if err == nil {
		return nil
	}
	msg := err.Error()
	var gqlErr *gqlerror.Error
	if errors.As(err, &gqlErr) && len(gqlErr.Locations) > 0 {
		line := gqlErr.Locations[0].Line
		msg = fmt.Sprintf("%s (line %d)", gqlErr.Message, line)
		if lines := strings.Split(sdl, "\n"); line >= 1 && line <= len(lines) {
			msg += fmt.Sprintf(": %q", lines[line-1])
		}
	}
	return fmt.Errorf("graphql: generated SDL %s is invalid: %s", name, msg)
}

// sdlDescription renders text as a GraphQL block-string description:
//
//	"""
//	<indent>text
//	<indent>"""
//
// followed by a newline. The first line carries no indent so callers can
// prefix it. `"""` inside text is escaped as `\"""`, the only escape a block
// string has (GraphQL spec §2.9.4); every other character, backslashes
// included, is literal. Every SDL description must go through here.
func sdlDescription(text, indent string) string {
	text = strings.ReplaceAll(text, `"""`, `\"""`)
	return `"""` + "\n" + indent + text + "\n" + indent + `"""` + "\n"
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
