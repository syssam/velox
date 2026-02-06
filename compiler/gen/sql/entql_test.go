package sql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
)

func TestGenEntQL_BasicEntity(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genEntQL(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "SchemaConfig")
	assert.Contains(t, code, "FieldConfig")
	assert.Contains(t, code, "EdgeConfig")
}

func TestGenEntQL_RuntimeFilter(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genEntQL(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "RuntimeFilter")
	assert.Contains(t, code, "CompositeFilter")
}

func TestGenEntQL_TypeSchemas(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	helper.graph.Nodes = []*gen.Type{userType, postType}

	file := genEntQL(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "TypeSchemas")
	assert.Contains(t, code, "GetSchema")
}

func TestGenEntQL_ApplyFilter(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genEntQL(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "ApplyFilter")
}

func TestGenEntQL_Operators(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genEntQL(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "OpEQ")
	assert.Contains(t, code, "OpNEQ")
}

func TestGenEntQL_MultipleEntities(t *testing.T) {
	helper := newMockHelper()
	types := []*gen.Type{
		createTestType("User"),
		createTestType("Post"),
		createTestType("Comment"),
	}
	helper.graph.Nodes = types

	file := genEntQL(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "UserSchema")
	assert.Contains(t, code, "PostSchema")
	assert.Contains(t, code, "CommentSchema")
}

func TestGenEntQLSchemaDescriptor_SingleEntity(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	genEntQLSchemaDescriptor(helper, f, userType)

	code := f.GoString()
	assert.Contains(t, code, "UserSchema")
}

func TestGenEntQL_EntityWithEdges(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")

	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_posts"),
	}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	file := genEntQL(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "EdgeConfig")
}
