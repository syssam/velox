package field_test

import (
	"database/sql/driver"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/syssam/velox/schema/field"
)

func TestType_Float(t *testing.T) {
	tests := []struct {
		name string
		typ  field.Type
		want bool
	}{
		{"Float32", field.TypeFloat32, true},
		{"Float64", field.TypeFloat64, true},
		{"Int", field.TypeInt, false},
		{"Int64", field.TypeInt64, false},
		{"String", field.TypeString, false},
		{"Bool", field.TypeBool, false},
		{"Invalid", field.TypeInvalid, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.typ.Float())
		})
	}
}

func TestType_Integer(t *testing.T) {
	tests := []struct {
		name string
		typ  field.Type
		want bool
	}{
		{"Int", field.TypeInt, true},
		{"Int8", field.TypeInt8, true},
		{"Int16", field.TypeInt16, true},
		{"Int32", field.TypeInt32, true},
		{"Int64", field.TypeInt64, true},
		{"Uint", field.TypeUint, true},
		{"Uint8", field.TypeUint8, true},
		{"Uint16", field.TypeUint16, true},
		{"Uint32", field.TypeUint32, true},
		{"Uint64", field.TypeUint64, true},
		{"Float32_not_integer", field.TypeFloat32, false},
		{"Float64_not_integer", field.TypeFloat64, false},
		{"String_not_integer", field.TypeString, false},
		{"Bool_not_integer", field.TypeBool, false},
		{"Invalid_not_integer", field.TypeInvalid, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.typ.Integer())
		})
	}
}

func TestType_IsStandardType(t *testing.T) {
	standardTypes := []field.Type{
		field.TypeBool, field.TypeString, field.TypeTime,
		field.TypeBytes, field.TypeUUID, field.TypeJSON,
		field.TypeInt, field.TypeInt8, field.TypeInt16, field.TypeInt32, field.TypeInt64,
		field.TypeUint, field.TypeUint8, field.TypeUint16, field.TypeUint32, field.TypeUint64,
		field.TypeFloat32, field.TypeFloat64,
	}
	for _, typ := range standardTypes {
		t.Run(typ.String()+"_standard", func(t *testing.T) {
			assert.True(t, typ.IsStandardType(), "%s should be standard", typ)
		})
	}

	nonStandardTypes := []struct {
		name string
		typ  field.Type
	}{
		{"Invalid", field.TypeInvalid},
		{"Enum", field.TypeEnum},
		{"Other", field.TypeOther},
	}
	for _, tt := range nonStandardTypes {
		t.Run(tt.name+"_not_standard", func(t *testing.T) {
			assert.False(t, tt.typ.IsStandardType(), "%s should not be standard", tt.name)
		})
	}
}

func TestType_Valid(t *testing.T) {
	tests := []struct {
		name string
		typ  field.Type
		want bool
	}{
		{"Invalid", field.TypeInvalid, false},
		{"Bool", field.TypeBool, true},
		{"String", field.TypeString, true},
		{"Int", field.TypeInt, true},
		{"Float64", field.TypeFloat64, true},
		{"Enum", field.TypeEnum, true},
		{"Other", field.TypeOther, true},
		{"OutOfRange", field.Type(255), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.typ.Valid())
		})
	}
}

func TestType_Numeric(t *testing.T) {
	numericTypes := []field.Type{
		field.TypeInt8, field.TypeInt16, field.TypeInt32, field.TypeInt, field.TypeInt64,
		field.TypeUint8, field.TypeUint16, field.TypeUint32, field.TypeUint, field.TypeUint64,
		field.TypeFloat32, field.TypeFloat64,
	}
	for _, typ := range numericTypes {
		t.Run(typ.String()+"_numeric", func(t *testing.T) {
			assert.True(t, typ.Numeric(), "%s should be numeric", typ)
		})
	}

	nonNumericTypes := []field.Type{
		field.TypeInvalid, field.TypeBool, field.TypeTime,
		field.TypeJSON, field.TypeUUID, field.TypeBytes,
		field.TypeEnum, field.TypeString, field.TypeOther,
	}
	for _, typ := range nonNumericTypes {
		t.Run(typ.String()+"_not_numeric", func(t *testing.T) {
			assert.False(t, typ.Numeric(), "%s should not be numeric", typ)
		})
	}
}

