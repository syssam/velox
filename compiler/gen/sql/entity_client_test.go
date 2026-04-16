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
// genEntityClient — Entity Sub-Package Client Tests
// =============================================================================

// TestGenEntityClient_BasicStructAndConstructor verifies the entity client
// struct and constructor are generated correctly.
func TestGenEntityClient_BasicStructAndConstructor(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genEntityClient(helper, userType)
	require.NotNil(t, file)

	code := file.GoString()

	// Client struct
	assert.Contains(t, code, "type UserClient struct")
	assert.Contains(t, code, "runtime.Config")

	// Constructor
	assert.Contains(t, code, "func NewUserClient(")
	assert.Contains(t, code, "runtime.Config")
}

// TestGenEntityClient_CRUDMethods verifies that Create, Update, Delete,
// and related methods ARE generated on the entity client in entity-package mode.
func TestGenEntityClient_CRUDMethods(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genEntityClient(helper, userType)
	code := file.GoString()

	// Create methods
	assert.Contains(t, code, "func (c *UserClient) Create()")
	assert.Contains(t, code, "func (c *UserClient) CreateBulk(")
	assert.Contains(t, code, "func (c *UserClient) MapCreateBulk(")

	// Update methods
	assert.Contains(t, code, "func (c *UserClient) Update()")
	assert.Contains(t, code, "func (c *UserClient) UpdateOneID(")
	assert.Contains(t, code, "func (c *UserClient) UpdateOne(")

	// Delete methods
	assert.Contains(t, code, "func (c *UserClient) Delete()")
	assert.Contains(t, code, "func (c *UserClient) DeleteOneID(")
	assert.Contains(t, code, "func (c *UserClient) DeleteOne(")
}

// TestGenEntityClient_QueryMethod verifies that Query() is generated using
// runtime.NewEntityQuery (not direct query/ import).
func TestGenEntityClient_QueryMethod(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genEntityClient(helper, userType)
	code := file.GoString()

	// Query method should exist
	assert.Contains(t, code, "func (c *UserClient) Query()")
	// Uses runtime.NewEntityQuery
	assert.Contains(t, code, "runtime.NewEntityQuery")
	// Returns entity.UserQuerier
	assert.Contains(t, code, "UserQuerier")
	// Does NOT import query/ package directly
	assert.NotContains(t, code, "query.NewUserQuery")
}

// TestGenEntityClient_GetMethods verifies Get/GetX are generated on the entity client.
func TestGenEntityClient_GetMethods(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	postType := createTestType("Post")
	helper.graph.Nodes = []*gen.Type{postType}

	file := genEntityClient(helper, postType)
	code := file.GoString()

	assert.Contains(t, code, "func (c *PostClient) Get(")
	assert.Contains(t, code, "func (c *PostClient) GetX(")
	// Get calls Query internally
	assert.Contains(t, code, "c.Query()")
}

// TestGenEntityClient_QueryEdgeMethods verifies QueryXxx edge traversal
// methods are generated using runtime.NewEntityQuery.
func TestGenEntityClient_QueryEdgeMethods(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createO2MEdge("posts", postType, "posts", "user_id")
	userType.Edges = []*gen.Edge{edge}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	file := genEntityClient(helper, userType)
	code := file.GoString()

	assert.Contains(t, code, "func (c *UserClient) QueryPosts(")
	// Uses runtime.NewEntityQuery for target
	assert.Contains(t, code, "runtime.NewEntityQuery")
	// Uses SetPath via type assertion
	assert.Contains(t, code, "SetPath")
}

// TestGenEntityClient_MutateMethod verifies mutate is generated in entity sub-package mode.
func TestGenEntityClient_MutateMethod(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genEntityClient(helper, userType)
	code := file.GoString()

	assert.Contains(t, code, "func (c *UserClient) mutate(")
	assert.Contains(t, code, "runtime.OpCreate")
	assert.Contains(t, code, "runtime.OpUpdate")
	assert.Contains(t, code, "runtime.OpUpdateOne")
	assert.Contains(t, code, "runtime.OpDelete")
	assert.Contains(t, code, "runtime.OpDeleteOne")
}

// TestGenEntityClient_CreateUsesLocalConstructors verifies Create methods use
// local constructors (same package, no cross-package import).
func TestGenEntityClient_CreateUsesLocalConstructors(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genEntityClient(helper, userType)
	code := file.GoString()

	// Local constructor calls (no package qualification)
	assert.Contains(t, code, "NewUserMutation(")
	assert.Contains(t, code, "NewUserCreate(")
	assert.Contains(t, code, "NewUserCreateBulk(")
	assert.Contains(t, code, "NewUserUpdate(")
	assert.Contains(t, code, "NewUserDelete(")
}

// TestGenEntityClient_MapCreateBulk verifies MapCreateBulk generates reflect-based
// slice iteration with error handling for non-slice arguments.
func TestGenEntityClient_MapCreateBulk(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genEntityClient(helper, userType)
	code := file.GoString()

	// Method signature
	assert.Contains(t, code, "func (c *UserClient) MapCreateBulk(slice")
	assert.Contains(t, code, "setFunc func(*UserCreate, int)")
	assert.Contains(t, code, "*UserCreateBulk")

	// Reflect-based slice validation
	assert.Contains(t, code, "reflect.ValueOf(slice)")
	assert.Contains(t, code, "reflect.Slice")

	// Error path returns struct with err field
	assert.Contains(t, code, "MapCreateBulk with wrong type")

	// Success path uses constructor and calls c.Create()
	assert.Contains(t, code, "c.Create()")
	assert.Contains(t, code, "setFunc(builders[i], i)")
	assert.Contains(t, code, "NewUserCreateBulk(")
}

