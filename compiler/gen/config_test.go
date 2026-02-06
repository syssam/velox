package gen

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/syssam/velox/schema/field"
)

func TestOutputConfig(t *testing.T) {
	t.Run("returns grouped output settings", func(t *testing.T) {
		c := &Config{
			Target:  "./ent",
			Package: "github.com/test/project/ent",
			Header:  "// Custom header",
		}

		output := c.Output()

		assert.Equal(t, "./ent", output.Target)
		assert.Equal(t, "github.com/test/project/ent", output.Package)
		assert.Equal(t, "// Custom header", output.Header)
	})

	t.Run("handles empty config", func(t *testing.T) {
		c := &Config{}

		output := c.Output()

		assert.Empty(t, output.Target)
		assert.Empty(t, output.Package)
		assert.Empty(t, output.Header)
	})
}

func TestSchemaConfigGroup(t *testing.T) {
	t.Run("returns grouped schema settings", func(t *testing.T) {
		idType := &field.TypeInfo{Type: field.TypeString}
		storage := &Storage{}
		c := &Config{
			Schema:  "github.com/test/project/ent/schema",
			IDType:  idType,
			Storage: storage,
		}

		schemaOpts := c.SchemaOpts()

		assert.Equal(t, "github.com/test/project/ent/schema", schemaOpts.Schema)
		assert.Equal(t, idType, schemaOpts.IDType)
		assert.Equal(t, storage, schemaOpts.Storage)
	})

	t.Run("handles nil fields", func(t *testing.T) {
		c := &Config{
			Schema: "test/schema",
		}

		schemaOpts := c.SchemaOpts()

		assert.Equal(t, "test/schema", schemaOpts.Schema)
		assert.Nil(t, schemaOpts.IDType)
		assert.Nil(t, schemaOpts.Storage)
	})
}

func TestConfigFeatureEnabled(t *testing.T) {
	t.Run("returns true for enabled feature", func(t *testing.T) {
		c := &Config{
			Features: []Feature{
				{Name: "privacy"},
				{Name: "intercept"},
			},
		}

		enabled, err := c.FeatureEnabled("privacy")

		assert.NoError(t, err)
		assert.True(t, enabled)
	})

	t.Run("returns false for disabled feature", func(t *testing.T) {
		c := &Config{
			Features: []Feature{
				{Name: "privacy"},
			},
		}

		enabled, err := c.FeatureEnabled("intercept")

		assert.NoError(t, err)
		assert.False(t, enabled)
	})

	t.Run("returns error for unknown feature", func(t *testing.T) {
		c := &Config{}

		_, err := c.FeatureEnabled("nonexistent")

		assert.Error(t, err)
		assert.True(t, IsConfigError(err))
	})
}

func TestConfigHasFeature(t *testing.T) {
	t.Run("returns true for enabled feature", func(t *testing.T) {
		c := &Config{
			Features: []Feature{
				{Name: "privacy"},
			},
		}

		assert.True(t, c.HasFeature("privacy"))
	})

	t.Run("returns false for disabled feature", func(t *testing.T) {
		c := &Config{
			Features: []Feature{},
		}

		assert.False(t, c.HasFeature("privacy"))
	})
}

func TestDefaultConfig(t *testing.T) {
	t.Run("has default header", func(t *testing.T) {
		c := DefaultConfig()

		assert.Equal(t, defaultHeader, c.Header)
	})

	t.Run("has default ID type", func(t *testing.T) {
		c := DefaultConfig()

		assert.NotNil(t, c.IDType)
		assert.Equal(t, field.TypeInt, c.IDType.Type)
	})
}

func TestConfigFeatureEnabled_AllFeatures(t *testing.T) {
	// Test that all declared features can be queried
	for _, f := range allFeatures {
		t.Run(f.Name, func(t *testing.T) {
			c := &Config{Features: []Feature{f}}

			enabled, err := c.FeatureEnabled(f.Name)

			assert.NoError(t, err)
			assert.True(t, enabled)
		})
	}
}