func TestType_ConstName(t *testing.T) {
	tests := []struct {
		name string
		typ  field.Type
		want string
	}{
		// Types with explicit constNames entries
		{"JSON", field.TypeJSON, "TypeJSON"},
		{"UUID", field.TypeUUID, "TypeUUID"},
		{"Time", field.TypeTime, "TypeTime"},
		{"Enum", field.TypeEnum, "TypeEnum"},
		{"Bytes", field.TypeBytes, "TypeBytes"},
		{"Other", field.TypeOther, "TypeOther"},
		// Types using fallback Title-case logic
		{"Bool", field.TypeBool, "TypeBool"},
		{"String", field.TypeString, "TypeString"},
		{"Int", field.TypeInt, "TypeInt"},
		{"Int8", field.TypeInt8, "TypeInt8"},
		{"Int64", field.TypeInt64, "TypeInt64"},
		{"Uint", field.TypeUint, "TypeUint"},
		{"Float32", field.TypeFloat32, "TypeFloat32"},
		{"Float64", field.TypeFloat64, "TypeFloat64"},
		// Invalid type
		{"Invalid", field.TypeInvalid, "invalid"},
		{"OutOfRange", field.Type(255), "invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.typ.ConstName())
		})
	}
}

func TestType_String(t *testing.T) {
	tests := []struct {
		name string
		typ  field.Type
		want string
	}{
		{"Invalid", field.TypeInvalid, "invalid"},
		{"Bool", field.TypeBool, "bool"},
		{"Time", field.TypeTime, "time.Time"},
		{"JSON", field.TypeJSON, "json.RawMessage"},
		{"UUID", field.TypeUUID, "[16]byte"},
		{"Bytes", field.TypeBytes, "[]byte"},
		{"Enum", field.TypeEnum, "string"},
		{"String", field.TypeString, "string"},
		{"Other", field.TypeOther, "other"},
		{"Int", field.TypeInt, "int"},
		{"Int8", field.TypeInt8, "int8"},
		{"Int16", field.TypeInt16, "int16"},
		{"Int32", field.TypeInt32, "int32"},
		{"Int64", field.TypeInt64, "int64"},
		{"Uint", field.TypeUint, "uint"},
		{"Uint8", field.TypeUint8, "uint8"},
		{"Uint16", field.TypeUint16, "uint16"},
		{"Uint32", field.TypeUint32, "uint32"},
		{"Uint64", field.TypeUint64, "uint64"},
		{"Float32", field.TypeFloat32, "float32"},
		{"Float64", field.TypeFloat64, "float64"},
		{"OutOfRange", field.Type(255), "invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.typ.String())
		})
	}
}

func TestTypeInfo_Validator(t *testing.T) {
	// TypeInfo.Validator() delegates to RType.Implements(validatorType).
	// A nil RType returns false.
	ti := field.TypeInfo{Type: field.TypeString}
	assert.False(t, ti.Validator(), "nil RType should not implement Validator")
}

func TestTypeInfo_Comparable(t *testing.T) {
	tests := []struct {
		name string
		ti   field.TypeInfo
		want bool
	}{
		{"Bool", field.TypeInfo{Type: field.TypeBool}, true},
		{"Time", field.TypeInfo{Type: field.TypeTime}, true},
		{"UUID", field.TypeInfo{Type: field.TypeUUID}, true},
		{"Enum", field.TypeInfo{Type: field.TypeEnum}, true},
		{"String", field.TypeInfo{Type: field.TypeString}, true},
		{"Other", field.TypeInfo{Type: field.TypeOther}, true},
		{"Int", field.TypeInfo{Type: field.TypeInt}, true},
		{"Int64", field.TypeInfo{Type: field.TypeInt64}, true},
		{"Float64", field.TypeInfo{Type: field.TypeFloat64}, true},
		{"JSON_not_comparable", field.TypeInfo{Type: field.TypeJSON}, false},
		{"Bytes_not_comparable", field.TypeInfo{Type: field.TypeBytes}, false},
		{"Invalid_not_comparable", field.TypeInfo{Type: field.TypeInvalid}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.ti.Comparable())
		})
	}
}

func TestTypeInfo_String(t *testing.T) {
	tests := []struct {
		name string
		ti   field.TypeInfo
		want string
	}{
		{
			name: "with_ident",
			ti:   field.TypeInfo{Type: field.TypeOther, Ident: "net.IP"},
			want: "net.IP",
		},
		{
			name: "without_ident_valid",
			ti:   field.TypeInfo{Type: field.TypeString},
			want: "string",
		},
		{
			name: "without_ident_invalid",
			ti:   field.TypeInfo{Type: field.Type(255)},
			want: "invalid",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.ti.String())
		})
	}
}

