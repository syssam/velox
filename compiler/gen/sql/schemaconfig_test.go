package sql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
)

func TestGenSchemaConfig_BasicEntity(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genSchemaConfig(helper, helper.graph)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "SchemaConfig")
	assert.Contains(t, code, "User")
}

func TestGenSchemaConfig_ContextFunctions(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genSchemaConfig(helper, helper.graph)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "SchemaConfigFromContext")
	assert.Contains(t, code, "NewSchemaConfigContext")
}

func TestGenSchemaConfig_MultipleEntities(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	commentType := createTestType("Comment")
	helper.graph.Nodes = []*gen.Type{userType, postType, commentType}

	file := genSchemaConfig(helper, helper.graph)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "SchemaConfig")
	assert.Contains(t, code, "User")
	assert.Contains(t, code, "Post")
	assert.Contains(t, code, "Comment")
}

func TestGenSchemaConfig_WithM2MEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	groupType := createTestType("Group")

	// M2M edge: User has many Groups
	userType.Edges = []*gen.Edge{
		createM2MEdge("groups", groupType, "user_groups", []string{"user_id", "group_id"}),
	}
	helper.graph.Nodes = []*gen.Type{userType, groupType}

	file := genSchemaConfig(helper, helper.graph)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "SchemaConfig")
}

func TestGenSchemaConfig_CtxKeyType(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genSchemaConfig(helper, helper.graph)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "schemaCtxKey")
}
