package gqlcore

import "reflect"

// Walk calls f on err and on each error it wraps, depth-first through both
// Unwrap() error and Unwrap() []error, in errors.As's order, until f returns
// true, and reports whether it did. A nil pointer (a typed nil, which a
// driver may put in an error's tree) is skipped and not unwrapped, and the
// walk goes on past it: its methods (Error, Unwrap, Is) could panic on it.
// It is velox's own walk, which velox does not export.
func Walk(err error, f func(error) bool) bool {
	if err == nil {
		return false
	}
	if NilPointer(err) {
		return false
	}
	if f(err) {
		return true
	}
	switch u := err.(type) { //nolint:errorlint // Walk is the tree walk itself, which errors.Is and As do not offer with a nil-pointer skip
	case interface{ Unwrap() error }:
		return Walk(u.Unwrap(), f)
	case interface{ Unwrap() []error }:
		for _, e := range u.Unwrap() {
			if Walk(e, f) {
				return true
			}
		}
	}
	return false
}

// NilPointer reports whether err is a nil pointer, a typed nil: no error,
// whose methods could panic on it.
func NilPointer(err error) bool {
	v := reflect.ValueOf(err)
	return v.Kind() == reflect.Pointer && v.IsNil()
}

// Is is errors.Is over Walk: a nil pointer in err's tree is passed over. A
// nil target matches a nil err only, as with errors.Is.
func Is(err, target error) bool {
	if target == nil {
		return err == nil
	}
	canEq := reflect.TypeOf(target).Comparable()
	return Walk(err, func(e error) bool {
		if canEq && e == target { //nolint:errorlint // errors.Is over Walk: f judges one node
			return true
		}
		x, ok := e.(interface{ Is(error) bool })
		return ok && x.Is(target)
	})
}

// As is errors.As over Walk: it returns the first error in err's tree of
// type T, or that an As method gives as T, which is not a nil pointer.
func As[T error](err error) (T, bool) {
	var found T
	ok := Walk(err, func(e error) bool {
		if t, ok := e.(T); ok { //nolint:errorlint // errors.As over Walk: f judges one node
			found = t
			return true
		}
		x, ok := e.(interface{ As(any) bool })
		if !ok {
			return false
		}
		var t T
		if !x.As(&t) {
			return false
		}
		if v := reflect.ValueOf(t); !v.IsValid() || v.Kind() == reflect.Pointer && v.IsNil() {
			return false
		}
		found = t
		return true
	})
	return found, ok
}
