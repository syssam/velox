package sql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
)

func TestGenGlobalID_BasicEntity(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genGlobalID(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "GlobalID")
	assert.Contains(t, code, "NewGlobalID")
}

func TestGenGlobalID_DecodeMethods(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genGlobalID(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "Decode")
	assert.Contains(t, code, "String")
}

func TestGenGlobalID_IDParsers(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genGlobalID(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "IntID")
	assert.Contains(t, code, "Int64ID")
}

func TestGenGlobalID_TypeMap(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	helper.graph.Nodes = []*gen.Type{userType, postType}

	file := genGlobalID(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "TypeMap")
	assert.Contains(t, code, "TypeNames")
}

func TestGenGlobalID_ParseGlobalID(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genGlobalID(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "ParseGlobalID")
}

func TestGenGlobalID_MultipleEntities(t *testing.T) {
	helper := newMockHelper()
	types := []*gen.Type{
		createTestType("User"),
		createTestType("Post"),
		createTestType("Comment"),
		createTestType("Tag"),
	}
	helper.graph.Nodes = types

	file := genGlobalID(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "TypeMap")
	for _, typ := range types {
		assert.Contains(t, code, typ.Name, "missing entity %s in generated global ID code", typ.Name)
	}
}

func TestGenGlobalID_WithIncrementStarts(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}
	helper.graph.Annotations = map[string]any{
		"IncrementStarts": map[string]int{"User": 1000, "Post": 2000},
	}

	file := genGlobalID(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "IncrementStarts")
	assert.Contains(t, code, "1000")
}

func TestGenGlobalID_NoAnnotations(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}
	// No annotations set — should default to "{}"

	file := genGlobalID(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "IncrementStarts")
	assert.Contains(t, code, `"{}"`)
}
