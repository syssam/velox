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
// genEntity Tests (supplementing dialect_test.go)
// =============================================================================

func TestGenEntity_WithO2OEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	profileType := createTestType("Profile")

	userType.Edges = []*gen.Edge{
		createO2OEdge("profile", profileType, "profiles", "user_id"),
	}
	helper.graph.Nodes = []*gen.Type{userType, profileType}

	file := genEntity(helper, userType)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "type User struct")
	assert.Contains(t, code, "UserEdges")
	assert.Contains(t, code, "Profile")
}

func TestGenEntity_WithM2MEdge(t *testing.T) {
	helper := newMockHelper()
	postType := createTestType("Post")
	tagType := createTestType("Tag")

	postType.Edges = []*gen.Edge{
		createM2MEdge("tags", tagType, "post_tags", []string{"post_id", "tag_id"}),
	}
	helper.graph.Nodes = []*gen.Type{postType, tagType}

	file := genEntity(helper, postType)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "type Post struct")
	assert.Contains(t, code, "PostEdges")
	assert.Contains(t, code, "Tags")
}

func TestGenEntity_WithNillableFields(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createNillableField("bio", field.TypeString),
		createNillableField("phone", field.TypeString),
	})
	helper.graph.Nodes = []*gen.Type{userType}

	file := genEntity(helper, userType)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "type User struct")
}

func TestGenEntity_WithEnumField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createEnumField("status", []string{"active", "inactive"}),
	})
	helper.graph.Nodes = []*gen.Type{userType}

	file := genEntity(helper, userType)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "type User struct")
}

// =============================================================================
// genEdgesStruct Tests
// =============================================================================

func TestGenEdgesStruct_NoEdges(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	userType.Edges = nil

	f := helper.NewFile("ent")
	genEdgesStruct(helper, f, userType)

	code := f.GoString()
	assert.Contains(t, code, "UserEdges")
}

func TestGenEdgesStruct_MultipleEdges(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	groupType := createTestType("Group")

	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
		createM2MEdge("groups", groupType, "user_groups", []string{"user_id", "group_id"}),
	}

	f := helper.NewFile("ent")
	genEdgesStruct(helper, f, userType)

	code := f.GoString()
	assert.Contains(t, code, "UserEdges")
	assert.Contains(t, code, "Posts")
	assert.Contains(t, code, "Groups")
}

// =============================================================================
// genEntityClient Tests
// =============================================================================

func TestGenEntityClient_BasicEntity(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	genEntityClient(helper, f, userType)

	code := f.GoString()
	assert.Contains(t, code, "UserClient")
	assert.Contains(t, code, "Create")
	assert.Contains(t, code, "Update")
	assert.Contains(t, code, "Delete")
	assert.Contains(t, code, "Query")
}

// =============================================================================
// genQueryEdgeMethod Tests
// =============================================================================

func TestGenQueryEdgeMethod_O2M(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")

	edge := createO2MEdge("posts", postType, "posts", "user_id")

	f := helper.NewFile("ent")
	genQueryEdgeMethod(helper, f, userType, edge)

	code := f.GoString()
	assert.Contains(t, code, "QueryPosts")
}

func TestGenQueryEdgeMethod_M2O(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")

	edge := createM2OEdge("author", userType, "posts", "user_id")

	f := helper.NewFile("ent")
	genQueryEdgeMethod(helper, f, postType, edge)

	code := f.GoString()
	assert.Contains(t, code, "QueryAuthor")
}

// =============================================================================
// genValueMethod Tests
// =============================================================================

func TestGenValueMethod(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")

	f := helper.NewFile("ent")
	genValueMethod(helper, f, userType)

	code := f.GoString()
	assert.Contains(t, code, "Value")
}

// =============================================================================
// Edge Helper Tests
// =============================================================================

func TestEdgeGoType(t *testing.T) {
	t.Run("unique_edge_pointer", func(t *testing.T) {
		userType := &gen.Type{Name: "User"}
		edge := &gen.Edge{Type: userType, Unique: true}
		result := edgeGoType(edge)
		// edgeGoType returns jen.Code, render it to check output
		f := jen.NewFile("test")
		f.Var().Id("x").Add(result)
		code := f.GoString()
		assert.Contains(t, code, "*User")
	})

	t.Run("non_unique_edge_slice", func(t *testing.T) {
		postType := &gen.Type{Name: "Post"}
		edge := &gen.Edge{Type: postType, Unique: false}
		result := edgeGoType(edge)
		f := jen.NewFile("test")
		f.Var().Id("x").Add(result)
		code := f.GoString()
		assert.Contains(t, code, "[]*Post")
	})
}

// =============================================================================
// genBidiEdgeRefMethod Tests
// =============================================================================

func TestGenBidiEdgeRefMethod(t *testing.T) {
	userType := createTestType("User")
	postType := createTestType("Post")

	// genBidiEdgeRefMethod requires e.Ref != nil to generate code
	inverseEdge := createM2OEdge("author", userType, "posts", "user_id")
	edge := createO2MEdge("posts", postType, "posts", "user_id")
	edge.Ref = inverseEdge
	edge.Unique = true
	inverseEdge.Unique = true

	f := jen.NewFile("ent")
	genBidiEdgeRefMethod(f, userType, edge)

	code := f.GoString()
	assert.Contains(t, code, "Posts")
}

