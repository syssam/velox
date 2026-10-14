package velox

import (
	"errors"
	"fmt"
)

// Errors velox returns. Each is returned wrapped, in an error whose text
// starts "velox: <Entity>: " (policy design §6), so a sentinel's own text is
// a bare phrase; test for them with errors.Is.
var (
	// ErrNotFound: One found no row, or Save no row with the key.
	ErrNotFound = errors.New("not found")
	// ErrNotSingular: One found more than one row.
	ErrNotSingular = errors.New("more than one row")
	// ErrUnbounded: a query with no Limit that did not say Unbounded.
	ErrUnbounded = errors.New("query has no Limit")
	// ErrNoWhere: an update or delete with no Where that did not say All.
	ErrNoWhere = errors.New("update or delete has no Where")
	// ErrLimitTooLarge: a query whose Limit is over the DB's WithMaxLimit.
	ErrLimitTooLarge = errors.New("limit is over the maximum")
	// ErrStale: an entity's struct changed after velox-gen last ran.
	ErrStale = errors.New("generated code is out of date")
)

// SchemaError reports a mistake in an entity's Schema method or in a
// mixin's Mixin method.
type SchemaError struct {
	Entity string // the Go type, e.g. "billing.Invoice"
	Field  string // the Go field name; empty when the error is about the entity
	Reason string
}

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
	Unique ConstraintKind = iota + 1 // a unique index or primary key
	ForeignKey
	NotNull
	Check
)

func (k ConstraintKind) String() string {
	switch k {
	case Unique:
		return "unique"
	case ForeignKey:
		return "foreign key"
	case NotNull:
		return "not null"
	case Check:
		return "check"
	}
	return "unknown"
}

// ConstraintError reports a violated constraint, in the same form on every
// dialect; find it with errors.As. Err is the driver's error.
type ConstraintError struct {
	Entity string
	Kind   ConstraintKind
	// Constraint is the constraint's name. SQLite reports the columns instead
	// ("invoices.org_id, invoices.number"), and a NOT NULL error names the
	// column.
	Constraint string
	Err        error
}

func (e *ConstraintError) Error() string {
	name := e.Constraint
	if name == "" {
		name = "(unnamed)"
	}
	return fmt.Sprintf("velox: %s: %s constraint %s violated: %v", e.Entity, e.Kind, name, e.Err)
}

func (e *ConstraintError) Unwrap() error { return e.Err }
