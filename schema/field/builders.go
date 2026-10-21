package field

import (
	"database/sql/driver"
	"math"
	"reflect"
	"regexp"
	"time"

	"github.com/syssam/velox2/schema/internal/desc"
)

// newField makes the descriptor of a field whose standard Go type is rt,
// declared at pos. Each exported constructor passes desc.Caller(), called in
// its own body, so that pos is the line of the schema that calls it.
func newField(name, typ string, rt reflect.Type, pos string) *desc.Field {
	return &desc.Field{Name: name, Type: typ, RType: rt, Pos: pos}
}

// --- bool

type boolBuilder struct{ typed[*boolBuilder, bool] }

// Bool declares a bool field.
func Bool(name string) *boolBuilder {
	b := &boolBuilder{}
	b.typed = typed[*boolBuilder, bool]{common[*boolBuilder]{d: newField(name, "bool", reflect.TypeFor[bool](), desc.Caller()), self: b}}
	return b
}

// --- numbers

type number interface {
	int | int8 | int16 | int32 | int64 | uint | uint8 | uint16 | uint32 | uint64 | float32 | float64
}

type numberBuilder[T number] struct{ typed[*numberBuilder[T], T] }

func newNumber[T number](name, pos string) *numberBuilder[T] {
	b := &numberBuilder[T]{}
	rt := reflect.TypeFor[T]()
	b.typed = typed[*numberBuilder[T], T]{common[*numberBuilder[T]]{d: newField(name, rt.Kind().String(), rt, pos), self: b}}
	return b
}

// Int declares an int field.
func Int(name string) *numberBuilder[int] { return newNumber[int](name, desc.Caller()) }

// Int8 declares an int8 field.
func Int8(name string) *numberBuilder[int8] { return newNumber[int8](name, desc.Caller()) }

// Int16 declares an int16 field.
func Int16(name string) *numberBuilder[int16] { return newNumber[int16](name, desc.Caller()) }

// Int32 declares an int32 field.
func Int32(name string) *numberBuilder[int32] { return newNumber[int32](name, desc.Caller()) }

// Int64 declares an int64 field.
func Int64(name string) *numberBuilder[int64] { return newNumber[int64](name, desc.Caller()) }

// Uint declares a uint field.
func Uint(name string) *numberBuilder[uint] { return newNumber[uint](name, desc.Caller()) }

// Uint8 declares a uint8 field.
func Uint8(name string) *numberBuilder[uint8] { return newNumber[uint8](name, desc.Caller()) }

// Uint16 declares a uint16 field.
func Uint16(name string) *numberBuilder[uint16] { return newNumber[uint16](name, desc.Caller()) }

// Uint32 declares a uint32 field.
func Uint32(name string) *numberBuilder[uint32] { return newNumber[uint32](name, desc.Caller()) }

// Uint64 declares a uint64 field.
func Uint64(name string) *numberBuilder[uint64] { return newNumber[uint64](name, desc.Caller()) }

// Float declares a float64 field.
func Float(name string) *numberBuilder[float64] { return newNumber[float64](name, desc.Caller()) }

// Float32 declares a float32 field.
func Float32(name string) *numberBuilder[float32] { return newNumber[float32](name, desc.Caller()) }

// Float64 declares a float64 field.
func Float64(name string) *numberBuilder[float64] { return newNumber[float64](name, desc.Caller()) }

// Range requires lo <= value <= hi.
// An Optional field's zero value means unset, and is not validated.
func (b *numberBuilder[T]) Range(lo, hi T) *numberBuilder[T] {
	return b.check("Range", lo, hi)
}

// Min requires value >= v.
// An Optional field's zero value means unset, and is not validated.
func (b *numberBuilder[T]) Min(v T) *numberBuilder[T] { return b.check("Min", v) }

// Max requires value <= v.
// An Optional field's zero value means unset, and is not validated.
func (b *numberBuilder[T]) Max(v T) *numberBuilder[T] { return b.check("Max", v) }

// Positive requires value > 0.
// An Optional field's zero value means unset, and is not validated.
func (b *numberBuilder[T]) Positive() *numberBuilder[T] { return b.check("Positive") }

// Negative requires value < 0.
// An Optional field's zero value means unset, and is not validated.
func (b *numberBuilder[T]) Negative() *numberBuilder[T] { return b.check("Negative") }

