package sql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
)

// =============================================================================
// Dialect.GenFilter Tests
// =============================================================================

func TestDialect_GenFilter(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	d := NewDialect(helper)
	file, err := d.GenFilter(userType)
	require.NoError(t, err)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "UserFilter")
	assert.Contains(t, code, "WhereP")
}

// =============================================================================
// Dialect.GenMigrate Tests
// =============================================================================

func TestDialect_GenMigrate(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	d := NewDialect(helper)
	files, err := d.GenMigrate()
	require.NoError(t, err)
	assert.NotNil(t, files.Schema)
	assert.NotNil(t, files.Migrate)
}

// =============================================================================
// Dialect.GenEntQL Tests
// =============================================================================

func TestDialect_GenEntQL(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	d := NewDialect(helper)
	file, err := d.GenEntQL()
	require.NoError(t, err)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "EntitySchema")
	assert.Contains(t, code, "UserSchema")
}

// =============================================================================
// Dialect.SupportsFeature Tests
// =============================================================================

func TestDialect_SupportsFeature(t *testing.T) {
	helper := newMockHelper()
	d := NewDialect(helper)

	// "migrate" is handled by MigrateGenerator (GenMigrate), not FeatureGenerator.
	assert.False(t, d.SupportsFeature("migrate"))
	assert.True(t, d.SupportsFeature("upsert"))
	assert.True(t, d.SupportsFeature("lock"))
	assert.True(t, d.SupportsFeature("modifier"))
	assert.True(t, d.SupportsFeature("hook"))
	// intercept and privacy are handled by OptionalFeatureGenerator,
	// not by SupportsFeature/GenFeature.
	assert.False(t, d.SupportsFeature("intercept"))
	assert.False(t, d.SupportsFeature("privacy"))
	assert.False(t, d.SupportsFeature("nonexistent"))
}

// =============================================================================
// Dialect.GenFeature Tests
// =============================================================================

func TestDialect_GenFeature(t *testing.T) {
	helper := newMockHelper()
	d := NewDialect(helper)

	// GenFeature returns nil for unsupported features — intercept, privacy,
	// and migrate are handled by separate interfaces.
	f2, err := d.GenFeature("hook")
	assert.NoError(t, err)
	assert.NotNil(t, f2)

	f3, err := d.GenFeature("nonexistent")
	assert.NoError(t, err)
	assert.Nil(t, f3)
}

// =============================================================================
// Dialect.Name Tests
// =============================================================================

func TestDialect_Name(t *testing.T) {
	helper := newMockHelper()
	d := NewDialect(helper)
	assert.Equal(t, "sql", d.Name())
}

// =============================================================================
// Dialect Interface Compliance
// =============================================================================

func TestDialect_ImplementsDialectGenerator(t *testing.T) {
	helper := newMockHelper()
	d := NewDialect(helper)

	// Verify all interface methods exist via type assertion
	var _ gen.DialectGenerator = d
}
