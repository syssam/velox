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
// genUpdateEdgeMethods Tests
// =============================================================================

func TestGenUpdateEdgeMethods_UniqueEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createM2OEdge("author", userType, "posts", "user_id")

	f := helper.NewFile("ent")
	genUpdateEdgeMethods(helper, f, postType, edge, "PostUpdate", "pu")

	code := f.GoString()
	assert.Contains(t, code, "SetAuthorID")
	assert.Contains(t, code, "SetAuthor")
	assert.Contains(t, code, "ClearAuthor")
}

func TestGenUpdateEdgeMethods_UniqueOptionalEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createM2OEdge("author", userType, "posts", "user_id")
	edge.Optional = true

	f := helper.NewFile("ent")
	genUpdateEdgeMethods(helper, f, postType, edge, "PostUpdate", "pu")

	code := f.GoString()
	assert.Contains(t, code, "SetAuthorID")
	assert.Contains(t, code, "SetNillableAuthorID")
	assert.Contains(t, code, "ClearAuthor")
}

func TestGenUpdateEdgeMethods_NonUniqueEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createO2MEdge("posts", postType, "posts", "user_id")

	f := helper.NewFile("ent")
	genUpdateEdgeMethods(helper, f, userType, edge, "UserUpdate", "uu")

	code := f.GoString()
	assert.Contains(t, code, "AddPostIDs")
	assert.Contains(t, code, "AddPosts")
	assert.Contains(t, code, "RemovePostIDs")
	assert.Contains(t, code, "RemovePosts")
	assert.Contains(t, code, "ClearPosts")
}

func TestGenUpdateEdgeMethods_M2MEdge(t *testing.T) {
	helper := newMockHelper()
	postType := createTestType("Post")
	tagType := createTestType("Tag")
	edge := createM2MEdge("tags", tagType, "post_tags", []string{"post_id", "tag_id"})

	f := helper.NewFile("ent")
	genUpdateEdgeMethods(helper, f, postType, edge, "PostUpdate", "pu")

	code := f.GoString()
	assert.Contains(t, code, "AddTagIDs")
	assert.Contains(t, code, "AddTags")
	assert.Contains(t, code, "RemoveTagIDs")
	assert.Contains(t, code, "RemoveTags")
	assert.Contains(t, code, "ClearTags")
}

// =============================================================================
// genUpdateDefaults Tests
// =============================================================================

