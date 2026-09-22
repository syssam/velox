package gen

// Tests for Field accessors and scan-type helpers (type_field.go).

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// type_field.go — zero-coverage functions
// =============================================================================

func TestField_NillableValue(t *testing.T) {
	// Nillable=true, no RType → true (not a pointer already).
	f := Field{
		Nillable: true,
		Type:     &field.TypeInfo{Type: field.TypeString},
	}
	assert.True(t, f.NillableValue())

	// Not nillable → false.
	f2 := Field{
		Nillable: false,
		Type:     &field.TypeInfo{Type: field.TypeString},
	}
	assert.False(t, f2.NillableValue())

	// Nillable but RType is already a pointer → false.
	f3 := Field{
		Nillable: true,
		Type: &field.TypeInfo{
			Type:  field.TypeString,
			RType: &field.RType{Kind: reflect.Pointer},
		},
	}
	assert.False(t, f3.NillableValue())
}

func TestField_ScanType(t *testing.T) {
	tests := []struct {
		name string
		ft   field.Type
		want string
	}{
		{"json", field.TypeJSON, "[]byte"},
		{"bytes", field.TypeBytes, "[]byte"},
		{"string", field.TypeString, "sql.NullString"},
		{"enum", field.TypeEnum, "sql.NullString"},
		{"bool", field.TypeBool, "sql.NullBool"},
		{"time", field.TypeTime, "sql.NullTime"},
		{"int", field.TypeInt, "sql.NullInt64"},
		{"int8", field.TypeInt8, "sql.NullInt64"},
		{"int16", field.TypeInt16, "sql.NullInt64"},
		{"int32", field.TypeInt32, "sql.NullInt64"},
		{"int64", field.TypeInt64, "sql.NullInt64"},
		{"uint", field.TypeUint, "sql.NullInt64"},
		{"uint8", field.TypeUint8, "sql.NullInt64"},
		{"uint16", field.TypeUint16, "sql.NullInt64"},
		{"uint32", field.TypeUint32, "sql.NullInt64"},
		{"uint64", field.TypeUint64, "sql.NullInt64"},
		{"float32", field.TypeFloat32, "sql.NullFloat64"},
		{"float64", field.TypeFloat64, "sql.NullFloat64"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := Field{Type: &field.TypeInfo{Type: tt.ft}}
			assert.Equal(t, tt.want, f.ScanType())
		})
	}
}

func TestField_NewScanType(t *testing.T) {
	tests := []struct {
		name string
		ft   field.Type
		want string
	}{
		{"json", field.TypeJSON, "new([]byte)"},
		{"bytes", field.TypeBytes, "new([]byte)"},
		{"string", field.TypeString, "new(sql.NullString)"},
		{"enum", field.TypeEnum, "new(sql.NullString)"},
		{"bool", field.TypeBool, "new(sql.NullBool)"},
		{"time", field.TypeTime, "new(sql.NullTime)"},
		{"int", field.TypeInt, "new(sql.NullInt64)"},
		{"float64", field.TypeFloat64, "new(sql.NullFloat64)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := Field{Type: &field.TypeInfo{Type: tt.ft}}
			assert.Equal(t, tt.want, f.NewScanType())
		})
	}
}

func TestField_ValueFunc_NoScanner(t *testing.T) {
	f := Field{
		Name: "name",
		def:  &load.Field{ValueScanner: false},
	}
	_, err := f.ValueFunc()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not have an external ValueScanner")
}

func TestField_ScanValueFunc_NoScanner(t *testing.T) {
	f := Field{
		Name: "name",
		def:  &load.Field{ValueScanner: false},
	}
	_, err := f.ScanValueFunc()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not have an external ValueScanner")
}

func TestField_FromValueFunc_NoScanner(t *testing.T) {
	f := Field{
		Name: "name",
		def:  &load.Field{ValueScanner: false},
	}
	_, err := f.FromValueFunc()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not have an external ValueScanner")
}

func TestField_SupportsMutationAppend(t *testing.T) {
	// JSON slice field → true.
	f := Field{
		Type: &field.TypeInfo{
			Type:  field.TypeJSON,
			RType: &field.RType{Kind: reflect.Slice},
		},
	}
	assert.True(t, f.SupportsMutationAppend())

	// JSON but non-slice → false.
	f2 := Field{
		Type: &field.TypeInfo{
			Type:  field.TypeJSON,
			RType: &field.RType{Kind: reflect.Map},
		},
	}
	assert.False(t, f2.SupportsMutationAppend())

	// Not JSON → false.
	f3 := Field{
		Type: &field.TypeInfo{
			Type:  field.TypeString,
			RType: &field.RType{Kind: reflect.Slice},
		},
	}
	assert.False(t, f3.SupportsMutationAppend())

	// nil RType → false.
	f4 := Field{
		Type: &field.TypeInfo{Type: field.TypeJSON},
	}
	assert.False(t, f4.SupportsMutationAppend())
}

func TestField_SignedType(t *testing.T) {
	tests := []struct {
		in  field.Type
		out field.Type
	}{
		{field.TypeUint8, field.TypeInt8},
		{field.TypeUint16, field.TypeInt16},
		{field.TypeUint32, field.TypeInt32},
		{field.TypeUint64, field.TypeInt64},
		{field.TypeUint, field.TypeInt},
		{field.TypeInt, field.TypeInt}, // signed int stays int
	}
	for _, tt := range tests {
		t.Run(tt.in.String(), func(t *testing.T) {
			f := Field{
				Name: "count",
				Type: &field.TypeInfo{Type: tt.in},
			}
			signed, err := f.SignedType()
			require.NoError(t, err)
			assert.Equal(t, tt.out, signed.Type)
		})
	}
}

