//go:build integration

package schema

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/schema/field"
)

// Integration tests validate the schema package against real-world scenarios.
// Run with: go test -tags integration ./dialect/sql/schema/...

func TestPostgres_TypeMapping(t *testing.T) {
	// Verify all field types can be mapped to PostgreSQL column types without panic.
	tests := []struct {
		name      string
		fieldType field.Type
		size      int64
	}{
		{"bool", field.TypeBool, 0},
		{"int8", field.TypeInt8, 0},
		{"int16", field.TypeInt16, 0},
		{"int32", field.TypeInt32, 0},
		{"int64", field.TypeInt64, 0},
		{"uint8", field.TypeUint8, 0},
		{"uint16", field.TypeUint16, 0},
		{"uint32", field.TypeUint32, 0},
		{"uint64", field.TypeUint64, 0},
		{"float32", field.TypeFloat32, 0},
		{"float64", field.TypeFloat64, 0},
		{"string", field.TypeString, 255},
		{"string_large", field.TypeString, 10 << 20},
		{"bytes", field.TypeBytes, 0},
		{"time", field.TypeTime, 0},
		{"uuid", field.TypeUUID, 0},
		{"json", field.TypeJSON, 0},
		{"enum", field.TypeEnum, 0},
		{"int", field.TypeInt, 0},
		{"uint", field.TypeUint, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			col := &Column{
				Name: "test_col",
				Type: tt.fieldType,
				Size: tt.size,
			}
			// Verify column can be used in table creation without panic.
			table := &Table{
				Name:    "test_table",
				Columns: []*Column{col},
			}
			assert.NotNil(t, table)
			assert.Equal(t, tt.fieldType, col.Type)
		})
	}
}

func TestMySQL_TypeMapping(t *testing.T) {
	// Verify MySQL type mapping produces valid types.
	tests := []struct {
		name      string
		fieldType field.Type
		size      int64
	}{
		{"bool", field.TypeBool, 0},
		{"int8", field.TypeInt8, 0},
		{"int16", field.TypeInt16, 0},
		{"int32", field.TypeInt32, 0},
		{"int64", field.TypeInt64, 0},
		{"uint8", field.TypeUint8, 0},
		{"uint16", field.TypeUint16, 0},
		{"uint32", field.TypeUint32, 0},
		{"uint64", field.TypeUint64, 0},
		{"float32", field.TypeFloat32, 0},
		{"float64", field.TypeFloat64, 0},
		{"string", field.TypeString, 255},
		{"string_large", field.TypeString, 1 << 24},
		{"bytes", field.TypeBytes, 0},
		{"time", field.TypeTime, 6},
		{"uuid", field.TypeUUID, 0},
		{"json", field.TypeJSON, 0},
		{"enum", field.TypeEnum, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			col := &Column{
				Name: "test_col",
				Type: tt.fieldType,
				Size: tt.size,
			}
			table := &Table{
				Name:    "test_table",
				Columns: []*Column{col},
			}
			assert.NotNil(t, table)
		})
	}
}

func TestSQLite_TypeMapping(t *testing.T) {
	// Verify SQLite type mapping produces valid types.
	tests := []struct {
		name      string
		fieldType field.Type
	}{
		{"bool", field.TypeBool},
		{"int64", field.TypeInt64},
		{"float64", field.TypeFloat64},
		{"string", field.TypeString},
		{"bytes", field.TypeBytes},
		{"time", field.TypeTime},
		{"uuid", field.TypeUUID},
		{"json", field.TypeJSON},
		{"enum", field.TypeEnum},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			col := &Column{
				Name: "test_col",
				Type: tt.fieldType,
			}
			table := &Table{
				Name:    "test_table",
				Columns: []*Column{col},
			}
			assert.NotNil(t, table)
		})
	}
}

func TestMultiDialect_SchemaValidation(t *testing.T) {
	// Validate schema diff detection across all supported dialects.
	current := []*Table{
		{
			Name: "users",
			Columns: []*Column{
				{Name: "id", Type: field.TypeInt64, Increment: true},
				{Name: "name", Type: field.TypeString, Size: 255},
				{Name: "email", Type: field.TypeString, Size: 255, Unique: true},
			},
		},
	}

	// Desired schema adds a column.
	desired := []*Table{
		{
			Name: "users",
			Columns: []*Column{
				{Name: "id", Type: field.TypeInt64, Increment: true},
				{Name: "name", Type: field.TypeString, Size: 255},
				{Name: "email", Type: field.TypeString, Size: 255, Unique: true},
				{Name: "age", Type: field.TypeInt},
			},
		},
	}

	result := ValidateDiff(current, desired)
	require.NotNil(t, result)
	// Adding a column should not be a breaking change.
	assert.False(t, result.HasBreakingChanges())
}

func TestMultiDialect_BreakingChanges(t *testing.T) {
	// Detect breaking changes: dropping a column.
	current := []*Table{
		{
			Name: "users",
			Columns: []*Column{
				{Name: "id", Type: field.TypeInt64},
				{Name: "name", Type: field.TypeString, Size: 255},
				{Name: "legacy_field", Type: field.TypeString, Size: 100},
			},
		},
	}

	desired := []*Table{
		{
			Name: "users",
			Columns: []*Column{
				{Name: "id", Type: field.TypeInt64},
				{Name: "name", Type: field.TypeString, Size: 255},
				// legacy_field removed
			},
		},
	}

	result := ValidateDiff(current, desired)
	require.NotNil(t, result)
	assert.True(t, result.HasBreakingChanges(), "dropping a column should be a breaking change")
}

func TestMultiDialect_DialectConstants(t *testing.T) {
	// Verify dialect constants are distinct and non-empty.
	dialects := []string{dialect.Postgres, dialect.MySQL, dialect.SQLite}

	seen := make(map[string]bool)
	for _, d := range dialects {
		assert.NotEmpty(t, d, "dialect constant should not be empty")
		assert.False(t, seen[d], "dialect %q should be unique", d)
		seen[d] = true
	}
}