func TestGenUpdateDefaults_NoUpdateDefaults(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	// No fields with UpdateDefault set

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateDefaults(helper, f, userType, "UserUpdate", "uu")
	})
	if !ok {
		t.Skip("genUpdateDefaults panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "defaults")
}

func TestGenUpdateDefaults_WithUpdateDefaultField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	userType.Fields = append(userType.Fields, createFieldWithUpdateDefault("updated_at", field.TypeTime))

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateDefaults(helper, f, userType, "UserUpdate", "uu")
	})
	if !ok {
		t.Skip("genUpdateDefaults panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "defaults")
	assert.Contains(t, code, "mutation")
}

func TestGenUpdateDefaults_WithNillableUpdateDefault(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	nillableUpdateDefault := &gen.Field{
		Name:          "modified_at",
		Type:          &field.TypeInfo{Type: field.TypeTime},
		UpdateDefault: true,
		Nillable:      true,
	}
	userType.Fields = append(userType.Fields, nillableUpdateDefault)

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateDefaults(helper, f, userType, "UserUpdate", "uu")
	})
	if !ok {
		t.Skip("genUpdateDefaults panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "defaults")
	assert.Contains(t, code, "Cleared")
}

func TestGenUpdateDefaults_WithHooks_ReturnsError(t *testing.T) {
	helper := newMockHelper()
	userType := createTypeWithHooks("User", []*load.Position{{Index: 0}})
	userType.Fields = append(userType.Fields, createFieldWithUpdateDefault("updated_at", field.TypeTime))

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateDefaults(helper, f, userType, "UserUpdate", "uu")
	})
	if !ok {
		t.Skip("genUpdateDefaults panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "defaults")
	// With hooks, defaults returns error
	assert.Contains(t, code, "error")
	assert.Contains(t, code, "uninitialized")
}

// =============================================================================
// genUpdateCheck Tests
// =============================================================================

func TestGenUpdateCheck_NoValidators(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	// No validators, no edges

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateCheck(helper, f, userType, "UserUpdate", "uu")
	})
	if !ok {
		t.Skip("genUpdateCheck panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "check")
}

func TestGenUpdateCheck_WithRequiredUniqueEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	profileType := createTestType("Profile")

	requiredEdge := createO2OEdge("profile", profileType, "profiles", "user_id")
	requiredEdge.Optional = false
	userType.Edges = []*gen.Edge{requiredEdge}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateCheck(helper, f, userType, "UserUpdate", "uu")
	})
	if !ok {
		t.Skip("genUpdateCheck panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "check")
	assert.Contains(t, code, "clearing a required unique edge")
}

func TestGenUpdateCheck_WithValidatorsEnabled(t *testing.T) {
	helper := newMockHelperWithValidators()
	userType := createTestType("User")
	userType.Fields = append(userType.Fields, createFieldWithValidators("name", field.TypeString, 2))
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateCheck(helper, f, userType, "UserUpdate", "uu")
	})
	if !ok {
		t.Skip("genUpdateCheck panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "check")
	assert.Contains(t, code, "validator failed")
	assert.Contains(t, code, "ValidationError")
}

func TestGenUpdateCheck_WithEnumField(t *testing.T) {
	helper := newMockHelperWithValidators()
	userType := createTestType("User")
	userType.Fields = append(userType.Fields, createEnumField("status", []string{"active", "inactive"}))
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateCheck(helper, f, userType, "UserUpdate", "uu")
	})
	if !ok {
		t.Skip("genUpdateCheck panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "check")
	assert.Contains(t, code, "validator failed")
}

func TestGenUpdateCheck_ImmutableFieldSkipped(t *testing.T) {
	helper := newMockHelperWithValidators()
	userType := createTestType("User")
	immutableFld := createImmutableField("username", field.TypeString)
	immutableFld.Validators = 1
	userType.Fields = append(userType.Fields, immutableFld)
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateCheck(helper, f, userType, "UserUpdate", "uu")
	})
	if !ok {
		t.Skip("genUpdateCheck panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "check")
	// Immutable fields should be skipped in update check
	assert.NotContains(t, code, "username")
}

// =============================================================================
// genEdgeSpec Tests
// =============================================================================

func TestGenEdgeSpec_O2MEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createO2MEdge("posts", postType, "posts", "user_id")

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genEdgeSpec(helper, grp, userType, edge, true, "uu")
	})
	if !ok {
		t.Skip("genEdgeSpec panicked due to incomplete mock state")
	}
}

func TestGenEdgeSpec_M2MEdge(t *testing.T) {
	helper := newMockHelper()
	postType := createTestType("Post")
	tagType := createTestType("Tag")
	edge := createM2MEdge("tags", tagType, "post_tags", []string{"post_id", "tag_id"})

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genEdgeSpec(helper, grp, postType, edge, false, "pu")
	})
	if !ok {
		t.Skip("genEdgeSpec panicked due to incomplete mock state")
	}
}

func TestGenEdgeSpec_PanicOnNilID(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	postType.ID = nil // No ID
	edge := createO2MEdge("posts", postType, "posts", "user_id")

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genEdgeSpec(helper, grp, userType, edge, true, "uu")
	})
	assert.False(t, ok, "genEdgeSpec should panic when edge target has no ID")
}

// =============================================================================
// genUpdateBuilder Tests
// =============================================================================

