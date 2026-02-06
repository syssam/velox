package gen

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchemaError(t *testing.T) {
	t.Run("Error message with all fields", func(t *testing.T) {
		cause := errors.New("underlying error")
		err := NewSchemaError("User", "email", "invalid format", cause)

		assert.Contains(t, err.Error(), "velox: schema error")
		assert.Contains(t, err.Error(), "type User")
		assert.Contains(t, err.Error(), "field email")
		assert.Contains(t, err.Error(), "invalid format")
		assert.Contains(t, err.Error(), "underlying error")
	})

	t.Run("Error message with type only", func(t *testing.T) {
		err := &SchemaError{Type: "User"}
		assert.Contains(t, err.Error(), "type User")
		assert.NotContains(t, err.Error(), "field")
	})

	t.Run("Unwrap returns cause", func(t *testing.T) {
		cause := errors.New("root cause")
		err := NewSchemaError("User", "", "", cause)

		assert.Equal(t, cause, err.Unwrap())
		assert.True(t, errors.Is(err, cause))
	})

	t.Run("Is matches ErrInvalidSchema", func(t *testing.T) {
		err := NewSchemaError("User", "", "", nil)
		assert.True(t, err.Is(ErrInvalidSchema))
	})

	t.Run("IsSchemaError helper", func(t *testing.T) {
		err := NewSchemaError("User", "email", "test", nil)
		assert.True(t, IsSchemaError(err))
		assert.False(t, IsSchemaError(errors.New("other")))
	})
}

func TestConfigError(t *testing.T) {
	t.Run("Error message with value", func(t *testing.T) {
		err := NewConfigError("IDType", "invalid", "unsupported type")

		assert.Contains(t, err.Error(), "velox: config error")
		assert.Contains(t, err.Error(), "IDType")
		assert.Contains(t, err.Error(), "invalid")
		assert.Contains(t, err.Error(), "unsupported type")
	})

	t.Run("Error message without value", func(t *testing.T) {
		err := NewConfigError("Package", nil, "cannot be empty")

		assert.Contains(t, err.Error(), "Package")
		assert.Contains(t, err.Error(), "cannot be empty")
		assert.NotContains(t, err.Error(), "value:")
	})

	t.Run("Is matches ErrMissingConfig", func(t *testing.T) {
		err := NewConfigError("Target", nil, "missing")
		assert.True(t, err.Is(ErrMissingConfig))
	})

	t.Run("IsConfigError helper", func(t *testing.T) {
		err := NewConfigError("Target", nil, "missing")
		assert.True(t, IsConfigError(err))
		assert.False(t, IsConfigError(errors.New("other")))
	})
}

func TestEdgeError(t *testing.T) {
	t.Run("Error message with all fields", func(t *testing.T) {
		cause := errors.New("type not found")
		err := NewEdgeError("User", "Post", "posts", "invalid reference", cause)

		assert.Contains(t, err.Error(), "velox: edge error")
		assert.Contains(t, err.Error(), "edge posts")
		assert.Contains(t, err.Error(), "User -> Post")
		assert.Contains(t, err.Error(), "invalid reference")
		assert.Contains(t, err.Error(), "type not found")
	})

	t.Run("Error message with from only", func(t *testing.T) {
		err := &EdgeError{From: "User", Edge: "posts", Message: "test"}
		assert.Contains(t, err.Error(), "from User")
		assert.NotContains(t, err.Error(), "->")
	})

	t.Run("Unwrap returns cause", func(t *testing.T) {
		cause := errors.New("root cause")
		err := NewEdgeError("User", "Post", "posts", "", cause)

		assert.Equal(t, cause, err.Unwrap())
		assert.True(t, errors.Is(err, cause))
	})

	t.Run("Is matches ErrInvalidEdge", func(t *testing.T) {
		err := NewEdgeError("User", "Post", "posts", "", nil)
		assert.True(t, err.Is(ErrInvalidEdge))
	})

	t.Run("IsEdgeError helper", func(t *testing.T) {
		err := NewEdgeError("User", "Post", "posts", "", nil)
		assert.True(t, IsEdgeError(err))
		assert.False(t, IsEdgeError(errors.New("other")))
	})
}

func TestGenerationError(t *testing.T) {
	t.Run("Error message with all fields", func(t *testing.T) {
		cause := errors.New("write failed")
		err := NewGenerationError("entity", "user.go", "cannot write file", cause)

		assert.Contains(t, err.Error(), "velox: generation error")
		assert.Contains(t, err.Error(), "phase entity")
		assert.Contains(t, err.Error(), "file: user.go")
		assert.Contains(t, err.Error(), "cannot write file")
		assert.Contains(t, err.Error(), "write failed")
	})

	t.Run("Error message with phase only", func(t *testing.T) {
		err := &GenerationError{Phase: "client"}
		assert.Contains(t, err.Error(), "phase client")
	})

	t.Run("Unwrap returns cause", func(t *testing.T) {
		cause := errors.New("io error")
		err := NewGenerationError("entity", "", "", cause)

		assert.Equal(t, cause, err.Unwrap())
		assert.True(t, errors.Is(err, cause))
	})

	t.Run("Is matches ErrGenerationFailed", func(t *testing.T) {
		err := NewGenerationError("entity", "", "", nil)
		assert.True(t, err.Is(ErrGenerationFailed))
	})

	t.Run("IsGenerationError helper", func(t *testing.T) {
		err := NewGenerationError("entity", "user.go", "", nil)
		assert.True(t, IsGenerationError(err))
		assert.False(t, IsGenerationError(errors.New("other")))
	})
}

