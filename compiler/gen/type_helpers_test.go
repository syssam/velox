package gen

// Tests for the annotation and naming helpers in type_helpers.go.

import (
	"testing"

	"github.com/stretchr/testify/assert"

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

func TestSqlIndexAnnotate(t *testing.T) {
	// Nil → nil.
	assert.Nil(t, sqlIndexAnnotate(nil))

	// Missing key → nil.
	assert.Nil(t, sqlIndexAnnotate(map[string]any{"other": "val"}))
}

// =============================================================================
// fieldAnnotate — with valid annotation key
// =============================================================================

func TestFieldAnnotate_WithAnnotationKey(t *testing.T) {
	annotationName := (&field.Annotation{}).Name()
	ann := fieldAnnotate(map[string]any{
		annotationName: map[string]any{
			"OrderField": "EMAIL",
		},
	})
	_ = ann
}
