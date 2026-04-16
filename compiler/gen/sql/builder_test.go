package sql

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/dave/jennifer/jen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

// assertValidGo validates that the generated code is syntactically valid Go
// using go/parser.
func assertValidGo(t *testing.T, f *jen.File, label string) {
	t.Helper()
	code := f.GoString()
	fset := token.NewFileSet()
	_, err := parser.ParseFile(fset, label+".go", code, parser.AllErrors)
	if err != nil {
		t.Errorf("generated code for %s is not valid Go:\n%v\n\nCode:\n%s", label, err, code)
	}
}

// =============================================================================
// Query Tests
// =============================================================================

func TestGenQueryPkg_BasicStructure_Builder(t *testing.T) {
	h := newFeatureMockHelper()
	h.rootPkg = "github.com/test/project"
	testType := createTestType("User")
	h.graph.Nodes = []*gen.Type{testType}

	file := genQueryPkg(h, testType, h.graph.Nodes, "github.com/test/project/ent/entity")
	require.NotNil(t, file)
	assertValidGo(t, file, "user_query")

	code := file.GoString()

	// Should contain the query struct
	assert.Contains(t, code, "type UserQuery struct")

	// Should have Where method
	assert.Contains(t, code, "func (q *UserQuery) Where")

	// Should have Limit method
	assert.Contains(t, code, "func (q *UserQuery) Limit")

	// Should have Offset method
	assert.Contains(t, code, "func (q *UserQuery) Offset")

	// Should have All/First/Only/Count/Exist
	assert.Contains(t, code, "func (q *UserQuery) All(")
	assert.Contains(t, code, "func (q *UserQuery) First(")
	assert.Contains(t, code, "func (q *UserQuery) Only(")
	assert.Contains(t, code, "func (q *UserQuery) Count(")
	assert.Contains(t, code, "func (q *UserQuery) Exist(")
}

func TestGenQueryPkg_WithEdges_Builder(t *testing.T) {
	h := newFeatureMockHelper()
	h.rootPkg = "github.com/test/project"
	userType := createTestType("User")
	postType := createTestType("Post")

	postsEdge := createO2MEdge("posts", postType, "posts", "user_id")
	userType.Edges = []*gen.Edge{postsEdge}
	h.graph.Nodes = []*gen.Type{userType, postType}

	file := genQueryPkg(h, userType, h.graph.Nodes, "github.com/test/project/ent/entity")
	require.NotNil(t, file)

	code := file.GoString()

	// Should have WithPosts method
	assert.Contains(t, code, "WithPosts")
}

func TestGenQueryPkg_Clone_Builder(t *testing.T) {
	h := newFeatureMockHelper()
	h.rootPkg = "github.com/test/project"
	testType := createTestType("User")
	h.graph.Nodes = []*gen.Type{testType}

	file := genQueryPkg(h, testType, h.graph.Nodes, "github.com/test/project/ent/entity")
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "func (q *UserQuery) clone()")
}

// =============================================================================
// Delete Tests
// =============================================================================

func TestGenDelete_BasicStructure(t *testing.T) {
	h := newFeatureMockHelper()
	testType := createTestType("User")
	h.graph.Nodes = []*gen.Type{testType}

	file := testGenDelete(h, testType)
	require.NotNil(t, file)
	assertValidGo(t, file, "user_delete")

	code := file.GoString()

	// Should contain both delete structs
	assert.Contains(t, code, "type UserDelete struct")
	assert.Contains(t, code, "type UserDeleteOne struct")

	// Should have Where method
	assert.Contains(t, code, "func (_d *UserDelete) Where")

	// Should delegate to runtime.DeleteNodes
	assert.Contains(t, code, "runtime.DeleterBase")
	assert.Contains(t, code, "runtime.DeleteNodes")

	// Should have ExecX
	assert.Contains(t, code, "func (_d *UserDelete) ExecX")
}

func TestGenDelete_DeleteOneNotFound(t *testing.T) {
	h := newFeatureMockHelper()
	testType := createTestType("User")
	h.graph.Nodes = []*gen.Type{testType}

	file := testGenDelete(h, testType)
	require.NotNil(t, file)

	code := file.GoString()
	// DeleteOne.Exec should return NotFoundError when no rows affected
	assert.Contains(t, code, "NotFoundError")
}

func TestGenDelete_LineCount(t *testing.T) {
	h := newFeatureMockHelper()
	testType := createTestType("User")
	h.graph.Nodes = []*gen.Type{testType}

	file := testGenDelete(h, testType)
	require.NotNil(t, file)

	code := file.GoString()
	lines := strings.Count(code, "\n")
	assert.Less(t, lines, 120, "delete should be under 120 lines, got %d", lines)
}

