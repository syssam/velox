package sql

import (
	"testing"

	"github.com/dave/jennifer/jen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// genRuntimeCombined Tests
// =============================================================================

func TestGenRuntimeCombined_EmptyNodes(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()

	file := genRuntimeCombined(helper, nil)
	require.NotNil(t, file)
}

func TestGenRuntimeCombined_WithNodes(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genRuntimeCombined(helper, helper.graph.Nodes)
	require.NotNil(t, file)

	code := file.GoString()
	// genRuntimeCombined only outputs Version/Sum if available from build info
	assert.NotEmpty(t, code)
}

// =============================================================================
// genEntityRuntime Tests
// =============================================================================

func TestGenEntityRuntime_BasicNoRootPkg(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project/ent"
	userType := createTestType("User")

	file := genEntityRuntime(helper, userType)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "func init()")
	// Entity mode uses consolidated RegisterEntity
	assert.Contains(t, code, "RegisterEntity")
}

func TestGenEntityRuntime_WithRootPkg(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project/ent"
	userType := createTestType("User")

	file := genEntityRuntime(helper, userType)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "func init()")
	// Should contain RegisterEntity with all fields in entity-package mode
	assert.Contains(t, code, "RegisterEntity")
	assert.Contains(t, code, "RegisteredTypeInfo")
	assert.Contains(t, code, "Table")
	assert.Contains(t, code, "Columns")
	assert.Contains(t, code, "FieldID")
	assert.Contains(t, code, "ValidColumn")
	assert.Contains(t, code, "ScanValues")
	assert.Contains(t, code, "AssignValues")
}

func TestGenEntityRuntime_WithRootPkg_EntityTypeRef(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project/ent"
	userType := createTestType("User")

	file := genEntityRuntime(helper, userType)
	require.NotNil(t, file)

	code := file.GoString()
	// Should reference entity.User from the entity/ package
	assert.Contains(t, code, "entity")
	assert.Contains(t, code, "User")
}

func TestGenEntityRuntime_WithRootPkg_NoForeignKeys(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project/ent"
	userType := createTestType("User")

	file := genEntityRuntime(helper, userType)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "RegisterEntity")
	// RegisteredTypeInfo.ForeignKeys field no longer exists — the registry it
	// served was removed once GraphQL edges migrated to direct entity method calls.
	assert.NotContains(t, code, "ForeignKeys")
}

func TestGenEntityRuntime_WithRootPkg_WithForeignKeys(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project/ent"

	// Create a type with edges that produce foreign keys
	ownerType := createTestType("User")
	postType := createTestTypeWithSchema(t, "Post", &load.Schema{
		Fields: []*load.Field{
			{Name: "title", Info: &field.TypeInfo{Type: field.TypeString}},
		},
	})
	// Add a M2O edge (produces a foreign key column)
	postType.Edges = append(postType.Edges, createM2OEdge("owner", ownerType, "posts", "post_owner"))

	file := genEntityRuntime(helper, postType)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "RegisterEntity")
	// RegisteredTypeInfo no longer has a ForeignKeys field — even for entities
	// with foreign keys, the struct literal omits it. The package-level
	// ForeignKeys variable (emitted by package.go) is unaffected.
	assert.NotContains(t, code, "ForeignKeys:")
}

func TestGenEntityRuntime_WithRootPkg_WithDefaults(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project/ent"

	userType := createTypeWithSchemaFields(t, "User", []*load.Field{
		{
			Name:    "name",
			Info:    &field.TypeInfo{Type: field.TypeString},
			Default: true,
		},
	})

	file := genEntityRuntime(helper, userType)
	require.NotNil(t, file)

	code := file.GoString()
	// Should have BOTH schema defaults AND registration
	assert.Contains(t, code, "RegisterEntity")
	assert.Contains(t, code, "Default")
}

// =============================================================================
// genEntityRuntimeRegistration Tests
// =============================================================================

