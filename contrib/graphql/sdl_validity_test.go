package graphql

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	entgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/schema/field"
)

// A description is emitted as a GraphQL block string ("""..."""). A user
// comment containing `"""` used to be written verbatim, closing the block
// early; the rest of the comment was then parsed as SDL and the generated
// schema.graphql failed in gqlgen, far from the schema that caused it. The
// spec escape inside a block string is `\"""`.
func TestSDLDescription_EscapesTripleQuotes(t *testing.T) {
	const hostile = `Use """quoted""" text; "a" and \ stay`
	typ, err := entgen.NewType(&entgen.Config{Package: "example.com/app/velox"}, &load.Schema{
		Name: "Note",
		Fields: []*load.Field{
			{Name: "body", Info: &field.TypeInfo{Type: field.TypeString}, Comment: hostile},
		},
		Annotations: map[string]any{
			"graphql": Annotation{
				ResolverMappings: []ResolverMapping{Map("summary", "String").WithComment(hostile)},
				QueryField:       true,
				QueryFieldConfig: &QueryFieldSettings{Description: hostile},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	typ.ID = &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}}
	g := &entgen.Graph{Config: &entgen.Config{Package: "example.com/app/velox"}, Nodes: []*entgen.Type{typ}}
	gen := NewGenerator(g, Config{Package: "graphql", RelaySpec: true})

	sdl := gen.genFullSchema()
	if n := strings.Count(sdl, `\"""quoted\"""`); n < 3 {
		t.Errorf("want the comment escaped at the field, resolver-mapping and query-field sites, found %d:\n%s", n, sdl)
	}
	if _, err := parser.ParseSchema(&ast.Source{Name: "schema.graphql", Input: sdl}); err != nil {
		t.Fatalf("generated SDL does not parse: %v\n%s", err, sdl)
	}
}

func TestSDLDescription_Shape(t *testing.T) {
	if got, want := sdlDescription("a\"\"\"b", "  "), "\"\"\"\n  a\\\"\"\"b\n  \"\"\"\n"; got != want {
		t.Errorf("sdlDescription = %q, want %q", got, want)
	}
}

// The SDL is assembled by string concatenation. It used to be parsed only
// when schema hooks or an output writer were configured, so malformed SDL
// was written to disk silently and surfaced later as a gqlgen error.
// writeSchema now parses every file it writes and fails generation with the
// file name and position.
func TestWriteSchema_RejectsInvalidSDL(t *testing.T) {
	dir := t.TempDir()
	gen := NewGenerator(&entgen.Graph{Config: &entgen.Config{}}, Config{OutDir: dir})

	err := gen.writeSchema(context.Background(), "type Broken {\n  id: ID!\n", "", "schema.graphql")
	if err == nil {
		t.Fatal("writeSchema accepted malformed SDL")
	}
	for _, want := range []string{"schema.graphql", "line 3"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks context %q", err, want)
		}
	}
	if _, statErr := os.Stat(filepath.Join(dir, "schema.graphql")); !os.IsNotExist(statErr) {
		t.Error("malformed SDL was written to disk")
	}

	// A fragment that references types declared in another file is still
	// valid on its own — only syntax is checked, not type resolution.
	if err := gen.writeSchema(context.Background(), "type A {\n  b: DefinedElsewhere\n}\n", "schema", "types.graphql"); err != nil {
		t.Fatalf("valid fragment rejected: %v", err)
	}
}
