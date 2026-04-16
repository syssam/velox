package gen

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/schema/field"
)

func TestWithHeader(t *testing.T) {
	t.Run("sets header", func(t *testing.T) {
		c := &Config{}
		err := WithHeader("// Custom header")(c)

		require.NoError(t, err)
		assert.Equal(t, "// Custom header", c.Header)
	})

	t.Run("empty header is allowed", func(t *testing.T) {
		c := &Config{Header: "existing"}
		err := WithHeader("")(c)

		require.NoError(t, err)
		assert.Equal(t, "", c.Header)
	})
}

func TestWithIDType(t *testing.T) {
	tests := []struct {
		name     string
		idType   string
		expected field.Type
		wantErr  bool
	}{
		{"int", "int", field.TypeInt, false},
		{"int64", "int64", field.TypeInt64, false},
		{"uint64", "uint64", field.TypeUint64, false},
		{"string", "string", field.TypeString, false},
		{"uuid", "uuid", field.TypeUUID, false},
		{"invalid", "float", 0, true},
		{"empty", "", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{}
			err := WithIDType(tt.idType)(c)

			if tt.wantErr {
				require.Error(t, err)
				assert.True(t, IsConfigError(err))
			} else {
				require.NoError(t, err)
				require.NotNil(t, c.IDType)
				assert.Equal(t, tt.expected, c.IDType.Type)
			}
		})
	}
}

func TestWithIDTypeInfo(t *testing.T) {
	t.Run("sets TypeInfo", func(t *testing.T) {
		info := &field.TypeInfo{Type: field.TypeString}
		c := &Config{}
		err := WithIDTypeInfo(info)(c)

		require.NoError(t, err)
		assert.Equal(t, info, c.IDType)
	})

	t.Run("nil TypeInfo returns error", func(t *testing.T) {
		c := &Config{}
		err := WithIDTypeInfo(nil)(c)

		require.Error(t, err)
		assert.True(t, IsConfigError(err))
	})
}

func TestWithPackage(t *testing.T) {
	t.Run("sets package", func(t *testing.T) {
		c := &Config{}
		err := WithPackage("github.com/org/project/ent")(c)

		require.NoError(t, err)
		assert.Equal(t, "github.com/org/project/ent", c.Package)
	})

	t.Run("empty package returns error", func(t *testing.T) {
		c := &Config{}
		err := WithPackage("")(c)

		require.Error(t, err)
		assert.True(t, IsConfigError(err))
	})
}

func TestWithSchema(t *testing.T) {
	t.Run("sets schema", func(t *testing.T) {
		c := &Config{}
		err := WithSchema("github.com/org/project/ent/schema")(c)

		require.NoError(t, err)
		assert.Equal(t, "github.com/org/project/ent/schema", c.Schema)
	})

	t.Run("empty schema returns error", func(t *testing.T) {
		c := &Config{}
		err := WithSchema("")(c)

		require.Error(t, err)
		assert.True(t, IsConfigError(err))
	})
}

func TestWithTarget(t *testing.T) {
	t.Run("sets target directory", func(t *testing.T) {
		c := &Config{}
		err := WithTarget("./ent")(c)

		require.NoError(t, err)
		assert.Equal(t, "./ent", c.Target)
	})

	t.Run("empty target returns error", func(t *testing.T) {
		c := &Config{}
		err := WithTarget("")(c)

		require.Error(t, err)
		assert.True(t, IsConfigError(err))
	})
}

func TestWithFeatures(t *testing.T) {
	t.Run("adds single feature", func(t *testing.T) {
		c := &Config{}
		feature := Feature{Name: "privacy"}
		err := WithFeatures(feature)(c)

		require.NoError(t, err)
		assert.Equal(t, 1, len(c.Features))
		assert.Equal(t, "privacy", c.Features[0].Name)
	})

	t.Run("adds multiple features", func(t *testing.T) {
		c := &Config{}
		f1 := Feature{Name: "privacy"}
		f2 := Feature{Name: "intercept"}
		err := WithFeatures(f1, f2)(c)

		require.NoError(t, err)
		assert.Equal(t, 2, len(c.Features))
	})

	t.Run("appends to existing features", func(t *testing.T) {
		c := &Config{Features: []Feature{{Name: "existing"}}}
		err := WithFeatures(Feature{Name: "new"})(c)

		require.NoError(t, err)
		assert.Equal(t, 2, len(c.Features))
	})
}

func TestWithStorage(t *testing.T) {
	t.Run("sets storage", func(t *testing.T) {
		storage := &Storage{}
		c := &Config{}
		err := WithStorage(storage)(c)

		require.NoError(t, err)
		assert.Equal(t, storage, c.Storage)
	})

	t.Run("nil storage is allowed", func(t *testing.T) {
		c := &Config{Storage: &Storage{}}
		err := WithStorage(nil)(c)

		require.NoError(t, err)
		assert.Nil(t, c.Storage)
	})
}

