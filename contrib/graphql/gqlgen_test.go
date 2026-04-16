package graphql

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/yaml.v3"
)

// =============================================================================
// StringList YAML Tests
// =============================================================================

func TestStringList_UnmarshalYAML_String(t *testing.T) {
	input := `schema: "schema.graphql"`
	var result struct {
		Schema StringList `yaml:"schema"`
	}
	err := yaml.Unmarshal([]byte(input), &result)
	require.NoError(t, err)
	assert.Equal(t, StringList{"schema.graphql"}, result.Schema)
}

func TestStringList_UnmarshalYAML_List(t *testing.T) {
	input := "schema:\n  - schema1.graphql\n  - schema2.graphql"
	var result struct {
		Schema StringList `yaml:"schema"`
	}
	err := yaml.Unmarshal([]byte(input), &result)
	require.NoError(t, err)
	assert.Equal(t, StringList{"schema1.graphql", "schema2.graphql"}, result.Schema)
}

func TestStringList_UnmarshalYAML_Invalid(t *testing.T) {
	input := "schema:\n  key: value"
	var result struct {
		Schema StringList `yaml:"schema"`
	}
	err := yaml.Unmarshal([]byte(input), &result)
	assert.Error(t, err)
}

func TestStringList_MarshalYAML_Single(t *testing.T) {
	s := StringList{"schema.graphql"}
	result, err := s.MarshalYAML()
	require.NoError(t, err)
	assert.Equal(t, "schema.graphql", result)
}

func TestStringList_MarshalYAML_Multiple(t *testing.T) {
	s := StringList{"a.graphql", "b.graphql"}
	result, err := s.MarshalYAML()
	require.NoError(t, err)
	assert.Equal(t, []string{"a.graphql", "b.graphql"}, result)
}

// =============================================================================
// LoadGQLGenConfig Tests
// =============================================================================

func TestLoadGQLGenConfig_NonExistent(t *testing.T) {
	cfg, err := LoadGQLGenConfig("/tmp/nonexistent-gqlgen-test.yml")
	require.NoError(t, err, "non-existent file should return empty config, not error")
	assert.NotNil(t, cfg)
	assert.NotNil(t, cfg.Models)
}

func TestLoadGQLGenConfig_ValidFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "gqlgen.yml")

	content := `schema:
  - "schema.graphql"
autobind:
  - "example.com/ent"
nullable_input_omittable: true
models:
  ID:
    model:
      - "github.com/99designs/gqlgen/graphql.UUID"
`
	err := os.WriteFile(cfgPath, []byte(content), 0o644)
	require.NoError(t, err)

	cfg, err := LoadGQLGenConfig(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, StringList{"schema.graphql"}, cfg.SchemaFilename)
	assert.Equal(t, []string{"example.com/ent"}, cfg.Autobind)
	assert.True(t, cfg.NullableInputOmittable)
	assert.Contains(t, cfg.Models, "ID")
}

func TestLoadGQLGenConfig_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "gqlgen.yml")

	err := os.WriteFile(cfgPath, []byte("invalid: yaml: [broken"), 0o644)
	require.NoError(t, err)

	_, err = LoadGQLGenConfig(cfgPath)
	assert.Error(t, err)
}

// =============================================================================
// SaveGQLGenConfig Tests
// =============================================================================

func TestSaveGQLGenConfig(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "gqlgen.yml")

	cfg := &GQLGenConfig{
		SchemaFilename: StringList{"schema.graphql"},
		Autobind:       []string{"example.com/ent"},
		Models:         map[string]TypeMapEntry{},
	}

	err := SaveGQLGenConfig(cfgPath, cfg)
	require.NoError(t, err)

	// Verify file was created and is valid YAML
	loaded, err := LoadGQLGenConfig(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, StringList{"schema.graphql"}, loaded.SchemaFilename)
}

func TestSaveGQLGenConfig_CreatesDir(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "subdir", "gqlgen.yml")

	cfg := &GQLGenConfig{
		Models: map[string]TypeMapEntry{},
	}

	err := SaveGQLGenConfig(cfgPath, cfg)
	require.NoError(t, err)

	_, err = os.Stat(cfgPath)
	assert.NoError(t, err)
}

// =============================================================================
// GQLGenConfig Method Tests
// =============================================================================

func TestGQLGenConfig_AddSchemaPath(t *testing.T) {
	cfg := &GQLGenConfig{}

	cfg.AddSchemaPath("schema.graphql")
	assert.Equal(t, StringList{"schema.graphql"}, cfg.SchemaFilename)

	// Should not add duplicates
	cfg.AddSchemaPath("schema.graphql")
	assert.Len(t, cfg.SchemaFilename, 1)

	cfg.AddSchemaPath("other.graphql")
	assert.Len(t, cfg.SchemaFilename, 2)
}

func TestGQLGenConfig_AddAutobind(t *testing.T) {
	cfg := &GQLGenConfig{}

	cfg.AddAutobind("example.com/ent")
	assert.Equal(t, []string{"example.com/ent"}, cfg.Autobind)

	// Should not add duplicates
	cfg.AddAutobind("example.com/ent")
	assert.Len(t, cfg.Autobind, 1)

	cfg.AddAutobind("example.com/pkg")
	assert.Len(t, cfg.Autobind, 2)
}

func TestGQLGenConfig_SetModel(t *testing.T) {
	cfg := &GQLGenConfig{
		Models: make(map[string]TypeMapEntry),
	}

	cfg.SetModel("ID", "github.com/99designs/gqlgen/graphql.UUID")
	assert.Contains(t, cfg.Models, "ID")
	assert.Equal(t, StringList{"github.com/99designs/gqlgen/graphql.UUID"}, cfg.Models["ID"].Model)

	// Should not add duplicate model
	cfg.SetModel("ID", "github.com/99designs/gqlgen/graphql.UUID")
	assert.Len(t, cfg.Models["ID"].Model, 1)

	// Should add different model
	cfg.SetModel("ID", "github.com/99designs/gqlgen/graphql.IntID")
	assert.Len(t, cfg.Models["ID"].Model, 2)
}

func TestGQLGenConfig_InjectVeloxBindings(t *testing.T) {
	t.Run("EmptyORMPackage", func(t *testing.T) {
		cfg := &GQLGenConfig{Models: make(map[string]TypeMapEntry)}
		cfg.InjectVeloxBindings("", "schema.graphql")
		// Should not modify config when ormPackage is empty
		assert.Empty(t, cfg.SchemaFilename)
		assert.Empty(t, cfg.Autobind)
	})

	t.Run("FullInjection", func(t *testing.T) {
		cfg := &GQLGenConfig{Models: make(map[string]TypeMapEntry)}
		cfg.InjectVeloxBindings("example.com/ent", "schema.graphql")

		assert.Contains(t, cfg.SchemaFilename, "schema.graphql")
		assert.Contains(t, cfg.Autobind, "example.com/ent")
		assert.Contains(t, cfg.Models, "ID")
		assert.Contains(t, cfg.Models, "UUID")
		assert.Contains(t, cfg.Models, "JSON")
	})

	t.Run("EmptySchemaPath", func(t *testing.T) {
		cfg := &GQLGenConfig{Models: make(map[string]TypeMapEntry)}
		cfg.InjectVeloxBindings("example.com/ent", "")

		// Should not add empty schema path
		assert.Empty(t, cfg.SchemaFilename)
		// Should still add autobind and models
		assert.Contains(t, cfg.Autobind, "example.com/ent")
		assert.Contains(t, cfg.Models, "ID")
	})
}
