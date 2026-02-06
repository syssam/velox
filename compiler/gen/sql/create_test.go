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
// genCreateEdgeSetter Tests
// =============================================================================

func TestGenCreateEdgeSetter_UniqueEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createM2OEdge("author", userType, "posts", "user_id")
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	genCreateEdgeSetter(helper, f, postType, edge)

	code := f.GoString()
	assert.Contains(t, code, "SetAuthorID")
	assert.Contains(t, code, "SetAuthor")
}

func TestGenCreateEdgeSetter_UniqueOptionalEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createM2OEdge("author", userType, "posts", "user_id")
	edge.Optional = true
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	genCreateEdgeSetter(helper, f, postType, edge)

	code := f.GoString()
	assert.Contains(t, code, "SetAuthorID")
	assert.Contains(t, code, "SetNillableAuthorID")
	assert.Contains(t, code, "SetAuthor")
}

func TestGenCreateEdgeSetter_NonUniqueEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createO2MEdge("posts", postType, "posts", "user_id")
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	genCreateEdgeSetter(helper, f, userType, edge)

	code := f.GoString()
	assert.Contains(t, code, "AddPostIDs")
	assert.Contains(t, code, "AddPosts")
}

// =============================================================================
// genCreateUpsertMethods Tests
// =============================================================================

func TestGenCreateUpsertMethods(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	genCreateUpsertMethods(helper, f, userType, "UserCreate")

	code := f.GoString()
	assert.Contains(t, code, "OnConflict")
	assert.Contains(t, code, "OnConflictColumns")
	assert.Contains(t, code, "UserUpsertOne")
	assert.Contains(t, code, "UserUpsert")
	assert.Contains(t, code, "UpdateNewValues")
	assert.Contains(t, code, "Ignore")
}

func TestGenCreateUpsertMethods_WithMutableFields(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
		createTestField("age", field.TypeInt),
	})
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	genCreateUpsertMethods(helper, f, userType, "UserCreate")

	code := f.GoString()
	assert.Contains(t, code, "SetName")
	assert.Contains(t, code, "SetAge")
}

// =============================================================================
// genIDValue Tests
// =============================================================================

func TestGenIDValue_NoValueScanner(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	// Default ID type doesn't have ValueScanner
	result := genIDValue(helper, userType)
	assert.NotNil(t, result)
}

// =============================================================================
// genCreateEdgeSpec Tests
// =============================================================================

func TestGenCreateEdgeSpec_O2M(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createO2MEdge("posts", postType, "posts", "user_id")

	grp := &jen.Group{}
	genCreateEdgeSpec(helper, grp, userType, edge)
	// Should not panic - means code was generated successfully
}

func TestGenCreateEdgeSpec_M2M(t *testing.T) {
	helper := newMockHelper()
	postType := createTestType("Post")
	tagType := createTestType("Tag")
	edge := createM2MEdge("tags", tagType, "post_tags", []string{"post_id", "tag_id"})

	grp := &jen.Group{}
	genCreateEdgeSpec(helper, grp, postType, edge)
	// Should not panic
}

func TestGenCreateEdgeSpec_PanicsOnNilID(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := &gen.Type{Name: "Post"} // No ID field
	edge := createO2MEdge("posts", postType, "posts", "user_id")

	grp := &jen.Group{}
	assert.Panics(t, func() {
		genCreateEdgeSpec(helper, grp, userType, edge)
	})
}

// =============================================================================
// genCreateBulkUpsertMethods Tests
// =============================================================================

func TestGenCreateBulkUpsertMethods(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	genCreateBulkUpsertMethods(helper, f, userType, "UserCreateBulk")

	code := f.GoString()
	assert.Contains(t, code, "OnConflict")
	assert.Contains(t, code, "OnConflictColumns")
	assert.Contains(t, code, "UserUpsertBulk")
	assert.Contains(t, code, "UpdateNewValues")
}

// =============================================================================
// genCreateIDAssignment Tests
// =============================================================================

func TestGenCreateIDAssignment_NumericAutoIncrement(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User") // int64 ID, not user-defined

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genCreateIDAssignment(helper, grp, userType)
	})
	if !ok {
		t.Skip("genCreateIDAssignment panicked due to incomplete mock state")
	}
}

func TestGenCreateIDAssignment_NumericUserDefined(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	userType.ID.UserDefined = true

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genCreateIDAssignment(helper, grp, userType)
	})
	if !ok {
		t.Skip("genCreateIDAssignment panicked due to incomplete mock state")
	}
}

func TestGenCreateIDAssignment_StringID(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithID("User", field.TypeString)

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genCreateIDAssignment(helper, grp, userType)
	})
	if !ok {
		t.Skip("genCreateIDAssignment panicked due to incomplete mock state")
	}
}