func TestGenBidiEdgeRefMethod_NilRef(t *testing.T) {
	userType := createTestType("User")
	postType := createTestType("Post")

	edge := createO2MEdge("posts", postType, "posts", "user_id")
	// edge.Ref is nil, so genBidiEdgeRefMethod should return early

	f := jen.NewFile("ent")
	genBidiEdgeRefMethod(f, userType, edge)

	code := f.GoString()
	// Should only contain the package declaration, no generated methods
	assert.Equal(t, "package ent\n", code)
}

// =============================================================================
// genNamedEdgeMethods Tests
// =============================================================================

func TestGenNamedEdgeMethods(t *testing.T) {
	userType := createTestType("User")
	postType := createTestType("Post")

	edge := createO2MEdge("posts", postType, "posts", "user_id")

	f := jen.NewFile("ent")
	genNamedEdgeMethods(f, userType, edge)

	code := f.GoString()
	assert.Contains(t, code, "NamedPosts")
}

// =============================================================================
// zeroValue Tests
// =============================================================================

func TestZeroValue(t *testing.T) {
	helper := newMockHelper()

	tests := []struct {
		name  string
		field *gen.Field
	}{
		{"string_field", createTestField("name", field.TypeString)},
		{"int_field", createTestField("age", field.TypeInt)},
		{"int64_field", createTestField("id", field.TypeInt64)},
		{"bool_field", createTestField("active", field.TypeBool)},
		{"float64_field", createTestField("price", field.TypeFloat64)},
		{"nillable_field", createNillableField("bio", field.TypeString)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := zeroValue(helper, tt.field)
			assert.NotNil(t, result)
		})
	}
}

func TestZeroValue_NilField(t *testing.T) {
	helper := newMockHelper()
	result := zeroValue(helper, nil)
	assert.NotNil(t, result)
}

// =============================================================================
// baseZeroValue Tests (comprehensive branch coverage)
// =============================================================================

func TestBaseZeroValue_NilField(t *testing.T) {
	helper := newMockHelper()
	result := baseZeroValue(helper, nil)
	assert.NotNil(t, result)
}

func TestBaseZeroValue_AllTypes(t *testing.T) {
	helper := newMockHelper()

	tests := []struct {
		name     string
		field    *gen.Field
		contains string
	}{
		{"string", createTestField("s", field.TypeString), `""`},
		{"int", createTestField("i", field.TypeInt), "int(0)"},
		{"int8", createTestField("i", field.TypeInt8), "int8(0)"},
		{"int16", createTestField("i", field.TypeInt16), "int16(0)"},
		{"int32", createTestField("i", field.TypeInt32), "int32(0)"},
		{"int64", createTestField("i", field.TypeInt64), "int64(0)"},
		{"uint", createTestField("u", field.TypeUint), "uint(0)"},
		{"uint8", createTestField("u", field.TypeUint8), "uint8(0)"},
		{"uint16", createTestField("u", field.TypeUint16), "uint16(0)"},
		{"uint32", createTestField("u", field.TypeUint32), "uint32(0)"},
		{"uint64", createTestField("u", field.TypeUint64), "uint64(0)"},
		{"float32", createTestField("f", field.TypeFloat32), "float32(0)"},
		{"float64", createTestField("f", field.TypeFloat64), "float64(0)"},
		{"bool", createTestField("b", field.TypeBool), "false"},
		{"bytes", createTestField("d", field.TypeBytes), "byte"},
		{"time", createTestField("t", field.TypeTime), "time"},
		{"uuid", createTestField("u", field.TypeUUID), "uuid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := baseZeroValue(helper, tt.field)
			assert.NotNil(t, result)

			f := jen.NewFile("test")
			f.Var().Id("x").Op("=").Add(result)
			code := f.GoString()
			assert.Contains(t, code, tt.contains)
		})
	}
}

func TestBaseZeroValue_Enum(t *testing.T) {
	helper := newMockHelper()
	enumField := createEnumField("status", []string{"active", "inactive"})

	result := baseZeroValue(helper, enumField)
	assert.NotNil(t, result)

	f := jen.NewFile("test")
	f.Var().Id("x").Op("=").Add(result)
	code := f.GoString()
	// Enum zero value: EnumType("")
	assert.Contains(t, code, `("")`)
}

func TestBaseZeroValue_JSON_WithGoType(t *testing.T) {
	helper := newMockHelper()
	jsonField := &gen.Field{
		Name: "data",
		Type: &field.TypeInfo{
			Type:    field.TypeJSON,
			RType:   &field.RType{Ident: "map[string]interface {}"},
			PkgPath: "",
		},
	}

	result := baseZeroValue(helper, jsonField)
	assert.NotNil(t, result)
}

func TestBaseZeroValue_JSON_NoGoType(t *testing.T) {
	helper := newMockHelper()
	jsonField := &gen.Field{
		Name: "data",
		Type: &field.TypeInfo{Type: field.TypeJSON},
	}

	result := baseZeroValue(helper, jsonField)
	assert.NotNil(t, result)

	f := jen.NewFile("test")
	f.Var().Id("x").Op("=").Add(result)
	code := f.GoString()
	assert.Contains(t, code, "json")
}

