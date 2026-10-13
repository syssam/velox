package orm

import (
	"fmt"

	"github.com/syssam/velox"
)

// What the generated schemainit package fills a FieldMeta in with. A schema
// descriptor holds a field's default and validators as any; these turn them
// into what the runtime calls. They are here, and not written out per field,
// because a schema of some size has thousands of them: generated as closures,
// they were most of what recompiles on every edit to the schema (on a schema
// of 455 entities, 9,921 closures in a package that took 11.8 CPU-s and
// 1.0 GB to compile).
//
// They are not inlined: inlined, each call is the closure written out again,
// and the package was dearer than before (16-17 CPU-s, 1.2 GB).
//
// schemainit names them in the rows of a table per entity (FieldCode) and
// leaves the calling to StitchFields.

// DefaultValue is the default of a field whose schema gives a value: the
// value, every time.
//
//go:noinline
func DefaultValue(v any) func() any { return func() any { return v } }

// DefaultFunc is the default, or the update default, of a field whose schema
// gives a function: fn, which must be a func() T, called every time.
//
//go:noinline
func DefaultFunc[T any](fn any) func() any {
	f, ok := fn.(func() T)
	if !ok {
		panic(stale("default", fn, f))
	}
	return func() any { return f() }
}

// Validators is the check of a field of Go type T: the schema's validators,
// each a func(T) error, in order; the first error is the field's.
//
//go:noinline
func Validators[T any](checks []any) func(any) error {
	return ValidatorsOf(checks, func(v T) T { return v })
}

// ValidatorsOf is Validators for a field whose Go type T is not the type B
// its validators take (a named string type, validated as a string): basic
// converts.
//
//go:noinline
func ValidatorsOf[T, B any](checks []any, basic func(T) B) func(any) error {
	fns := make([]func(B) error, len(checks))
	for i, c := range checks {
		fn, ok := c.(func(B) error)
		if !ok {
			panic(stale("validator", c, fn))
		}
		fns[i] = fn
	}
	return func(v any) error {
		x, ok := v.(T)
		if !ok {
			return fmt.Errorf("velox: a %T where the field holds a %T", v, x)
		}
		b := basic(x)
		for _, check := range fns {
			if err := check(b); err != nil {
				return err
			}
		}
		return nil
	}
}

// DefaultOf is the default of a field whose schema gives a value of the
// field's Go type T: the value, every time.
//
//go:noinline
func DefaultOf[T any](v any) func() any {
	t, ok := v.(T)
	if !ok {
		panic(stale("default", v, t))
	}
	return DefaultValue(t)
}

// DefaultString is DefaultOf for a field whose Go type T is a named string
// type, an enum, and whose schema gives the default as a string.
//
//go:noinline
func DefaultString[T ~string](v any) func() any {
	s, ok := v.(string)
	if !ok {
		panic(stale("default", v, s))
	}
	return DefaultValue(T(s))
}

// ValidatorsString is Validators for a field whose Go type T is a named
// string type and whose validators take a string.
//
//go:noinline
func ValidatorsString[T ~string](checks []any) func(any) error {
	return ValidatorsOf(checks, func(v T) string { return string(v) })
}

// FieldCode is what schemainit says of one field: where its descriptor is
// among those its schema (or one of the schema's mixins) returns, its column,
// and for each thing the descriptor holds, what makes of it the function the
// runtime calls: DefaultOf, DefaultFunc and Validators for the field's Go
// type, or a function written out where none of them fits. A nil function is
// a thing the field does not have.
//
// The fields of an entity are a list of these, data, and not a statement
// per field: as statements they were nine tenths of a package that every
// edit to the schema recompiles.
type FieldCode struct {
	Index         int
	Column        string
	Default       func(v any) func() any
	UpdateDefault func(fn any) func() any
	Validate      func(checks []any) func(any) error
}

// StitchFields gives the fields of m their defaults and validators: for
// each row, from the descriptor of fields[row.Index] to the field of the
// row's column. fields is what the schema's, or a mixin's, Fields returns.
//
//go:noinline
func StitchFields(m *Meta, fields []velox.Field, codes []FieldCode) {
	for _, c := range codes {
		f := m.Field(c.Column)
		if c.Index >= len(fields) || f == nil {
			panic(fmt.Sprintf("velox: field %d of %d of the schema of %s, column %q, is not what the generated code expects: generate the code again", c.Index, len(fields), m.Name, c.Column))
		}
		d := fields[c.Index].Descriptor()
		if c.Default != nil {
			f.Default = c.Default(d.Default)
		}
		if c.UpdateDefault != nil {
			f.UpdateDefault = c.UpdateDefault(d.UpdateDefault)
		}
		if c.Validate != nil {
			f.Validate = c.Validate(d.Validators)
		}
	}
}

// stale is what stitching panics with when the schema holds something of
// another type than the generated code was written for: the schema was
// edited and the code not generated again.
func stale(what string, got, want any) string {
	return fmt.Sprintf("velox: a %s of the schema is a %T where the generated code expects a %T: generate the code again", what, got, want)
}