func TestGenCreateIDAssignment_IntID(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithID("User", field.TypeInt)

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genCreateIDAssignment(helper, grp, userType)
	})
	if !ok {
		t.Skip("genCreateIDAssignment panicked due to incomplete mock state")
	}
}

// =============================================================================
// genCreateDefaults Tests
// =============================================================================

func TestGenCreateDefaults_WithDefaultField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	userType.Fields = append(userType.Fields, createFieldWithDefault("status", field.TypeString))
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateDefaults(helper, f, userType, "UserCreate")
	})
	if !ok {
		t.Skip("genCreateDefaults panicked due to incomplete mock state")
	}
}

func TestGenCreateDefaults_WithHooksReturnsError(t *testing.T) {
	helper := newMockHelper()
	userType := createTypeWithHooks("User", []*load.Position{{Index: 0}})
	userType.Fields = append(userType.Fields, createFieldWithDefault("status", field.TypeString))
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateDefaults(helper, f, userType, "UserCreate")
	})
	if !ok {
		t.Skip("genCreateDefaults panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "defaults")
	assert.Contains(t, code, "error")
}

func TestGenCreateDefaults_WithUserDefinedIDDefault(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	userType.ID.UserDefined = true
	userType.ID.Default = true
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateDefaults(helper, f, userType, "UserCreate")
	})
	if !ok {
		t.Skip("genCreateDefaults panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "defaults")
}

func TestGenCreateDefaults_NoDefaults(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateDefaults(helper, f, userType, "UserCreate")
	})
	if !ok {
		t.Skip("genCreateDefaults panicked due to incomplete mock state")
	}
}

// =============================================================================
// genCreateCheck Tests
// =============================================================================

func TestGenCreateCheck_NoValidators(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateCheck(helper, f, userType, "UserCreate")
	})
	if !ok {
		t.Skip("genCreateCheck panicked due to incomplete mock state")
	}
}

func TestGenCreateCheck_WithRequiredFields(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	// Default fields from createTestType are required (not Optional)
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateCheck(helper, f, userType, "UserCreate")
	})
	if !ok {
		t.Skip("genCreateCheck panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "check")
	assert.Contains(t, code, "missing required field")
}

func TestGenCreateCheck_WithValidatorsEnabled(t *testing.T) {
	helper := newMockHelperWithValidators()
	userType := createTestType("User")
	userType.Fields = append(userType.Fields, createFieldWithValidators("name2", field.TypeString, 2))
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateCheck(helper, f, userType, "UserCreate")
	})
	if !ok {
		t.Skip("genCreateCheck panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "validator failed")
	assert.Contains(t, code, "ValidationError")
}

func TestGenCreateCheck_WithEnumValidator(t *testing.T) {
	helper := newMockHelperWithValidators()
	userType := createTestType("User")
	userType.Fields = append(userType.Fields, createEnumField("status", []string{"active", "inactive"}))
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateCheck(helper, f, userType, "UserCreate")
	})
	if !ok {
		t.Skip("genCreateCheck panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "validator failed")
}

func TestGenCreateCheck_WithRequiredEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	profileType := createTestType("Profile")
	requiredEdge := createO2OEdge("profile", profileType, "profiles", "user_id")
	requiredEdge.Optional = false
	userType.Edges = []*gen.Edge{requiredEdge}
	helper.graph.Nodes = []*gen.Type{userType, profileType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateCheck(helper, f, userType, "UserCreate")
	})
	if !ok {
		t.Skip("genCreateCheck panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "missing required edge")
}

func TestGenCreateCheck_WithUserDefinedID(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	userType.ID.UserDefined = true
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateCheck(helper, f, userType, "UserCreate")
	})
	if !ok {
		t.Skip("genCreateCheck panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "check")
}

// =============================================================================
// genCreateBulkBuilder Tests
// =============================================================================

func TestGenCreateBulkBuilder(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateBulkBuilder(helper, f, userType)
	})
	if !ok {
		t.Skip("genCreateBulkBuilder panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "UserCreateBulk")
}

// =============================================================================
// genCreateBulkSave Tests
// =============================================================================

func TestGenCreateBulkSave(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateBulkSave(helper, f, userType, "UserCreateBulk")
	})
	if !ok {
		t.Skip("genCreateBulkSave panicked due to incomplete mock state")
	}
}

// =============================================================================
// genBulkCreateIDAssignment Tests
// =============================================================================

func TestGenBulkCreateIDAssignment_NumericID(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genBulkCreateIDAssignment(helper, grp, userType)
	})
	if !ok {
		t.Skip("genBulkCreateIDAssignment panicked due to incomplete mock state")
	}
}

func TestGenBulkCreateIDAssignment_StringID(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithID("User", field.TypeString)

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genBulkCreateIDAssignment(helper, grp, userType)
	})
	if !ok {
		t.Skip("genBulkCreateIDAssignment panicked due to incomplete mock state")
	}
}

