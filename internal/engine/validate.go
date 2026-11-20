package engine

import (
	"fmt"
	"reflect"
	"strings"
	"unsafe"
)

// ValidateRow checks the row of in at p: it refuses a nil key of a
// RequiredEdge among cols, then checks every column in cols whose value is
// not a nil pointer. Non-generic.
func ValidateRow(in *EntityInfo, p unsafe.Pointer, cols []int) error {
	return validateRow(in, p, cols, nil)
}

// ValidateInsert checks the row of in at p, which an insert of the columns
// cols on dialect d, with the conflict clause cf (nil for none), writes, as
// ValidateRow does, except that a column the insert leaves to the
// database's default (leftToDB) skips its validators: the database stores
// its default, not the Go zero value (D10), so an enum with a declared
// default inserts with its zero. A column cf writes (conflictWritten) is
// sent as the row holds it (ADR 0093), and is validated.
func ValidateInsert(in *EntityInfo, d Dialect, p unsafe.Pointer, cols []int, cf *ConflictClause) error {
	return validateRow(in, p, cols, func(c int, v any) bool {
		return !conflictWritten(cf, c) && leftToDB(in, d, c, v)
	})
}

// validateRow is ValidateRow, which skips the validators of each column
// col whose value v skip, when not nil, reports.
func validateRow(in *EntityInfo, p unsafe.Pointer, cols []int, skip func(col int, v any) bool) error {
	for _, c := range cols {
		if err := checkCol(in, c); err != nil {
			return err
		}
	}
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
		v := in.kit.Value(p, c)
		if skip != nil && skip(c, v) {
			continue
		}
		if err := in.checkValue(c, v); err != nil {
			return err
		}
	}
	return nil
}

// ValidateSetters refuses an Immutable column among set, and a nil AssignSet of
// a RequiredEdge's key, then checks every AssignSet value that is not nil.
func ValidateSetters(in *EntityInfo, set []Assignment) error {
	for _, a := range set {
		if err := checkCol(in, a.Column); err != nil {
			return err
		}
	}
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
