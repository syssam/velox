package sql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
)

// =============================================================================
// genDelete Tests
// =============================================================================

func TestGenDelete_BasicEntity(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genDelete(helper, userType)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "UserDelete")
	assert.Contains(t, code, "UserDeleteOne")
	assert.Contains(t, code, "Where")
	assert.Contains(t, code, "Exec")
	assert.Contains(t, code, "ExecX")
}

// =============================================================================
// genDeleteBuilder Tests
// =============================================================================

func TestGenDeleteBuilder(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")

	f := helper.NewFile("ent")
	genDeleteBuilder(helper, f, userType)

	code := f.GoString()
	assert.Contains(t, code, "UserDelete")
	assert.Contains(t, code, "mutation")
	assert.Contains(t, code, "hooks")
	assert.Contains(t, code, "Where")
	assert.Contains(t, code, "Exec")
	assert.Contains(t, code, "ExecX")
	assert.Contains(t, code, "sqlExec")
}

// =============================================================================
// genDeleteOneBuilder Tests
// =============================================================================

func TestGenDeleteOneBuilder(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")

	f := helper.NewFile("ent")
	genDeleteOneBuilder(helper, f, userType)

	code := f.GoString()
	assert.Contains(t, code, "UserDeleteOne")
	assert.Contains(t, code, "Where")
	assert.Contains(t, code, "Exec")
	assert.Contains(t, code, "ExecX")
	assert.Contains(t, code, "NotFoundError")
}

// =============================================================================
// genDeleteSQLExec Tests
// =============================================================================

func TestGenDeleteSqlExec(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")

	f := helper.NewFile("ent")
	genDeleteSQLExec(helper, f, userType, "UserDelete")

	code := f.GoString()
	assert.Contains(t, code, "sqlExec")
	assert.Contains(t, code, "NewDeleteSpec")
	assert.Contains(t, code, "Predicate")
}

func TestGenDeleteSqlExec_NoID(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	userType.ID = nil

	f := helper.NewFile("ent")
	genDeleteSQLExec(helper, f, userType, "UserDelete")

	code := f.GoString()
	assert.Contains(t, code, "sqlExec")
	assert.Contains(t, code, "NewDeleteSpec")
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkGenDelete(b *testing.B) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	for b.Loop() {
		genDelete(helper, userType)
	}
}