// =============================================================================
// Builder Split Isolation Tests
// =============================================================================

// TestGenCreate_IsolatedFromUpdateAndDelete verifies that GenCreate output
// contains only create-related types and does not leak update or delete types.
func TestGenCreate_IsolatedFromUpdateAndDelete(t *testing.T) {
	t.Parallel()
	h := newFeatureMockHelper()
	userType := createTestType("User")
	h.graph.Nodes = []*gen.Type{userType}

	file, err := genCreate(h, userType)
	require.NoError(t, err)
	require.NotNil(t, file)
	assertValidGo(t, file, "user_create")

	code := file.GoString()
	assert.Contains(t, code, "UserCreate")
	assert.NotContains(t, code, "UserUpdate")
	assert.NotContains(t, code, "UserUpdateOne")
	assert.NotContains(t, code, "UserDelete")
	assert.NotContains(t, code, "UserDeleteOne")
}

// TestGenUpdate_IsolatedFromCreateAndDelete verifies that GenUpdate output
// contains only update-related types and does not leak create or delete types.
func TestGenUpdate_IsolatedFromCreateAndDelete(t *testing.T) {
	t.Parallel()
	h := newFeatureMockHelper()
	userType := createTestType("User")
	h.graph.Nodes = []*gen.Type{userType}

	file, err := genUpdate(h, userType)
	require.NoError(t, err)
	require.NotNil(t, file)
	assertValidGo(t, file, "user_update")

	code := file.GoString()
	assert.Contains(t, code, "UserUpdate")
	assert.Contains(t, code, "UserUpdateOne")
	assert.NotContains(t, code, "UserCreate")
	assert.NotContains(t, code, "UserCreateBulk")
	assert.NotContains(t, code, "UserDelete")
	assert.NotContains(t, code, "UserDeleteOne")
}

// TestGenDelete_IsolatedFromCreateAndUpdate verifies that GenDelete output
// contains only delete-related types and does not leak create or update types.
func TestGenDelete_IsolatedFromCreateAndUpdate(t *testing.T) {
	t.Parallel()
	h := newFeatureMockHelper()
	userType := createTestType("User")
	h.graph.Nodes = []*gen.Type{userType}

	file, err := genDelete(h, userType)
	require.NoError(t, err)
	require.NotNil(t, file)
	assertValidGo(t, file, "user_delete")

	code := file.GoString()
	assert.Contains(t, code, "UserDelete")
	assert.Contains(t, code, "UserDeleteOne")
	assert.NotContains(t, code, "UserCreate")
	assert.NotContains(t, code, "UserCreateBulk")
	assert.NotContains(t, code, "UserUpdate")
	assert.NotContains(t, code, "UserUpdateOne")
}

// =============================================================================
// Mutation Tests
// =============================================================================

func TestGenMutation_BasicStructure(t *testing.T) {
	h := newFeatureMockHelper()
	testType := createTestType("User")
	h.graph.Nodes = []*gen.Type{testType}

	file := genMutation(h, testType)
	require.NotNil(t, file)
	assertValidGo(t, file, "user_mutation")

	code := file.GoString()

	// Should contain the mutation struct with a direct op field (no embedded base).
	assert.Contains(t, code, "type UserMutation struct")
	assert.Contains(t, code, "op runtime.Op")
	assert.NotContains(t, code, "runtime.MutationBase")

	// Should have typed field setters
	assert.Contains(t, code, "func (m *UserMutation) SetName")
	assert.Contains(t, code, "func (m *UserMutation) SetEmail")
	assert.Contains(t, code, "func (m *UserMutation) SetAge")

	// Should have typed field getters
	assert.Contains(t, code, "func (m *UserMutation) Name()")
	assert.Contains(t, code, "func (m *UserMutation) Email()")

	// Should have Reset methods
	assert.Contains(t, code, "func (m *UserMutation) ResetName()")

	// Should have Fields and ClearedFields
	assert.Contains(t, code, "func (m *UserMutation) Fields()")
	assert.Contains(t, code, "func (m *UserMutation) ClearedFields()")

	// Setters write typed pointer fields only (no dual-write).
	assert.NotContains(t, code, "MutationBase")
	// Getters read from typed fields.
	assert.Contains(t, code, "m._name == nil")
	assert.Contains(t, code, "*m._name")
	// Should have typed pointer fields and clearedFields map
	assert.Contains(t, code, "_name")
	assert.Contains(t, code, "_email")
	assert.Contains(t, code, "_age")
	assert.Contains(t, code, "clearedFields")
}