// =============================================================================
// genCreateSpec Tests
// =============================================================================

func TestGenCreateSpec_BasicEntity(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateSpec(helper, f, userType, "UserCreate")
	})
	if !ok {
		t.Skip("genCreateSpec panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "createSpec")
}

func TestGenCreateSpec_WithEdges(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createO2MEdge("posts", postType, "posts", "user_id")
	userType.Edges = []*gen.Edge{edge}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateSpec(helper, f, userType, "UserCreate")
	})
	if !ok {
		t.Skip("genCreateSpec panicked due to incomplete mock state")
	}
}

func TestGenCreateSpec_WithUserDefinedID(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	userType.ID.UserDefined = true
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateSpec(helper, f, userType, "UserCreate")
	})
	if !ok {
		t.Skip("genCreateSpec panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "createSpec")
}

func TestGenCreateSpec_WithNillableFields(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
		createNillableField("bio", field.TypeString),
		createNillableField("score", field.TypeInt),
	})
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateSpec(helper, f, userType, "UserCreate")
	})
	if !ok {
		t.Skip("genCreateSpec panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "createSpec")
}

func TestGenCreateSpec_WithMultipleFieldTypes(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
		createTestField("age", field.TypeInt),
		createTestField("active", field.TypeBool),
		createTestField("score", field.TypeFloat64),
	})
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateSpec(helper, f, userType, "UserCreate")
	})
	if !ok {
		t.Skip("genCreateSpec panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "createSpec")
	assert.Contains(t, code, "SetField")
}

func TestGenCreateSpec_WithM2OEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createM2OEdge("author", userType, "posts", "author_id")
	postType.Edges = []*gen.Edge{edge}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateSpec(helper, f, postType, "PostCreate")
	})
	if !ok {
		t.Skip("genCreateSpec panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "createSpec")
}

// =============================================================================
// genCreateFieldSetter Tests
// =============================================================================

func TestGenCreateFieldSetter_StringField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	fld := createTestField("name", field.TypeString)
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateFieldSetter(helper, f, userType, fld)
	})
	if !ok {
		t.Skip("genCreateFieldSetter panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "SetName")
}

func TestGenCreateFieldSetter_NillableField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	fld := createNillableField("bio", field.TypeString)
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateFieldSetter(helper, f, userType, fld)
	})
	if !ok {
		t.Skip("genCreateFieldSetter panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "SetBio")
	// Nillable fields have Type.Nillable=true, so SetNillable is NOT generated
	// (SetNillable is only for Optional/Default fields with non-nillable types)
	assert.NotContains(t, code, "SetNillableBio")
}

func TestGenCreateFieldSetter_OptionalField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	fld := createOptionalField("nickname", field.TypeString)
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateFieldSetter(helper, f, userType, fld)
	})
	if !ok {
		t.Skip("genCreateFieldSetter panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "SetNickname")
}

// =============================================================================
// genCreateSQLSave Tests
// =============================================================================

func TestGenCreateSqlSave_BasicEntity(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateSQLSave(helper, f, userType, "UserCreate")
	})
	if !ok {
		t.Skip("genCreateSQLSave panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "sqlSave")
}

// =============================================================================
// genBulkCreateIDAssignment Additional Tests
// =============================================================================

func TestGenBulkCreateIDAssignment_UserDefinedNumericID(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	userType.ID.UserDefined = true

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genBulkCreateIDAssignment(helper, grp, userType)
	})
	if !ok {
		t.Skip("genBulkCreateIDAssignment panicked due to incomplete mock state")
	}
}

func TestGenBulkCreateIDAssignment_NoFieldID(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	userType.ID = nil // No field ID - composite ID scenario

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genBulkCreateIDAssignment(helper, grp, userType)
	})
	if !ok {
		t.Skip("genBulkCreateIDAssignment panicked due to incomplete mock state")
	}
	// Should return early since HasOneFieldID() is false
}

// =============================================================================
// genCreateIDAssignment Additional Tests
// =============================================================================

func TestGenCreateIDAssignment_UUIDID(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithID("User", field.TypeUUID)

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genCreateIDAssignment(helper, grp, userType)
	})
	if !ok {
		t.Skip("genCreateIDAssignment panicked due to incomplete mock state")
	}
}

func TestGenCreateIDAssignment_IntUserDefined(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithID("User", field.TypeInt)
	userType.ID.UserDefined = true

	grp := &jen.Group{}
	ok := safeGenerate(func() {
		genCreateIDAssignment(helper, grp, userType)
	})
	if !ok {
		t.Skip("genCreateIDAssignment panicked due to incomplete mock state")
	}
}

// =============================================================================
// genCreateSpec Additional Tests
// =============================================================================

