package engine

import (
	"fmt"
	"reflect"
	"strings"
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
//
// It is also a nil key of a column with a RequiredEdge (see ColumnDesc), in an
// insert or a Set assignment: its Err says missing required edge
// "<Type>.<edge>", as ent's does.
type ValidationError struct {
	Entity string // the Go type, e.g. "gen.User"
	Field  string // the Go field name
	Err    error  // the validator's error
}

// Error is "velox: <Entity>: <Field>: <Err>".
func (e *ValidationError) Error() string {
	return "velox: " + e.Entity + ": " + e.Field + ": " + e.Err.Error()
}

// Unwrap is Err.
func (e *ValidationError) Unwrap() error { return e.Err }

// ValidateRow checks the row of in at p: it refuses a nil key of a
// RequiredEdge among cols, then checks every column in cols whose value is
// not a nil pointer. Non-generic.
func ValidateRow(in *EntityInfo, p unsafe.Pointer, cols []int) error {
	if in.required {
		for _, c := range cols {
			if in.Columns[c].RequiredEdge != "" && isNil(in.kit.Value(p, c)) {
				return missingEdge(in, c)
			}
		}
	}
	if in.check == nil {
		return nil
	}
	for _, c := range cols {
		if err := in.checkValue(c, in.kit.Value(p, c)); err != nil {
			return err
		}
	}
	return nil
}

// ValidateSetters refuses an Immutable column among set, and a nil AssignSet of
// a RequiredEdge's key, then checks every AssignSet value that is not nil.
func ValidateSetters(in *EntityInfo, set []Assignment) error {
	if in.required {
		for _, a := range set {
			if a.Op == AssignSet && in.Columns[a.Column].RequiredEdge != "" && isNil(a.Value) {
				return missingEdge(in, a.Column)
			}
		}
	}
	if in.immutable {
		for _, a := range set {
			if c := &in.Columns[a.Column]; c.Immutable {
				return fmt.Errorf("velox: %s: %s: %w", in.Name, c.Field, ErrImmutable)
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

// missingEdge is the error of a nil key of column col's RequiredEdge, in
// ent's words: missing required edge "Ticket.parent".
func missingEdge(in *EntityInfo, col int) error {
	c := &in.Columns[col]
	typ := in.Name[strings.LastIndexByte(in.Name, '.')+1:]
	return &ValidationError{Entity: in.Name, Field: c.Field, Err: fmt.Errorf("missing required edge %q", typ+"."+c.RequiredEdge)}
}

// isNil reports whether v is nil or a nil pointer.
func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
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
