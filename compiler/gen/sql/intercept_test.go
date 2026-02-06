package sql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
)

func TestGenIntercept_BasicEntity(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genIntercept(helper)
	require.NotNil(t, file)

	code := file.GoString()
	// Query interface
	assert.Contains(t, code, "type Query interface")
	// Func adapter
	assert.Contains(t, code, "Func")
	// TraverseFunc adapter
	assert.Contains(t, code, "TraverseFunc")
}

func TestGenIntercept_EntityTypes(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	helper.graph.Nodes = []*gen.Type{userType, postType}

	file := genIntercept(helper)
	require.NotNil(t, file)

	code := file.GoString()
	// Per-entity types
	assert.Contains(t, code, "UserFunc")
	assert.Contains(t, code, "PostFunc")
}

func TestGenIntercept_NewQuery(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genIntercept(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "NewQuery")
}

func TestGenIntercept_MultipleEntities(t *testing.T) {
	helper := newMockHelper()
	types := []*gen.Type{
		createTestType("User"),
		createTestType("Post"),
		createTestType("Comment"),
	}
	helper.graph.Nodes = types

	file := genIntercept(helper)
	require.NotNil(t, file)

	code := file.GoString()
	for _, typ := range types {
		assert.Contains(t, code, typ.Name+"Func", "missing %sFunc", typ.Name)
	}
}

func TestGenInterceptEntityTypes_SingleEntity(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("intercept")
	genInterceptEntityTypes(helper, f, userType, "github.com/test/project/ent", "github.com/syssam/velox")

	code := f.GoString()
	assert.Contains(t, code, "UserFunc")
}
