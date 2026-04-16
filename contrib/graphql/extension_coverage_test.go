package graphql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	entgen "github.com/syssam/velox/compiler/gen"
)

// =============================================================================
// Extension Method Tests
// =============================================================================

func TestExtension_Options(t *testing.T) {
	ext, err := NewExtension()
	require.NoError(t, err)

	opts := ext.Options()
	assert.NotEmpty(t, opts, "Options should return compiler options")
}

func TestExtension_Config(t *testing.T) {
	ext, err := NewExtension(
		WithPackage("mypkg"),
		WithRelayConnection(true),
	)
	require.NoError(t, err)

	cfg := ext.Config()
	assert.Equal(t, "mypkg", cfg.Package)
	assert.True(t, cfg.RelayConnection)
}

func TestExtension_GQLGenConfig_Nil(t *testing.T) {
	ext, err := NewExtension()
	require.NoError(t, err)

	assert.Nil(t, ext.GQLGenConfig())
}

func TestExtension_GQLGenConfig_WithConfig(t *testing.T) {
	gqlCfg := &GQLGenConfig{
		NullableInputOmittable: true,
	}
	ext, err := NewExtension(WithGQLGenConfig(gqlCfg))
	require.NoError(t, err)

	assert.NotNil(t, ext.GQLGenConfig())
	assert.True(t, ext.GQLGenConfig().NullableInputOmittable)
}

// =============================================================================
// Extension Option Tests
// =============================================================================

func TestWithORMPackage(t *testing.T) {
	ext, err := NewExtension(WithORMPackage("example.com/ent"))
	require.NoError(t, err)
	assert.Equal(t, "example.com/ent", ext.config.ORMPackage)
}

func TestWithConfig(t *testing.T) {
	cfg := Config{
		Package:         "custom",
		ORMPackage:      "example.com/ent",
		RelayConnection: true,
	}
	ext, err := NewExtension(WithConfig(cfg))
	require.NoError(t, err)
	assert.Equal(t, "custom", ext.config.Package)
	assert.Equal(t, "example.com/ent", ext.config.ORMPackage)
	assert.True(t, ext.config.RelayConnection)
}

func TestWithConfigPath_InvalidPath(t *testing.T) {
	// WithConfigPath reads a YAML file; an invalid path should return an error
	// Note: LoadGQLGenConfig may or may not error on a non-existent path
	// depending on implementation. Just verify it doesn't panic.
	_, _ = NewExtension(WithConfigPath("/nonexistent/gqlgen.yml"))
}

func TestWithSchemaHook(t *testing.T) {
	hook := func(g *entgen.Graph, schema *ast.Schema) error {
		return nil
	}
	ext, err := NewExtension(WithSchemaHook(hook))
	require.NoError(t, err)
	assert.Len(t, ext.schemaHooks, 1)
}

func TestWithSchemaHook_Multiple(t *testing.T) {
	hook1 := func(g *entgen.Graph, schema *ast.Schema) error { return nil }
	hook2 := func(g *entgen.Graph, schema *ast.Schema) error { return nil }

	ext, err := NewExtension(WithSchemaHook(hook1), WithSchemaHook(hook2))
	require.NoError(t, err)
	assert.Len(t, ext.schemaHooks, 2)
}

func TestWithSchemaPath_DirectoryPath(t *testing.T) {
	ext, err := NewExtension(WithSchemaPath("./schema"))
	require.NoError(t, err)
	assert.Equal(t, "./schema", ext.config.SchemaOutDir)
	assert.Equal(t, "", ext.config.SchemaFilename)
}

func TestWithSchemaPath_FilePath(t *testing.T) {
	ext, err := NewExtension(WithSchemaPath("./schema/custom.graphql"))
	require.NoError(t, err)
	// filepath.Dir("./schema/custom.graphql") returns "schema" (not "./schema")
	assert.Equal(t, "schema", ext.config.SchemaOutDir)
	assert.Equal(t, "custom.graphql", ext.config.SchemaFilename)
}

// =============================================================================
// packageName Tests
// =============================================================================

func TestPackageName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"example.com/myapp/velox", "velox"},
		{"example.com/ent", "ent"},
		{"pkg", "pkg"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, packageName(tt.input))
		})
	}
}

// =============================================================================
// extensionAnnotation Tests
// =============================================================================

func TestExtensionAnnotation_Name(t *testing.T) {
	ann := &extensionAnnotation{}
	assert.Equal(t, "GraphQL", ann.Name())
}

// =============================================================================
// Options adds FeatureNamedEdges
// =============================================================================