func TestField_SignedType_Error(t *testing.T) {
	// String field doesn't support MutationAdd.
	f := Field{
		Name: "name",
		Type: &field.TypeInfo{Type: field.TypeString},
	}
	_, err := f.SignedType()
	assert.Error(t, err)
}

func TestField_MutationAddAssignExpr(t *testing.T) {
	// Basic int field.
	f := Field{
		Name: "age",
		Type: &field.TypeInfo{Type: field.TypeInt},
	}
	expr, err := f.MutationAddAssignExpr("m.age", "v")
	require.NoError(t, err)
	assert.Equal(t, "*m.age += v", expr)
}

func TestField_MutationAddAssignExpr_Error(t *testing.T) {
	// String field → no add support.
	f := Field{
		Name: "name",
		Type: &field.TypeInfo{Type: field.TypeString},
	}
	_, err := f.MutationAddAssignExpr("m.name", "v")
	assert.Error(t, err)
}

func TestField_BasicType_NoGoType(t *testing.T) {
	// No GoType → returns the ident unchanged.
	f := Field{Type: &field.TypeInfo{Type: field.TypeString}}
	assert.Equal(t, "v", f.BasicType("v"))
}

func TestField_EnumPkgPath(t *testing.T) {
	// nil typ → empty.
	f := Field{Type: &field.TypeInfo{Type: field.TypeEnum}}
	assert.Equal(t, "", f.EnumPkgPath())

	// With typ and cfg.
	typ := &Type{Name: "User"}
	cfg := &Config{Package: "example.com/project/ent"}
	f2 := Field{
		Type: &field.TypeInfo{Type: field.TypeEnum},
		typ:  typ,
		cfg:  cfg,
	}
	assert.Equal(t, "example.com/project/ent/user", f2.EnumPkgPath())

	// With typ but no package in cfg → just dir.
	f3 := Field{
		Type: &field.TypeInfo{Type: field.TypeEnum},
		typ:  typ,
		cfg:  &Config{},
	}
	assert.Equal(t, "user", f3.EnumPkgPath())
}

func TestField_Ops_NilCfg(t *testing.T) {
	// Ops() should not panic when cfg is nil.
	f := &Field{
		Name: "age",
		Type: &field.TypeInfo{Type: field.TypeInt},
	}
	ops := f.Ops()
	assert.NotNil(t, ops)
}

func TestField_BuilderField_EdgeField(t *testing.T) {
	// We can at least call on a non-edge field to confirm the non-panic path.
	fNonEdge := Field{Name: "email"}
	assert.Equal(t, "email", fNonEdge.BuilderField())
}

func TestField_DefaultValue_Nil(t *testing.T) {
	f := Field{}
	assert.Nil(t, f.DefaultValue())
}

func TestField_DefaultValue_WithDef(t *testing.T) {
	f := Field{def: &load.Field{DefaultValue: "hello"}}
	assert.Equal(t, "hello", f.DefaultValue())
}

func TestField_DefaultFunc_Nil(t *testing.T) {
	f := Field{}
	assert.False(t, f.DefaultFunc())
}

func TestField_DefaultFunc_Func(t *testing.T) {
	f := Field{def: &load.Field{DefaultKind: reflect.Func}}
	assert.True(t, f.DefaultFunc())
}

func TestField_DefaultFunc_NonFunc(t *testing.T) {
	f := Field{def: &load.Field{DefaultKind: reflect.String}}
	assert.False(t, f.DefaultFunc())
}

// =============================================================================
// type_field.go — rtypeEqual, goType, standardNullType
// =============================================================================

func TestRtypeEqual(t *testing.T) {
	t1 := &field.RType{Kind: reflect.String, Ident: "string", PkgPath: ""}
	t2 := &field.RType{Kind: reflect.String, Ident: "string", PkgPath: ""}
	assert.True(t, rtypeEqual(t1, t2))

	t3 := &field.RType{Kind: reflect.Int, Ident: "int", PkgPath: ""}
	assert.False(t, rtypeEqual(t1, t3))
}

func TestField_GoType_NoGoType(t *testing.T) {
	// goType with no custom type → returns ident unchanged.
	f := Field{Type: &field.TypeInfo{Type: field.TypeString}}
	assert.Equal(t, "v", f.goType("v"))
}

// =============================================================================
// type_field.go — ScanTypeField (basic path)
// =============================================================================

func TestField_ScanTypeField_BasicTypes(t *testing.T) {
	tests := []struct {
		name   string
		ft     field.Type
		rec    string
		wantFn func(string) bool
	}{
		{"string", field.TypeString, "v", func(s string) bool { return s != "" }},
		{"bool", field.TypeBool, "v", func(s string) bool { return s != "" }},
		{"int64", field.TypeInt64, "v", func(s string) bool { return s != "" }},
		{"float64", field.TypeFloat64, "v", func(s string) bool { return s != "" }},
		{"time", field.TypeTime, "v", func(s string) bool { return s == "v.Time" }},
		{"float32", field.TypeFloat32, "v", func(s string) bool { return s != "" }},
		{"int", field.TypeInt, "v", func(s string) bool { return s != "" }},
		{"uint", field.TypeUint, "v", func(s string) bool { return s != "" }},
		{"json", field.TypeJSON, "v", func(s string) bool { return s == "v" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := Field{Type: &field.TypeInfo{Type: tt.ft}}
			result := f.ScanTypeField(tt.rec)
			assert.True(t, tt.wantFn(result), "ScanTypeField(%q) = %q", tt.rec, result)
		})
	}
}
