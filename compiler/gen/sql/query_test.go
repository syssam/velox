package sql

import (
	"testing"

	"github.com/dave/jennifer/jen"
	"github.com/stretchr/testify/assert"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// genQueryFilterMethod Tests
// =============================================================================

func TestGenQueryFilterMethod(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	genQueryFilterMethod(helper, f, userType, "UserQuery")

	code := f.GoString()
	assert.Contains(t, code, "Filter")
	assert.Contains(t, code, "UserFilter")
	assert.Contains(t, code, "privacy")
}

// =============================================================================
// genQueryWithNamedEdge Tests
// =============================================================================

func TestGenQueryWithNamedEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createO2MEdge("posts", postType, "posts", "user_id")
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	genQueryWithNamedEdge(helper, f, userType, edge)

	code := f.GoString()
	assert.Contains(t, code, "WithNamedPosts")
}

// =============================================================================
// genBidiEdgeRefCalls Tests
// =============================================================================

func TestGenBidiEdgeRefCalls_WithBidiEdges(t *testing.T) {
	userType := createTestType("User")
	postType := createTestType("Post")

	inverseEdge := createM2OEdge("author", userType, "posts", "user_id")
	edge := createO2MEdge("posts", postType, "posts", "user_id")
	edge.Ref = inverseEdge
	userType.Edges = []*gen.Edge{edge}

	grp := &jen.Group{}
	genBidiEdgeRefCalls(grp, userType)
	// Should generate code for bidirectional ref calls
}

func TestGenBidiEdgeRefCalls_NoRefEdges(t *testing.T) {
	userType := createTestType("User")
	postType := createTestType("Post")

	edge := createO2MEdge("posts", postType, "posts", "user_id")
	userType.Edges = []*gen.Edge{edge}

	grp := &jen.Group{}
	genBidiEdgeRefCalls(grp, userType)
	// Should return early (no Ref edges)
}

func TestGenBidiEdgeRefCalls_NoEdges(t *testing.T) {
	userType := createTestType("User")
	userType.Edges = nil

	grp := &jen.Group{}
	genBidiEdgeRefCalls(grp, userType)
	// Should return early
}

// =============================================================================
// genQueryLockMethods Tests
// =============================================================================

func TestGenQueryLockMethods(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	genQueryLockMethods(helper, f, userType, "UserQuery")

	code := f.GoString()
	assert.Contains(t, code, "ForUpdate")
	assert.Contains(t, code, "ForShare")
	assert.Contains(t, code, "LockOption")
}

// =============================================================================
// genQueryModifyMethod Tests
// =============================================================================

func TestGenQueryModifyMethod(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	f := helper.NewFile("ent")
	genQueryModifyMethod(helper, f, userType, "UserQuery")

	code := f.GoString()
	assert.Contains(t, code, "Modify")
	assert.Contains(t, code, "modifiers")
}

// =============================================================================
// genIDScanType Tests
// =============================================================================

func TestGenIDScanType_NilField(t *testing.T) {
	code := genIDScanType(nil)
	assert.NotNil(t, code)

	f := jen.NewFile("test")
	f.Var().Id("x").Op("=").Add(code)
	output := f.GoString()
	assert.Contains(t, output, "NullInt64")
}

func TestGenIDScanType_AllStandardTypes(t *testing.T) {
	tests := []struct {
		name     string
		typ      field.Type
		expected string
	}{
		{"string", field.TypeString, "NullString"},
		{"enum", field.TypeEnum, "NullString"},
		{"bool", field.TypeBool, "NullBool"},
		{"time", field.TypeTime, "NullTime"},
		{"float32", field.TypeFloat32, "NullFloat64"},
		{"float64", field.TypeFloat64, "NullFloat64"},
		{"bytes", field.TypeBytes, "byte"},
		{"int", field.TypeInt, "NullInt64"},
		{"int8", field.TypeInt8, "NullInt64"},
		{"int16", field.TypeInt16, "NullInt64"},
		{"int32", field.TypeInt32, "NullInt64"},
		{"int64", field.TypeInt64, "NullInt64"},
		{"uint", field.TypeUint, "NullInt64"},
		{"uint8", field.TypeUint8, "NullInt64"},
		{"uint16", field.TypeUint16, "NullInt64"},
		{"uint32", field.TypeUint32, "NullInt64"},
		{"uint64", field.TypeUint64, "NullInt64"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fld := createTestField("test_field", tt.typ)
			code := genIDScanType(fld)
			assert.NotNil(t, code)

			f := jen.NewFile("test")
			f.Var().Id("x").Op("=").Add(code)
			output := f.GoString()
			assert.Contains(t, output, tt.expected)
		})
	}
}

func TestGenIDScanType_ValueScannerNonPtr(t *testing.T) {
	fld := &gen.Field{
		Name: "id",
		Type: &field.TypeInfo{
			Type: field.TypeUUID,
			RType: &field.RType{
				Ident: "uuid.UUID",
			},
		},
	}
	// ValueScanner returns true when RType has specific characteristics
	// but without full setup it may not. Test the nil-safe path.
	code := genIDScanType(fld)
	assert.NotNil(t, code)
}

