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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project/ent"
	userType := createTestType("User")
	enumField := createEnumField("status", []string{"active", "inactive"})
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("user")
	genSubpackageEnumType(helper, f, userType, enumField, buildEntityPkgEnumRegistry(helper.graph.Nodes))

	code := f.GoString()
	// Target state (cycle-break): generates a real type in the leaf sub-package.
	assert.Contains(t, code, "type Status string",
		"leaf package must declare the real enum type, not an alias")
	assert.Contains(t, code, "StatusActive")
	assert.Contains(t, code, "StatusInactive")
	assert.Contains(t, code, "StatusValues")
	// Must NOT alias back to entity/
	assert.NotContains(t, code, "entity.UserStatus",
		"leaf package must not alias the enum type from entity/")
}

func TestGenSubpackageEnumType_MultipleValues(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project/ent"
	userType := createTestType("User")
	enumField := createEnumField("role", []string{"admin", "user", "moderator"})
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("user")
	genSubpackageEnumType(helper, f, userType, enumField, buildEntityPkgEnumRegistry(helper.graph.Nodes))

	code := f.GoString()
	assert.Contains(t, code, "RoleAdmin")
	assert.Contains(t, code, "RoleUser")
	assert.Contains(t, code, "RoleModerator")
}

// =============================================================================
// subpkgGoType Tests
// =============================================================================

func TestSubpkgGoType_NillableField(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	helper := newMockHelper()
	f := createTestField("name", field.TypeString)

	result := subpkgGoType(helper, f)
	assert.NotNil(t, result)
}

func TestSubpkgBaseType_EnumWithoutGoType(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	f := createEnumField("status", []string{"active", "inactive"})

	result := subpkgBaseType(helper, f)
	assert.NotNil(t, result)
}

func TestSubpkgBaseType_NonEnumField(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	f := createTestField("name", field.TypeString)

	result := subpkgBaseType(helper, f)
	assert.NotNil(t, result)
}

// =============================================================================
// genPackage Tests
// =============================================================================

func TestGenPackage_BasicEntity(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	file := genPackage(helper, userType, buildEntityPkgEnumRegistry(helper.graph.Nodes))

	code := file.GoString()
	assert.Contains(t, code, "package user")
	assert.Contains(t, code, "Label")
	assert.Contains(t, code, "Table")
	assert.Contains(t, code, "Columns")
	assert.Contains(t, code, "ValidColumn")
	assert.Contains(t, code, "OrderOption")
}

func TestGenPackage_WithEdges(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
	}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := genPackage(helper, userType, buildEntityPkgEnumRegistry(helper.graph.Nodes))
	code := f.GoString()

	assert.Contains(t, code, "EdgePosts")
	assert.Contains(t, code, "PostsTable")
	assert.Contains(t, code, "PostsColumn")
}

func TestGenPackage_WithM2MEdge(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	postType := createTestType("Post")
	tagType := createTestType("Tag")
	postType.Edges = []*gen.Edge{
		createM2MEdge("tags", tagType, "post_tags", []string{"post_id", "tag_id"}),
	}
	helper.graph.Nodes = []*gen.Type{postType, tagType}

	f := genPackage(helper, postType, buildEntityPkgEnumRegistry(helper.graph.Nodes))
	code := f.GoString()

	assert.Contains(t, code, "TagsTable")
	assert.Contains(t, code, "TagsPrimaryKey")
}

func TestGenPackage_WithEnumField(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestType("User")
	userType.Fields = append(userType.Fields, createEnumField("status", []string{"active", "inactive"}))
	helper.graph.Nodes = []*gen.Type{userType}

	f := genPackage(helper, userType, buildEntityPkgEnumRegistry(helper.graph.Nodes))
	code := f.GoString()

	assert.Contains(t, code, "FieldStatus")
}

func TestGenPackage_WithDefaultField(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTypeWithSchemaFields(t, "User", []*load.Field{
		{Name: "name", Info: &field.TypeInfo{Type: field.TypeString}},
		{Name: "email", Info: &field.TypeInfo{Type: field.TypeString}},
		{Name: "age", Info: &field.TypeInfo{Type: field.TypeInt}},
		{Name: "status", Info: &field.TypeInfo{Type: field.TypeString}, Default: true, DefaultValue: "active", DefaultKind: 0},
	})
	helper.graph.Nodes = []*gen.Type{userType}

	f := genPackage(helper, userType, buildEntityPkgEnumRegistry(helper.graph.Nodes))
	code := f.GoString()

	assert.Contains(t, code, "DefaultStatus")
}

func TestGenPackage_WithUpdateDefaultField(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTypeWithSchemaFields(t, "User", []*load.Field{
		{Name: "name", Info: &field.TypeInfo{Type: field.TypeString}},
		{Name: "email", Info: &field.TypeInfo{Type: field.TypeString}},
		{Name: "age", Info: &field.TypeInfo{Type: field.TypeInt}},
		{Name: "status", Info: &field.TypeInfo{Type: field.TypeString}, Default: true, DefaultValue: "active"},
		{Name: "updated_at", Info: &field.TypeInfo{Type: field.TypeTime}, UpdateDefault: true},
	})
	helper.graph.Nodes = []*gen.Type{userType}

	f := genPackage(helper, userType, buildEntityPkgEnumRegistry(helper.graph.Nodes))
	code := f.GoString()

	assert.Contains(t, code, "UpdateDefaultUpdatedAt")
	assert.Contains(t, code, "DefaultStatus")
}

