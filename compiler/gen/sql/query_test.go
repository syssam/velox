package sql

import (
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// genQueryPkg Tests
// =============================================================================

func TestGenQueryPkg_BasicStructure(t *testing.T) {
	t.Parallel()
	h := newFeatureMockHelper()
	userType := createTestType("User")
	h.graph.Nodes = []*gen.Type{userType}

	file := genQueryPkg(h, userType, h.graph.Nodes, "github.com/test/project/ent/entity")
	require.NotNil(t, file)
	code := file.GoString()

	assert.Contains(t, code, "type UserQuery struct")
	assert.Contains(t, code, "func (q *UserQuery) Where(")
	assert.Contains(t, code, "func (q *UserQuery) Limit(")
	assert.Contains(t, code, "func (q *UserQuery) Offset(")
	assert.Contains(t, code, "func (q *UserQuery) All(")
	assert.Contains(t, code, "func (q *UserQuery) First(")
	assert.Contains(t, code, "func (q *UserQuery) Only(")
	assert.Contains(t, code, "func (q *UserQuery) Count(")
	assert.Contains(t, code, "func (q *UserQuery) Exist(")
}

func TestGenQueryPkg_WithEdges(t *testing.T) {
	t.Parallel()
	h := newFeatureMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	postsEdge := createO2MEdge("posts", postType, "posts", "user_id")
	userType.Edges = []*gen.Edge{postsEdge}
	h.graph.Nodes = []*gen.Type{userType, postType}

	file := genQueryPkg(h, userType, h.graph.Nodes, "github.com/test/project/ent/entity")
	require.NotNil(t, file)
	code := file.GoString()

	assert.Contains(t, code, "WithPosts")
	assert.Contains(t, code, "withPosts")
	assert.Contains(t, code, "loadPosts")
}

func TestGenQueryPkg_WithM2OEdge(t *testing.T) {
	t.Parallel()
	h := newFeatureMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	authorEdge := createM2OEdge("author", userType, "posts", "user_id")
	postType.Edges = []*gen.Edge{authorEdge}
	h.graph.Nodes = []*gen.Type{userType, postType}

	file := genQueryPkg(h, postType, h.graph.Nodes, "github.com/test/project/ent/entity")
	require.NotNil(t, file)
	code := file.GoString()

	assert.Contains(t, code, "WithAuthor")
	assert.Contains(t, code, "loadAuthor")
}

func TestGenQueryPkg_WithM2MEdge(t *testing.T) {
	t.Parallel()
	h := newFeatureMockHelper()
	userType := createTestType("User")
	tagType := createTestType("Tag")
	tagsEdge := createM2MEdge("tags", tagType, "user_tags", []string{"user_id", "tag_id"})
	userType.Edges = []*gen.Edge{tagsEdge}
	h.graph.Nodes = []*gen.Type{userType, tagType}

	file := genQueryPkg(h, userType, h.graph.Nodes, "github.com/test/project/ent/entity")
	require.NotNil(t, file)
	code := file.GoString()

	assert.Contains(t, code, "WithTags")
	assert.Contains(t, code, "loadTags")
}

func TestGenQueryPkg_ValidGo(t *testing.T) {
	t.Parallel()
	h := newFeatureMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
		createTestField("age", field.TypeInt),
	})
	postType := createTestType("Post")
	postsEdge := createO2MEdge("posts", postType, "posts", "user_id")
	userType.Edges = []*gen.Edge{postsEdge}
	h.graph.Nodes = []*gen.Type{userType, postType}

	file := genQueryPkg(h, userType, h.graph.Nodes, "github.com/test/project/ent/entity")
	assertValidGo(t, file, "query_pkg_user")
}

func TestGenQueryPkg_CloneMethod(t *testing.T) {
	t.Parallel()
	h := newFeatureMockHelper()
	userType := createTestType("User")
	h.graph.Nodes = []*gen.Type{userType}

	file := genQueryPkg(h, userType, h.graph.Nodes, "github.com/test/project/ent/entity")
	code := file.GoString()

	// Clone is used for internal copy before First/Only
	assert.Contains(t, code, "func (q *UserQuery) clone()")
}

