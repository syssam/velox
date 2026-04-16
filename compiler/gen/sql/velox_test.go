package sql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
)

func TestGenVelox_BasicEntity(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genVelox(helper)
	require.NotNil(t, file)

	code := file.GoString()
	// genVelox uses type aliases from velox package
	assert.Contains(t, code, "Op")
	assert.Contains(t, code, "OpCreate")
	assert.Contains(t, code, "OpUpdate")
	assert.Contains(t, code, "OpUpdateOne")
	assert.Contains(t, code, "OpDelete")
	assert.Contains(t, code, "OpDeleteOne")
	assert.Contains(t, code, "Hook")
	assert.Contains(t, code, "Mutator")
	assert.Contains(t, code, "Querier")
	assert.Contains(t, code, "MutateFunc")
	assert.Contains(t, code, "Interceptor")
}

func TestGenErrors_ErrorTypes(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genErrors(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "NotFoundError")
	assert.Contains(t, code, "NotSingularError")
	assert.Contains(t, code, "NotLoadedError")
	assert.Contains(t, code, "ConstraintError")
	assert.Contains(t, code, "ValidationError")
	assert.Contains(t, code, "IsNotFound")
	assert.Contains(t, code, "IsNotSingular")
	assert.Contains(t, code, "IsConstraintError")
	assert.Contains(t, code, "IsValidationError")
}

func TestGenVelox_NoErrorTypes(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}
	file := genVelox(helper)
	code := file.GoString()
	assert.NotContains(t, code, "IsNotFound")
	assert.NotContains(t, code, "MaskNotFound")
	assert.NotContains(t, code, "IsConstraintError")
}

func TestGenVelox_ContextFunctions(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genVelox(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "NewContext")
	assert.Contains(t, code, "FromContext")
	assert.Contains(t, code, "NewTxContext")
	assert.Contains(t, code, "TxFromContext")
}

func TestGenVelox_AggregateFunctions(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genVelox(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "AggregateFunc")
	assert.Contains(t, code, "As")
}

func TestGenVelox_OrderFunctions(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genVelox(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "Asc")
	assert.Contains(t, code, "Desc")
}

func TestGenVelox_NoHookHelpers(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genVelox(helper)
	require.NotNil(t, file)

	code := file.GoString()
	// Hook helpers are handled by runtime, not generated in velox.go
	assert.NotContains(t, code, "withHooks")
	assert.NotContains(t, code, "querierAll")
	assert.NotContains(t, code, "querierCount")
	assert.NotContains(t, code, "setContextOp")
	assert.NotContains(t, code, "withInterceptors")
	assert.NotContains(t, code, "scanWithInterceptors")
	assert.NotContains(t, code, "queryHook")
	// User-facing utilities always present
	assert.Contains(t, code, "Ptr")
	assert.Contains(t, code, "Deref")
	assert.Contains(t, code, "Filter")
}

func TestGenVelox_MultipleEntities(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{
		createTestType("User"),
		createTestType("Post"),
		createTestType("Comment"),
	}

	file := genVelox(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "TypeUser")
	assert.Contains(t, code, "TypePost")
	assert.Contains(t, code, "TypeComment")
}

func TestGenVelox_CheckColumnUsesRuntimeRegistry(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{
		createTestType("User"),
		createTestType("Post"),
	}

	file := genVelox(helper)
	require.NotNil(t, file)

	code := file.GoString()
	// Should use runtime.ValidColumn instead of static entity imports
	assert.Contains(t, code, "runtime.ValidColumn")
	// Should NOT contain entity sub-package references for column checking
	assert.NotContains(t, code, "user.Table")
	assert.NotContains(t, code, "user.ValidColumn")
	assert.NotContains(t, code, "post.Table")
	assert.NotContains(t, code, "post.ValidColumn")
	// Should NOT contain sync.Once / static column check map
	assert.NotContains(t, code, "initCheck")
	assert.NotContains(t, code, "NewColumnCheck")
}

func TestGenVelox_GenericUtilities(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genVelox(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "Ptr")
	assert.Contains(t, code, "Deref")
	assert.Contains(t, code, "DerefOr")
	assert.Contains(t, code, "Map")
	assert.Contains(t, code, "Filter")
}
