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

// renderGroup renders the statements fn appends to a jen.Group as a block,
// so generator helpers that write into a caller's group can be asserted on.
func renderGroup(fn func(*jen.Group)) string {
	return jen.BlockFunc(fn).GoString()
}

// TestGenFieldAssignment pins the assignValues arm genFieldAssignment emits
// for each scan shape: the type the scanned value is asserted to, and how it
// is stored on the receiver (nullable wrapper field, new(T) for nillable
// fields, a deref for non-pointer values, a conversion for narrowed ints and
// floats, json.Unmarshal for JSON).
func TestGenFieldAssignment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		field   *gen.Field
		want    []string
		notWant []string
	}{
		{
			name:    "string",
			field:   createTestField("name", field.TypeString),
			want:    []string{"values[0].(*sql.NullString)", "if value.Valid {", "u.F = value.String\n"},
			notWant: []string{"new("},
		},
		{
			name:  "nillable string",
			field: createNillableField("bio", field.TypeString),
			want:  []string{"values[0].(*sql.NullString)", "u.F = new(string)\n", "*u.F = value.String\n"},
		},
		{
			name:    "bool",
			field:   createTestField("active", field.TypeBool),
			want:    []string{"values[0].(*sql.NullBool)", "u.F = value.Bool\n"},
			notWant: []string{"new("},
		},
		{
			name:  "nillable bool",
			field: createNillableField("active", field.TypeBool),
			want:  []string{"values[0].(*sql.NullBool)", "u.F = new(bool)\n", "*u.F = value.Bool\n"},
		},
		{
			name:    "int",
			field:   createTestField("age", field.TypeInt),
			want:    []string{"values[0].(*sql.NullInt64)", "u.F = int(value.Int64)\n"},
			notWant: []string{"new("},
		},
		{
			name:  "nillable int",
			field: createNillableField("score", field.TypeInt),
			want:  []string{"values[0].(*sql.NullInt64)", "u.F = new(int)\n", "*u.F = int(value.Int64)\n"},
		},
		{
			name:  "int8",
			field: createTestField("priority", field.TypeInt8),
			want:  []string{"values[0].(*sql.NullInt64)", "u.F = int8(value.Int64)\n"},
		},
		{
			name:  "uint8",
			field: createTestField("flags", field.TypeUint8),
			want:  []string{"values[0].(*sql.NullInt64)", "u.F = uint8(value.Int64)\n"},
		},
		{
			name:    "float64",
			field:   createTestField("price", field.TypeFloat64),
			want:    []string{"values[0].(*sql.NullFloat64)", "u.F = value.Float64\n"},
			notWant: []string{"new("},
		},
		{
			name:  "nillable float64",
			field: createNillableField("score", field.TypeFloat64),
			want:  []string{"values[0].(*sql.NullFloat64)", "u.F = new(float64)\n", "*u.F = value.Float64\n"},
		},
		{
			name:  "float32",
			field: createTestField("rating", field.TypeFloat32),
			want:  []string{"values[0].(*sql.NullFloat64)", "u.F = float32(value.Float64)\n"},
		},
		{
			name:    "time",
			field:   createTestField("created_at", field.TypeTime),
			want:    []string{"values[0].(*sql.NullTime)", "u.F = value.Time\n"},
			notWant: []string{"new("},
		},
		{
			name:  "nillable time",
			field: createNillableField("deleted_at", field.TypeTime),
			want:  []string{"values[0].(*sql.NullTime)", "u.F = new(time.Time)\n", "*u.F = value.Time\n"},
		},
		{
			name:  "enum converts the scanned string",
			field: createEnumField("status", []string{"active", "inactive"}),
			want:  []string{"values[0].(*sql.NullString)", "u.F = Status(value.String)\n"},
		},
		{
			name:  "uuid is dereferenced",
			field: createTestField("uuid", field.TypeUUID),
			want:  []string{"values[0].(*[16]byte)", "if value != nil {", "u.F = *value\n"},
		},
		{
			name:  "bytes are dereferenced",
			field: createTestField("data", field.TypeBytes),
			want:  []string{"values[0].(*[]byte)", "if value != nil {", "u.F = *value\n"},
		},
		{
			name:    "json is unmarshaled",
			field:   &gen.Field{Name: "metadata", Type: &field.TypeInfo{Type: field.TypeJSON}},
			want:    []string{"values[0].(*[]byte)", "value != nil && len(*value) > 0", "json.Unmarshal(*value, &u.F)", `"unmarshal field metadata: %w"`},
			notWant: []string{"u.F = "},
		},
		{
			name:  "other type is dereferenced",
			field: &gen.Field{Name: "custom", Type: &field.TypeInfo{Type: field.TypeOther, Ident: "MyType"}},
			want:  []string{"values[0].(*MyType)", "u.F = *value\n"},
		},
		{
			name: "nillable other type keeps the pointer",
			field: &gen.Field{
				Name:     "custom",
				Type:     &field.TypeInfo{Type: field.TypeOther, Ident: "MyType", RType: &field.RType{Ident: "MyType"}},
				Nillable: true,
			},
			want:    []string{"values[0].(*MyType)", "u.F = value\n"},
			notWant: []string{"*value\n"},
		},
		{
			// RType.IsPtr, not nillable: the value is assigned without a deref
			// (same as Ent's decode template).
			name: "pointer go type keeps the pointer",
			field: &gen.Field{
				Name: "custom",
				Type: &field.TypeInfo{
					Type:  field.TypeOther,
					Ident: "*MyType",
					RType: &field.RType{Ident: "*MyType", Kind: reflect.Pointer},
				},
			},
			want:    []string{"values[0].(**MyType)", "u.F = value\n"},
			notWant: []string{"*value\n"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code := renderGroup(func(g *jen.Group) {
				genFieldAssignment(newMockHelper(), g, createTestType("User"), tt.field, "0", "u", "F")
			})
			// Every arm rejects a slot of the wrong type with a named error.
			assert.Contains(t, code, `fmt.Errorf("unexpected type %T for field `+tt.field.Name+`", values[0])`)
			for _, w := range tt.want {
				assert.Contains(t, code, w)
			}
			for _, nw := range tt.notWant {
				assert.NotContains(t, code, nw)
			}
		})
	}
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
	require.NotEmpty(t, userType.Fields)
	require.True(t, userType.Fields[0].HasValueScanner(), "expected ValueScanner to be true")

	code := renderGroup(func(g *jen.Group) {
		genFieldAssignment(newMockHelper(), g, userType, userType.Fields[0], "0", "u", "CustomType")
	})
	// An external ValueScanner decodes through its FromValue func and
	// propagates its error instead of type-asserting the slot.
	assert.Contains(t, code, "if value, err := user.ValueScanner.CustomType.FromValue(values[0]); err != nil {")
	assert.Contains(t, code, "return err\n")
	assert.Contains(t, code, "u.CustomType = value\n")
	assert.NotContains(t, code, "unexpected type")
}

// =============================================================================
// genScanTypeFieldExpr Tests
// =============================================================================

func TestGenScanTypeFieldExpr(t *testing.T) {
	t.Parallel()
	render := func(c jen.Code) string { return jen.Add(c).GoString() }

	assert.Equal(t, "value.String", render(genScanTypeFieldExpr(createTestField("name", field.TypeString), false)))
	assert.Equal(t, "value.Int64", render(genScanTypeFieldExpr(createTestField("id", field.TypeInt64), false)))
	assert.Equal(t, "int8(value.Int64)", render(genScanTypeFieldExpr(createTestField("n", field.TypeInt8), false)))

	enum := createEnumField("status", []string{"active", "inactive"})
	// Same package (entity model): the enum type is referenced unqualified.
	assert.Equal(t, "Status(value.String)", render(genScanTypeFieldExpr(enum, true)))
}