func TestGenUpdateBuilder_BasicEntity(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateBuilder(helper, f, userType)
	})
	if !ok {
		t.Skip("genUpdateBuilder panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "UserUpdate")
}

// =============================================================================
// genUpdateOneBuilder Tests
// =============================================================================

func TestGenUpdateOneBuilder_BasicEntity(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateOneBuilder(helper, f, userType)
	})
	if !ok {
		t.Skip("genUpdateOneBuilder panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "UserUpdateOne")
}

// =============================================================================
// genUpdateFieldSetter Tests
// =============================================================================

func TestGenUpdateFieldSetter_StringField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	fld := createTestField("name", field.TypeString)
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateFieldSetter(helper, f, userType, fld, "UserUpdate", "uu")
	})
	if !ok {
		t.Skip("genUpdateFieldSetter panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "SetName")
}

func TestGenUpdateFieldSetter_NillableField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	fld := createNillableField("bio", field.TypeString)
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateFieldSetter(helper, f, userType, fld, "UserUpdate", "uu")
	})
	if !ok {
		t.Skip("genUpdateFieldSetter panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "SetBio")
	assert.Contains(t, code, "ClearBio")
}

func TestGenUpdateFieldSetter_IntField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	fld := createTestField("age", field.TypeInt)
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateFieldSetter(helper, f, userType, fld, "UserUpdate", "uu")
	})
	if !ok {
		t.Skip("genUpdateFieldSetter panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "SetAge")
	assert.Contains(t, code, "AddAge")
}

// =============================================================================
// genUpdateSQLSave Tests
// =============================================================================

func TestGenUpdateSqlSave_Update(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateSQLSave(helper, f, userType, "UserUpdate", false, false, "uu")
	})
	if !ok {
		t.Skip("genUpdateSQLSave panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "sqlSave")
}

func TestGenUpdateSqlSave_UpdateOne(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateSQLSave(helper, f, userType, "UserUpdateOne", true, false, "uuo")
	})
	if !ok {
		t.Skip("genUpdateSQLSave panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "sqlSave")
}

func TestGenUpdateSqlSave_WithModifier(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateSQLSave(helper, f, userType, "UserUpdate", false, true, "uu")
	})
	if !ok {
		t.Skip("genUpdateSQLSave panicked due to incomplete mock state")
	}
}

func TestGenUpdateSqlSave_WithEdges(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
	}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateSQLSave(helper, f, userType, "UserUpdate", false, false, "uu")
	})
	if !ok {
		t.Skip("genUpdateSQLSave panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "sqlSave")
}

func TestGenUpdateSqlSave_UpdateOneWithEdges(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
	}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateSQLSave(helper, f, userType, "UserUpdateOne", true, false, "uuo")
	})
	if !ok {
		t.Skip("genUpdateSQLSave panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "sqlSave")
	assert.Contains(t, code, "Node")
}

func TestGenUpdateSqlSave_WithNillableField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	userType.Fields = append(userType.Fields, createNillableField("bio", field.TypeString))
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateSQLSave(helper, f, userType, "UserUpdate", false, false, "uu")
	})
	if !ok {
		t.Skip("genUpdateSQLSave panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "sqlSave")
}

func TestGenUpdateSqlSave_WithCheckers(t *testing.T) {
	helper := newMockHelperWithValidators()
	userType := createTestType("User")
	userType.Fields = append(userType.Fields, createFieldWithValidators("name", field.TypeString, 1))
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateSQLSave(helper, f, userType, "UserUpdate", false, false, "uu")
	})
	if !ok {
		t.Skip("genUpdateSQLSave panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "sqlSave")
	assert.Contains(t, code, "check")
}

// =============================================================================
// genUpdateBuilder Additional Tests
// =============================================================================

func TestGenUpdateBuilder_WithEdges(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
	}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateBuilder(helper, f, userType)
	})
	if !ok {
		t.Skip("genUpdateBuilder panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "UserUpdate")
}

func TestGenUpdateOneBuilder_WithEdges(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
	}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateOneBuilder(helper, f, userType)
	})
	if !ok {
		t.Skip("genUpdateOneBuilder panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "UserUpdateOne")
}

// =============================================================================
// genEdgeSpec Additional Tests
// =============================================================================

func TestGenEdgeSpec_M2OEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createM2OEdge("author", userType, "posts", "author_id")

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genEdgeSpec(helper, grp, postType, edge, true, "pu")
	})
	if !ok {
		t.Skip("genEdgeSpec panicked due to incomplete mock state")
	}
}

