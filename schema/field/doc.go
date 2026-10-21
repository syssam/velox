// Package field declares the fields of a schema. Each constructor takes the
// field's name and returns a builder whose methods set its options; every
// builder has a Descriptor method, so it is a schema.Field.
//
// The constructors, and the builder each returns:
//
//	Bool                                  *BoolBuilder
//	Int, Int8, Int16, Int32, Int64        *NumberBuilder[int], …[int64]
//	Uint, Uint8, Uint16, Uint32, Uint64   *NumberBuilder[uint], …[uint64]
//	Float, Float32                        *NumberBuilder[float64], *NumberBuilder[float32]
//	Number[T]                             *NumberBuilder[T]
//	String, Text                          *StringBuilder
//	Bytes                                 *BytesBuilder
//	Time                                  *TimeBuilder
//	UUID                                  *UUIDBuilder
//	UUIDOf[T]                             *UUIDOfBuilder[T]
//	JSON, Strings, Ints, Floats           *JSONBuilder
//	Enum                                  *EnumBuilder
//	EnumOf[T]                             *EnumOfBuilder[T]
//	Other                                 *OtherBuilder
//
// Number, UUIDOf and EnumOf are the typed forms: their options take the
// field's Go type T. Number[Cents] is Int64 with GoType(Cents(0)), UUIDOf[T]
// is UUID(name, T's zero value), and EnumOf[T] is Enum with GoType(T("")).
//
// A builder never panics. velox-gen reports every misuse, such as a GoType of
// the wrong kind or a DefaultFunc of the wrong signature, with its file:line.
//
// The builders share their options through embedded generic types, so godoc
// lists some options with type parameters: in "Optional() B" B is the builder,
// and in "Default(v V) B" V is the field's value type. Each option returns the
// builder it is called on.
package field
