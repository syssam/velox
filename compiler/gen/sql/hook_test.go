package sql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
)

func TestGenHook_BasicEntities(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{
		createTestType("User"),
		createTestType("Post"),
	}

	file := genHook(helper)
	require.NotNil(t, file)

	code := file.GoString()

	// Per-entity typed func adapters
	assert.Contains(t, code, "type UserFunc func")
	assert.Contains(t, code, "type PostFunc func")
	assert.Contains(t, code, "func (f UserFunc) Mutate")
	assert.Contains(t, code, "func (f PostFunc) Mutate")

	// Condition type and combinators
	assert.Contains(t, code, "type Condition func")
	assert.Contains(t, code, "func And(")
	assert.Contains(t, code, "func Or(")
	assert.Contains(t, code, "func Not(")

	// Operation conditions
	assert.Contains(t, code, "func HasOp(")
	assert.Contains(t, code, "func HasFields(")
	assert.Contains(t, code, "func HasAddedFields(")
	assert.Contains(t, code, "func HasClearedFields(")

	// Hook utilities
	assert.Contains(t, code, "func If(")
	assert.Contains(t, code, "func On(")
	assert.Contains(t, code, "func Unless(")
	assert.Contains(t, code, "func FixedError(")
	assert.Contains(t, code, "func Reject(")

	// Chain type
	assert.Contains(t, code, "type Chain struct")
	assert.Contains(t, code, "func NewChain(")
	assert.Contains(t, code, "func (c Chain) Hook()")
	assert.Contains(t, code, "func (c Chain) Append(")
	assert.Contains(t, code, "func (c Chain) Extend(")
}

func TestGenHook_PackageName(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genHook(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "package hook")
}

func TestGenHook_NoEntities(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{}

	file := genHook(helper)
	require.NotNil(t, file)

	code := file.GoString()
	// Should still have the condition/hook utilities even with no entities
	assert.Contains(t, code, "type Condition func")
	assert.Contains(t, code, "func On(")

	// Should NOT have entity-specific func types
	assert.NotContains(t, code, "type UserFunc")
}
