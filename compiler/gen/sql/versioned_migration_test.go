package sql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
)

// =============================================================================
// genVersionedMigration Tests
// =============================================================================

func TestGenVersionedMigration_BasicEntity(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genVersionedMigration(helper)
	require.NotNil(t, file)

	code := file.GoString()
	// Core types
	assert.Contains(t, code, "Migration")
	assert.Contains(t, code, "MigrationDir")
	assert.Contains(t, code, "LocalDir")
	assert.Contains(t, code, "NewLocalDir")
	// MigrationRunner
	assert.Contains(t, code, "MigrationRunner")
	assert.Contains(t, code, "NewMigrationRunner")
}

func TestGenVersionedMigration_MigratorMethods(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genVersionedMigration(helper)
	require.NotNil(t, file)

	code := file.GoString()
	// Public methods
	assert.Contains(t, code, "Up")
	assert.Contains(t, code, "Status")
	assert.Contains(t, code, "WithTable")
	// Helper methods
	assert.Contains(t, code, "ensureTable")
	assert.Contains(t, code, "appliedMigrations")
	assert.Contains(t, code, "pendingMigrations")
	assert.Contains(t, code, "runMigration")
	assert.Contains(t, code, "versionFromFile")
}

func TestGenVersionedMigration_HelperTypes(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genVersionedMigration(helper)
	require.NotNil(t, file)

	code := file.GoString()
	// Migration struct fields
	assert.Contains(t, code, "Version")
	assert.Contains(t, code, "Name")
	assert.Contains(t, code, "SQL")
	assert.Contains(t, code, "Applied")
	// MigrationDir interface
	assert.Contains(t, code, "Files")
	assert.Contains(t, code, "ReadFile")
	// SQL operations
	assert.Contains(t, code, "CREATE TABLE IF NOT EXISTS")
	assert.Contains(t, code, "schema_migrations")
	assert.Contains(t, code, "BeginTx")
	assert.Contains(t, code, "Commit")
}
