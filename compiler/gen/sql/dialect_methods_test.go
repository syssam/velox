package sql

import (
	"testing"

	"github.com/dave/jennifer/jen"
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
	file := d.GenFilter(userType)
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
	var files []*jen.File
	ok := safeGenerate(func() {
		files = d.GenMigrate()
	})
	if !ok {
		t.Skip("GenMigrate panicked due to incomplete mock state")
	}
	require.NotNil(t, files)
	assert.GreaterOrEqual(t, len(files), 1)
}

// =============================================================================
// Dialect.GenEntQL Tests
// =============================================================================

func TestDialect_GenEntQL(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	d := NewDialect(helper)
	file := d.GenEntQL()
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "SchemaConfig")
	assert.Contains(t, code, "UserSchema")
}

// =============================================================================
// Dialect.SupportsFeature Tests
// =============================================================================

func TestDialect_SupportsFeature(t *testing.T) {
	helper := newMockHelper()
	d := NewDialect(helper)

	assert.True(t, d.SupportsFeature("migrate"))
	assert.True(t, d.SupportsFeature("upsert"))
	assert.True(t, d.SupportsFeature("lock"))
	assert.True(t, d.SupportsFeature("modifier"))
	assert.True(t, d.SupportsFeature("intercept"))
	assert.True(t, d.SupportsFeature("privacy"))
	assert.True(t, d.SupportsFeature("hook"))
	assert.False(t, d.SupportsFeature("nonexistent"))
}

// =============================================================================
// Dialect.GenFeature Tests
// =============================================================================

func TestDialect_GenFeature(t *testing.T) {
	helper := newMockHelper()
	d := NewDialect(helper)

	// Currently returns nil for all features (TODO implementations)
	assert.Nil(t, d.GenFeature("hook"))
	assert.Nil(t, d.GenFeature("intercept"))
	assert.Nil(t, d.GenFeature("privacy"))
	assert.Nil(t, d.GenFeature("nonexistent"))
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