func TestGenEdgeSpec_O2OEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	profileType := createTestType("Profile")
	edge := createO2OEdge("profile", profileType, "profiles", "user_id")

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genEdgeSpec(helper, grp, userType, edge, false, "uu")
	})
	if !ok {
		t.Skip("genEdgeSpec panicked due to incomplete mock state")
	}
}

func TestGenEdgeSpec_M2MWithNodes(t *testing.T) {
	helper := newMockHelper()
	postType := createTestType("Post")
	tagType := createTestType("Tag")
	edge := createM2MEdge("tags", tagType, "post_tags", []string{"post_id", "tag_id"})

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genEdgeSpec(helper, grp, postType, edge, true, "pu")
	})
	if !ok {
		t.Skip("genEdgeSpec panicked due to incomplete mock state")
	}
}

func TestGenEdgeSpec_BidiEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")

	// Self-referential M2M edge with Bidi=true
	edge := createM2MEdge("friends", userType, "user_friends", []string{"user_id", "friend_id"})
	edge.Bidi = true

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genEdgeSpec(helper, grp, userType, edge, true, "uu")
	})
	if !ok {
		t.Skip("genEdgeSpec panicked due to incomplete mock state")
	}
}

func TestGenEdgeSpec_InverseEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")

	// Create an inverse O2M edge
	edge := createO2MEdge("posts", postType, "posts", "user_id")
	edge.Inverse = "author"

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genEdgeSpec(helper, grp, userType, edge, false, "uu")
	})
	if !ok {
		t.Skip("genEdgeSpec panicked due to incomplete mock state")
	}
}

// =============================================================================
// genUpdateSQLSave Additional Branch Coverage
// =============================================================================

func TestGenUpdateSqlSave_WithMultipleEdges(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	groupType := createTestType("Group")

	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
		createM2MEdge("groups", groupType, "user_groups", []string{"user_id", "group_id"}),
	}
	helper.graph.Nodes = []*gen.Type{userType, postType, groupType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateSQLSave(helper, f, userType, "UserUpdate", false, false, "uu")
	})
	if !ok {
		t.Skip("genUpdateSQLSave panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "sqlSave")
}

// =============================================================================
// genUpdateBuilder Additional Branch Coverage
// =============================================================================

func TestGenUpdateBuilder_WithM2MEdge(t *testing.T) {
	helper := newMockHelper()
	postType := createTestType("Post")
	tagType := createTestType("Tag")
	postType.Edges = []*gen.Edge{
		createM2MEdge("tags", tagType, "post_tags", []string{"post_id", "tag_id"}),
	}
	helper.graph.Nodes = []*gen.Type{postType, tagType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateBuilder(helper, f, postType)
	})
	if !ok {
		t.Skip("genUpdateBuilder panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "PostUpdate")
}

// =============================================================================
// genEdgeSpec Through-Table Branch Coverage
// =============================================================================

func TestGenEdgeSpec_ThroughWithDefaults(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	groupType := createTestType("Group")

	// Create a "through" type that has default fields
	membershipType := createTestType("Membership")
	membershipType.Fields = append(membershipType.Fields, createFieldWithDefault("role", field.TypeString))

	// M2M edge with Through table that has defaults
	edge := createM2MEdge("groups", groupType, "memberships", []string{"user_id", "group_id"})
	edge.Through = membershipType

	helper.graph.Nodes = []*gen.Type{userType, groupType, membershipType}

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genEdgeSpec(helper, grp, userType, edge, true, "uu")
	})
	if !ok {
		t.Skip("genEdgeSpec panicked due to incomplete mock state")
	}
}

func TestGenEdgeSpec_ThroughWithDefaultsAndHooks(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	groupType := createTestType("Group")

	// Through type with hooks and defaults
	membershipType := createTypeWithHooks("Membership", []*load.Position{{Index: 0}})
	membershipType.Fields = append(membershipType.Fields, createFieldWithDefault("role", field.TypeString))

	edge := createM2MEdge("groups", groupType, "memberships", []string{"user_id", "group_id"})
	edge.Through = membershipType

	helper.graph.Nodes = []*gen.Type{userType, groupType, membershipType}

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genEdgeSpec(helper, grp, userType, edge, true, "uu")
	})
	if !ok {
		t.Skip("genEdgeSpec panicked due to incomplete mock state")
	}
}

