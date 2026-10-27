package velox

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

// Validate runs the validators of the columns cs of x on the values x holds
// now, before defaults are filled and before any hook, and before any
// statement: velox-graphql's CreateX checks the fields its input sets this way,
// so that an invalid one costs no query. For generated code. The checks and the
// error are Insert's (see ValidationError); Insert validates again after its
// defaults and hooks.
func Validate[E any, PE Entity[E]](x PE, cs ...Column[E]) error {
	m := x.VeloxMeta()
	if m.err != nil {
		return m.err
	}
	cols := make([]int, len(cs))
	for i, c := range cs {
		cols[i] = int(c.column(nil))
	}
	return validateRow(m.info, unsafe.Pointer(x), cols)
}

// ValidateSet runs the validators of the Set values of set, and refuses an
// Immutable column among them, on the values as given, before any hook or
// UpdateDefault and before any statement: velox-graphql's UpdateX checks the
// fields its input sets this way. For generated code. The checks and the error
// are Update's (see ValidationError); Update validates again after its hooks.
func ValidateSet[E any, PE Entity[E]](set ...Setter[E]) error {
	m := PE(nil).VeloxMeta()
	if m.err != nil {
		return m.err
	}
	as := make([]Assignment, len(set))
	for i, s := range set {
		as[i] = s.a
	}
	return validateSet(m.info, as)
}

// validateRow checks the row of in at p: it refuses a nil key of a
// RequiredEdge among cols, then checks every column in cols whose value is
// not a nil pointer. Non-generic.
func validateRow(in *EntityInfo, p unsafe.Pointer, cols []int) error {
	if in.required {
		for _, c := range cols {
			if in.Columns[c].RequiredEdge != "" && isNil(in.kit.value(p, c)) {
				return missingEdge(in, c)
			}
		}
	}
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

// validateSet refuses an Immutable column among set, and a nil AssignSet of
// a RequiredEdge's key, then checks every AssignSet value that is not nil.
func validateSet(in *EntityInfo, set []Assignment) error {
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