func TestGenMutation_WithEdges(t *testing.T) {
	h := newFeatureMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")

	postsEdge := createO2MEdge("posts", postType, "posts", "user_id")
	userType.Edges = []*gen.Edge{postsEdge}
	h.graph.Nodes = []*gen.Type{userType, postType}

	file := genMutation(h, userType)
	require.NotNil(t, file)

	code := file.GoString()

	// O2M edge should have Add/Remove/Clear methods (singularized per Ent convention)
	assert.Contains(t, code, "AddPostIDs")
	assert.Contains(t, code, "RemovePostIDs")
	assert.Contains(t, code, "ClearPosts")
	assert.Contains(t, code, "ResetPosts")
}

func TestGenMutation_UniqueEdge(t *testing.T) {
	h := newFeatureMockHelper()
	userType := createTestType("User")
	profileType := createTestType("Profile")

	profileEdge := createO2OEdge("profile", profileType, "profiles", "user_id")
	userType.Edges = []*gen.Edge{profileEdge}
	h.graph.Nodes = []*gen.Type{userType, profileType}

	file := genMutation(h, userType)
	require.NotNil(t, file)

	code := file.GoString()

	// Unique edge should have Set/Clear (not Add/Remove)
	assert.Contains(t, code, "SetProfileID")
	assert.Contains(t, code, "ClearProfile")
	assert.Contains(t, code, "ProfileCleared")
}

func TestGenMutation_OptionalField(t *testing.T) {
	h := newFeatureMockHelper()
	testType := createTestTypeWithFields("User", []*gen.Field{
		createOptionalField("bio", field.TypeString),
	})
	h.graph.Nodes = []*gen.Type{testType}

	file := genMutation(h, testType)
	require.NotNil(t, file)

	code := file.GoString()
	// Optional (non-nillable) fields have NOT NULL columns — ClearXxx must NOT
	// be generated because it would attempt SET col = NULL violating the constraint.
	assert.NotContains(t, code, "ClearBio", "Optional non-nillable field should not have ClearXxx")
	assert.NotContains(t, code, "BioCleared", "Optional non-nillable field should not have XxxCleared")
}

func TestGenMutation_NillableField(t *testing.T) {
	h := newFeatureMockHelper()
	testType := createTestTypeWithFields("User", []*gen.Field{
		{Name: "bio", Type: &field.TypeInfo{Type: field.TypeString}, Nillable: true},
	})
	h.graph.Nodes = []*gen.Type{testType}

	file := genMutation(h, testType)
	require.NotNil(t, file)

	code := file.GoString()
	// Nillable fields have NULL columns — ClearXxx should be generated.
	assert.Contains(t, code, "ClearBio", "Nillable field should have ClearXxx")
	assert.Contains(t, code, "BioCleared", "Nillable field should have XxxCleared")
}

func TestGenMutation_OptionalNillableField(t *testing.T) {
	h := newFeatureMockHelper()
	testType := createTestTypeWithFields("User", []*gen.Field{
		{Name: "bio", Type: &field.TypeInfo{Type: field.TypeString}, Optional: true, Nillable: true},
	})
	h.graph.Nodes = []*gen.Type{testType}

	file := genMutation(h, testType)
	require.NotNil(t, file)

	code := file.GoString()
	// Optional+Nillable fields have NULL columns — ClearXxx should be generated.
	assert.Contains(t, code, "ClearBio", "Optional+Nillable field should have ClearXxx")
	assert.Contains(t, code, "BioCleared", "Optional+Nillable field should have XxxCleared")
}

func TestGenMutation_NumericAddField(t *testing.T) {
	h := newFeatureMockHelper()
	testType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("age", field.TypeInt),
	})
	h.graph.Nodes = []*gen.Type{testType}

	file := genMutation(h, testType)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "AddAge")
	assert.Contains(t, code, "AddedAge")
}

func TestGenMutation_LineCount(t *testing.T) {
	h := newFeatureMockHelper()
	testType := createTestType("User")
	h.graph.Nodes = []*gen.Type{testType}

	file := genMutation(h, testType)
	require.NotNil(t, file)

	code := file.GoString()
	lines := strings.Count(code, "\n")
	assert.Less(t, lines, 450, "mutation should be under 450 lines, got %d", lines)
}

// =============================================================================
// Helpers Tests
// =============================================================================

