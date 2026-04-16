package sql

import (
	"reflect"
	"testing"

	"github.com/dave/jennifer/jen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// zeroValue Tests
// =============================================================================

func TestZeroValue(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	helper := newMockHelper()
	result := zeroValue(helper, nil)
	assert.NotNil(t, result)
}

// =============================================================================
// baseZeroValue Tests (comprehensive branch coverage)
// =============================================================================

func TestBaseZeroValue_NilField(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	result := baseZeroValue(helper, nil)
	assert.NotNil(t, result)
}

func TestBaseZeroValue_AllTypes(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	helper := newMockHelper()
	jsonField := &gen.Field{
		Name: "data",
		Type: &field.TypeInfo{
			Type:    field.TypeJSON,
			RType:   &field.RType{Ident: "map[string]interface {}", Kind: reflect.Map},
			PkgPath: "",
		},
	}

	result := baseZeroValue(helper, jsonField)
	assert.NotNil(t, result)
}

func TestBaseZeroValue_JSON_NoGoType(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	helper := newMockHelper()
	f := &gen.Field{Name: "data"}
	result := jsonFieldZeroValue(helper, f)
	assert.NotNil(t, result)
}

func TestJsonFieldZeroValue_NilRType(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	f := &gen.Field{
		Name: "data",
		Type: &field.TypeInfo{Type: field.TypeJSON},
	}
	result := jsonFieldZeroValue(helper, f)
	assert.NotNil(t, result)
}

func TestJsonFieldZeroValue_AllBranches(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()

	tests := []struct {
		name  string
		ident string
		kind  reflect.Kind
	}{
		{"map_string_any", "map[string]any", reflect.Map},
		{"map_string_interface", "map[string]interface {}", reflect.Map},
		{"slice_any", "[]any", reflect.Slice},
		{"slice_map_string_any", "[]map[string]any", reflect.Slice},
		{"slice_interface", "[]interface {}", reflect.Slice},
		{"generic_slice", "[]MyType", reflect.Slice},
		{"generic_map", "map[string]MyType", reflect.Map},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &gen.Field{
				Name: "data",
				Type: &field.TypeInfo{
					Type:  field.TypeJSON,
					RType: &field.RType{Ident: tt.ident, Kind: tt.kind},
				},
			}
			result := jsonFieldZeroValue(helper, f)
			assert.NotNil(t, result)
		})
	}
}

func TestJsonFieldZeroValue_StructWithPkgPath(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	f := &gen.Field{
		Name: "data",
		Type: &field.TypeInfo{
			Type:    field.TypeJSON,
			Ident:   "mypkg.MyStruct",
			PkgPath: "github.com/test/mypkg",
			RType:   &field.RType{Ident: "mypkg.MyStruct", Kind: reflect.Struct},
		},
	}
	result := jsonFieldZeroValue(helper, f)
	assert.NotNil(t, result)
}

func TestJsonFieldZeroValue_StructWithIdent(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	f := &gen.Field{
		Name: "data",
		Type: &field.TypeInfo{
			Type:  field.TypeJSON,
			Ident: "MyStruct",
			RType: &field.RType{Ident: "MyStruct", Kind: reflect.Struct},
		},
	}
	result := jsonFieldZeroValue(helper, f)
	assert.NotNil(t, result)
}

func TestJsonFieldZeroValue_DefaultFallback(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	f := &gen.Field{
		Name: "data",
		Type: &field.TypeInfo{
			Type:  field.TypeJSON,
			RType: &field.RType{Ident: "SomeUnknownType", Kind: reflect.Struct},
		},
	}
	result := jsonFieldZeroValue(helper, f)
	assert.NotNil(t, result)
}

// =============================================================================
// Field-backed edge validation tests (Bug #1 fix)
// =============================================================================

