package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