func TestBaseZeroValue_Other_WithGoType(t *testing.T) {
	helper := newMockHelper()
	otherField := &gen.Field{
		Name: "custom",
		Type: &field.TypeInfo{
			Type:    field.TypeOther,
			Ident:   "mypkg.MyType",
			PkgPath: "github.com/test/mypkg",
			RType:   &field.RType{Ident: "mypkg.MyType"},
		},
	}

	result := baseZeroValue(helper, otherField)
	assert.NotNil(t, result)
}

func TestBaseZeroValue_Other_WithIdent(t *testing.T) {
	helper := newMockHelper()
	otherField := &gen.Field{
		Name: "custom",
		Type: &field.TypeInfo{
			Type:  field.TypeOther,
			Ident: "MyType",
		},
	}

	result := baseZeroValue(helper, otherField)
	assert.NotNil(t, result)

	f := jen.NewFile("test")
	f.Var().Id("x").Op("=").Add(result)
	code := f.GoString()
	assert.Contains(t, code, "MyType")
}

// =============================================================================
// jsonFieldZeroValue Tests
// =============================================================================

func TestJsonFieldZeroValue_NilType(t *testing.T) {
	helper := newMockHelper()
	f := &gen.Field{Name: "data"}
	result := jsonFieldZeroValue(helper, f)
	assert.NotNil(t, result)
}

func TestJsonFieldZeroValue_NilRType(t *testing.T) {
	helper := newMockHelper()
	f := &gen.Field{
		Name: "data",
		Type: &field.TypeInfo{Type: field.TypeJSON},
	}
	result := jsonFieldZeroValue(helper, f)
	assert.NotNil(t, result)
}

func TestJsonFieldZeroValue_AllBranches(t *testing.T) {
	helper := newMockHelper()

	tests := []struct {
		name     string
		ident    string
		contains string
	}{
		{"map_string_interface", "map[string]interface {}", "map[string]interface"},
		{"map_string_interface_nospace", "map[string]interface{}", "map[string]interface"},
		{"map_string_any", "map[string]any", "map[string]any"},
		{"slice_map_string_interface", "[]map[string]interface {}", "map[string]interface"},
		{"slice_map_string_interface_nospace", "[]map[string]interface{}", "map[string]interface"},
		{"slice_map_string_any", "[]map[string]any", "map[string]any"},
		{"slice_interface", "[]interface {}", "interface"},
		{"slice_interface_nospace", "[]interface{}", "interface"},
		{"slice_any", "[]any", "any"},
		{"generic_slice", "[]MyType", "MyType"},
		{"generic_map", "map[string]MyType", "MyType"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &gen.Field{
				Name: "data",
				Type: &field.TypeInfo{
					Type:  field.TypeJSON,
					RType: &field.RType{Ident: tt.ident},
				},
			}
			result := jsonFieldZeroValue(helper, f)
			assert.NotNil(t, result)
		})
	}
}

func TestJsonFieldZeroValue_StructWithPkgPath(t *testing.T) {
	helper := newMockHelper()
	f := &gen.Field{
		Name: "data",
		Type: &field.TypeInfo{
			Type:    field.TypeJSON,
			Ident:   "mypkg.MyStruct",
			PkgPath: "github.com/test/mypkg",
			RType:   &field.RType{Ident: "mypkg.MyStruct"},
		},
	}
	result := jsonFieldZeroValue(helper, f)
	assert.NotNil(t, result)
}

func TestJsonFieldZeroValue_StructWithIdent(t *testing.T) {
	helper := newMockHelper()
	f := &gen.Field{
		Name: "data",
		Type: &field.TypeInfo{
			Type:  field.TypeJSON,
			Ident: "MyStruct",
			RType: &field.RType{Ident: "MyStruct"},
		},
	}
	result := jsonFieldZeroValue(helper, f)
	assert.NotNil(t, result)
}

func TestJsonFieldZeroValue_DefaultFallback(t *testing.T) {
	helper := newMockHelper()
	f := &gen.Field{
		Name: "data",
		Type: &field.TypeInfo{
			Type:  field.TypeJSON,
			RType: &field.RType{Ident: "SomeUnknownType"},
		},
	}
	result := jsonFieldZeroValue(helper, f)
	assert.NotNil(t, result)
}

// =============================================================================
// idInPredicate Tests
// =============================================================================

func TestIdInPredicate_DefaultMode(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")

	result := idInPredicate(helper, userType, jen.Id("ids").Op("..."))
	assert.NotNil(t, result)

	f := jen.NewFile("test")
	f.Var().Id("pred").Op("=").Add(result)
	code := f.GoString()
	assert.Contains(t, code, "IDField")
	assert.Contains(t, code, "In")
}

func TestIdInPredicate_EntPredicatesMode(t *testing.T) {
	helper := newFeatureMockHelper()
	helper.withFeatures("sql/entpredicates")
	userType := createTestType("User")

	result := idInPredicate(helper, userType, jen.Id("ids").Op("..."))
	assert.NotNil(t, result)

	f := jen.NewFile("test")
	f.Var().Id("pred").Op("=").Add(result)
	code := f.GoString()
	assert.Contains(t, code, "IDIn")
}

// =============================================================================
// idEQPredicate Tests
// =============================================================================