func TestGenWrapperCreateCheck_FieldBackedEdge(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestType("User")
	groupType := createTestType("Group")

	// Create a field-backed M2O edge: edge.From("group").Field("group_id")
	fkField := &gen.Field{
		Name:        "group_id",
		Type:        &field.TypeInfo{Type: field.TypeInt64},
		UserDefined: true,
	}
	edge := createM2OEdge("group", groupType, "users", "group_id")
	edge.SetDef(&load.Edge{Field: "group_id"})
	fk := &gen.ForeignKey{
		Field:       fkField,
		Edge:        edge,
		UserDefined: true,
	}
	edge.Rel.SetForeignKey(fk)
	// Mark fkField as an edge field
	fkField.SetForeignKey(fk)
	userType.Edges = []*gen.Edge{edge}
	userType.Fields = append(userType.Fields, fkField)
	helper.graph.Nodes = []*gen.Type{userType, groupType}

	f := helper.NewFile("ent")
	genCreateCheck(helper, f, userType, "UserCreate", "c")

	code := f.GoString()
	// The generated check should use the field getter (GroupID) not the edge IDs (GroupIDs).
	// With field-backed edges, mutation.GroupID() is how the value is set,
	// not mutation.GroupIDs() which reads from the edge ID store.
	assert.Contains(t, code, "mutation.GroupID()", "field-backed edge should check field value, not edge IDs")
	assert.NotContains(t, code, "mutation.GroupIDs()", "field-backed edge should NOT check edge IDs")
}

func TestGenWrapperCreateCheck_RegularEdge(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestType("User")
	groupType := createTestType("Group")

	// Create a regular M2O edge (no user-defined field)
	edge := createM2OEdge("group", groupType, "users", "group_id")
	userType.Edges = []*gen.Edge{edge}
	helper.graph.Nodes = []*gen.Type{userType, groupType}

	f := helper.NewFile("ent")
	genCreateCheck(helper, f, userType, "UserCreate", "c")

	code := f.GoString()
	// Regular edge should still check edge IDs
	assert.Contains(t, code, "GroupIDs()", "regular edge should check edge IDs")
}

func TestGenMutation_FieldBackedEdge_TypedFieldStorage(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	userType := createTestType("User")
	groupType := createTestType("Group")

	// Create a field-backed M2O edge: field.Int("group_id") + edge.From("group").Field("group_id")
	fkField := &gen.Field{
		Name:        "group_id",
		Type:        &field.TypeInfo{Type: field.TypeInt64},
		UserDefined: true,
	}
	edge := createM2OEdge("group", groupType, "users", "group_id")
	edge.SetDef(&load.Edge{Field: "group_id"})
	fk := &gen.ForeignKey{
		Field:       fkField,
		Edge:        edge,
		UserDefined: true,
	}
	edge.Rel.SetForeignKey(fk)
	fkField.SetForeignKey(fk)
	userType.Edges = []*gen.Edge{edge}
	userType.Fields = append(userType.Fields, fkField)
	helper.graph.Nodes = []*gen.Type{userType, groupType}

	f := genMutation(helper, userType)
	code := f.GoString()
	// SetGroupID writes only the typed pointer field — no dual-write.
	assert.Contains(t, code, `m._group_id = &v`, "field setter should write typed pointer")
	assert.NotContains(t, code, `Set("group_id"`, "field setter must not dual-write")
	// Constructor no longer registers field↔edge mappings.
	assert.NotContains(t, code, `EdgeToField`, "constructor should not register EdgeToField mapping")
	assert.NotContains(t, code, `FieldToEdge`, "constructor should not register FieldToEdge mapping")
}

// =============================================================================
// genFieldAssignment Tests
// =============================================================================

func TestGenFieldAssignment_StringField(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	fld := createTestField("name", field.TypeString)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Name")
	// Should not panic
}

func TestGenFieldAssignment_NillableField(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	fld := createNillableField("bio", field.TypeString)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Bio")
	// Should not panic
}

func TestGenFieldAssignment_BoolField(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	fld := createTestField("active", field.TypeBool)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Active")
}

func TestGenFieldAssignment_IntField(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	fld := createTestField("age", field.TypeInt)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Age")
}

func TestGenFieldAssignment_JSONField(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	helper := newMockHelper()
	fld := createTestField("created_at", field.TypeTime)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "CreatedAt")
}

func TestGenFieldAssignment_NillableIntField(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	fld := createNillableField("score", field.TypeInt)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Score")
}

func TestGenFieldAssignment_UUIDField(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	fld := createTestField("uuid", field.TypeUUID)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "UUID")
}

func TestGenFieldAssignment_EnumField(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	fld := createEnumField("status", []string{"active", "inactive"})

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Status")
}