func TestGenPackage_WithValidatorsEnabled(t *testing.T) {
	t.Parallel()
	helper := newMockHelperWithValidators()
	userType := createTestType("User")
	userType.Fields = append(userType.Fields, createFieldWithValidators("name", field.TypeString, 2))
	helper.graph.Nodes = []*gen.Type{userType}

	f := genPackage(helper, userType, buildEntityPkgEnumRegistry(helper.graph.Nodes))
	code := f.GoString()

	assert.Contains(t, code, "NameValidator")
}

func TestGenPackage_WithHooks(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTypeWithHooks(t, "User", []*load.Position{{Index: 0}})
	helper.graph.Nodes = []*gen.Type{userType}

	f := genPackage(helper, userType, buildEntityPkgEnumRegistry(helper.graph.Nodes))
	code := f.GoString()

	assert.Contains(t, code, "Hooks")
	assert.Contains(t, code, "import _")
}

func TestGenPackage_WithInterceptors(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTypeWithInterceptors(t, "User", []*load.Position{{Index: 0}})
	helper.graph.Nodes = []*gen.Type{userType}

	f := genPackage(helper, userType, buildEntityPkgEnumRegistry(helper.graph.Nodes))
	code := f.GoString()

	assert.Contains(t, code, "Interceptors")
}

func TestGenPackage_WithPolicies(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTypeWithPolicies(t, "User", []*load.Position{{Index: 0}})
	helper.graph.Nodes = []*gen.Type{userType}

	f := genPackage(helper, userType, buildEntityPkgEnumRegistry(helper.graph.Nodes))
	code := f.GoString()

	assert.Contains(t, code, "Policy")
}

func TestGenPackage_WithUserDefinedID(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTypeWithSchemaFields(t, "User", []*load.Field{
		{Name: "name", Info: &field.TypeInfo{Type: field.TypeString}},
		{Name: "email", Info: &field.TypeInfo{Type: field.TypeString}},
		{Name: "age", Info: &field.TypeInfo{Type: field.TypeInt}},
	})
	userType.ID.UserDefined = true
	userType.ID.Default = true
	helper.graph.Nodes = []*gen.Type{userType}

	f := genPackage(helper, userType, buildEntityPkgEnumRegistry(helper.graph.Nodes))
	code := f.GoString()

	assert.Contains(t, code, "DefaultID")
}

// =============================================================================
// Enum Name Collision Tests
// =============================================================================

// createTypeWithEnumField creates a Type via NewType with an enum field, ensuring
// the Field.typ pointer is properly set for EnumTypeName() to work.
func createTypeWithEnumField(t *testing.T, name, fieldName string, values []string) *gen.Type {
	t.Helper()
	enums := make([]struct{ N, V string }, len(values))
	for i, v := range values {
		enums[i] = struct{ N, V string }{N: v, V: v}
	}
	return createTypeWithSchemaFields(t, name, []*load.Field{
		{
			Name:  fieldName,
			Info:  &field.TypeInfo{Type: field.TypeEnum},
			Enums: enums,
		},
	})
}

func TestBuildEntityPkgEnumRegistry_NoCollision(t *testing.T) {
	t.Parallel()
	userType := createTypeWithEnumField(t, "User", "status", []string{"active", "inactive"})
	postType := createTypeWithEnumField(t, "Post", "role", []string{"admin", "user"})

	reg := buildEntityPkgEnumRegistry([]*gen.Type{userType, postType})

	assert.Equal(t, "UserStatus", reg.resolve("User", "status"))
	assert.Equal(t, "PostRole", reg.resolve("Post", "role"))
	assert.True(t, reg.isOwner("User", "status"))
	assert.True(t, reg.isOwner("Post", "role"))
}

func TestBuildEntityPkgEnumRegistry_CollisionSameValues(t *testing.T) {
	t.Parallel()
	// Asset.depreciation_method and AssetDepreciation.method both produce "AssetDepreciationMethod"
	assetType := createTypeWithEnumField(t, "Asset", "depreciation_method", []string{"straight_line", "declining_balance"})
	assetDepType := createTypeWithEnumField(t, "AssetDepreciation", "method", []string{"straight_line", "declining_balance"})

	reg := buildEntityPkgEnumRegistry([]*gen.Type{assetType, assetDepType})

	// Both should resolve to the same name since values are identical.
	assert.Equal(t, "AssetDepreciationMethod", reg.resolve("Asset", "depreciation_method"))
	assert.Equal(t, "AssetDepreciationMethod", reg.resolve("AssetDepreciation", "method"))

	// Only the first (alphabetically) should be the owner.
	assert.True(t, reg.isOwner("Asset", "depreciation_method"))
	assert.False(t, reg.isOwner("AssetDepreciation", "method"))
}

func TestBuildEntityPkgEnumRegistry_CollisionDifferentValues(t *testing.T) {
	t.Parallel()
	// Same name collision but different enum values — must disambiguate.
	assetType := createTypeWithEnumField(t, "Asset", "depreciation_method", []string{"straight_line", "declining_balance"})
	assetDepType := createTypeWithEnumField(t, "AssetDepreciation", "method", []string{"sum_of_years", "units_of_production"})

	reg := buildEntityPkgEnumRegistry([]*gen.Type{assetType, assetDepType})

	// First entity keeps the original name.
	assert.Equal(t, "AssetDepreciationMethod", reg.resolve("Asset", "depreciation_method"))
	// Second entity gets disambiguated name.
	assert.Equal(t, "AssetDepreciationMethodEnum", reg.resolve("AssetDepreciation", "method"))

	// Both should be owners of their respective names.
	assert.True(t, reg.isOwner("Asset", "depreciation_method"))
	assert.True(t, reg.isOwner("AssetDepreciation", "method"))
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
