package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterTypeInfo_and_FindRegisteredType(t *testing.T) {
	info := &RegisteredTypeInfo{
		Table:    "test_table",
		Columns:  []string{"id", "name"},
		IDColumn: "id",
	}

	RegisterTypeInfo("test_reg_table", info)
	defer func() {
		typeInfoMu.Lock()
		delete(registeredTypes, "test_reg_table")
		typeInfoMu.Unlock()
	}()

	t.Run("found", func(t *testing.T) {
		got := FindRegisteredType("test_reg_table")
		require.NotNil(t, got)
		assert.Equal(t, "test_table", got.Table)
	})

	t.Run("not_registered", func(t *testing.T) {
		got := FindRegisteredType("no_such_table")
		assert.Nil(t, got)
	})
}

func TestRegisterColumns_And_ValidColumn(t *testing.T) {
	// Clean up after test
	columnMu.Lock()
	saved := make(map[string]func(string) bool, len(columnRegistry))
	for k, v := range columnRegistry {
		saved[k] = v
	}
	columnRegistry = map[string]func(string) bool{}
	columnMu.Unlock()
	defer func() {
		columnMu.Lock()
		columnRegistry = saved
		columnMu.Unlock()
	}()

	// Register a table with known columns
	RegisterColumns("users", func(col string) bool {
		return col == "id" || col == "name" || col == "email"
	})

	// Valid column
	err := ValidColumn("users", "name")
	assert.NoError(t, err)

	// Invalid column
	err = ValidColumn("users", "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent")

	// Unknown table
	err = ValidColumn("unknown_table", "id")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown_table")
}

func TestFindRegisteredType_Lookup(t *testing.T) {
	meta := testTypeInfo()
	RegisterTypeInfo("test_must_table", &RegisteredTypeInfo{
		Table:      meta.Table,
		Columns:    meta.Columns,
		IDColumn:   meta.IDColumn,
		ScanValues: meta.ScanValues,
		New:        func() any { return meta.New() },
		Assign:     func(e any, cols []string, vals []any) error { return meta.Assign(e.(*testEntity), cols, vals) },
		GetID:      func(e any) any { return meta.GetID(e.(*testEntity)) },
	})
	defer func() {
		typeInfoMu.Lock()
		delete(registeredTypes, "test_must_table")
		typeInfoMu.Unlock()
	}()

	t.Run("found", func(t *testing.T) {
		info := FindRegisteredType("test_must_table")
		require.NotNil(t, info)
		assert.Equal(t, "users", info.Table)
	})

	t.Run("not_found", func(t *testing.T) {
		info := FindRegisteredType("missing_table")
		assert.Nil(t, info)
	})
}