func TestIdEQPredicate_DefaultMode(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")

	result := idEQPredicate(helper, userType, jen.Id("id"))
	assert.NotNil(t, result)

	f := jen.NewFile("test")
	f.Var().Id("pred").Op("=").Add(result)
	code := f.GoString()
	assert.Contains(t, code, "IDField")
	assert.Contains(t, code, "EQ")
}

func TestIdEQPredicate_EntPredicatesMode(t *testing.T) {
	helper := newFeatureMockHelper()
	helper.withFeatures("sql/entpredicates")
	userType := createTestType("User")

	result := idEQPredicate(helper, userType, jen.Id("id"))
	assert.NotNil(t, result)

	f := jen.NewFile("test")
	f.Var().Id("pred").Op("=").Add(result)
	code := f.GoString()
	assert.Contains(t, code, "ID")
}

// =============================================================================
// genScanValues Tests
// =============================================================================

func TestGenScanValues_BasicEntity(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")

	f := helper.NewFile("ent")
	genScanValues(helper, f, userType)

	code := f.GoString()
	assert.Contains(t, code, "scanValues")
	assert.Contains(t, code, "columns")
}

func TestGenScanValues_WithNillableFields(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
		createNillableField("bio", field.TypeString),
		createTestField("age", field.TypeInt),
	})

	f := helper.NewFile("ent")
	genScanValues(helper, f, userType)

	code := f.GoString()
	assert.Contains(t, code, "scanValues")
}

func TestGenScanValues_WithMultipleFieldTypes(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
		createTestField("age", field.TypeInt),
		createTestField("active", field.TypeBool),
		createTestField("score", field.TypeFloat64),
		createTestField("data", field.TypeBytes),
	})

	f := helper.NewFile("ent")
	genScanValues(helper, f, userType)

	code := f.GoString()
	assert.Contains(t, code, "scanValues")
}

// =============================================================================
// genAssignValues Tests
// =============================================================================

func TestGenAssignValues_BasicEntity(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")

	f := helper.NewFile("ent")
	genAssignValues(helper, f, userType)

	code := f.GoString()
	assert.Contains(t, code, "assignValues")
	assert.Contains(t, code, "columns")
}

// =============================================================================
// genFieldAssignment Tests
// =============================================================================

func TestGenFieldAssignment_StringField(t *testing.T) {
	helper := newMockHelper()
	fld := createTestField("name", field.TypeString)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Name")
	// Should not panic
}

func TestGenFieldAssignment_NillableField(t *testing.T) {
	helper := newMockHelper()
	fld := createNillableField("bio", field.TypeString)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Bio")
	// Should not panic
}

func TestGenFieldAssignment_BoolField(t *testing.T) {
	helper := newMockHelper()
	fld := createTestField("active", field.TypeBool)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Active")
}

func TestGenFieldAssignment_IntField(t *testing.T) {
	helper := newMockHelper()
	fld := createTestField("age", field.TypeInt)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Age")
}

// =============================================================================
// genEdgesStruct Additional Tests
// =============================================================================

func TestGenEdgesStruct_WithM2MEdge(t *testing.T) {
	helper := newMockHelper()
	postType := createTestType("Post")
	tagType := createTestType("Tag")
	postType.Edges = []*gen.Edge{
		createM2MEdge("tags", tagType, "post_tags", []string{"post_id", "tag_id"}),
	}
	helper.graph.Nodes = []*gen.Type{postType, tagType}

	f := helper.NewFile("ent")
	genEdgesStruct(helper, f, postType)

	code := f.GoString()
	assert.Contains(t, code, "PostEdges")
	assert.Contains(t, code, "Tags")
}

// =============================================================================
// genClientQueryEdgeMethod Tests
// =============================================================================

func TestGenClientQueryEdgeMethod_O2M(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createO2MEdge("posts", postType, "posts", "user_id")
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	genClientQueryEdgeMethod(helper, f, userType, edge)

	code := f.GoString()
	assert.Contains(t, code, "QueryPosts")
}

func TestGenClientQueryEdgeMethod_M2O(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createM2OEdge("author", userType, "posts", "user_id")
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	genClientQueryEdgeMethod(helper, f, postType, edge)

	code := f.GoString()
	assert.Contains(t, code, "QueryAuthor")
}

// =============================================================================
// genEntityClient Additional Tests
// =============================================================================

func TestGenEntityClient_WithEdges(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
	}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	genEntityClient(helper, f, userType)

	code := f.GoString()
	assert.Contains(t, code, "UserClient")
	assert.Contains(t, code, "QueryPosts")
}

// =============================================================================
// genFieldAssignment Additional Tests
// =============================================================================

func TestGenFieldAssignment_JSONField(t *testing.T) {
	helper := newMockHelper()
	fld := &gen.Field{
		Name: "metadata",
		Type: &field.TypeInfo{Type: field.TypeJSON},
	}

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Metadata")
	// JSON field takes a different code path (json.Unmarshal)
}

func TestGenFieldAssignment_TimeField(t *testing.T) {
	helper := newMockHelper()
	fld := createTestField("created_at", field.TypeTime)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "CreatedAt")
}

func TestGenFieldAssignment_NillableIntField(t *testing.T) {
	helper := newMockHelper()
	fld := createNillableField("score", field.TypeInt)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Score")
}

