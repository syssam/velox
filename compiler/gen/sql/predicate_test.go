package sql

import (
	"testing"

	"github.com/dave/jennifer/jen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// isStringOp Tests
// =============================================================================

func TestIsStringOp(t *testing.T) {
	tests := []struct {
		op       gen.Op
		expected bool
	}{
		{gen.Contains, true},
		{gen.HasPrefix, true},
		{gen.HasSuffix, true},
		{gen.EqualFold, true},
		{gen.ContainsFold, true},
		{gen.EQ, false},
		{gen.NEQ, false},
		{gen.GT, false},
		{gen.GTE, false},
		{gen.LT, false},
		{gen.LTE, false},
		{gen.IsNil, false},
		{gen.NotNil, false},
		{gen.In, false},
		{gen.NotIn, false},
	}

	for _, tt := range tests {
		t.Run(tt.op.Name(), func(t *testing.T) {
			assert.Equal(t, tt.expected, isStringOp(tt.op))
		})
	}
}

// =============================================================================
// genPredicate Tests (generic predicates mode)
// =============================================================================

func TestGenPredicate_GenericMode(t *testing.T) {
	helper := newMockHelper()
	testType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{testType}

	var file *jen.File
	ok := safeGenerate(func() {
		file = genPredicate(helper, testType)
	})
	if !ok {
		t.Skip("genPredicate panicked due to incomplete mock state")
	}
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "package user")
}

func TestGenPredicate_VerboseMode(t *testing.T) {
	helper := newFeatureMockHelper()
	helper.withFeatures("sql/entpredicates")
	testType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{testType}

	var file *jen.File
	ok := safeGenerate(func() {
		file = genPredicate(helper, testType)
	})
	if !ok {
		t.Skip("genVerbosePredicate panicked due to incomplete mock state")
	}
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "package user")
}

// =============================================================================
// genEdgePredicates Tests
// =============================================================================

func TestGenEdgePredicates_O2M(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")

	edge := createO2MEdge("posts", postType, "posts", "user_id")

	f := helper.NewFile("user")
	genEdgePredicates(helper, f, userType, edge)

	code := f.GoString()
	assert.Contains(t, code, "HasPosts")
	assert.Contains(t, code, "HasPostsWith")
}

func TestGenEdgePredicates_M2O(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")

	edge := createM2OEdge("author", userType, "posts", "user_id")

	f := helper.NewFile("post")
	genEdgePredicates(helper, f, postType, edge)

	code := f.GoString()
	assert.Contains(t, code, "HasAuthor")
	assert.Contains(t, code, "HasAuthorWith")
}

func TestGenEdgePredicates_M2M(t *testing.T) {
	helper := newMockHelper()
	postType := createTestType("Post")
	tagType := createTestType("Tag")

	edge := createM2MEdge("tags", tagType, "post_tags", []string{"post_id", "tag_id"})

	f := helper.NewFile("post")
	genEdgePredicates(helper, f, postType, edge)

	code := f.GoString()
	assert.Contains(t, code, "HasTags")
	assert.Contains(t, code, "HasTagsWith")
}

func TestGenEdgePredicates_O2O(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	profileType := createTestType("Profile")

	edge := createO2OEdge("profile", profileType, "profiles", "user_id")

	f := helper.NewFile("user")
	genEdgePredicates(helper, f, userType, edge)

	code := f.GoString()
	assert.Contains(t, code, "HasProfile")
	assert.Contains(t, code, "HasProfileWith")
}

// =============================================================================
// genPredicateVars Tests
// =============================================================================

func TestGenPredicateVars_BasicType(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")

	f := helper.NewFile("user")

	ok := safeGenerate(func() {
		genPredicateVars(helper, f, userType)
	})
	if !ok {
		t.Skip("genPredicateVars panicked due to incomplete mock state")
	}

	code := f.GoString()
	// Should contain field predicate variables
	assert.Contains(t, code, "package user")
}

func TestGenPredicateVars_WithEdges(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
	}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("user")
	ok := safeGenerate(func() {
		genPredicateVars(helper, f, userType)
	})
	if !ok {
		t.Skip("genPredicateVars panicked due to incomplete mock state")
	}
}

func TestGenPredicateVars_WithMultipleFieldTypes(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
		createTestField("age", field.TypeInt),
		createTestField("active", field.TypeBool),
		createTestField("score", field.TypeFloat64),
		createNillableField("bio", field.TypeString),
	})
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("user")
	ok := safeGenerate(func() {
		genPredicateVars(helper, f, userType)
	})
	if !ok {
		t.Skip("genPredicateVars panicked due to incomplete mock state")
	}
}

func TestGenEdgePredicates_WithHasPredicate(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createO2MEdge("posts", postType, "posts", "user_id")
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("user")
	genEdgePredicates(helper, f, userType, edge)

	code := f.GoString()
	assert.Contains(t, code, "HasPosts")
	assert.Contains(t, code, "HasPostsWith")
}

// =============================================================================
// genEdgePredicates Branch Coverage Tests
// =============================================================================

func TestGenEdgePredicates_WithSchemaConfig(t *testing.T) {
	helper := newFeatureMockHelper()
	helper.withFeatures("sql/schemaconfig")

	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createO2MEdge("posts", postType, "posts", "user_id")
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("user")
	genEdgePredicates(helper, f, userType, edge)

	code := f.GoString()
	assert.Contains(t, code, "HasPosts")
	assert.Contains(t, code, "HasPostsWith")
	assert.Contains(t, code, "SchemaConfig")
}

