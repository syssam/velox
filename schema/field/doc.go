// Package field declares the fields of a schema. Each constructor takes the
// field's name and returns a builder whose methods set its options; every
// builder has a Descriptor method, so it is a schema.Field.
//
// The constructors are Bool; Int, Int8, Int16, Int32, Int64, Uint, Uint8,
// Uint16, Uint32, Uint64, Float32 and Float64; String and Text; Bytes; Time;
// UUID; JSON, Strings, Ints and Floats; Enum; and Other. See section 1.1 of
// the schema design for what each one means.
//
// A builder never panics. A misuse, such as a GoType of the wrong kind or a
// DefaultFunc of the wrong signature, is recorded in the descriptor's Err, and
// the loader reports it.
package field