func TestWithStorageDriver(t *testing.T) {
	tests := []struct {
		name    string
		driver  string
		wantErr bool
	}{
		{"sqlite", "sqlite", false},
		{"mysql", "mysql", false},
		{"postgres", "postgres", false},
		{"invalid", "mongodb", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{}
			err := WithStorageDriver(tt.driver)(c)

			if tt.wantErr {
				require.Error(t, err)
				assert.True(t, IsConfigError(err))
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestWithHooks(t *testing.T) {
	t.Run("adds hooks", func(t *testing.T) {
		hook := func(next Generator) Generator { return next }
		c := &Config{}
		err := WithHooks(hook)(c)

		require.NoError(t, err)
		assert.Equal(t, 1, len(c.Hooks))
	})

	t.Run("appends to existing hooks", func(t *testing.T) {
		hook1 := func(next Generator) Generator { return next }
		hook2 := func(next Generator) Generator { return next }
		c := &Config{Hooks: []Hook{hook1}}
		err := WithHooks(hook2)(c)

		require.NoError(t, err)
		assert.Equal(t, 2, len(c.Hooks))
	})
}

func TestWithTemplates(t *testing.T) {
	t.Run("adds templates", func(t *testing.T) {
		tmpl := NewTemplate("custom")
		c := &Config{}
		err := WithTemplates(tmpl)(c)

		require.NoError(t, err)
		assert.Equal(t, 1, len(c.Templates))
	})

	t.Run("appends to existing templates", func(t *testing.T) {
		tmpl1 := NewTemplate("existing")
		tmpl2 := NewTemplate("new")
		c := &Config{Templates: []*Template{tmpl1}}
		err := WithTemplates(tmpl2)(c)

		require.NoError(t, err)
		assert.Equal(t, 2, len(c.Templates))
	})
}

func TestWithAnnotations(t *testing.T) {
	t.Run("sets annotations on nil map", func(t *testing.T) {
		c := &Config{}
		err := WithAnnotations(Annotations{"key": "value"})(c)

		require.NoError(t, err)
		assert.Equal(t, "value", c.Annotations["key"])
	})

	t.Run("merges with existing annotations", func(t *testing.T) {
		c := &Config{Annotations: Annotations{"existing": "value"}}
		err := WithAnnotations(Annotations{"new": "value2"})(c)

		require.NoError(t, err)
		assert.Equal(t, "value", c.Annotations["existing"])
		assert.Equal(t, "value2", c.Annotations["new"])
	})

	t.Run("overwrites existing keys", func(t *testing.T) {
		c := &Config{Annotations: Annotations{"key": "old"}}
		err := WithAnnotations(Annotations{"key": "new"})(c)

		require.NoError(t, err)
		assert.Equal(t, "new", c.Annotations["key"])
	})
}

func TestWithBuildFlags(t *testing.T) {
	t.Run("adds build flags", func(t *testing.T) {
		c := &Config{}
		err := WithBuildFlags("-tags=test")(c)

		require.NoError(t, err)
		assert.Equal(t, []string{"-tags=test"}, c.BuildFlags)
	})

	t.Run("appends to existing flags", func(t *testing.T) {
		c := &Config{BuildFlags: []string{"-mod=vendor"}}
		err := WithBuildFlags("-tags=test")(c)

		require.NoError(t, err)
		assert.Equal(t, []string{"-mod=vendor", "-tags=test"}, c.BuildFlags)
	})
}

func TestConfigApply(t *testing.T) {
	t.Run("applies multiple options", func(t *testing.T) {
		c := &Config{}
		err := c.Apply(
			WithPackage("github.com/test/project"),
			WithTarget("./ent"),
			WithHeader("// Custom"),
		)

		require.NoError(t, err)
		assert.Equal(t, "github.com/test/project", c.Package)
		assert.Equal(t, "./ent", c.Target)
		assert.Equal(t, "// Custom", c.Header)
	})

	t.Run("stops on first error", func(t *testing.T) {
		c := &Config{}
		err := c.Apply(
			WithPackage(""),     // Error
			WithTarget("./ent"), // Should not be applied
		)

		require.Error(t, err)
		assert.Empty(t, c.Package)
		assert.Empty(t, c.Target)
	})
}

func TestConfigApplyAll(t *testing.T) {
	t.Run("collects all errors", func(t *testing.T) {
		c := &Config{}
		err := c.ApplyAll(
			WithPackage(""), // Error
			WithTarget(""),  // Error
		)

		require.Error(t, err)
		// errors.Join returns an error with Unwrap() []error
		unwrapper, ok := err.(interface{ Unwrap() []error })
		require.True(t, ok, "error should implement Unwrap() []error")
		assert.Equal(t, 2, len(unwrapper.Unwrap()))
	})

	t.Run("returns nil when all succeed", func(t *testing.T) {
		c := &Config{}
		err := c.ApplyAll(
			WithPackage("github.com/test"),
			WithTarget("./ent"),
		)

		require.NoError(t, err)
	})
}

func TestNewConfig(t *testing.T) {
	t.Run("creates config with options", func(t *testing.T) {
		c, err := NewConfig(
			WithPackage("github.com/test/project"),
			WithTarget("./ent"),
		)

		require.NoError(t, err)
		require.NotNil(t, c)
		assert.Equal(t, "github.com/test/project", c.Package)
		assert.Equal(t, "./ent", c.Target)
	})

	t.Run("returns error on invalid option", func(t *testing.T) {
		c, err := NewConfig(
			WithPackage(""),
		)

		require.Error(t, err)
		assert.Nil(t, c)
	})
}

func TestMustNewConfig(t *testing.T) {
	t.Run("returns config on success", func(t *testing.T) {
		c := MustNewConfig(
			WithPackage("github.com/test/project"),
		)

		require.NotNil(t, c)
		assert.Equal(t, "github.com/test/project", c.Package)
	})

	t.Run("panics on error", func(t *testing.T) {
		assert.Panics(t, func() {
			MustNewConfig(WithPackage(""))
		})
	})
}
