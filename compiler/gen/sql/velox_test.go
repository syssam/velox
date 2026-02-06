package sql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
)

func TestGenVelox_BasicEntity(t *testing.T) {
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

func TestGenVelox_ErrorTypes(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genVelox(helper)
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

func TestGenVelox_ContextFunctions(t *testing.T) {
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

func TestGenVelox_SelectorStruct(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genVelox(helper)
	require.NotNil(t, file)

	code := file.GoString()
	// genVelox generates a selector struct for Select/GroupBy
	assert.Contains(t, code, "type selector struct")
}

func TestGenVelox_AggregateFunctions(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genVelox(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "AggregateFunc")
	assert.Contains(t, code, "As")
}

func TestGenVelox_OrderFunctions(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genVelox(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "Asc")
	assert.Contains(t, code, "Desc")
}

func TestGenVelox_GenericHelpers(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genVelox(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "withHooks")
	assert.Contains(t, code, "withInterceptors")
}

func TestGenVelox_MultipleEntities(t *testing.T) {
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

func TestGenVelox_GenericUtilities(t *testing.T) {
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
