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
// declared at pc. Each exported constructor passes desc.Caller(), called in
// its own body, so that pc is the call in the schema.
func newField(name, typ string, rt reflect.Type, pc uintptr) *desc.Field {
	return &desc.Field{Name: name, Type: typ, RType: rt, PC: pc}
}

// --- bool

// BoolBuilder builds a bool field.
type BoolBuilder struct {
	common[*BoolBuilder]
	typed[*BoolBuilder, bool]
}

// Bool declares a bool field.
func Bool(name string) *BoolBuilder {
	b := &BoolBuilder{}
	b.common = common[*BoolBuilder]{d: newField(name, "bool", reflect.TypeFor[bool](), desc.Caller()), self: b}
	b.typed = newTyped[*BoolBuilder, bool](&b.common)
	return b
}

// --- numbers

// Numeric is the Go types of a number field: the builtin integer and float
// types, and the types defined on them.
type Numeric interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~float32 | ~float64
}

// NumberBuilder builds a number field whose values are of the Go type T.
type NumberBuilder[T Numeric] struct {
	common[*NumberBuilder[T]]
	typed[*NumberBuilder[T], T]
}

// Number declares a number field of the Go type T. The field's type is T's
// kind, so that Number[int64] is Int64. A T that is a defined type, such as
// type Cents int64, is the field's GoType, as GoType(Cents(0)) makes it, and
// Default, Validate, Min and the other options take a T.
func Number[T Numeric](name string) *NumberBuilder[T] { return newNumber[T](name, desc.Caller()) }

func newNumber[T Numeric](name string, pc uintptr) *NumberBuilder[T] {
	b := &NumberBuilder[T]{}
	rt := reflect.TypeFor[T]()
	d := newField(name, rt.Kind().String(), rt, pc)
	if rt.PkgPath() != "" {
		d.GoType = desc.GoTypeOf(rt)
	}
	b.common = common[*NumberBuilder[T]]{d: d, self: b}
	b.typed = newTyped[*NumberBuilder[T], T](&b.common)
	return b
}

// Int declares an int field.
func Int(name string) *NumberBuilder[int] { return newNumber[int](name, desc.Caller()) }

// Int8 declares an int8 field.
func Int8(name string) *NumberBuilder[int8] { return newNumber[int8](name, desc.Caller()) }

// Int16 declares an int16 field.
func Int16(name string) *NumberBuilder[int16] { return newNumber[int16](name, desc.Caller()) }

// Int32 declares an int32 field.
func Int32(name string) *NumberBuilder[int32] { return newNumber[int32](name, desc.Caller()) }

// Int64 declares an int64 field.
func Int64(name string) *NumberBuilder[int64] { return newNumber[int64](name, desc.Caller()) }

// Uint declares a uint field.
func Uint(name string) *NumberBuilder[uint] { return newNumber[uint](name, desc.Caller()) }

// Uint8 declares a uint8 field.
func Uint8(name string) *NumberBuilder[uint8] { return newNumber[uint8](name, desc.Caller()) }

// Uint16 declares a uint16 field.
func Uint16(name string) *NumberBuilder[uint16] { return newNumber[uint16](name, desc.Caller()) }

// Uint32 declares a uint32 field.
func Uint32(name string) *NumberBuilder[uint32] { return newNumber[uint32](name, desc.Caller()) }

// Uint64 declares a uint64 field. SQLite's integer holds at most 2⁶³−1.
func Uint64(name string) *NumberBuilder[uint64] { return newNumber[uint64](name, desc.Caller()) }

// Float declares a float64 field.
func Float(name string) *NumberBuilder[float64] { return newNumber[float64](name, desc.Caller()) }

// Float32 declares a float32 field.
func Float32(name string) *NumberBuilder[float32] { return newNumber[float32](name, desc.Caller()) }

// Validate adds a function that checks the field's value.
// An Optional field's zero value means unset, and is not validated.
func (b *NumberBuilder[T]) Validate(fn func(T) error) *NumberBuilder[T] {
	if fn == nil {
		fail(b.d, "Validate is given a nil function")
		return b
	}
	b.d.Validators = append(b.d.Validators, ofKind(fn))
	return b
}