// NonNegative requires value >= 0.
// An Optional field's zero value means unset, and is not validated.
func (b *numberBuilder[T]) NonNegative() *numberBuilder[T] { return b.check("NonNegative") }

func (b *numberBuilder[T]) check(kind string, args ...any) *numberBuilder[T] {
	b.d.Checks = append(b.d.Checks, desc.Check{Kind: kind, Args: args})
	return b
}

// --- string

type stringBuilder struct{ typed[*stringBuilder, string] }

// String declares a string field.
func String(name string) *stringBuilder { return newString(name, desc.Caller()) }

func newString(name, pos string) *stringBuilder {
	b := &stringBuilder{}
	b.typed = typed[*stringBuilder, string]{common[*stringBuilder]{d: newField(name, "string", reflect.TypeFor[string](), pos), self: b}}
	return b
}

// Text declares a string field with no length limit (Size is math.MaxInt32).
func Text(name string) *stringBuilder {
	b := newString(name, desc.Caller())
	b.d.Size = math.MaxInt32
	return b
}

// NotEmpty requires a non-empty string.
// An Optional field's zero value means unset, and is not validated.
func (b *stringBuilder) NotEmpty() *stringBuilder { return b.check("NotEmpty") }

// MinLen requires at least n bytes.
// An Optional field's zero value means unset, and is not validated.
func (b *stringBuilder) MinLen(n int) *stringBuilder { return b.check("MinLen", n) }

// MaxLen requires at most n bytes, and sets the column's size to n.
// An Optional field's zero value means unset, and is not validated.
func (b *stringBuilder) MaxLen(n int) *stringBuilder {
	b.d.Size = n
	return b.check("MaxLen", n)
}

// Match requires the string to match re.
// An Optional field's zero value means unset, and is not validated.
func (b *stringBuilder) Match(re *regexp.Regexp) *stringBuilder {
	if re == nil {
		fail(b.d, "Match is given a nil regexp")
		return b
	}
	return b.check("Match", re.String())
}

func (b *stringBuilder) check(kind string, args ...any) *stringBuilder {
	b.d.Checks = append(b.d.Checks, desc.Check{Kind: kind, Args: args})
	return b
}

// --- bytes

type bytesBuilder struct{ typed[*bytesBuilder, []byte] }

// Bytes declares a []byte field.
func Bytes(name string) *bytesBuilder {
	b := &bytesBuilder{}
	b.typed = typed[*bytesBuilder, []byte]{common[*bytesBuilder]{d: newField(name, "bytes", reflect.TypeFor[[]byte](), desc.Caller()), self: b}}
	return b
}

// NotEmpty requires a non-empty value.
// An Optional field's zero value means unset, and is not validated.
func (b *bytesBuilder) NotEmpty() *bytesBuilder { return b.check("NotEmpty") }

// MinLen requires at least n bytes.
// An Optional field's zero value means unset, and is not validated.
func (b *bytesBuilder) MinLen(n int) *bytesBuilder { return b.check("MinLen", n) }

// MaxLen requires at most n bytes.
// An Optional field's zero value means unset, and is not validated.
func (b *bytesBuilder) MaxLen(n int) *bytesBuilder { return b.check("MaxLen", n) }

func (b *bytesBuilder) check(kind string, args ...any) *bytesBuilder {
	b.d.Checks = append(b.d.Checks, desc.Check{Kind: kind, Args: args})
	return b
}

// --- time

type timeBuilder struct{ common[*timeBuilder] }

// Time declares a time.Time field.
func Time(name string) *timeBuilder {
	b := &timeBuilder{}
	b.common = common[*timeBuilder]{d: newField(name, "time", reflect.TypeFor[time.Time](), desc.Caller()), self: b}
	return b
}

// Default sets a func() time.Time, called when a new row does not set the field.
func (b *timeBuilder) Default(fn any) *timeBuilder { b.d.Default = fn; return b }

// UpdateDefault sets a func() time.Time, called on every update that does not
// set the field.
func (b *timeBuilder) UpdateDefault(fn any) *timeBuilder { b.d.UpdateDefault = fn; return b }

// GoType makes the field's Go type that of typ, which must be a struct.
func (b *timeBuilder) GoType(typ any) *timeBuilder {
	setGoType(b.d, typ, reflect.TypeFor[time.Time]())
	return b
}

// --- typed values: uuid, json, other