func TestIDFieldTypeVar(t *testing.T) {
	tests := []struct {
		name     string
		typeName string
		expected string
	}{
		{"simple", "User", "userIDFieldType"},
		{"multi_word", "BlogPost", "blogPostIDFieldType"},
		{"single_char", "A", "aIDFieldType"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typ := &gen.Type{Name: tt.typeName}
			assert.Equal(t, tt.expected, idFieldTypeVar(typ))
		})
	}
}

// =============================================================================
// Dialect Integration Tests (Runtime-based Generation)
// =============================================================================

func TestDialect_GenMutation(t *testing.T) {
	h := newFeatureMockHelper()
	testType := createTestType("User")
	h.graph.Nodes = []*gen.Type{testType}

	dialect := NewDialect(h)
	file, err := dialect.GenMutation(testType)
	require.NoError(t, err)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "op runtime.Op", "mutation should carry op field directly")
	assert.NotContains(t, code, "runtime.MutationBase")
}

func TestDialect_GenRuntime(t *testing.T) {
	h := newFeatureMockHelper()
	testType := createTestType("User")
	h.graph.Nodes = []*gen.Type{testType}

	dialect := NewDialect(h)
	file, err := dialect.GenRuntime()
	require.NoError(t, err)
	require.NotNil(t, file)

	code := file.GoString()
	assert.NotContains(t, code, "UserTypeInfo", "runtime should not contain entity meta (generated per-entity in meta.go)")
}

// =============================================================================
// Total Line Count Test
// =============================================================================

func TestGen_TotalLineCount(t *testing.T) {
	h := newFeatureMockHelper()
	h.rootPkg = "github.com/test/project"

	userType := createTestType("User")
	postType := createTestType("Post")
	postsEdge := createO2MEdge("posts", postType, "posts", "user_id")
	authorEdge := createM2OEdge("author", userType, "posts", "user_id")
	userType.Edges = []*gen.Edge{postsEdge}
	postType.Edges = []*gen.Edge{authorEdge}
	h.graph.Nodes = []*gen.Type{userType, postType}

	totalLines := 0
	for _, typ := range h.graph.Nodes {
		queryFile := genQueryPkg(h, typ, h.graph.Nodes, "github.com/test/project/ent/entity")
		deleteFile := testGenDelete(h, typ)
		mutationFile := genMutation(h, typ)

		totalLines += strings.Count(queryFile.GoString(), "\n")
		totalLines += strings.Count(deleteFile.GoString(), "\n")
		totalLines += strings.Count(mutationFile.GoString(), "\n")
	}

	// With 2 entities (User + Post), total builder code for query+delete+mutation.
	// Create/Update are now generated as root wrappers, not inner builders.
	assert.Less(t, totalLines, 3200,
		"total builder lines for 2 entities should be under 3200, got %d", totalLines)
	t.Logf("Total builder lines for 2 entities: %d", totalLines)
}

// =============================================================================
// Comprehensive Parse Validation
// =============================================================================

func TestAllGeneratedCodeIsValidGo(t *testing.T) {
	h := newFeatureMockHelper()
	h.rootPkg = "github.com/test/project"

	// Build a realistic type graph with fields, edges, and various field types.
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
		createTestField("email", field.TypeString),
		createTestField("age", field.TypeInt),
		createOptionalField("bio", field.TypeString),
		createNillableField("nickname", field.TypeString),
		createImmutableField("created_by", field.TypeString),
	})
	postType := createTestTypeWithFields("Post", []*gen.Field{
		createTestField("title", field.TypeString),
		createTestField("body", field.TypeString),
	})

	// Wire up edges: User -O2M-> Posts, Post -M2O-> Author
	postsEdge := createO2MEdge("posts", postType, "posts", "user_id")
	authorEdge := createM2OEdge("author", userType, "posts", "user_id")
	userType.Edges = []*gen.Edge{postsEdge}
	postType.Edges = []*gen.Edge{authorEdge}
	h.graph.Nodes = []*gen.Type{userType, postType}

	// Validate all per-entity generated files for each type.
	for _, typ := range h.graph.Nodes {
		label := strings.ToLower(typ.Name)

		t.Run(typ.Name+"/query", func(t *testing.T) {
			assertValidGo(t, genQueryPkg(h, typ, h.graph.Nodes, "github.com/test/project/ent/entity"), label+"_query")
		})
		t.Run(typ.Name+"/delete", func(t *testing.T) {
			assertValidGo(t, testGenDelete(h, typ), label+"_delete")
		})
		t.Run(typ.Name+"/mutation", func(t *testing.T) {
			assertValidGo(t, genMutation(h, typ), label+"_mutation")
		})
	}
}
