package gen

// Tests for the annotation and naming helpers in type_helpers.go.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// type_helpers.go
// =============================================================================

func TestTitleCase(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"string", "String"},
		{"bool", "Bool"},
		{"int64", "Int64"},
		{"Float64", "Float64"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, titleCase(tt.in), "titleCase(%q)", tt.in)
	}
}

func TestValidateSQLAnnotation(t *testing.T) {
	err := validateSQLAnnotation(map[string]any{
		"sql": map[string]any{"ColumnType": "TEXT"},
	})
	assert.NoError(t, err)
	err2 := validateSQLAnnotation(map[string]any{
		"sql": map[string]any{"ColumnType": "DROP TABLE users"},
	})
	assert.Error(t, err2)
	err3 := validateSQLAnnotation(nil)
	assert.NoError(t, err3)
}

// TestAnnotationDecoders pins the three loaded-annotation decoders: each
// returns nil unless its own annotation name is present, and otherwise
// decodes the JSON-shaped map the loader produces into the typed struct.
func TestAnnotationDecoders(t *testing.T) {
	fieldName := field.Annotation{}.Name()
	indexName := sqlschema.IndexAnnotation{}.Name()

	t.Run("fieldAnnotate", func(t *testing.T) {
		assert.Nil(t, fieldAnnotate(nil))
		assert.Nil(t, fieldAnnotate(map[string]any{}))
		// A key that is not field.Annotation.Name() is ignored.
		assert.Nil(t, fieldAnnotate(map[string]any{"FieldAnnotation": map[string]any{"ID": []string{"a", "b"}}}))

		got := fieldAnnotate(map[string]any{
			fieldName: map[string]any{
				"StructTag": map[string]any{"name": `json:"n"`},
				"ID":        []any{"user_id", "tweet_id"},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, map[string]string{"name": `json:"n"`}, got.StructTag)
		assert.Equal(t, []string{"user_id", "tweet_id"}, got.ID)
	})

	t.Run("sqlIndexAnnotate", func(t *testing.T) {
		assert.Nil(t, sqlIndexAnnotate(nil))
		assert.Nil(t, sqlIndexAnnotate(map[string]any{"other": "val"}))
		// The struct name is not the annotation name.
		assert.Nil(t, sqlIndexAnnotate(map[string]any{"IndexAnnotation": map[string]any{"Type": "GIN"}}))

		got := sqlIndexAnnotate(map[string]any{
			indexName: map[string]any{"Type": "GIN", "Types": map[string]any{"postgres": "GIST"}},
		})
		require.NotNil(t, got)
		assert.Equal(t, "GIN", got.Type)
		assert.Equal(t, map[string]string{"postgres": "GIST"}, got.Types)
	})
}
