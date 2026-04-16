package gen

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetDriver_SQL(t *testing.T) {
	d, err := GetDriver("sql")
	require.NoError(t, err)
	assert.Equal(t, "sql", d.Name)
	assert.Equal(t, "SQL", d.IdentName)
	assert.True(t, d.SchemaMode.Support(Unique))
	assert.True(t, d.SchemaMode.Support(Migrate))
}

func TestGetDriver_Unknown(t *testing.T) {
	_, err := GetDriver("nosuchdriver")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nosuchdriver")
}

func TestRegisterDriver_Custom(t *testing.T) {
	const name = "test-custom-driver"

	RegisterDriver(&Storage{
		Name:      name,
		IdentName: "TestCustom",
	})
	t.Cleanup(func() {
		driversMu.Lock()
		delete(drivers, name)
		driversMu.Unlock()
	})

	d, err := GetDriver(name)
	require.NoError(t, err)
	assert.Equal(t, name, d.Name)
	assert.Equal(t, "TestCustom", d.IdentName)
}

func TestListDrivers(t *testing.T) {
	names := ListDrivers()
	assert.Contains(t, names, "sql")
	// Must be sorted.
	for i := 1; i < len(names); i++ {
		assert.LessOrEqual(t, names[i-1], names[i], "ListDrivers should return sorted names")
	}
}