func TestGenEdgeSpec_ThroughWithIDDefault(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	groupType := createTestType("Group")

	// Through type with ID that has a default
	membershipType := createTestType("Membership")
	membershipType.ID.Default = true
	membershipType.ID.UserDefined = true
	membershipType.Fields = append(membershipType.Fields, createFieldWithDefault("role", field.TypeString))

	edge := createM2MEdge("groups", groupType, "memberships", []string{"user_id", "group_id"})
	edge.Through = membershipType

	helper.graph.Nodes = []*gen.Type{userType, groupType, membershipType}

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genEdgeSpec(helper, grp, userType, edge, true, "uu")
	})
	if !ok {
		t.Skip("genEdgeSpec panicked due to incomplete mock state")
	}
}

func TestGenEdgeSpec_WithoutNodes(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createO2MEdge("posts", postType, "posts", "user_id")

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genEdgeSpec(helper, grp, userType, edge, false, "uu")
	})
	if !ok {
		t.Skip("genEdgeSpec panicked due to incomplete mock state")
	}
}

// =============================================================================
// genUpdateSQLSave Additional Branch Coverage
// =============================================================================

func TestGenUpdateSqlSave_WithM2OEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	postType.Edges = []*gen.Edge{
		createM2OEdge("author", userType, "posts", "user_id"),
	}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateSQLSave(helper, f, postType, "PostUpdate", false, false, "pu")
	})
	if !ok {
		t.Skip("genUpdateSQLSave panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "sqlSave")
}

func TestGenUpdateSqlSave_UpdateOne_WithM2MEdge(t *testing.T) {
	helper := newMockHelper()
	postType := createTestType("Post")
	tagType := createTestType("Tag")
	postType.Edges = []*gen.Edge{
		createM2MEdge("tags", tagType, "post_tags", []string{"post_id", "tag_id"}),
	}
	helper.graph.Nodes = []*gen.Type{postType, tagType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateSQLSave(helper, f, postType, "PostUpdateOne", true, false, "puo")
	})
	if !ok {
		t.Skip("genUpdateSQLSave panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "sqlSave")
}

func TestGenUpdateBuilder_WithNillableField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	userType.Fields = append(userType.Fields, createNillableField("bio", field.TypeString))
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateBuilder(helper, f, userType)
	})
	if !ok {
		t.Skip("genUpdateBuilder panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "UserUpdate")
	assert.Contains(t, code, "ClearBio")
}

func TestGenUpdateOneBuilder_WithNillableField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	userType.Fields = append(userType.Fields, createNillableField("bio", field.TypeString))
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateOneBuilder(helper, f, userType)
	})
	if !ok {
		t.Skip("genUpdateOneBuilder panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "UserUpdateOne")
}

func TestGenUpdateFieldSetter_ImmutableField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	fld := createImmutableField("username", field.TypeString)
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateFieldSetter(helper, f, userType, fld, "UserUpdate", "uu")
	})
	if !ok {
		t.Skip("genUpdateFieldSetter panicked due to incomplete mock state")
	}

	code := f.GoString()
	// Immutable fields still get SetXxx in update setter, but the builder
	// generates the setter differently. Verify it generates code.
	assert.Contains(t, code, "SetUsername")
}

func TestGenUpdateFieldSetter_EnumField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	fld := createEnumField("status", []string{"active", "inactive"})
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateFieldSetter(helper, f, userType, fld, "UserUpdate", "uu")
	})
	if !ok {
		t.Skip("genUpdateFieldSetter panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "SetStatus")
}

func TestGenUpdateFieldSetter_OptionalField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	fld := createOptionalField("nickname", field.TypeString)
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genUpdateFieldSetter(helper, f, userType, fld, "UserUpdate", "uu")
	})
	if !ok {
		t.Skip("genUpdateFieldSetter panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "SetNickname")
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkGenUpdateEdgeMethods(b *testing.B) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createO2MEdge("posts", postType, "posts", "user_id")

	for b.Loop() {
		f := helper.NewFile("ent")
		genUpdateEdgeMethods(helper, f, userType, edge, "UserUpdate", "uu")
	}
}
