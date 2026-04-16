package graphql

import (
	"testing"

	entgen "github.com/syssam/velox/compiler/gen"
)

func TestNewExtension(t *testing.T) {
	ext, err := NewExtension(
		WithSchemaPath("./gql"),
		WithPackage("mygql"),
		WithRelayConnection(true),
		WithWhereInputs(true),
		WithMutations(true),
		WithOrdering(true),
		WithRelaySpec(true),
	)
	if err != nil {
		t.Fatalf("NewExtension() error = %v", err)
	}

	if ext.config.SchemaOutDir != "./gql" {
		t.Errorf("SchemaOutDir = %q, want %q", ext.config.SchemaOutDir, "./gql")
	}
	if ext.config.Package != "mygql" {
		t.Errorf("Package = %q, want %q", ext.config.Package, "mygql")
	}
	if !ext.config.RelayConnection {
		t.Error("RelayConnection should be true")
	}
	if !ext.config.WhereInputs {
		t.Error("WhereInputs should be true")
	}
	if !ext.config.Mutations {
		t.Error("Mutations should be true")
	}
	if !ext.config.Ordering {
		t.Error("Ordering should be true")
	}
	if !ext.config.RelaySpec {
		t.Error("RelaySpec should be true")
	}
}

func TestExtension_Hooks(t *testing.T) {
	ext, err := NewExtension()
	if err != nil {
		t.Fatalf("NewExtension() error = %v", err)
	}

	hooks := ext.Hooks()
	if len(hooks) != 1 {
		t.Errorf("expected 1 hook, got %d", len(hooks))
	}
}

func TestExtension_Annotations(t *testing.T) {
	ext, err := NewExtension()
	if err != nil {
		t.Fatalf("NewExtension() error = %v", err)
	}

	annotations := ext.Annotations()
	if len(annotations) != 1 {
		t.Errorf("expected 1 annotation, got %d", len(annotations))
	}

	if annotations[0].Name() != "GraphQL" {
		t.Errorf("annotation name = %q, want %q", annotations[0].Name(), "GraphQL")
	}
}

func TestExtension_WithMapScalarFunc(t *testing.T) {
	// Custom scalar mapping function
	mapFn := func(_ *entgen.Type, f *entgen.Field) string {
		if f.Name == "special" {
			return "SpecialScalar"
		}
		return ""
	}

	ext, err := NewExtension(WithMapScalarFunc(mapFn))
	if err != nil {
		t.Fatalf("NewExtension() error = %v", err)
	}

	if ext.config.MapScalarFunc == nil {
		t.Error("MapScalarFunc should not be nil")
	}

	// Test that the function works correctly
	result := ext.config.MapScalarFunc(nil, &entgen.Field{Name: "special"})
	if result != "SpecialScalar" {
		t.Errorf("MapScalarFunc(special) = %q, want %q", result, "SpecialScalar")
	}

	result = ext.config.MapScalarFunc(nil, &entgen.Field{Name: "other"})
	if result != "" {
		t.Errorf("MapScalarFunc(other) = %q, want empty", result)
	}
}

func TestExtension_WithTemplates(t *testing.T) {
	tmpl := entgen.NewTemplate("test-template")

	ext, err := NewExtension(WithTemplates(tmpl))
	if err != nil {
		t.Fatalf("NewExtension() error = %v", err)
	}

	templates := ext.Templates()
	if len(templates) != 1 {
		t.Errorf("expected 1 template, got %d", len(templates))
	}

	if templates[0] != tmpl {
		t.Error("template should match the one passed to WithTemplates")
	}
}

func TestExtension_WithRelaySpec_BackwardCompatibility(t *testing.T) {
	// Test that WithNodeInterface still works for backward compatibility
	ext, err := NewExtension(WithNodeInterface(true))
	if err != nil {
		t.Fatalf("NewExtension() error = %v", err)
	}

	if !ext.config.RelaySpec {
		t.Error("RelaySpec should be true when using deprecated WithNodeInterface")
	}
}

func TestExtension_WithSchemaGenerator(t *testing.T) {
	// By default, schemaGenerator should be false
	ext, err := NewExtension()
	if err != nil {
		t.Fatalf("NewExtension() error = %v", err)
	}

	if ext.schemaGenerator {
		t.Error("schemaGenerator should be false by default")
	}

	// When WithSchemaGenerator is used, it should be true
	ext2, err := NewExtension(WithSchemaGenerator())
	if err != nil {
		t.Fatalf("NewExtension() error = %v", err)
	}

	if !ext2.schemaGenerator {
		t.Error("schemaGenerator should be true when WithSchemaGenerator is used")
	}
}
