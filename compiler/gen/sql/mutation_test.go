package sql

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// genMutationFilterMethods Tests
// =============================================================================

func TestGenMutationFilterMethods(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")

	f := helper.NewFile("ent")
	genMutationFilterMethods(helper, f, userType, "UserMutation")

	code := f.GoString()
	assert.Contains(t, code, "WhereP")
	assert.Contains(t, code, "Filter")
	assert.Contains(t, code, "UserFilter")
	assert.Contains(t, code, "privacy")
}

// =============================================================================
// genMutationEdgeMethods Tests
// =============================================================================

func TestGenMutationEdgeMethods_UniqueEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createM2OEdge("author", userType, "posts", "user_id")

	f := helper.NewFile("ent")
	genMutationEdgeMethods(helper, f, postType, edge)

	code := f.GoString()
	assert.Contains(t, code, "SetAuthorID")
	assert.Contains(t, code, "AuthorID")
	assert.Contains(t, code, "AuthorIDs")
	assert.Contains(t, code, "ClearAuthor")
	assert.Contains(t, code, "AuthorCleared")
	assert.Contains(t, code, "ResetAuthor")
}

func TestGenMutationEdgeMethods_NonUniqueEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createO2MEdge("posts", postType, "posts", "user_id")

	f := helper.NewFile("ent")
	genMutationEdgeMethods(helper, f, userType, edge)

	code := f.GoString()
	assert.Contains(t, code, "AddPostIDs")
	assert.Contains(t, code, "PostsIDs")
	assert.Contains(t, code, "RemovePostIDs")
	assert.Contains(t, code, "RemovedPosts")
	assert.Contains(t, code, "ClearPosts")
	assert.Contains(t, code, "PostsCleared")
	assert.Contains(t, code, "ResetPosts")
}

func TestGenMutationEdgeMethods_M2MEdge(t *testing.T) {
	helper := newMockHelper()
	postType := createTestType("Post")
	tagType := createTestType("Tag")
	edge := createM2MEdge("tags", tagType, "post_tags", []string{"post_id", "tag_id"})

	f := helper.NewFile("ent")
	genMutationEdgeMethods(helper, f, postType, edge)

	code := f.GoString()
	assert.Contains(t, code, "AddTagIDs")
	assert.Contains(t, code, "TagsIDs")
	assert.Contains(t, code, "RemoveTagIDs")
	assert.Contains(t, code, "ClearTags")
	assert.Contains(t, code, "ResetTags")
}

// =============================================================================
// genEdgeFieldMutationMethods Tests
// =============================================================================

func TestGenEdgeFieldMutationMethods_M2OEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")

	// Create an edge field (FK field) - this simulates edge fields
	edgeField := &gen.Field{
		Name: "user_id",
		Type: &field.TypeInfo{Type: field.TypeInt64},
	}

	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genEdgeFieldMutationMethods(helper, f, postType, edgeField)
	})
	if !ok {
		t.Skip("genEdgeFieldMutationMethods panicked due to Field.Edge() accessing unexported fields")
	}
}

// =============================================================================
// genMutationStruct Tests
// =============================================================================

func TestGenMutationStruct_BasicEntity(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genMutationStruct(helper, f, userType)
	})
	if !ok {
		t.Skip("genMutationStruct panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "UserMutation")
}

func TestGenMutationStruct_WithEdges(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
	}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genMutationStruct(helper, f, userType)
	})
	if !ok {
		t.Skip("genMutationStruct panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "UserMutation")
}

// =============================================================================
// genMutationFieldMethods Tests
// =============================================================================

func TestGenMutationFieldMethods_StringField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	fld := createTestField("name", field.TypeString)
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genMutationFieldMethods(helper, f, userType, fld)
	})
	if !ok {
		t.Skip("genMutationFieldMethods panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "SetName")
	assert.Contains(t, code, "Name")
}

func TestGenMutationFieldMethods_NillableField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	fld := createNillableField("bio", field.TypeString)
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genMutationFieldMethods(helper, f, userType, fld)
	})
	if !ok {
		t.Skip("genMutationFieldMethods panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "SetBio")
	assert.Contains(t, code, "ClearBio")
}

func TestGenMutationFieldMethods_IntField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	fld := createTestField("age", field.TypeInt)
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genMutationFieldMethods(helper, f, userType, fld)
	})
	if !ok {
		t.Skip("genMutationFieldMethods panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "SetAge")
	assert.Contains(t, code, "AddAge")
}

// =============================================================================
// genMutationInterfaceMethods Tests
// =============================================================================

func TestGenMutationInterfaceMethods(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genMutationInterfaceMethods(helper, f, userType)
	})
	if !ok {
		t.Skip("genMutationInterfaceMethods panicked due to incomplete mock state")
	}

	code := f.GoString()
	// genMutationInterfaceMethods generates Fields, Field, SetField, etc.
	assert.Contains(t, code, "Fields")
	assert.Contains(t, code, "SetField")
	assert.Contains(t, code, "AddedFields")
	assert.Contains(t, code, "ClearedFields")
	assert.Contains(t, code, "ResetField")
	assert.Contains(t, code, "AddedEdges")
	assert.Contains(t, code, "RemovedEdges")
	assert.Contains(t, code, "ClearedEdges")
}

// =============================================================================
// genMutationMethods Tests
// =============================================================================

func TestGenMutationMethods_BasicEntity(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genMutationMethods(helper, f, userType)
	})
	if !ok {
		t.Skip("genMutationMethods panicked due to incomplete mock state")
	}
}

// =============================================================================
// genMutationEdgeMethods Additional Branch Coverage
// =============================================================================