func TestValidationError(t *testing.T) {
	t.Run("Error message with all fields", func(t *testing.T) {
		err := NewValidationError("User", "age", -1, "must be non-negative")

		assert.Contains(t, err.Error(), "velox: validation error")
		assert.Contains(t, err.Error(), "type User")
		assert.Contains(t, err.Error(), "field age")
		assert.Contains(t, err.Error(), "must be non-negative")
	})

	t.Run("Error message with type only", func(t *testing.T) {
		err := &ValidationError{Type: "User", Message: "invalid"}
		assert.Contains(t, err.Error(), "type User")
		assert.Contains(t, err.Error(), "invalid")
	})

	t.Run("Unwrap returns cause", func(t *testing.T) {
		cause := errors.New("validation failed")
		err := &ValidationError{Cause: cause}

		assert.Equal(t, cause, err.Unwrap())
		assert.True(t, errors.Is(err, cause))
	})

	t.Run("Is matches ErrValidationFailed", func(t *testing.T) {
		err := NewValidationError("User", "age", -1, "")
		assert.True(t, err.Is(ErrValidationFailed))
	})

	t.Run("IsValidationError helper", func(t *testing.T) {
		err := NewValidationError("User", "age", nil, "test")
		assert.True(t, IsValidationError(err))
		assert.False(t, IsValidationError(errors.New("other")))
	})
}

func TestSentinelErrors(t *testing.T) {
	t.Run("ErrInvalidSchema", func(t *testing.T) {
		assert.Equal(t, "velox: invalid schema", ErrInvalidSchema.Error())
	})

	t.Run("ErrMissingConfig", func(t *testing.T) {
		assert.Equal(t, "velox: missing configuration", ErrMissingConfig.Error())
	})

	t.Run("ErrInvalidEdge", func(t *testing.T) {
		assert.Equal(t, "velox: invalid edge definition", ErrInvalidEdge.Error())
	})

	t.Run("ErrGenerationFailed", func(t *testing.T) {
		assert.Equal(t, "velox: code generation failed", ErrGenerationFailed.Error())
	})

	t.Run("ErrValidationFailed", func(t *testing.T) {
		assert.Equal(t, "velox: validation failed", ErrValidationFailed.Error())
	})
}

func TestErrorTypeChecking(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		isSchema bool
		isConfig bool
		isEdge   bool
		isGen    bool
		isVal    bool
	}{
		{
			name:     "SchemaError",
			err:      NewSchemaError("User", "", "", nil),
			isSchema: true,
		},
		{
			name:     "ConfigError",
			err:      NewConfigError("Package", nil, ""),
			isConfig: true,
		},
		{
			name:   "EdgeError",
			err:    NewEdgeError("User", "Post", "posts", "", nil),
			isEdge: true,
		},
		{
			name:  "GenerationError",
			err:   NewGenerationError("entity", "", "", nil),
			isGen: true,
		},
		{
			name:  "ValidationError",
			err:   NewValidationError("User", "age", nil, ""),
			isVal: true,
		},
		{
			name: "Other error",
			err:  errors.New("other"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.isSchema, IsSchemaError(tt.err))
			assert.Equal(t, tt.isConfig, IsConfigError(tt.err))
			assert.Equal(t, tt.isEdge, IsEdgeError(tt.err))
			assert.Equal(t, tt.isGen, IsGenerationError(tt.err))
			assert.Equal(t, tt.isVal, IsValidationError(tt.err))
		})
	}
}

func TestErrorsAs(t *testing.T) {
	t.Run("As SchemaError", func(t *testing.T) {
		err := NewSchemaError("User", "email", "invalid", nil)
		var schemaErr *SchemaError
		require.True(t, errors.As(err, &schemaErr))
		assert.Equal(t, "User", schemaErr.Type)
		assert.Equal(t, "email", schemaErr.Field)
	})

	t.Run("As ConfigError", func(t *testing.T) {
		err := NewConfigError("Package", "test", "invalid")
		var configErr *ConfigError
		require.True(t, errors.As(err, &configErr))
		assert.Equal(t, "Package", configErr.Option)
		assert.Equal(t, "test", configErr.Value)
	})

	t.Run("As EdgeError", func(t *testing.T) {
		err := NewEdgeError("User", "Post", "posts", "invalid", nil)
		var edgeErr *EdgeError
		require.True(t, errors.As(err, &edgeErr))
		assert.Equal(t, "User", edgeErr.From)
		assert.Equal(t, "Post", edgeErr.To)
		assert.Equal(t, "posts", edgeErr.Edge)
	})

	t.Run("As GenerationError", func(t *testing.T) {
		err := NewGenerationError("entity", "user.go", "failed", nil)
		var genErr *GenerationError
		require.True(t, errors.As(err, &genErr))
		assert.Equal(t, "entity", genErr.Phase)
		assert.Equal(t, "user.go", genErr.File)
	})

	t.Run("As ValidationError", func(t *testing.T) {
		err := NewValidationError("User", "age", -1, "invalid")
		var valErr *ValidationError
		require.True(t, errors.As(err, &valErr))
		assert.Equal(t, "User", valErr.Type)
		assert.Equal(t, "age", valErr.Field)
		assert.Equal(t, -1, valErr.Value)
	})
}