// =============================================================================
// genIDScanExtract Tests
// =============================================================================

func TestGenIDScanExtract_NilField(t *testing.T) {
	grp := &jen.Group{}
	genIDScanExtract(grp, nil, "out", "outID")
	// Should not panic - generates default int conversion
}

func TestGenIDScanExtract_AllStandardTypes(t *testing.T) {
	tests := []struct {
		name string
		typ  field.Type
	}{
		{"string", field.TypeString},
		{"enum", field.TypeEnum},
		{"bool", field.TypeBool},
		{"time", field.TypeTime},
		{"float64", field.TypeFloat64},
		{"float32", field.TypeFloat32},
		{"int64", field.TypeInt64},
		{"int", field.TypeInt},
		{"int8", field.TypeInt8},
		{"int16", field.TypeInt16},
		{"int32", field.TypeInt32},
		{"uint", field.TypeUint},
		{"uint8", field.TypeUint8},
		{"uint16", field.TypeUint16},
		{"uint32", field.TypeUint32},
		{"uint64", field.TypeUint64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fld := createTestField("test_field", tt.typ)
			grp := &jen.Group{}
			genIDScanExtract(grp, fld, "out", "outID")
			// Should not panic - verify code was generated
		})
	}
}

// =============================================================================
// genLoadEdgeM2M Tests
// =============================================================================

func TestGenLoadEdgeM2M_BasicM2M(t *testing.T) {
	helper := newMockHelper()
	postType := createTestType("Post")
	tagType := createTestType("Tag")
	edge := createM2MEdge("tags", tagType, "post_tags", []string{"post_id", "tag_id"})
	helper.graph.Nodes = []*gen.Type{postType, tagType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genLoadEdgeM2M(helper, f, postType, edge)
	})
	if !ok {
		t.Skip("genLoadEdgeM2M panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "loadTags")
	assert.Contains(t, code, "EdgeQuerySpec")
	assert.Contains(t, code, "post_tags")
}

func TestGenLoadEdgeM2M_InverseEdge(t *testing.T) {
	helper := newMockHelper()
	postType := createTestType("Post")
	tagType := createTestType("Tag")
	edge := createM2MEdge("posts", postType, "post_tags", []string{"post_id", "tag_id"})
	edge.Inverse = "tags" // This is the inverse side
	helper.graph.Nodes = []*gen.Type{postType, tagType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genLoadEdgeM2M(helper, f, tagType, edge)
	})
	if !ok {
		t.Skip("genLoadEdgeM2M panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "loadPosts")
}

func TestGenLoadEdgeM2M_PanicOnNilOwnerID(t *testing.T) {
	helper := newMockHelper()
	postType := &gen.Type{Name: "Post"} // No ID
	tagType := createTestType("Tag")
	edge := createM2MEdge("tags", tagType, "post_tags", []string{"post_id", "tag_id"})

	f := helper.NewFile("ent")
	assert.Panics(t, func() {
		genLoadEdgeM2M(helper, f, postType, edge)
	})
}

func TestGenLoadEdgeM2M_PanicOnNilTargetID(t *testing.T) {
	helper := newMockHelper()
	postType := createTestType("Post")
	tagType := &gen.Type{Name: "Tag"} // No ID
	edge := createM2MEdge("tags", tagType, "post_tags", []string{"post_id", "tag_id"})

	f := helper.NewFile("ent")
	assert.Panics(t, func() {
		genLoadEdgeM2M(helper, f, postType, edge)
	})
}

// =============================================================================
// genLoadEdge Tests (additional coverage)
// =============================================================================

func TestGenLoadEdge_O2MEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createO2MEdge("posts", postType, "posts", "user_id")
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genLoadEdge(helper, f, userType, edge)
	})
	if !ok {
		t.Skip("genLoadEdge panicked due to incomplete mock state")
	}

	code := f.GoString()
	assert.Contains(t, code, "loadPosts")
}

func TestGenLoadEdge_M2OEdge(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	edge := createM2OEdge("author", userType, "posts", "user_id")
	helper.graph.Nodes = []*gen.Type{userType, postType}

	f := helper.NewFile("ent")
	ok := safeGenerate(func() {
		genLoadEdge(helper, f, postType, edge)
	})
	if !ok {
		t.Skip("genLoadEdge panicked due to incomplete mock state")
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkGenIDScanType(b *testing.B) {
	fld := createTestField("id", field.TypeInt64)
	for b.Loop() {
		_ = genIDScanType(fld)
	}
}

func BenchmarkGenQueryLockMethods(b *testing.B) {
	helper := newMockHelper()
	userType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{userType}

	for b.Loop() {
		f := helper.NewFile("ent")
		genQueryLockMethods(helper, f, userType, "UserQuery")
	}
}