// withType makes d's Go type that of typ, or fails when typ is nil.
func withType(d *desc.Field, typ any) {
	if typ == nil {
		fail(d, "the type is nil")
		return
	}
	t := reflect.TypeOf(typ)
	d.GoType = desc.GoTypeOf(t)
	d.RType = t
}

type uuidBuilder struct{ common[*uuidBuilder] }

// UUID declares a field of the UUID type of typ, such as uuid.UUID.
func UUID(name string, typ driver.Valuer) *uuidBuilder {
	b := &uuidBuilder{}
	b.common = common[*uuidBuilder]{d: newField(name, "uuid", nil, desc.Caller()), self: b}
	withType(b.d, typ)
	return b
}

// Default sets a func() returning the UUID type, called when a new row does
// not set the field.
func (b *uuidBuilder) Default(fn any) *uuidBuilder { b.d.Default = fn; return b }

type jsonBuilder struct{ common[*jsonBuilder] }

// JSON declares a field stored as JSON, of the type of typ.
func JSON(name string, typ any) *jsonBuilder { return newJSON(name, typ, desc.Caller()) }

func newJSON(name string, typ any, pos string) *jsonBuilder {
	b := &jsonBuilder{}
	b.common = common[*jsonBuilder]{d: newField(name, "json", nil, pos), self: b}
	withType(b.d, typ)
	return b
}

// Strings declares a []string field stored as JSON.
func Strings(name string) *jsonBuilder { return newJSON(name, []string{}, desc.Caller()) }

// Ints declares an []int field stored as JSON.
func Ints(name string) *jsonBuilder { return newJSON(name, []int{}, desc.Caller()) }

// Floats declares a []float64 field stored as JSON.
func Floats(name string) *jsonBuilder { return newJSON(name, []float64{}, desc.Caller()) }

// Default sets the value a new row gets, of the JSON type.
func (b *jsonBuilder) Default(v any) *jsonBuilder { b.d.Default = v; return b }

type otherBuilder struct{ common[*otherBuilder] }

// Other declares a field of a type the DSL does not know, of the type of typ.
// Use SchemaType to give its column type.
func Other(name string, typ driver.Valuer) *otherBuilder {
	b := &otherBuilder{}
	b.common = common[*otherBuilder]{d: newField(name, "other", nil, desc.Caller()), self: b}
	withType(b.d, typ)
	return b
}

// Default sets the value a new row gets, of the field's type.
func (b *otherBuilder) Default(v any) *otherBuilder { b.d.Default = v; return b }

// --- enum

// EnumValues is implemented by a string type that lists its enum values.
type EnumValues interface{ Values() []string }

type enumBuilder struct{ common[*enumBuilder] }

// Enum declares an enum field, stored as a string.
func Enum(name string) *enumBuilder {
	b := &enumBuilder{}
	b.common = common[*enumBuilder]{d: newField(name, "enum", reflect.TypeFor[string](), desc.Caller()), self: b}
	return b
}

// Values adds enum values whose Go name suffix is the value itself.
func (b *enumBuilder) Values(vs ...string) *enumBuilder {
	for _, v := range vs {
		b.d.Enums = append(b.d.Enums, desc.Enum{Name: v, Value: v})
	}
	return b
}

// NamedValues adds enum values as pairs of the Go name suffix and the stored
// value: NamedValues("InProgress", "in-progress", "Done", "done").
func (b *enumBuilder) NamedValues(pairs ...string) *enumBuilder {
	if len(pairs)%2 != 0 {
		fail(b.d, "NamedValues needs name and value pairs, got %d arguments", len(pairs))
		return b
	}
	for i := 0; i < len(pairs); i += 2 {
		b.d.Enums = append(b.d.Enums, desc.Enum{Name: pairs[i], Value: pairs[i+1]})
	}
	return b
}

// Default sets the value a new row gets, one of the enum values.
func (b *enumBuilder) Default(v string) *enumBuilder { b.d.Default = v; return b }

// GoType makes the field's Go type that of ev, and adds the values it lists.
func (b *enumBuilder) GoType(ev EnumValues) *enumBuilder {
	if ev == nil {
		fail(b.d, "GoType is given a nil type")
		return b
	}
	if setGoType(b.d, ev, reflect.TypeFor[string]()) {
		b.Values(ev.Values()...)
	}
	return b
}