func TestGenQueryPkg_InterfaceCompliance(t *testing.T) {
	t.Parallel()
	h := newFeatureMockHelper()
	userType := createTestType("User")
	h.graph.Nodes = []*gen.Type{userType}

	file := genQueryPkg(h, userType, h.graph.Nodes, "github.com/test/project/ent/entity")
	code := file.GoString()

	// Should verify interface compliance at compile time
	assert.Contains(t, code, "UserQuerier")
}

func TestGenQueryPkg_InterfaceReturnTypes(t *testing.T) {
	t.Parallel()
	h := newFeatureMockHelper()
	userType := createTestType("User")
	h.graph.Nodes = []*gen.Type{userType}

	file := genQueryPkg(h, userType, h.graph.Nodes, "github.com/test/project/ent/entity")
	code := file.GoString()

	// Select returns entity.UserSelector
	assert.Contains(t, code, "func (q *UserQuery) Select(fields ...string) entity.UserSelector")
	// Modify returns entity.UserQuerier
	assert.Contains(t, code, "func (q *UserQuery) Modify(modifiers ...func(*sql.Selector)) entity.UserQuerier")
	// GroupBy returns entity.UserGroupByer
	assert.Contains(t, code, "func (q *UserQuery) GroupBy(field string, fields ...string) entity.UserGroupByer")
	// Aggregate on query returns entity.UserSelector
	assert.Contains(t, code, "func (q *UserQuery) Aggregate(fns ...runtime.AggregateFunc) entity.UserSelector")
	// Aggregate on Select returns entity.UserSelector
	assert.Contains(t, code, "func (s *UserSelect) Aggregate(fns ...runtime.AggregateFunc) entity.UserSelector")
	// Aggregate on GroupBy returns entity.UserGroupByer
	assert.Contains(t, code, "func (g *UserGroupBy) Aggregate(fns ...runtime.AggregateFunc) entity.UserGroupByer")
}

// =============================================================================
// SQL() debug method
// =============================================================================

func TestGenQueryPkg_SQLMethod(t *testing.T) {
	t.Parallel()
	h := newFeatureMockHelper()
	userType := createTestType("User")
	h.graph.Nodes = []*gen.Type{userType}

	file := genQueryPkg(h, userType, h.graph.Nodes, "github.com/test/project/ent/entity")
	require.NotNil(t, file)
	code := file.GoString()

	assert.Contains(t, code, "func (q *UserQuery) SQL(",
		"SQL method should be generated on per-entity query type")
	assert.Contains(t, code, "buildSelector",
		"SQL should call buildSelector")
	assert.Contains(t, code, "selector.Query()",
		"SQL should call selector.Query() to get SQL string and args")
}

// =============================================================================
// Shared query helpers
// =============================================================================

func TestGenQueryPkg_ValidGoComplexSchema(t *testing.T) {
	t.Parallel()
	h := newFeatureMockHelper()

	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
		createTestField("email", field.TypeString),
	})
	postType := createTestType("Post")
	groupType := createTestType("Group")

	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
		createM2MEdge("groups", groupType, "user_groups", []string{"user_id", "group_id"}),
	}
	h.graph.Nodes = []*gen.Type{userType, postType, groupType}

	file := genQueryPkg(h, userType, h.graph.Nodes, "github.com/test/project/ent/entity")
	code := file.GoString()

	fset := token.NewFileSet()
	_, err := parser.ParseFile(fset, "user_query.go", code, parser.AllErrors)
	assert.NoError(t, err, "complex query code should be valid Go")
}

func TestGenQueryHelpers(t *testing.T) {
	t.Parallel()
	h := newFeatureMockHelper()
	h.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genQueryHelpers(h)
	require.NotNil(t, file)
	assertValidGo(t, file, "query_helpers")
}

func TestEdgeCallbackField(t *testing.T) {
	edge := &gen.Edge{Name: "posts", Type: createTestType("Post")}
	assert.Equal(t, "withPosts", edgeCallbackField(edge))
}