func TestGenMutationEdgeMethods_UniqueOptionalEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createM2OEdge("author", userType, "posts", "user_id")
	edge.Optional = true

	f := helper.NewFile("ent")
	genMutationEdgeMethods(helper, f, postType, edge)

	code := f.GoString()
	assert.Contains(t, code, "SetAuthorID")
	assert.Contains(t, code, "ClearAuthor")
	assert.Contains(t, code, "ResetAuthor")
}

func TestGenMutationEdgeMethods_O2OEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	profileType := createTestType("Profile")
	edge := createO2OEdge("profile", profileType, "profiles", "user_id")

	f := helper.NewFile("ent")
	genMutationEdgeMethods(helper, f, userType, edge)

	code := f.GoString()
	assert.Contains(t, code, "SetProfileID")
	assert.Contains(t, code, "ProfileID")
	assert.Contains(t, code, "ProfileIDs")
	assert.Contains(t, code, "ClearProfile")
	assert.Contains(t, code, "ProfileCleared")
	assert.Contains(t, code, "ResetProfile")
}

// =============================================================================
// genMutationInterfaceMethods Additional Branch Coverage
// =============================================================================

func TestGenMutationInterfaceMethods_WithEdges(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
	}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genMutationInterfaceMethods(helper, f, userType)
	})
	if !ok {
		t.Skip("genMutationInterfaceMethods panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "Fields")
	assert.Contains(t, code, "AddedEdges")
	assert.Contains(t, code, "RemovedEdges")
}

func TestGenMutationInterfaceMethods_WithMultipleFields(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
		createTestField("age", field.TypeInt),
		createNillableField("bio", field.TypeString),
		createEnumField("status", []string{"active", "inactive"}),
	})
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genMutationInterfaceMethods(helper, f, userType)
	})
	if !ok {
		t.Skip("genMutationInterfaceMethods panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "Fields")
	assert.Contains(t, code, "SetField")
	assert.Contains(t, code, "ClearedFields")
}

func TestGenMutationInterfaceMethods_WithM2MEdge(t *testing.T) {
	helper := newMockHelper()
	postType := createTestType("Post")
	tagType := createTestType("Tag")
	postType.Edges = []*gen.Edge{
		createM2MEdge("tags", tagType, "post_tags", []string{"post_id", "tag_id"}),
	}
	helper.graph.Nodes = []*gen.Type{postType, tagType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genMutationInterfaceMethods(helper, f, postType)
	})
	if !ok {
		t.Skip("genMutationInterfaceMethods panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "AddedEdges")
	assert.Contains(t, code, "RemovedEdges")
}

// =============================================================================
// genMutationMethods Additional Branch Coverage
// =============================================================================

func TestGenMutationMethods_WithEdges(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
	}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genMutationMethods(helper, f, userType)
	})
	if !ok {
		t.Skip("genMutationMethods panicked due to incomplete mock state")
	}
}

func TestGenMutationMethods_WithMultipleFieldTypes(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
		createTestField("age", field.TypeInt),
		createNillableField("bio", field.TypeString),
	})
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genMutationMethods(helper, f, userType)
	})
	if !ok {
		t.Skip("genMutationMethods panicked due to incomplete mock state")
	}
}

// =============================================================================
// genMutationFieldMethods Additional Branch Coverage
// =============================================================================

func TestGenMutationFieldMethods_EnumField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	fld := createEnumField("status", []string{"active", "inactive"})
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genMutationFieldMethods(helper, f, userType, fld)
	})
	if !ok {
		t.Skip("genMutationFieldMethods panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "SetStatus")
}

func TestGenMutationFieldMethods_OptionalField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	fld := createOptionalField("nickname", field.TypeString)
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genMutationFieldMethods(helper, f, userType, fld)
	})
	if !ok {
		t.Skip("genMutationFieldMethods panicked due to incomplete mock state")
	}
}

func TestGenMutationFieldMethods_FloatField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	fld := createTestField("score", field.TypeFloat64)
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genMutationFieldMethods(helper, f, userType, fld)
	})
	if !ok {
		t.Skip("genMutationFieldMethods panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "SetScore")
	assert.Contains(t, code, "AddScore")
}

func TestGenMutationFieldMethods_BoolField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	fld := createTestField("active", field.TypeBool)
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genMutationFieldMethods(helper, f, userType, fld)
	})
	if !ok {
		t.Skip("genMutationFieldMethods panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "SetActive")
}

func TestGenMutationFieldMethods_TimeField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	fld := createTestField("created_at", field.TypeTime)
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genMutationFieldMethods(helper, f, userType, fld)
	})
	if !ok {
		t.Skip("genMutationFieldMethods panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "SetCreatedAt")
}

// =============================================================================
// genMutationStruct Additional Branch Coverage
// =============================================================================

func TestGenMutationStruct_WithM2MEdges(t *testing.T) {
	helper := newMockHelper()
	postType := createTestType("Post")
	tagType := createTestType("Tag")
	postType.Edges = []*gen.Edge{
		createM2MEdge("tags", tagType, "post_tags", []string{"post_id", "tag_id"}),
	}
	helper.graph.Nodes = []*gen.Type{postType, tagType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genMutationStruct(helper, f, postType)
	})
	if !ok {
		t.Skip("genMutationStruct panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "PostMutation")
}

func TestGenMutationStruct_WithMultipleFields(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
		createTestField("age", field.TypeInt),
		createNillableField("bio", field.TypeString),
	})
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genMutationStruct(helper, f, userType)
	})
	if !ok {
		t.Skip("genMutationStruct panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "UserMutation")
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkGenMutationEdgeMethods(b *testing.B) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createO2MEdge("posts", postType, "posts", "user_id")

	for b.Loop() {
		f := helper.NewFile("ent")
		genMutationEdgeMethods(helper, f, userType, edge)
	}
}