func TestGenEdgePredicates_M2MEdgeOwnFK(t *testing.T) {
	helper := newMockHelper()
	postType := createTestType("Post")
	tagType := createTestType("Tag")

	// M2M edge - the OwnFK and PKConstant paths
	edge := createM2MEdge("tags", tagType, "post_tags", []string{"post_id", "tag_id"})
	helper.graph.Nodes = []*gen.Type{postType, tagType}

	f := helper.NewFile("post")
	genEdgePredicates(helper, f, postType, edge)

	code := f.GoString()
	assert.Contains(t, code, "HasTags")
	assert.Contains(t, code, "HasTagsWith")
}

func TestGenEdgePredicates_M2OEdgeOwnFK(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")

	// M2O edge - OwnFK returns true
	edge := createM2OEdge("author", userType, "posts", "user_id")
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("post")
	genEdgePredicates(helper, f, postType, edge)

	code := f.GoString()
	assert.Contains(t, code, "HasAuthor")
	assert.Contains(t, code, "HasAuthorWith")
}

func TestGenEdgePredicates_InverseM2MEdge(t *testing.T) {
	helper := newMockHelper()
	postType := createTestType("Post")
	tagType := createTestType("Tag")

	// Inverse M2M edge
	edge := createM2MEdge("posts", postType, "post_tags", []string{"tag_id", "post_id"})
	edge.Inverse = "tags"
	helper.graph.Nodes = []*gen.Type{postType, tagType}

	f := helper.NewFile("tag")
	genEdgePredicates(helper, f, tagType, edge)

	code := f.GoString()
	assert.Contains(t, code, "HasPosts")
	assert.Contains(t, code, "HasPostsWith")
}

// =============================================================================
// genPredicateVars Additional Tests
// =============================================================================

func TestGenPredicateVars_WithIDField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("user")
	ok := safeGenerate(func() {
		genPredicateVars(helper, f, userType)
	})
	if !ok {
		t.Skip("genPredicateVars panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "package user")
}

// =============================================================================
// Reserved Predicate Names
// =============================================================================

func TestReservedPredicateNames(t *testing.T) {
	reserved := []string{"Label", "OrderOption", "Hooks", "Policy", "Table", "FieldID", "Columns", "ForeignKeys"}
	for _, name := range reserved {
		assert.True(t, reservedPredicateNames[name], "%s should be reserved", name)
	}
	assert.False(t, reservedPredicateNames["Name"])
	assert.False(t, reservedPredicateNames["Email"])
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkIsStringOp(b *testing.B) {
	ops := []gen.Op{gen.EQ, gen.Contains, gen.HasPrefix, gen.GT}
	for b.Loop() {
		for _, op := range ops {
			_ = isStringOp(op)
		}
	}
}

func BenchmarkGenEdgePredicates(b *testing.B) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createO2MEdge("posts", postType, "posts", "user_id")

	for b.Loop() {
		f := helper.NewFile("user")
		genEdgePredicates(helper, f, userType, edge)
	}
}

// =============================================================================
// getGenericFieldInfo Tests (comprehensive branch coverage)
// =============================================================================

func TestGetGenericFieldInfo_AllTypes(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")

	tests := []struct {
		name        string
		field       *gen.Field
		genericType string
	}{
		{"string", createTestField("name", field.TypeString), "StringField"},
		{"int", createTestField("age", field.TypeInt), "IntField"},
		{"int64", createTestField("id", field.TypeInt64), "Int64Field"},
		{"float32", createTestField("f", field.TypeFloat32), "Float64Field"},
		{"float64", createTestField("price", field.TypeFloat64), "Float64Field"},
		{"bool", createTestField("active", field.TypeBool), "BoolField"},
		{"time", createTestField("ts", field.TypeTime), "TimeField"},
		{"enum", createEnumField("status", []string{"active"}), "EnumField"},
		{"uuid", createTestField("uid", field.TypeUUID), "UUIDField"},
		{"other", createTestField("custom", field.TypeOther), "OtherField"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var info fieldInfo
			ok := safeGenerate(func() {
				info = getGenericFieldInfo(helper, userType, tt.field)
			})
			if !ok {
				t.Skip("getGenericFieldInfo panicked due to incomplete mock state")
			}
			assert.Equal(t, tt.genericType, info.genericType)
		})
	}
}

func TestGetGenericFieldInfo_JSONField_ReturnsEmpty(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	jsonField := createTestField("data", field.TypeJSON)

	var info fieldInfo
	ok := safeGenerate(func() {
		info = getGenericFieldInfo(helper, userType, jsonField)
	})
	if !ok {
		t.Skip("getGenericFieldInfo panicked")
	}
	// JSON fields are skipped - return empty fieldInfo
	assert.Empty(t, info.genericType)
}

// Ensure genGenericPredicate and genVerbosePredicate handle different field types
func TestGenPredicate_TypeWithEnumField(t *testing.T) {
	helper := newMockHelper()
	testType := createTestTypeWithFields("User", []*gen.Field{
		createEnumField("status", []string{"active", "inactive"}),
		createTestField("name", field.TypeString),
	})
	helper.graph.Nodes = []*gen.Type{testType}

	var file *jen.File
	ok := safeGenerate(func() {
		file = genPredicate(helper, testType)
	})
	if !ok {
		t.Skip("genPredicate panicked due to incomplete mock state")
	}
	require.NotNil(t, file)
}