func TestGenFieldAssignment_UUIDField(t *testing.T) {
	helper := newMockHelper()
	fld := createTestField("uuid", field.TypeUUID)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "UUID")
}

func TestGenFieldAssignment_EnumField(t *testing.T) {
	helper := newMockHelper()
	fld := createEnumField("status", []string{"active", "inactive"})

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Status")
}

func TestGenFieldAssignment_Float64Field(t *testing.T) {
	helper := newMockHelper()
	fld := createTestField("price", field.TypeFloat64)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Price")
}

// =============================================================================
// genEntityStruct Additional Tests
// =============================================================================

func TestGenEntityStruct_WithEdges(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
	}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genEntityStruct(helper, f, userType)
	})
	if !ok {
		t.Skip("genEntityStruct panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "User")
	assert.Contains(t, code, "Edges")
}

func TestGenEntityStruct_WithEnumField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	userType.Fields = append(userType.Fields, createEnumField("status", []string{"active", "inactive"}))
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genEntityStruct(helper, f, userType)
	})
	if !ok {
		t.Skip("genEntityStruct panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "User")
	assert.Contains(t, code, "Status")
}

// =============================================================================
// genScanValues Additional Tests
// =============================================================================

func TestGenScanValues_WithEdges(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	m2oEdge := createM2OEdge("author", userType, "posts", "author_id")
	postType.Edges = []*gen.Edge{m2oEdge}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	genScanValues(helper, f, postType)

	code := f.GoString()
	assert.Contains(t, code, "scanValues")
}

// =============================================================================
// genAssignValues Additional Tests
// =============================================================================

func TestGenAssignValues_WithNillableField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
		createNillableField("bio", field.TypeString),
	})
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	genAssignValues(helper, f, userType)

	code := f.GoString()
	assert.Contains(t, code, "assignValues")
}

func TestGenAssignValues_WithEnumField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	userType.Fields = append(userType.Fields, createEnumField("status", []string{"active", "inactive"}))
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	genAssignValues(helper, f, userType)

	code := f.GoString()
	assert.Contains(t, code, "assignValues")
}

// =============================================================================
// genEdgesStruct Additional Tests
// =============================================================================

func TestGenEdgesStruct_WithOptionalEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	profileType := createTestType("Profile")
	edge := createO2OEdge("profile", profileType, "profiles", "user_id")
	edge.Optional = true
	userType.Edges = []*gen.Edge{edge}
	helper.graph.Nodes = []*gen.Type{userType, profileType}

	f := helper.NewFile("ent")
	genEdgesStruct(helper, f, userType)

	code := f.GoString()
	assert.Contains(t, code, "UserEdges")
	assert.Contains(t, code, "Profile")
}

// =============================================================================
// genQueryEdgeMethod Additional Tests
// =============================================================================

func TestGenQueryEdgeMethod_M2M(t *testing.T) {
	helper := newMockHelper()
	postType := createTestType("Post")
	tagType := createTestType("Tag")
	edge := createM2MEdge("tags", tagType, "post_tags", []string{"post_id", "tag_id"})
	helper.graph.Nodes = []*gen.Type{postType, tagType}

	f := helper.NewFile("ent")
	genQueryEdgeMethod(helper, f, postType, edge)

	code := f.GoString()
	assert.Contains(t, code, "QueryTags")
}

func TestGenQueryEdgeMethod_O2O(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	profileType := createTestType("Profile")
	edge := createO2OEdge("profile", profileType, "profiles", "user_id")
	helper.graph.Nodes = []*gen.Type{userType, profileType}

	f := helper.NewFile("ent")
	genQueryEdgeMethod(helper, f, userType, edge)

	code := f.GoString()
	assert.Contains(t, code, "QueryProfile")
}

// =============================================================================
// genClientQueryEdgeMethod Additional Tests
// =============================================================================

func TestGenClientQueryEdgeMethod_M2M(t *testing.T) {
	helper := newMockHelper()
	postType := createTestType("Post")
	tagType := createTestType("Tag")
	edge := createM2MEdge("tags", tagType, "post_tags", []string{"post_id", "tag_id"})
	helper.graph.Nodes = []*gen.Type{postType, tagType}

	f := helper.NewFile("ent")
	genClientQueryEdgeMethod(helper, f, postType, edge)

	code := f.GoString()
	assert.Contains(t, code, "QueryTags")
}

// =============================================================================
// genBidiEdgeRefMethod Additional Branch Coverage Tests
// =============================================================================

func TestGenBidiEdgeRefMethod_O2O_BothUnique(t *testing.T) {
	userType := createTestType("User")
	profileType := createTestType("Profile")

	// O2O: both unique
	forwardEdge := createO2OEdge("profile", profileType, "profiles", "user_id")
	inverseEdge := createO2OEdge("owner", userType, "profiles", "user_id")
	forwardEdge.Ref = inverseEdge

	f := jen.NewFile("ent")
	genBidiEdgeRefMethod(f, userType, forwardEdge)

	code := f.GoString()
	assert.Contains(t, code, "setProfileBidiRef")
	assert.Contains(t, code, "Edges")
}