func TestTypeInfo_Valid(t *testing.T) {
	assert.True(t, field.TypeInfo{Type: field.TypeString}.Valid())
	assert.False(t, field.TypeInfo{Type: field.TypeInvalid}.Valid())
}

func TestTypeInfo_Numeric(t *testing.T) {
	assert.True(t, field.TypeInfo{Type: field.TypeInt64}.Numeric())
	assert.False(t, field.TypeInfo{Type: field.TypeString}.Numeric())
}

func TestTypeInfo_ConstName(t *testing.T) {
	assert.Equal(t, "TypeJSON", field.TypeInfo{Type: field.TypeJSON}.ConstName())
	assert.Equal(t, "TypeString", field.TypeInfo{Type: field.TypeString}.ConstName())
}

// RType tests

func TestRType_String(t *testing.T) {
	t.Run("with_rtype", func(t *testing.T) {
		rt := &field.RType{
			Name:  "MyType",
			Ident: "pkg.MyType",
		}
		// Without rtype set, returns Ident.
		assert.Equal(t, "pkg.MyType", rt.String())
	})

	t.Run("empty_ident", func(t *testing.T) {
		rt := &field.RType{}
		assert.Equal(t, "", rt.String())
	})
}

func TestRType_IsPtr(t *testing.T) {
	assert.True(t, (&field.RType{Kind: reflect.Pointer}).IsPtr())
	assert.False(t, (&field.RType{Kind: reflect.Struct}).IsPtr())
	// nil RType
	var rt *field.RType
	assert.False(t, rt.IsPtr())
}

func TestRType_TypeEqual(t *testing.T) {
	rt := &field.RType{
		Name:    "Stringer",
		Kind:    reflect.Interface,
		PkgPath: "fmt",
	}
	assert.True(t, rt.TypeEqual(reflect.TypeFor[fmt.Stringer]()))
	assert.False(t, rt.TypeEqual(reflect.TypeFor[error]()))
}

func TestRType_ImplementsStringer(t *testing.T) {
	// Build an RType that implements fmt.Stringer (has String() string method).
	stringerRType := &field.RType{
		Name:  "MyStringer",
		Ident: "pkg.MyStringer",
		Kind:  reflect.Struct,
		Methods: map[string]struct{ In, Out []*field.RType }{
			"String": {
				In: nil,
				Out: []*field.RType{
					{Name: "string", Kind: reflect.String, Ident: "string"},
				},
			},
		},
	}

	stringerType := reflect.TypeFor[fmt.Stringer]()
	assert.True(t, stringerRType.Implements(stringerType))

	// An RType missing the method should not implement Stringer.
	emptyRType := &field.RType{
		Name:    "Empty",
		Kind:    reflect.Struct,
		Methods: map[string]struct{ In, Out []*field.RType }{},
	}
	assert.False(t, emptyRType.Implements(stringerType))

	// nil RType returns false.
	var nilRType *field.RType
	assert.False(t, nilRType.Implements(stringerType))

	// Wrong output type should not match.
	wrongOutRType := &field.RType{
		Name: "WrongOut",
		Kind: reflect.Struct,
		Methods: map[string]struct{ In, Out []*field.RType }{
			"String": {
				In: nil,
				Out: []*field.RType{
					{Name: "int", Kind: reflect.Int, Ident: "int"},
				},
			},
		},
	}
	assert.False(t, wrongOutRType.Implements(stringerType))

	// Wrong number of outputs.
	wrongCountRType := &field.RType{
		Name: "WrongCount",
		Kind: reflect.Struct,
		Methods: map[string]struct{ In, Out []*field.RType }{
			"String": {
				In:  nil,
				Out: []*field.RType{},
			},
		},
	}
	assert.False(t, wrongCountRType.Implements(stringerType))
}

func TestRType_Implements_WithInput(t *testing.T) {
	// Test with driver.Valuer which has Value() (driver.Value, error)
	// and takes a receiver input.
	valuerType := reflect.TypeFor[driver.Valuer]()

	// Build an RType that matches driver.Valuer's Value method signature.
	// Value() (driver.Value, error)
	valuerRType := &field.RType{
		Name: "MyValuer",
		Kind: reflect.Struct,
		Methods: map[string]struct{ In, Out []*field.RType }{
			"Value": {
				In: nil,
				Out: []*field.RType{
					{Name: "Value", Kind: reflect.Interface, PkgPath: "database/sql/driver", Ident: "driver.Value"},
					{Name: "error", Kind: reflect.Interface, Ident: "error"},
				},
			},
		},
	}
	assert.True(t, valuerRType.Implements(valuerType))
}
