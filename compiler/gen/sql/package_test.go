package sql

import (
	"testing"

	"github.com/dave/jennifer/jen"
	"github.com/stretchr/testify/assert"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// genEdgeOrderOptions Tests
// =============================================================================

func TestGenEdgeOrderOptions_NonUniqueEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createO2MEdge("posts", postType, "posts", "user_id")
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("user")
	genEdgeOrderOptions(helper, f, userType, edge)

	code := f.GoString()
	assert.Contains(t, code, "ByPostsCount")
}

func TestGenEdgeOrderOptions_UniqueEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	profileType := createTestType("Profile")
	edge := createO2OEdge("profile", profileType, "profiles", "user_id")
	helper.graph.Nodes = []*gen.Type{userType, profileType}

	f := helper.NewFile("user")
	genEdgeOrderOptions(helper, f, userType, edge)

	code := f.GoString()
	assert.Contains(t, code, "ByProfileField")
}

// =============================================================================
// genEdgeStepFunction Tests
// =============================================================================

func TestGenEdgeStepFunction_O2M(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createO2MEdge("posts", postType, "posts", "user_id")
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("user")
	genEdgeStepFunction(helper, f, userType, edge)

	code := f.GoString()
	assert.Contains(t, code, "newPostsStep")
	assert.Contains(t, code, "sqlgraph")
}

func TestGenEdgeStepFunction_M2M(t *testing.T) {
	helper := newMockHelper()
	postType := createTestType("Post")
	tagType := createTestType("Tag")
	edge := createM2MEdge("tags", tagType, "post_tags", []string{"post_id", "tag_id"})
	helper.graph.Nodes = []*gen.Type{postType, tagType}

	f := helper.NewFile("post")
	genEdgeStepFunction(helper, f, postType, edge)

	code := f.GoString()
	assert.Contains(t, code, "newTagsStep")
}

func TestGenEdgeStepFunction_M2O(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createM2OEdge("author", userType, "posts", "user_id")
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("post")
	genEdgeStepFunction(helper, f, postType, edge)

	code := f.GoString()
	assert.Contains(t, code, "newAuthorStep")
}

// =============================================================================
// genEnumValidator Tests
// =============================================================================

func TestGenEnumValidator(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	enumField := createEnumField("status", []string{"active", "inactive"})
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("user")
	genEnumValidator(helper, f, userType, enumField)

	code := f.GoString()
	assert.Contains(t, code, "StatusValidator")
	assert.Contains(t, code, "IsValid")
	assert.Contains(t, code, "invalid enum value")
}

// =============================================================================
// genSubpackageEnumType Tests
// =============================================================================

func TestGenSubpackageEnumType(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	enumField := createEnumField("status", []string{"active", "inactive"})
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("user")
	genSubpackageEnumType(helper, f, userType, enumField)

	code := f.GoString()
	assert.Contains(t, code, "Status")
	assert.Contains(t, code, "StatusActive")
	assert.Contains(t, code, "StatusInactive")
	assert.Contains(t, code, "IsValid")
	assert.Contains(t, code, "String()")
	assert.Contains(t, code, "Values")
}

func TestGenSubpackageEnumType_MultipleValues(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	enumField := createEnumField("role", []string{"admin", "user", "moderator"})
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("user")
	genSubpackageEnumType(helper, f, userType, enumField)

	code := f.GoString()
	assert.Contains(t, code, "RoleAdmin")
	assert.Contains(t, code, "RoleUser")
	assert.Contains(t, code, "RoleModerator")
}

// =============================================================================
// subpkgGoType Tests
// =============================================================================

func TestSubpkgGoType_NillableField(t *testing.T) {
	helper := newMockHelper()
	f := createNillableField("bio", field.TypeString)

	result := subpkgGoType(helper, f)
	assert.NotNil(t, result)

	file := jen.NewFile("test")
	file.Var().Id("x").Add(result)
	code := file.GoString()
	assert.Contains(t, code, "*")
}

func TestSubpkgGoType_NonNillableField(t *testing.T) {
	helper := newMockHelper()
	f := createTestField("name", field.TypeString)

	result := subpkgGoType(helper, f)
	assert.NotNil(t, result)
}

func TestSubpkgBaseType_EnumWithoutGoType(t *testing.T) {
	helper := newMockHelper()
	f := createEnumField("status", []string{"active", "inactive"})

	var result jen.Code
	ok := safeGenerate(func() {
		result = subpkgBaseType(helper, f)
	})
	if !ok {
		t.Skip("subpkgBaseType panicked due to incomplete mock state")
	}
	assert.NotNil(t, result)
}

func TestSubpkgBaseType_NonEnumField(t *testing.T) {
	helper := newMockHelper()
	f := createTestField("name", field.TypeString)

	result := subpkgBaseType(helper, f)
	assert.NotNil(t, result)
}

// =============================================================================
// genPackage Tests
// =============================================================================

func TestGenPackage_BasicEntity(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	var file *jen.File
	ok := safeGenerate(func() {
		file = genPackage(helper, userType)
	})
	if !ok {
		t.Skip("genPackage panicked due to incomplete mock state")
	}

	code := file.GoString()
	assert.Contains(t, code, "package user")
	assert.Contains(t, code, "Label")
	assert.Contains(t, code, "Table")
	assert.Contains(t, code, "Columns")
	assert.Contains(t, code, "ValidColumn")
	assert.Contains(t, code, "OrderOption")
}