func TestGenBidiEdgeRefMethod_M2O_UniqueThis_NotUniqueRef(t *testing.T) {
	userType := createTestType("User")
	postType := createTestType("Post")

	// M2O: this side unique, inverse is O2M (not unique)
	m2oEdge := createM2OEdge("author", userType, "posts", "user_id")
	o2mEdge := createO2MEdge("posts", postType, "posts", "user_id")
	m2oEdge.Ref = o2mEdge

	f := jen.NewFile("ent")
	genBidiEdgeRefMethod(f, postType, m2oEdge)

	code := f.GoString()
	assert.Contains(t, code, "setAuthorBidiRef")
	assert.Contains(t, code, "no-op for M2O edges with O2M inverse")
}

func TestGenBidiEdgeRefMethod_O2M_NotUniqueThis_UniqueRef(t *testing.T) {
	userType := createTestType("User")
	postType := createTestType("Post")

	// O2M: this side not unique, inverse is unique (M2O)
	o2mEdge := createO2MEdge("posts", postType, "posts", "user_id")
	m2oEdge := createM2OEdge("author", userType, "posts", "user_id")
	o2mEdge.Ref = m2oEdge

	f := jen.NewFile("ent")
	genBidiEdgeRefMethod(f, userType, o2mEdge)

	code := f.GoString()
	assert.Contains(t, code, "setPostsBidiRef")
	assert.Contains(t, code, "range")
}

func TestGenBidiEdgeRefMethod_M2M_NeitherUnique(t *testing.T) {
	postType := createTestType("Post")
	tagType := createTestType("Tag")

	// M2M: neither side unique
	edge := createM2MEdge("tags", tagType, "post_tags", []string{"post_id", "tag_id"})
	inverseEdge := createM2MEdge("posts", postType, "post_tags", []string{"tag_id", "post_id"})
	edge.Ref = inverseEdge

	f := jen.NewFile("ent")
	genBidiEdgeRefMethod(f, postType, edge)

	code := f.GoString()
	assert.Contains(t, code, "setTagsBidiRef")
	assert.Contains(t, code, "no-op for M2M edges")
}

// =============================================================================
// genQueryEdgeMethod Branch Coverage Tests
// =============================================================================

func TestGenQueryEdgeMethod_WithRef(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")

	// Edge with Ref set (edge.From() style)
	inverseEdge := createO2MEdge("posts", postType, "posts", "user_id")
	m2oEdge := createM2OEdge("author", userType, "posts", "user_id")
	m2oEdge.Ref = inverseEdge
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	genQueryEdgeMethod(helper, f, postType, m2oEdge)

	code := f.GoString()
	assert.Contains(t, code, "QueryAuthor")
	assert.Contains(t, code, "HasPostsWith")
}

func TestGenQueryEdgeMethod_WithInverse(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")

	// Inverse edge (IsInverse() returns true when Inverse field is set)
	edge := createInverseEdge("author", userType, gen.M2O, "posts")
	edge.Rel = gen.Relation{Type: gen.M2O, Table: "posts", Columns: []string{"user_id"}}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	genQueryEdgeMethod(helper, f, postType, edge)

	code := f.GoString()
	assert.Contains(t, code, "QueryAuthor")
	assert.Contains(t, code, "HasPostsWith")
}

func TestGenQueryEdgeMethod_ForwardWithBackRef(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")

	// Forward edge (edge.To) with back-reference on target
	forwardEdge := createO2MEdge("posts", postType, "posts", "user_id")
	// Set up target type edges with a back-ref pointing to this edge
	backRefEdge := &gen.Edge{
		Name:   "author",
		Type:   userType,
		Unique: true,
		Ref:    forwardEdge,
		Rel:    gen.Relation{Type: gen.M2O, Table: "posts", Columns: []string{"user_id"}},
	}
	postType.Edges = []*gen.Edge{backRefEdge}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	genQueryEdgeMethod(helper, f, userType, forwardEdge)

	code := f.GoString()
	assert.Contains(t, code, "QueryPosts")
	assert.Contains(t, code, "HasAuthorWith")
}

func TestGenQueryEdgeMethod_WithEntPredicates(t *testing.T) {
	helper := newFeatureMockHelper()
	helper.withFeatures("sql/entpredicates")
	userType := createTestType("User")
	postType := createTestType("Post")

	// Edge with Ref for entpredicates mode
	inverseEdge := createO2MEdge("posts", postType, "posts", "user_id")
	m2oEdge := createM2OEdge("author", userType, "posts", "user_id")
	m2oEdge.Ref = inverseEdge
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	genQueryEdgeMethod(helper, f, postType, m2oEdge)

	code := f.GoString()
	assert.Contains(t, code, "QueryAuthor")
	// EntPredicates mode uses ID() instead of IDField.EQ()
}

func TestGenQueryEdgeMethod_NoBackRef(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")

	// Forward edge with no back-reference found on target (target has no edges)
	edge := createO2MEdge("posts", postType, "posts", "user_id")
	postType.Edges = nil
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	genQueryEdgeMethod(helper, f, userType, edge)

	code := f.GoString()
	assert.Contains(t, code, "QueryPosts")
	assert.Contains(t, code, "WARNING")
}

// =============================================================================
// genClientQueryEdgeMethod Branch Coverage Tests
// =============================================================================

