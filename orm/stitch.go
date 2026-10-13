package orm

import "fmt"

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

// stale is what stitching panics with when the schema holds something of
// another type than the generated code was written for: the schema was
// edited and the code not generated again.
func stale(what string, got, want any) string {
	return fmt.Sprintf("velox: a %s of the schema is a %T where the generated code expects a %T: generate the code again", what, got, want)
}