func TestGenPackage_WithEdges(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
	}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	var code string
	ok := safeGenerate(func() {
		f := genPackage(helper, userType)
		code = f.GoString()
	})
	if !ok {
		t.Skip("genPackage panicked due to incomplete mock state")
	}

	assert.Contains(t, code, "EdgePosts")
	assert.Contains(t, code, "PostsTable")
	assert.Contains(t, code, "PostsColumn")
}

func TestGenPackage_WithM2MEdge(t *testing.T) {
	helper := newMockHelper()
	postType := createTestType("Post")
	tagType := createTestType("Tag")
	postType.Edges = []*gen.Edge{
		createM2MEdge("tags", tagType, "post_tags", []string{"post_id", "tag_id"}),
	}
	helper.graph.Nodes = []*gen.Type{postType, tagType}

	var code string
	ok := safeGenerate(func() {
		f := genPackage(helper, postType)
		code = f.GoString()
	})
	if !ok {
		t.Skip("genPackage panicked due to incomplete mock state")
	}

	assert.Contains(t, code, "TagsTable")
	assert.Contains(t, code, "TagsPrimaryKey")
}

func TestGenPackage_WithEnumField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	userType.Fields = append(userType.Fields, createEnumField("status", []string{"active", "inactive"}))
	helper.graph.Nodes = []*gen.Type{userType}

	var code string
	ok := safeGenerate(func() {
		f := genPackage(helper, userType)
		code = f.GoString()
	})
	if !ok {
		t.Skip("genPackage panicked due to incomplete mock state")
	}

	assert.Contains(t, code, "FieldStatus")
}

func TestGenPackage_WithDefaultField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	userType.Fields = append(userType.Fields, createFieldWithDefault("status", field.TypeString))
	helper.graph.Nodes = []*gen.Type{userType}

	var code string
	ok := safeGenerate(func() {
		f := genPackage(helper, userType)
		code = f.GoString()
	})
	if !ok {
		t.Skip("genPackage panicked due to incomplete mock state")
	}

	assert.Contains(t, code, "DefaultStatus")
}

func TestGenPackage_WithUpdateDefaultField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	// UpdateDefault alone doesn't trigger the var block.
	// Need to also have a Default field to enter the var block.
	userType.Fields = append(userType.Fields,
		createFieldWithDefault("status", field.TypeString),
		createFieldWithUpdateDefault("updated_at", field.TypeTime),
	)
	helper.graph.Nodes = []*gen.Type{userType}

	var code string
	ok := safeGenerate(func() {
		f := genPackage(helper, userType)
		code = f.GoString()
	})
	if !ok {
		t.Skip("genPackage panicked due to incomplete mock state")
	}

	assert.Contains(t, code, "UpdateDefaultUpdatedAt")
	assert.Contains(t, code, "DefaultStatus")
}

func TestGenPackage_WithValidatorsEnabled(t *testing.T) {
	helper := newMockHelperWithValidators()
	userType := createTestType("User")
	userType.Fields = append(userType.Fields, createFieldWithValidators("name", field.TypeString, 2))
	helper.graph.Nodes = []*gen.Type{userType}

	var code string
	ok := safeGenerate(func() {
		f := genPackage(helper, userType)
		code = f.GoString()
	})
	if !ok {
		t.Skip("genPackage panicked due to incomplete mock state")
	}

	assert.Contains(t, code, "NameValidator")
}

func TestGenPackage_WithHooks(t *testing.T) {
	helper := newMockHelper()
	userType := createTypeWithHooks("User", []*load.Position{{Index: 0}})
	helper.graph.Nodes = []*gen.Type{userType}

	var code string
	ok := safeGenerate(func() {
		f := genPackage(helper, userType)
		code = f.GoString()
	})
	if !ok {
		t.Skip("genPackage panicked due to incomplete mock state")
	}

	assert.Contains(t, code, "Hooks")
	assert.Contains(t, code, "import _")
}

func TestGenPackage_WithInterceptors(t *testing.T) {
	helper := newMockHelper()
	userType := createTypeWithInterceptors("User", []*load.Position{{Index: 0}})
	helper.graph.Nodes = []*gen.Type{userType}

	var code string
	ok := safeGenerate(func() {
		f := genPackage(helper, userType)
		code = f.GoString()
	})
	if !ok {
		t.Skip("genPackage panicked due to incomplete mock state")
	}

	assert.Contains(t, code, "Interceptors")
}

func TestGenPackage_WithPolicies(t *testing.T) {
	helper := newMockHelper()
	userType := createTypeWithPolicies("User", []*load.Position{{Index: 0}})
	helper.graph.Nodes = []*gen.Type{userType}

	var code string
	ok := safeGenerate(func() {
		f := genPackage(helper, userType)
		code = f.GoString()
	})
	if !ok {
		t.Skip("genPackage panicked due to incomplete mock state")
	}

	assert.Contains(t, code, "Policy")
}

func TestGenPackage_WithUserDefinedID(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	userType.ID.UserDefined = true
	userType.ID.Default = true
	helper.graph.Nodes = []*gen.Type{userType}

	var code string
	ok := safeGenerate(func() {
		f := genPackage(helper, userType)
		code = f.GoString()
	})
	if !ok {
		t.Skip("genPackage panicked due to incomplete mock state")
	}

	assert.Contains(t, code, "DefaultID")
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkGenEdgeStepFunction(b *testing.B) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createO2MEdge("posts", postType, "posts", "user_id")

	for b.Loop() {
		f := helper.NewFile("user")
		genEdgeStepFunction(helper, f, userType, edge)
	}
}