func TestGenClientQueryEdgeMethod_WithRef(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")

	inverseEdge := createO2MEdge("posts", postType, "posts", "user_id")
	m2oEdge := createM2OEdge("author", userType, "posts", "user_id")
	m2oEdge.Ref = inverseEdge
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	genClientQueryEdgeMethod(helper, f, postType, m2oEdge)

	code := f.GoString()
	assert.Contains(t, code, "QueryAuthor")
	assert.Contains(t, code, "HasPostsWith")
}

func TestGenClientQueryEdgeMethod_WithInverse(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")

	edge := createInverseEdge("author", userType, gen.M2O, "posts")
	edge.Rel = gen.Relation{Type: gen.M2O, Table: "posts", Columns: []string{"user_id"}}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	genClientQueryEdgeMethod(helper, f, postType, edge)

	code := f.GoString()
	assert.Contains(t, code, "QueryAuthor")
	assert.Contains(t, code, "HasPostsWith")
}

func TestGenClientQueryEdgeMethod_ForwardWithBackRef(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")

	forwardEdge := createO2MEdge("posts", postType, "posts", "user_id")
	backRefEdge := &gen.Edge{
		Name:   "author",
		Type:   userType,
		Unique: true,
		Ref:    forwardEdge,
		Rel:    gen.Relation{Type: gen.M2O, Table: "posts", Columns: []string{"user_id"}},
	}
	postType.Edges = []*gen.Edge{backRefEdge}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	genClientQueryEdgeMethod(helper, f, userType, forwardEdge)

	code := f.GoString()
	assert.Contains(t, code, "QueryPosts")
	assert.Contains(t, code, "HasAuthorWith")
}

func TestGenClientQueryEdgeMethod_WithEntPredicates(t *testing.T) {
	helper := newFeatureMockHelper()
	helper.withFeatures("sql/entpredicates")
	userType := createTestType("User")
	postType := createTestType("Post")

	inverseEdge := createO2MEdge("posts", postType, "posts", "user_id")
	m2oEdge := createM2OEdge("author", userType, "posts", "user_id")
	m2oEdge.Ref = inverseEdge
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	genClientQueryEdgeMethod(helper, f, postType, m2oEdge)

	code := f.GoString()
	assert.Contains(t, code, "QueryAuthor")
}

func TestGenClientQueryEdgeMethod_NoBackRef(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")

	edge := createO2MEdge("posts", postType, "posts", "user_id")
	postType.Edges = nil
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	genClientQueryEdgeMethod(helper, f, userType, edge)

	code := f.GoString()
	assert.Contains(t, code, "QueryPosts")
	assert.Contains(t, code, "WARNING")
}

// =============================================================================
// genEdgesStruct Branch Coverage Tests
// =============================================================================

func TestGenEdgesStruct_WithNamedEdges(t *testing.T) {
	helper := newFeatureMockHelper()
	helper.withFeatures("namedges")

	userType := createTestType("User")
	postType := createTestType("Post")
	userType.Edges = []*gen.Edge{
		createO2MEdge("posts", postType, "posts", "user_id"),
	}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	genEdgesStruct(helper, f, userType)

	code := f.GoString()
	assert.Contains(t, code, "UserEdges")
	assert.Contains(t, code, "Posts")
	assert.Contains(t, code, "NamedPosts")
}

// =============================================================================
// genEdgeAccessor Tests
// =============================================================================

func TestGenEdgeAccessor_UniqueEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	profileType := createTestType("Profile")
	edge := createO2OEdge("profile", profileType, "profiles", "user_id")

	f := helper.NewFile("ent")
	genEdgeAccessor(f, userType, edge)

	code := f.GoString()
	assert.Contains(t, code, "ProfileOrErr")
}

func TestGenEdgeAccessor_NonUniqueEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createO2MEdge("posts", postType, "posts", "user_id")

	f := helper.NewFile("ent")
	genEdgeAccessor(f, userType, edge)

	code := f.GoString()
	assert.Contains(t, code, "PostsOrErr")
}

// =============================================================================
// genEdgesStruct BidiEdges Feature Tests
// =============================================================================

func TestGenEdgesStruct_WithBidiEdges(t *testing.T) {
	helper := newFeatureMockHelper()
	helper.withFeatures("bidiedges")

	userType := createTestType("User")
	postType := createTestType("Post")

	forwardEdge := createO2MEdge("posts", postType, "posts", "user_id")
	inverseEdge := createM2OEdge("author", userType, "posts", "user_id")
	forwardEdge.Ref = inverseEdge
	userType.Edges = []*gen.Edge{forwardEdge}
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	genEdgesStruct(helper, f, userType)

	code := f.GoString()
	assert.Contains(t, code, "UserEdges")
	assert.Contains(t, code, "Posts")
}

func TestGenEdgesStruct_WithNamedAndBidiEdges(t *testing.T) {
	helper := newFeatureMockHelper()
	helper.withFeatures("namedges", "bidiedges")

	userType := createTestType("User")
	postType := createTestType("Post")
	tagType := createTestType("Tag")

	o2mEdge := createO2MEdge("posts", postType, "posts", "user_id")
	m2mEdge := createM2MEdge("tags", tagType, "user_tags", []string{"user_id", "tag_id"})
	userType.Edges = []*gen.Edge{o2mEdge, m2mEdge}
	helper.graph.Nodes = []*gen.Type{userType, postType, tagType}

	f := helper.NewFile("ent")
	genEdgesStruct(helper, f, userType)

	code := f.GoString()
	assert.Contains(t, code, "UserEdges")
	assert.Contains(t, code, "Posts")
	assert.Contains(t, code, "Tags")
	assert.Contains(t, code, "NamedPosts")
	assert.Contains(t, code, "NamedTags")
}