// TestGenEntityClient_DeleteOneIDSetsOp verifies DeleteOneID sets the op to OpDeleteOne.
func TestGenEntityClient_DeleteOneIDSetsOp(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	sessionType := createTestType("Session")
	helper.graph.Nodes = []*gen.Type{sessionType}

	file := genEntityClient(helper, sessionType)
	code := file.GoString()

	assert.Contains(t, code, "runtime.OpDeleteOne")
}

// TestGenEntityClient_ValidGoOutput verifies the generated client is valid Go.
func TestGenEntityClient_ValidGoOutput(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
		createTestField("email", field.TypeString),
	})
	helper.graph.Nodes = []*gen.Type{userType}

	file := genEntityClient(helper, userType)
	code := file.GoString()

	fset := token.NewFileSet()
	_, err := parser.ParseFile(fset, "user_client.go", code, parser.AllErrors)
	assert.NoError(t, err, "entity client code should be valid Go")
}

// TestGenEntityClient_ValidGoWithEdges verifies the generated client with edges is valid Go.
func TestGenEntityClient_ValidGoWithEdges(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
	})
	postType := createTestType("Post")
	edge := createO2MEdge("posts", postType, "posts", "user_id")
	userType.Edges = []*gen.Edge{edge}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	file := genEntityClient(helper, userType)
	code := file.GoString()

	fset := token.NewFileSet()
	_, err := parser.ParseFile(fset, "user_client.go", code, parser.AllErrors)
	assert.NoError(t, err, "entity client code with edges should be valid Go")
}

// TestGenEntityClient_AlwaysGeneratesEntityMode verifies genEntityClient generates
// full CRUD + Query methods for entity sub-package mode.
func TestGenEntityClient_AlwaysGeneratesEntityMode(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genEntityClient(helper, userType)
	require.NotNil(t, file)
	code := file.GoString()

	// Entity mode always has CRUD methods via registry
	assert.Contains(t, code, "runtime.NewEntityQuery")
	assert.Contains(t, code, "func (c *UserClient) Get(")
	assert.Contains(t, code, "func (c *UserClient) GetX(")
	assert.Contains(t, code, "func (c *UserClient) Query()")
}

// TestGenEntityClient_M2MEdgeQuery verifies edge query for M2M edges.
func TestGenEntityClient_M2MEdgeQuery(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	postType := createTestType("Post")
	tagType := createTestType("Tag")
	edge := createM2MEdge("tags", tagType, "post_tags", []string{"post_id", "tag_id"})
	postType.Edges = []*gen.Edge{edge}
	helper.graph.Nodes = []*gen.Type{postType, tagType}

	file := genEntityClient(helper, postType)
	code := file.GoString()

	assert.Contains(t, code, "func (c *PostClient) QueryTags(")
	assert.Contains(t, code, "TagQuerier")
}

// TestGenEntityClient_M2OEdgeQuery verifies edge query for M2O edges.
func TestGenEntityClient_M2OEdgeQuery(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	postType := createTestType("Post")
	userType := createTestType("User")
	edge := createM2OEdge("author", userType, "posts", "user_id")
	postType.Edges = []*gen.Edge{edge}
	helper.graph.Nodes = []*gen.Type{postType, userType}

	file := genEntityClient(helper, postType)
	code := file.GoString()

	assert.Contains(t, code, "func (c *PostClient) QueryAuthor(")
	assert.Contains(t, code, "UserQuerier")
}

// TestGenEntityClient_HooksAndInterceptors verifies Use/Intercept/Hooks/Interceptors
// are always generated regardless of mode.
func TestGenEntityClient_HooksAndInterceptors(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genEntityClient(helper, userType)
	code := file.GoString()

	assert.Contains(t, code, "func (c *UserClient) Use(")
	assert.Contains(t, code, "func (c *UserClient) Intercept(")
	assert.Contains(t, code, "func (c *UserClient) Hooks()")
	assert.Contains(t, code, "func (c *UserClient) Interceptors()")
}

// TestGenEntityClient_HooksUsesDirectAccess verifies that Hooks() reads from
// the typed HookStore via direct field access, not callbacks.
func TestGenEntityClient_HooksUsesDirectAccess(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	typ := createTestType("User")
	helper.graph.Nodes = []*gen.Type{typ}

	file := genEntityClient(helper, typ)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, `hookStore.User`, "Hooks() must read via direct hookStore.User field access")
	assert.Contains(t, code, `entity.HookStore`, "entity client must reference entity.HookStore")
}

// TestGenEntityClient_InterceptorsUsesDirectAccess verifies that Interceptors()
// reads from the typed InterceptorStore via direct field access, not callbacks.
func TestGenEntityClient_InterceptorsUsesDirectAccess(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	typ := createTestType("User")
	helper.graph.Nodes = []*gen.Type{typ}

	file := genEntityClient(helper, typ)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, `interStore.User`, "Interceptors() must read via direct interStore.User field access")
	assert.Contains(t, code, `entity.InterceptorStore`, "entity client must reference entity.InterceptorStore")
}

// TestGenEntityClient_RuntimeOps verifies that runtime.OpXxx constants are used
// (not bare OpXxx) in the generated code.
func TestGenEntityClient_RuntimeOps(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project"

	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genEntityClient(helper, userType)
	code := file.GoString()

	assert.Contains(t, code, "runtime.OpCreate")
	assert.Contains(t, code, "runtime.OpUpdate")
	assert.Contains(t, code, "runtime.OpDelete")
	assert.Contains(t, code, "runtime.OpUpdateOne")
}