// ofKind returns fn as a function of T's kind, which the generated code calls
// with the field's value converted, as it calls the validator of a field
// spelled Int64("p").GoType(Cents(0)). fn still receives a T.
func ofKind[T Numeric](fn func(T) error) any {
	rt := reflect.TypeFor[T]()
	if rt.PkgPath() == "" {
		return fn
	}
	switch rt.Kind() {
	case reflect.Int:
		return func(x int) error { return fn(T(x)) }
	case reflect.Int8:
		return func(x int8) error { return fn(T(x)) }
	case reflect.Int16:
		return func(x int16) error { return fn(T(x)) }
	case reflect.Int32:
		return func(x int32) error { return fn(T(x)) }
	case reflect.Int64:
		return func(x int64) error { return fn(T(x)) }
	case reflect.Uint:
		return func(x uint) error { return fn(T(x)) }
	case reflect.Uint8:
		return func(x uint8) error { return fn(T(x)) }
	case reflect.Uint16:
		return func(x uint16) error { return fn(T(x)) }
	case reflect.Uint32:
		return func(x uint32) error { return fn(T(x)) }
	case reflect.Uint64:
		return func(x uint64) error { return fn(T(x)) }
	case reflect.Float32:
		return func(x float32) error { return fn(T(x)) }
	default: // reflect.Float64, the last kind Numeric allows
		return func(x float64) error { return fn(T(x)) }
	}
}

// Default sets the value a new row gets when the field is not set. It never
// changes the Go type. A field that is not a pointer is unset when it holds its
// zero value, so velox-gen refuses a Default on a number that is not Nillable,
// which could then never store 0: make the field Nillable or drop the Default.
func (b *NumberBuilder[T]) Default(v T) *NumberBuilder[T] { b.d.Default = base(v); return b }

// base returns v as a value of T's kind, such as an int for a slog.Level, so
// that Default, Min, Max and Range record what Int().GoType(T(0)) records,
// and a T with its own JSON or text encoding stores its number.
func base[T Numeric](v T) any {
	switch reflect.TypeFor[T]().Kind() {
	case reflect.Int:
		return int(v)
	case reflect.Int8:
		return int8(v)
	case reflect.Int16:
		return int16(v)
	case reflect.Int32:
		return int32(v)
	case reflect.Int64:
		return int64(v)
	case reflect.Uint:
		return uint(v)
	case reflect.Uint8:
		return uint8(v)
	case reflect.Uint16:
		return uint16(v)
	case reflect.Uint32:
		return uint32(v)
	case reflect.Uint64:
		return uint64(v)
	case reflect.Float32:
		return float32(v)
	default: // reflect.Float64, the last kind Numeric allows
		return float64(v)
	}
}

// Range requires lo <= value <= hi.
// An Optional field's zero value means unset, and is not validated.
func (b *NumberBuilder[T]) Range(lo, hi T) *NumberBuilder[T] {
	return b.check("Range", base(lo), base(hi))
}

// Min requires value >= v.
// An Optional field's zero value means unset, and is not validated.
func (b *NumberBuilder[T]) Min(v T) *NumberBuilder[T] { return b.check("Min", base(v)) }

// Max requires value <= v.
// An Optional field's zero value means unset, and is not validated.
func (b *NumberBuilder[T]) Max(v T) *NumberBuilder[T] { return b.check("Max", base(v)) }

// Positive requires value > 0.
// An Optional field's zero value means unset, and is not validated.
func (b *NumberBuilder[T]) Positive() *NumberBuilder[T] { return b.check("Positive") }

// Negative requires value < 0.
// An Optional field's zero value means unset, and is not validated.
func (b *NumberBuilder[T]) Negative() *NumberBuilder[T] { return b.check("Negative") }

// NonNegative requires value >= 0.
// An Optional field's zero value means unset, and is not validated.
func (b *NumberBuilder[T]) NonNegative() *NumberBuilder[T] { return b.check("NonNegative") }

func (b *NumberBuilder[T]) check(kind string, args ...any) *NumberBuilder[T] {
	b.d.Checks = append(b.d.Checks, desc.Check{Kind: kind, Args: args})
	return b
}

// --- string

// StringBuilder builds a string field.
type StringBuilder struct {
	common[*StringBuilder]
	typed[*StringBuilder, string]
}

// String declares a string field.
func String(name string) *StringBuilder { return newString(name, desc.Caller()) }

func newString(name string, pc uintptr) *StringBuilder {
	b := &StringBuilder{}
	b.common = common[*StringBuilder]{d: newField(name, "string", reflect.TypeFor[string](), pc), self: b}
	b.typed = newTyped[*StringBuilder, string](&b.common)
	return b
}

// Text declares a string field with no length limit (Size is math.MaxInt32).
func Text(name string) *StringBuilder {
	b := newString(name, desc.Caller())
	b.d.Size = math.MaxInt32
	return b
}

// NotEmpty requires a non-empty string.
// An Optional field's zero value means unset, and is not validated.
func (b *StringBuilder) NotEmpty() *StringBuilder { return b.check("NotEmpty") }

// MinLen requires at least n bytes.
// An Optional field's zero value means unset, and is not validated.
func (b *StringBuilder) MinLen(n int) *StringBuilder { return b.check("MinLen", n) }

// MaxLen requires at most n bytes, and sets the column's size to n.
// An Optional field's zero value means unset, and is not validated.
func (b *StringBuilder) MaxLen(n int) *StringBuilder {
	b.d.Size = n
	return b.check("MaxLen", n)
}