func TestExtension_Options_AddsNamedEdges(t *testing.T) {
	ext, err := NewExtension()
	require.NoError(t, err)

	cfg := &entgen.Config{Package: "test"}

	// Apply options to config
	for _, opt := range ext.Options() {
		err := opt(cfg)
		require.NoError(t, err)
	}

	// Check FeatureNamedEdges is added
	found := false
	for _, f := range cfg.Features {
		if f.Name == entgen.FeatureNamedEdges.Name {
			found = true
			break
		}
	}
	assert.True(t, found, "Options should add FeatureNamedEdges")
}

func TestExtension_Options_DoesNotDuplicateNamedEdges(t *testing.T) {
	ext, err := NewExtension()
	require.NoError(t, err)

	cfg := &entgen.Config{
		Package:  "test",
		Features: []entgen.Feature{entgen.FeatureNamedEdges},
	}

	for _, opt := range ext.Options() {
		err := opt(cfg)
		require.NoError(t, err)
	}

	count := 0
	for _, f := range cfg.Features {
		if f.Name == entgen.FeatureNamedEdges.Name {
			count++
		}
	}
	assert.Equal(t, 1, count, "FeatureNamedEdges should not be duplicated")
}

// =============================================================================
// Schema Hooks Invocation Tests (Fix #1)
// =============================================================================

func TestWithSchemaHook_PassedToConfig(t *testing.T) {
	var hookCalled bool
	hook := func(g *entgen.Graph, schema *ast.Schema) error {
		hookCalled = true
		return nil
	}
	ext, err := NewExtension(WithSchemaHook(hook))
	require.NoError(t, err)

	// Verify schemaHooks are stored
	assert.Len(t, ext.schemaHooks, 1)
	assert.False(t, hookCalled, "hook should not be called during construction")
}

func TestSchemaHooks_AppliedInWriteSchema(t *testing.T) {
	// Test that schema hooks are applied to the AST during writeSchema.
	hookCalled := false
	gen := &Generator{
		config: Config{
			OutDir: t.TempDir(),
			schemaHooks: []SchemaHook{
				func(g *entgen.Graph, schema *ast.Schema) error {
					hookCalled = true
					// Verify we received a typed AST
					assert.NotNil(t, schema.Types)
					return nil
				},
			},
			SchemaGenerator: true,
		},
	}

	// Call writeSchema with subdir="" (root file — hooks should apply)
	// Use valid SDL that gqlparser can parse
	err := gen.writeSchema(t.Context(), "type Query { hello: String }", "", "test.graphql")
	require.NoError(t, err)
	assert.True(t, hookCalled, "schema hook should be called for root schema files")
}

func TestSchemaHooks_AppliedToSubdir(t *testing.T) {
	hookCalled := false
	gen := &Generator{
		config: Config{
			OutDir: t.TempDir(),
			schemaHooks: []SchemaHook{
				func(g *entgen.Graph, schema *ast.Schema) error {
					hookCalled = true
					return nil
				},
			},
		},
	}

	// Hooks apply to ALL schema files including per-entity files,
	// so directives can be added to entity-specific types.
	err := gen.writeSchema(t.Context(), "type User { id: ID! }", "schema", "velox_user.graphql")
	require.NoError(t, err)
	assert.True(t, hookCalled, "schema hooks should be applied to all schema files")
}

func TestWithNodeDescriptor(t *testing.T) {
	ext, err := NewExtension(WithNodeDescriptor())
	require.NoError(t, err)
	assert.True(t, ext.config.NodeDescriptor)
}

func TestWithOutputWriter(t *testing.T) {
	ext, err := NewExtension(WithOutputWriter(func(s *ast.Schema) error {
		return nil
	}))
	require.NoError(t, err)
	assert.NotNil(t, ext.config.outputWriter)
}

func TestSchemaHooks_ASTAccess(t *testing.T) {
	// Verify that schema hooks receive a typed AST that can be inspected
	var foundQuery bool
	gen := &Generator{
		config: Config{
			OutDir: t.TempDir(),
			schemaHooks: []SchemaHook{
				func(g *entgen.Graph, schema *ast.Schema) error {
					// Verify we can access typed schema nodes
					if schema.Types["Query"] != nil {
						foundQuery = true
					}
					return nil
				},
			},
		},
	}

	err := gen.writeSchema(t.Context(), "type Query { hello: String }", "", "test.graphql")
	require.NoError(t, err)
	assert.True(t, foundQuery, "schema hook should find Query type in AST")
}

func TestWithOutputWriter_ReceivesSchema(t *testing.T) {
	var received *ast.Schema
	gen := &Generator{
		config: Config{
			OutDir: t.TempDir(),
			outputWriter: func(s *ast.Schema) error {
				received = s
				return nil
			},
		},
	}

	err := gen.writeSchema(t.Context(), "type Query { hello: String }", "", "test.graphql")
	require.NoError(t, err)
	assert.NotNil(t, received)
	assert.NotNil(t, received.Types["Query"])
}
