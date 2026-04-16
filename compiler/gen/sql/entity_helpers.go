package sql

import (
	"reflect"
	"strings"

	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

// zeroValue returns the zero value for a field type.
// For nillable fields, returns nil (for pointer types).
func zeroValue(h gen.GeneratorHelper, f *gen.Field) jen.Code {
	if f == nil {
		return jen.Lit(0)
	}
	if f.Nillable {
		return jen.Nil()
	}
	return baseZeroValue(h, f)
}

// baseZeroValue returns the zero value for the base type (ignoring nillability).
// Used in mutation getters where the return type is always the base type.
// Dispatches on field.Type enum constants (not Type.String()) for robustness.
func baseZeroValue(h gen.GeneratorHelper, f *gen.Field) jen.Code {
	if f == nil {
		return jen.Lit(0)
	}

	// Check for enum first - enums need type conversion: EnumType("")
	if f.IsEnum() {
		return jen.Add(h.BaseType(f)).Call(jen.Lit(""))
	}

	switch f.Type.Type {
	case field.TypeUUID:
		return jen.Qual("github.com/google/uuid", "Nil")
	case field.TypeTime:
		return jen.Qual("time", "Time").Block()
	case field.TypeString:
		return jen.Lit("")
	case field.TypeInt:
		return jen.Int().Call(jen.Lit(0))
	case field.TypeInt8:
		return jen.Int8().Call(jen.Lit(0))
	case field.TypeInt16:
		return jen.Int16().Call(jen.Lit(0))
	case field.TypeInt32:
		return jen.Int32().Call(jen.Lit(0))
	case field.TypeInt64:
		return jen.Int64().Call(jen.Lit(0))
	case field.TypeUint:
		return jen.Uint().Call(jen.Lit(0))
	case field.TypeUint8:
		return jen.Uint8().Call(jen.Lit(0))
	case field.TypeUint16:
		return jen.Uint16().Call(jen.Lit(0))
	case field.TypeUint32:
		return jen.Uint32().Call(jen.Lit(0))
	case field.TypeUint64:
		return jen.Uint64().Call(jen.Lit(0))
	case field.TypeFloat32:
		return jen.Float32().Call(jen.Lit(0))
	case field.TypeFloat64:
		return jen.Float64().Call(jen.Lit(0))
	case field.TypeBool:
		return jen.False()
	case field.TypeBytes:
		return jen.Index().Byte().Block()
	case field.TypeJSON:
		if f.HasGoType() {
			return jsonFieldZeroValue(h, f)
		}
		return jen.Qual("encoding/json", "RawMessage").Block()
	default:
		// For custom types (field.TypeOther) or unknown types, use empty struct literal.
		if f.HasGoType() && f.Type.PkgPath != "" {
			typeName := f.Type.Ident
			if idx := strings.LastIndex(typeName, "."); idx >= 0 {
				typeName = typeName[idx+1:]
			}
			return jen.Qual(f.Type.PkgPath, typeName).Block()
		}
		if f.Type.Ident != "" {
			return jen.Id(f.Type.Ident).Block()
		}
		return jen.Add(h.BaseType(f)).Block()
	}
}

// jsonFieldZeroValue returns the zero value for a JSON field with a custom Go type.
// Dispatches on reflect.Kind for robustness, avoiding brittle string comparisons
// that break across Go versions (e.g., "interface {}" vs "interface{}" vs "any").
func jsonFieldZeroValue(h gen.GeneratorHelper, f *gen.Field) jen.Code {
	if f.Type == nil || f.Type.RType == nil {
		return jen.Qual("encoding/json", "RawMessage").Block()
	}

	switch f.Type.RType.Kind {
	case reflect.Map:
		// map[string]any, map[string]interface{}, or other map types
		return jen.Add(h.BaseType(f)).Block()
	case reflect.Slice:
		// []any, []map[string]any, or other slice types
		return jen.Add(h.BaseType(f)).Block()
	default:
		// For struct types or other custom types, use empty literal
		if f.Type.PkgPath != "" {
			typeName := f.Type.Ident
			if idx := strings.LastIndex(typeName, "."); idx >= 0 {
				typeName = typeName[idx+1:]
			}
			return jen.Qual(f.Type.PkgPath, typeName).Block()
		}
		if f.Type.Ident != "" {
			return jen.Id(f.Type.Ident).Block()
		}
		return jen.Qual("encoding/json", "RawMessage").Block()
	}
}