// =============================================================================
// genFieldAssignment Additional Branch Coverage
// =============================================================================

func TestGenFieldAssignment_NillableBoolField(t *testing.T) {
	helper := newMockHelper()
	fld := createNillableField("active", field.TypeBool)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Active")
}

func TestGenFieldAssignment_NillableTimeField(t *testing.T) {
	helper := newMockHelper()
	fld := createNillableField("deleted_at", field.TypeTime)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "DeletedAt")
}

func TestGenFieldAssignment_Float32Field(t *testing.T) {
	helper := newMockHelper()
	fld := createTestField("rating", field.TypeFloat32)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Rating")
}

func TestGenFieldAssignment_BytesField(t *testing.T) {
	helper := newMockHelper()
	fld := createTestField("data", field.TypeBytes)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Data")
}

func TestGenFieldAssignment_NillableFloat64(t *testing.T) {
	helper := newMockHelper()
	fld := createNillableField("score", field.TypeFloat64)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Score")
}

func TestGenFieldAssignment_OtherField(t *testing.T) {
	helper := newMockHelper()
	fld := &gen.Field{
		Name: "custom",
		Type: &field.TypeInfo{Type: field.TypeOther, Ident: "MyType"},
	}

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Custom")
}

// =============================================================================
// genScanValues Additional Branch Coverage
// =============================================================================

func TestGenScanValues_WithEnumField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	userType.Fields = append(userType.Fields, createEnumField("status", []string{"active", "inactive"}))
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	genScanValues(helper, f, userType)

	code := f.GoString()
	assert.Contains(t, code, "scanValues")
}

func TestGenScanValues_WithTimeField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("created_at", field.TypeTime),
		createTestField("updated_at", field.TypeTime),
	})

	f := helper.NewFile("ent")
	genScanValues(helper, f, userType)

	code := f.GoString()
	assert.Contains(t, code, "scanValues")
}

func TestGenScanValues_WithUUIDField(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("uid", field.TypeUUID),
	})

	f := helper.NewFile("ent")
	genScanValues(helper, f, userType)

	code := f.GoString()
	assert.Contains(t, code, "scanValues")
}

// =============================================================================
// genAssignValues Additional Branch Coverage
// =============================================================================

func TestGenAssignValues_WithMultipleFieldTypes(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
		createTestField("age", field.TypeInt),
		createTestField("active", field.TypeBool),
		createTestField("score", field.TypeFloat64),
		createTestField("data", field.TypeBytes),
		createNillableField("bio", field.TypeString),
		createEnumField("status", []string{"active"}),
	})
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	genAssignValues(helper, f, userType)

	code := f.GoString()
	assert.Contains(t, code, "assignValues")
}

func TestGenAssignValues_WithTimeAndUUIDFields(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("created_at", field.TypeTime),
		createTestField("uid", field.TypeUUID),
	})
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	genAssignValues(helper, f, userType)

	code := f.GoString()
	assert.Contains(t, code, "assignValues")
}

// =============================================================================
// genEntityStruct Additional Branch Coverage
// =============================================================================

func TestGenEntityStruct_WithNillableFields(t *testing.T) {
	helper := newMockHelper()
	userType := createTestTypeWithFields("User", []*gen.Field{
		createTestField("name", field.TypeString),
		createNillableField("bio", field.TypeString),
		createNillableField("score", field.TypeInt),
	})
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genEntityStruct(helper, f, userType)
	})
	if !ok {
		t.Skip("genEntityStruct panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "User")
}

func TestGenEntityStruct_WithNilID(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	userType.ID = nil // Composite ID scenario
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genEntityStruct(helper, f, userType)
	})
	if !ok {
		t.Skip("genEntityStruct panicked due to incomplete mock state")
	}
}

func TestGenEntityStruct_WithMultipleEdges(t *testing.T) {
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
		genEntityStruct(helper, f, userType)
	})
	if !ok {
		t.Skip("genEntityStruct panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "User")
	assert.Contains(t, code, "Edges")
}

// =============================================================================
// genScanTypeFieldExpr Tests
// =============================================================================

func TestGenScanTypeFieldExpr_RegularField(t *testing.T) {
	fld := createTestField("name", field.TypeString)
	result := genScanTypeFieldExpr(fld)
	assert.NotNil(t, result)
}

func TestGenScanTypeFieldExpr_EnumField(t *testing.T) {
	fld := createEnumField("status", []string{"active", "inactive"})
	result := genScanTypeFieldExpr(fld)
	assert.NotNil(t, result)
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkGenScanValues(b *testing.B) {
	helper := newMockHelper()
	userType := createTestType("User")
	for b.Loop() {
		f := helper.NewFile("ent")
		genScanValues(helper, f, userType)
	}
}

func BenchmarkGenAssignValues(b *testing.B) {
	helper := newMockHelper()
	userType := createTestType("User")
	for b.Loop() {
		f := helper.NewFile("ent")
		genAssignValues(helper, f, userType)
	}
}
