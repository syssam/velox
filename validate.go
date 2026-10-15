package velox

import (
	"fmt"
	"reflect"
	"unsafe"
)

// ValidationError is a value a column's validators refused; nothing was sent.
// Find it with errors.As; errors.Is reaches the validator's own error.
//
// What validators check:
//   - For an insert, every column the statement sends.
//   - For an update or Save, the value of every Set assignment; not Add or Sub.
//   - A nil pointer, or a nil value, is never checked, because it is the
//     absence of a value; a non-nil pointer is checked as its element.
type ValidationError struct {
	Entity string // the Go type, e.g. "gen.User"
	Field  string // the Go field name
	Err    error  // the validator's error
}

func (e *ValidationError) Error() string {
	return "velox: " + e.Entity + ": " + e.Field + ": " + e.Err.Error()
}

func (e *ValidationError) Unwrap() error { return e.Err }

// validateRow checks the row of in at p: every column in cols whose value is
// not a nil pointer. Non-generic.
func validateRow(in *EntityInfo, p unsafe.Pointer, cols []int) error {
	if in.check == nil {
		return nil
	}
	for _, c := range cols {
		if err := in.checkValue(c, in.kit.value(p, c)); err != nil {
			return err
		}
	}
	return nil
}

// validateSet refuses an Immutable column among set, then checks every
// AssignSet value that is not nil.
func validateSet(in *EntityInfo, set []Assignment) error {
	if in.immutable {
		for _, a := range set {
			if c := &in.Columns[a.Column]; c.Immutable {
				return fmt.Errorf("velox: %s: %s: %w", in.Name, c.Name, ErrImmutable)
			}
		}
	}
	if in.check == nil {
		return nil
	}
	for _, a := range set {
		if a.Op != AssignSet {
			continue
		}
		if err := in.checkValue(a.Column, a.Value); err != nil {
			return err
		}
	}
	return nil
}

// checkValue runs column col's validators on v, unless v is nil or a nil
// pointer. A pointer is checked as its element.
func (in *EntityInfo) checkValue(col int, v any) error {
	if v == nil {
		return nil
	}
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		v = rv.Elem().Interface()
	}
	if err := in.check(col, v); err != nil {
		return &ValidationError{Entity: in.Name, Field: in.Columns[col].Field, Err: err}
	}
	return nil
}
