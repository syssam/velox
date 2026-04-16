package sql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// genSnapshot Tests
// =============================================================================

func TestGenSnapshot_BasicEntity(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genSnapshot(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "Snapshot")
	assert.Contains(t, code, "Node")
	assert.Contains(t, code, "FieldSnapshot")
	assert.Contains(t, code, "EdgeSnapshot")
	assert.Contains(t, code, "CurrentSnapshot")
	assert.Contains(t, code, "GetSnapshot")
}

func TestGenSnapshot_MultipleEntities(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	helper.graph.Nodes = []*gen.Type{userType, postType}

	file := genSnapshot(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "User")
	assert.Contains(t, code, "Post")
}

// =============================================================================
// buildSnapshotData Tests
// =============================================================================

func TestBuildSnapshotData_EmptyGraph(t *testing.T) {
	graph := &gen.Graph{
		Config: &gen.Config{Package: "github.com/test/project/ent"},
	}
	data := buildSnapshotData(graph)
	assert.Empty(t, data.Nodes)
}

func TestBuildSnapshotData_WithFields(t *testing.T) {
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
		createTestField("age", field.TypeInt),
		createNillableField("bio", field.TypeString),
		createOptionalField("nickname", field.TypeString),
		createImmutableField("username", field.TypeString),
	})

	graph := &gen.Graph{
		Config: &gen.Config{Package: "github.com/test/project/ent"},
		Nodes:  []*gen.Type{userType},
	}
	data := buildSnapshotData(graph)
	require.Len(t, data.Nodes, 1)
	assert.Equal(t, "User", data.Nodes[0].Name)
	assert.Len(t, data.Nodes[0].Fields, 5)
}

func TestBuildSnapshotData_WithEdges(t *testing.T) {
	userType := createTestType("User")
	postType := createTestType("Post")
	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
	}

	graph := &gen.Graph{
		Config: &gen.Config{Package: "github.com/test/project/ent"},
		Nodes:  []*gen.Type{userType, postType},
	}
	data := buildSnapshotData(graph)
	require.Len(t, data.Nodes, 2)
	assert.Len(t, data.Nodes[0].Edges, 1)
	assert.Equal(t, "posts", data.Nodes[0].Edges[0].Name)
}

func TestBuildSnapshotData_NilFieldType(t *testing.T) {
	userType := createTestTypeWithFields("User", []*gen.Field{
		{Name: "unknown"},
	})

	graph := &gen.Graph{
		Config: &gen.Config{Package: "github.com/test/project/ent"},
		Nodes:  []*gen.Type{userType},
	}
	data := buildSnapshotData(graph)
	require.Len(t, data.Nodes, 1)
	assert.Equal(t, "unknown", data.Nodes[0].Fields[0].Type)
}