// Match requires the string to match re.
// An Optional field's zero value means unset, and is not validated.
func (b *StringBuilder) Match(re *regexp.Regexp) *StringBuilder {
	if re == nil {
		fail(b.d, "Match is given a nil regexp")
		return b
	}
	return b.check("Match", re.String())
}

func (b *StringBuilder) check(kind string, args ...any) *StringBuilder {
	b.d.Checks = append(b.d.Checks, desc.Check{Kind: kind, Args: args})
	return b
}

// --- bytes

// BytesBuilder builds a []byte field.
type BytesBuilder struct {
	common[*BytesBuilder]
	typed[*BytesBuilder, []byte]
}

// Bytes declares a []byte field.
func Bytes(name string) *BytesBuilder {
	b := &BytesBuilder{}
	b.common = common[*BytesBuilder]{d: newField(name, "bytes", reflect.TypeFor[[]byte](), desc.Caller()), self: b}
	b.typed = newTyped[*BytesBuilder, []byte](&b.common)
	return b
}

// NotEmpty requires a non-empty value.
// An Optional field's zero value means unset, and is not validated.
func (b *BytesBuilder) NotEmpty() *BytesBuilder { return b.check("NotEmpty") }

// MinLen requires at least n bytes.
// An Optional field's zero value means unset, and is not validated.
func (b *BytesBuilder) MinLen(n int) *BytesBuilder { return b.check("MinLen", n) }

// MaxLen requires at most n bytes, and sets the column's size to n.
// An Optional field's zero value means unset, and is not validated.
func (b *BytesBuilder) MaxLen(n int) *BytesBuilder {
	b.d.Size = n
	return b.check("MaxLen", n)
}

func (b *BytesBuilder) check(kind string, args ...any) *BytesBuilder {
	b.d.Checks = append(b.d.Checks, desc.Check{Kind: kind, Args: args})
	return b
}

// --- time

// TimeBuilder builds a time.Time field.
type TimeBuilder struct{ common[*TimeBuilder] }

// Time declares a time.Time field.
func Time(name string) *TimeBuilder {
	b := &TimeBuilder{}
	b.common = common[*TimeBuilder]{d: newField(name, "time", reflect.TypeFor[time.Time](), desc.Caller()), self: b}
	return b
}

// Default sets a function, such as time.Now, called when a new row does not
// set the field. With a GoType, the GoType must convert from time.Time, and
// the generated code converts what fn returns.
func (b *TimeBuilder) Default(fn func() time.Time) *TimeBuilder { b.d.Default = fn; return b }

// UpdateDefault sets a function, such as time.Now, called on every update that
// does not set the field. With a GoType, the GoType must convert from
// time.Time, and the generated code converts what fn returns.
func (b *TimeBuilder) UpdateDefault(fn func() time.Time) *TimeBuilder {
	b.d.UpdateDefault = fn
	return b
}