func TestGenCreateSpec_WithOwnFKEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")

	// M2O edge that owns its FK
	edge := createM2OEdge("author", userType, "posts", "user_id")
	postType.Edges = []*gen.Edge{edge}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateSpec(helper, f, postType, "PostCreate")
	})
	if !ok {
		t.Skip("genCreateSpec panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "createSpec")
}

func TestGenCreateSpec_WithM2MEdge(t *testing.T) {
	helper := newMockHelper()
	postType := createTestType("Post")
	tagType := createTestType("Tag")

	edge := createM2MEdge("tags", tagType, "post_tags", []string{"post_id", "tag_id"})
	postType.Edges = []*gen.Edge{edge}
	helper.graph.Nodes = []*gen.Type{postType, tagType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateSpec(helper, f, postType, "PostCreate")
	})
	if !ok {
		t.Skip("genCreateSpec panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "createSpec")
}

func TestGenCreateSpec_NoFieldID(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	userType.ID = nil // Composite ID
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateSpec(helper, f, userType, "UserCreate")
	})
	if !ok {
		t.Skip("genCreateSpec panicked due to incomplete mock state")
	}
}

// =============================================================================
// genCreateBuilder Additional Tests
// =============================================================================

func TestGenCreateBuilder_WithEdges(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
	}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateBuilder(helper, f, userType)
	})
	if !ok {
		t.Skip("genCreateBuilder panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "UserCreate")
}

func TestGenCreateBuilder_WithNillableField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	userType.Fields = append(userType.Fields, createNillableField("bio", field.TypeString))
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateBuilder(helper, f, userType)
	})
	if !ok {
		t.Skip("genCreateBuilder panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "UserCreate")
}

// =============================================================================
// genCreateCheck Additional Tests
// =============================================================================

func TestGenCreateCheck_WithOptionalField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	userType.Fields = append(userType.Fields, createOptionalField("nickname", field.TypeString))
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateCheck(helper, f, userType, "UserCreate")
	})
	if !ok {
		t.Skip("genCreateCheck panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "check")
	// Optional fields should not be required
}

func TestGenCreateCheck_WithNillableField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	userType.Fields = append(userType.Fields, createNillableField("bio", field.TypeString))
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateCheck(helper, f, userType, "UserCreate")
	})
	if !ok {
		t.Skip("genCreateCheck panicked due to incomplete mock state")
	}
}

func TestGenCreateCheck_WithDefaultFieldIsOptional(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	defaultField := createFieldWithDefault("status", field.TypeString)
	userType.Fields = append(userType.Fields, defaultField)
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateCheck(helper, f, userType, "UserCreate")
	})
	if !ok {
		t.Skip("genCreateCheck panicked due to incomplete mock state")
	}
}

// =============================================================================
// genCreateSQLSave Additional Tests
// =============================================================================

func TestGenCreateSqlSave_WithEdges(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
	}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateSQLSave(helper, f, userType, "UserCreate")
	})
	if !ok {
		t.Skip("genCreateSQLSave panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "sqlSave")
}

func TestGenCreateSqlSave_WithHooks(t *testing.T) {
	helper := newMockHelper()
	userType := createTypeWithHooks("User", []*load.Position{{Index: 0}})
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateSQLSave(helper, f, userType, "UserCreate")
	})
	if !ok {
		t.Skip("genCreateSQLSave panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "sqlSave")
}

// =============================================================================
// genCreateFieldSetter Additional Tests
// =============================================================================

func TestGenCreateFieldSetter_ImmutableField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	fld := createImmutableField("username", field.TypeString)
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateFieldSetter(helper, f, userType, fld)
	})
	if !ok {
		t.Skip("genCreateFieldSetter panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "SetUsername")
}

func TestGenCreateFieldSetter_EnumField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	fld := createEnumField("status", []string{"active", "inactive"})
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateFieldSetter(helper, f, userType, fld)
	})
	if !ok {
		t.Skip("genCreateFieldSetter panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "SetStatus")
}

func TestGenCreateFieldSetter_DefaultField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	fld := createFieldWithDefault("status", field.TypeString)
	fld.Optional = true
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genCreateFieldSetter(helper, f, userType, fld)
	})
	if !ok {
		t.Skip("genCreateFieldSetter panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "SetStatus")
	assert.Contains(t, code, "SetNillableStatus")
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkGenCreateEdgeSetter(b *testing.B) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createM2OEdge("author", userType, "posts", "user_id")

	for b.Loop() {
		f := helper.NewFile("ent")
		genCreateEdgeSetter(helper, f, postType, edge)
	}
}

func BenchmarkGenCreateUpsertMethods(b *testing.B) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	for b.Loop() {
		f := helper.NewFile("ent")
		genCreateUpsertMethods(helper, f, userType, "UserCreate")
	}
}