func TestGenEntityRuntimeRegistration_BasicUser(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project/ent"
	userType := createTestType("User")

	grp := &jen.Group{}
	genEntityRuntimeRegistration(helper, grp, userType)
	// Should not panic
}

func TestGenEntityRuntimeRegistration_EmptyGraphPackage(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project/ent"
	helper.graph.Package = ""
	userType := createTestType("User")

	grp := &jen.Group{}
	genEntityRuntimeRegistration(helper, grp, userType)
	// Should fall back to h.Pkg() + "/entity" without panic
}

func TestGenEntityRuntimeRegistration_ContainsAllFields(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project/ent"
	userType := createTestType("User")

	// Use BlockFunc to properly add statements to the group.
	f := jen.NewFile("user")
	f.Func().Id("init").Params().BlockFunc(func(grp *jen.Group) {
		genEntityRuntimeRegistration(helper, grp, userType)
	})
	code := f.GoString()
	assert.Contains(t, code, "RegisterEntity")
	assert.Contains(t, code, `"User"`)
	assert.Contains(t, code, "NewUserClient")
	assert.Contains(t, code, "UserMutation")
	assert.Contains(t, code, "mutate")
	assert.Contains(t, code, "ValidColumn")
	assert.Contains(t, code, "RegisteredTypeInfo")
}

func TestGenEntityRuntime_WithRootPkg_RegistersMutator(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project/ent"
	userType := createTestType("User")

	file := genEntityRuntime(helper, userType)
	require.NotNil(t, file)

	code := file.GoString()
	// In entity-package mode, init() should contain RegisterEntity
	assert.Contains(t, code, "RegisterEntity")
	assert.Contains(t, code, `"User"`)
}

func TestGenEntityRuntime_WithRootPkg_AlwaysRegistersMutator(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project/ent"
	userType := createTestType("User")

	file := genEntityRuntime(helper, userType)
	require.NotNil(t, file)

	code := file.GoString()
	// Entity mode uses consolidated RegisterEntity
	assert.Contains(t, code, "RegisterEntity")
}

func TestGenEntityRuntimeRegistration_PostType(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project/ent"
	postType := createTestType("Post")

	f := jen.NewFile("post")
	f.Func().Id("init").Params().BlockFunc(func(grp *jen.Group) {
		genEntityRuntimeRegistration(helper, grp, postType)
	})
	code := f.GoString()
	assert.Contains(t, code, "RegisterEntity")
	assert.Contains(t, code, `"Post"`)
	assert.Contains(t, code, "NewPostClient")
	assert.Contains(t, code, "PostMutation")
	assert.Contains(t, code, "ValidColumn")
}

// =============================================================================
// genQueryHelpers RegisterQueryFactory Tests
// =============================================================================

func TestGenQueryHelpers_WithRootPkg_RegistersQueryFactory(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project/ent"
	helper.graph.Nodes = []*gen.Type{
		createTestType("User"),
		createTestType("Post"),
	}

	file := genQueryHelpers(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "func init()")
	assert.Contains(t, code, "RegisterQueryFactory")
	assert.Contains(t, code, `"User"`)
	assert.Contains(t, code, `"Post"`)
	assert.Contains(t, code, "NewUserQuery")
	assert.Contains(t, code, "NewPostQuery")
}

func TestGenQueryHelpers_AlwaysRegistersQueryFactory(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project/ent"
	helper.graph.Nodes = []*gen.Type{
		createTestType("User"),
	}

	file := genQueryHelpers(helper)
	require.NotNil(t, file)

	code := file.GoString()
	// Entity mode always registers query factory
	assert.Contains(t, code, "RegisterQueryFactory")
}

func TestGenQueryHelpers_WithRootPkg_EmptyNodes(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project/ent"
	helper.graph.Nodes = []*gen.Type{}

	file := genQueryHelpers(helper)
	require.NotNil(t, file)

	code := file.GoString()
	// No entities → no init() for registration
	assert.NotContains(t, code, "RegisterQueryFactory")
}