// GoType makes the field's Go type that of typ, which must be a struct. A
// Default or UpdateDefault needs one that converts from time.Time, such as
// type Stamp time.Time.
func (b *TimeBuilder) GoType(typ any) *TimeBuilder {
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

// UUIDBuilder builds a UUID field declared with UUID.
type UUIDBuilder struct{ common[*UUIDBuilder] }

// UUID declares a field of the UUID type of typ, such as uuid.UUID. UUIDOf is
// the typed form.
func UUID(name string, typ driver.Valuer) *UUIDBuilder {
	b := &UUIDBuilder{}
	b.common = common[*UUIDBuilder]{d: newUUID(name, typ, desc.Caller()), self: b}
	return b
}

func newUUID(name string, typ driver.Valuer, pc uintptr) *desc.Field {
	d := newField(name, "uuid", nil, pc)
	withType(d, typ)
	return d
}

// Default sets a func() returning the UUID type, such as uuid.New, called when
// a new row does not set the field.
func (b *UUIDBuilder) Default(fn any) *UUIDBuilder { b.d.Default = fn; return b }

// UUIDOfBuilder builds a UUID field of the Go type T, declared with UUIDOf.
type UUIDOfBuilder[T driver.Valuer] struct{ common[*UUIDOfBuilder[T]] }

// UUIDOf declares a field of the UUID type T, such as uuid.UUID. It is
// UUID(name, T's zero value), with a typed Default.
func UUIDOf[T driver.Valuer](name string) *UUIDOfBuilder[T] {
	b := &UUIDOfBuilder[T]{}
	var zero T
	b.common = common[*UUIDOfBuilder[T]]{d: newUUID(name, zero, desc.Caller()), self: b}
	return b
}

// Default sets a function, such as uuid.New, called when a new row does not
// set the field.
func (b *UUIDOfBuilder[T]) Default(fn func() T) *UUIDOfBuilder[T] { b.d.Default = fn; return b }

// JSONBuilder builds a field stored as JSON.
type JSONBuilder struct{ common[*JSONBuilder] }

// JSON declares a field stored as JSON, of the type of typ.
func JSON(name string, typ any) *JSONBuilder { return newJSON(name, typ, desc.Caller()) }

func newJSON(name string, typ any, pc uintptr) *JSONBuilder {
	b := &JSONBuilder{}
	b.common = common[*JSONBuilder]{d: newField(name, "json", nil, pc), self: b}
	withType(b.d, typ)
	return b
}

// Strings declares a []string field stored as JSON.
func Strings(name string) *JSONBuilder { return newJSON(name, []string{}, desc.Caller()) }

// Ints declares an []int field stored as JSON.
func Ints(name string) *JSONBuilder { return newJSON(name, []int{}, desc.Caller()) }

// Floats declares a []float64 field stored as JSON.
func Floats(name string) *JSONBuilder { return newJSON(name, []float64{}, desc.Caller()) }

// Default sets the value a new row gets, of the JSON type.
func (b *JSONBuilder) Default(v any) *JSONBuilder { b.d.Default = v; return b }

// OtherBuilder builds a field of a type the DSL does not know.
type OtherBuilder struct{ common[*OtherBuilder] }

// Other declares a field of a type the DSL does not know, of the type of typ.
// Use SchemaType to give its column type.
func Other(name string, typ driver.Valuer) *OtherBuilder {
	b := &OtherBuilder{}
	b.common = common[*OtherBuilder]{d: newField(name, "other", nil, desc.Caller()), self: b}
	withType(b.d, typ)
	return b
}

// Default sets the value a new row gets, of the field's type.
func (b *OtherBuilder) Default(v any) *OtherBuilder { b.d.Default = v; return b }

// --- enum

// EnumValues is implemented by a string type that lists its enum values.
type EnumValues interface{ Values() []string }

// EnumBuilder builds an enum field declared with Enum.
type EnumBuilder struct{ common[*EnumBuilder] }

// Enum declares an enum field, stored as a string. EnumOf is the typed form.
// Enums are native on PostgreSQL and MySQL; both sort an enum by the order its
// values are declared in, not alphabetically.
func Enum(name string) *EnumBuilder { return newEnum(name, desc.Caller()) }

func newEnum(name string, pc uintptr) *EnumBuilder {
	b := &EnumBuilder{}
	b.common = common[*EnumBuilder]{d: newField(name, "enum", reflect.TypeFor[string](), pc), self: b}
	return b
}

// Values adds enum values whose Go name suffix is the value itself.
func (b *EnumBuilder) Values(vs ...string) *EnumBuilder {
	for _, v := range vs {
		b.d.Enums = append(b.d.Enums, desc.Enum{Name: v, Value: v})
	}
	return b
}

// NamedValues adds enum values as pairs of the Go name suffix and the stored
// value: NamedValues("InProgress", "in-progress", "Done", "done").
func (b *EnumBuilder) NamedValues(pairs ...string) *EnumBuilder {
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
func (b *EnumBuilder) Default(v string) *EnumBuilder { b.d.Default = v; return b }

// GoType makes the field's Go type that of ev, and adds the values it lists.
func (b *EnumBuilder) GoType(ev EnumValues) *EnumBuilder {
	if ev == nil {
		fail(b.d, "GoType is given a nil type")
		return b
	}
	if setGoType(b.d, ev, reflect.TypeFor[string]()) {
		b.Values(ev.Values()...)
	}
	return b
}

// EnumType is the Go type of an enum field declared with EnumOf: a string
// type whose Values method lists the enum's values, such as
//
//	type Status string
//
//	func (Status) Values() []string { return []string{"active", "closed"} }
type EnumType interface {
	~string
	Values() []string
}

// EnumOfBuilder builds an enum field of the Go type T, declared with EnumOf.
type EnumOfBuilder[T EnumType] struct{ common[*EnumOfBuilder[T]] }

// EnumOf declares an enum field of the Go type T, whose Values method lists
// the values. It is Enum(name).GoType(T("")), with a typed Default. Enums are
// native on PostgreSQL and MySQL; both sort an enum by the order its values are
// declared in, not alphabetically.
func EnumOf[T EnumType](name string) *EnumOfBuilder[T] {
	e := newEnum(name, desc.Caller()).GoType(T(""))
	b := &EnumOfBuilder[T]{}
	b.common = common[*EnumOfBuilder[T]]{d: e.d, self: b}
	return b
}

// Default sets the value a new row gets, one of the enum values.
func (b *EnumOfBuilder[T]) Default(v T) *EnumOfBuilder[T] { b.d.Default = string(v); return b }
