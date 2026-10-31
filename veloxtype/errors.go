package veloxtype

import (
	"errors"
	"fmt"
)

// ErrDenied is returned by a statement when a policy refuses it. Every
// *PolicyError matches it. Like velox's other sentinels it is returned wrapped;
// test for it with errors.Is.
var ErrDenied = errors.New("denied by policy")

// SchemaError reports a mistake in an entity's generated metadata (its
// velox.Desc, or the Policy and Hooks methods it binds).
type SchemaError struct {
	Entity string // the Go type, e.g. "billing.Invoice"
	Field  string // the Go field name; empty when the error is about the entity
	Reason string // what is wrong
}

// Error is "velox: <Entity>: <Reason>", or "velox: <Entity>.<Field>: <Reason>"
// for an error about a field.
func (e *SchemaError) Error() string {
	if e.Field == "" {
		return "velox: " + e.Entity + ": " + e.Reason
	}
	return "velox: " + e.Entity + "." + e.Field + ": " + e.Reason
}

// ConstraintKind is the kind of constraint a statement violated.
type ConstraintKind uint8

// The kinds of constraint.
const (
	ConstraintUnique     ConstraintKind = iota + 1 // a unique index or primary key
	ConstraintForeignKey                           // a foreign key
	ConstraintNotNull                              // a NOT NULL column
	ConstraintCheck                                // a CHECK constraint
)

// String is "unique", "foreign key", "not null" or "check"; "unknown" for any
// other value.
func (k ConstraintKind) String() string {
	switch k {
	case ConstraintUnique:
		return "unique"
	case ConstraintForeignKey:
		return "foreign key"
	case ConstraintNotNull:
		return "not null"
	case ConstraintCheck:
		return "check"
	}
	return "unknown"
}

// ConstraintError reports a violated constraint, in the same form on every
// dialect; find it with errors.As.
type ConstraintError struct {
	Entity string         // the Go type
	Kind   ConstraintKind // the kind of constraint
	// Constraint is the constraint's name. SQLite reports the columns instead
	// ("invoices.org_id, invoices.number"), and a NOT NULL error names the
	// column.
	Constraint string
	Err        error // the driver's error
}

// Error is "velox: <Entity>: <Kind> constraint <Constraint> violated: <Err>",
// with "(unnamed)" for an empty Constraint.
func (e *ConstraintError) Error() string {
	name := e.Constraint
	if name == "" {
		name = "(unnamed)"
	}
	return fmt.Sprintf("velox: %s: %s constraint %s violated: %v", e.Entity, e.Kind, name, e.Err)
}

// Unwrap is Err.
func (e *ConstraintError) Unwrap() error { return e.Err }

// PolicyError is a statement a policy denied. Every PolicyError matches
// ErrDenied.
type PolicyError struct {
	Entity string // the Go type, e.g. "billing.Invoice"
	// Op is the kind of statement denied: the statement's own, or, for a
	// denial of the target of an EXISTS (a Has or HasWhere, in the statement
	// or in a policy's condition), OpSelect, since the EXISTS reads its target
	// whatever the statement does. An upsert's update branch is OpUpdate.
	Op  Op
	Err error // the policy's error
}

// Error is "velox: <Entity>: <Op> denied by policy", then ": <Err>" when Err
// is not nil.
func (e *PolicyError) Error() string {
	s := "velox: " + e.Entity + ": " + e.Op.String() + " denied by policy"
	if e.Err == nil {
		return s
	}
	return s + ": " + e.Err.Error()
}

// Unwrap returns the policy's error.
func (e *PolicyError) Unwrap() error { return e.Err }

// Is makes every PolicyError match ErrDenied.
func (e *PolicyError) Is(target error) bool { return target == ErrDenied }

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