func TestGenFieldAssignment_Float64Field(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	fld := createTestField("price", field.TypeFloat64)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Price")
}

// =============================================================================
// genFieldAssignment Additional Branch Coverage
// =============================================================================

func TestGenFieldAssignment_NillableBoolField(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	fld := createNillableField("active", field.TypeBool)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Active")
}

func TestGenFieldAssignment_NillableTimeField(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	fld := createNillableField("deleted_at", field.TypeTime)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "DeletedAt")
}

func TestGenFieldAssignment_Float32Field(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	fld := createTestField("rating", field.TypeFloat32)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Rating")
}

func TestGenFieldAssignment_BytesField(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	fld := createTestField("data", field.TypeBytes)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Data")
}

func TestGenFieldAssignment_NillableFloat64(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	fld := createNillableField("score", field.TypeFloat64)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Score")
}

func TestGenFieldAssignment_OtherField(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	fld := &gen.Field{
		Name: "custom",
		Type: &field.TypeInfo{Type: field.TypeOther, Ident: "MyType"},
	}

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Custom")
}

// =============================================================================
// genScanTypeFieldExpr Tests
// =============================================================================

func TestGenScanTypeFieldExpr_RegularField(t *testing.T) {
	t.Parallel()
	fld := createTestField("name", field.TypeString)
	result := genScanTypeFieldExpr(fld, false)
	assert.NotNil(t, result)
}

func TestGenScanTypeFieldExpr_EnumField(t *testing.T) {
	t.Parallel()
	fld := createEnumField("status", []string{"active", "inactive"})
	result := genScanTypeFieldExpr(fld, false)
	assert.NotNil(t, result)
}

// =============================================================================
// genFieldAssignment Additional Coverage (nillable pointer type)
// =============================================================================

func TestGenFieldAssignment_NillableWithPointerRType(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	fld := &gen.Field{
		Name:     "custom",
		Type:     &field.TypeInfo{Type: field.TypeOther, Ident: "MyType", RType: &field.RType{Ident: "MyType"}},
		Nillable: true,
	}

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Custom")
}

func TestGenFieldAssignment_Uint8Field(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	fld := createTestField("flags", field.TypeUint8)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Flags")
}

func TestGenFieldAssignment_Int8Field(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	fld := createTestField("priority", field.TypeInt8)

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Priority")
}

func TestGenFieldAssignment_FieldWithPointerRType(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	// Field with RType.IsPtr() true, Nillable false — tests the else branch
	// where value is assigned without dereference
	fld := &gen.Field{
		Name: "custom",
		Type: &field.TypeInfo{
			Type:  field.TypeOther,
			Ident: "*MyType",
			RType: &field.RType{
				Ident: "*MyType",
				Kind:  reflect.Pointer,
			},
		},
		Nillable: false,
	}

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Custom")
}

func TestGenFieldAssignment_NillableEnumField(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	fld := createEnumField("status", []string{"active", "inactive"})
	fld.Nillable = true

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, createTestType("User"), fld, "0", "u", "Status")
}

func TestGenFieldAssignment_ValueScannerField(t *testing.T) {
	t.Parallel()
	// ValueScanner requires f.def.ValueScanner = true, which needs gen.NewType
	// via load.Schema. Creating a field with ValueScanner through the schema loading path.
	userType := createTypeWithSchemaFields(t, "User", []*load.Field{
		{
			Name: "custom_type",
			Info: &field.TypeInfo{
				Type:    field.TypeOther,
				Ident:   "mypkg.MyType",
				PkgPath: "github.com/test/mypkg",
				RType: &field.RType{
					Ident:   "mypkg.MyType",
					Kind:    reflect.Struct,
					PkgPath: "github.com/test/mypkg",
					Name:    "MyType",
				},
			},
			ValueScanner: true,
		},
	})
	helper := newMockHelper()

	// Verify the field has ValueScanner set
	require.Greater(t, len(userType.Fields), 0, "expected at least one field")
	require.True(t, userType.Fields[0].HasValueScanner(), "expected ValueScanner to be true")

	grp := &jen.Group{}
	genFieldAssignment(helper, grp, userType, userType.Fields[0], "0", "u", "CustomType")
}
