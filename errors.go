package velox

import (
	"errors"
	"fmt"
)

// Errors velox returns, wrapped with the entity's name; test them with
// errors.Is.
var (
	// ErrNotFound: One found no row.
	ErrNotFound = errors.New("velox: not found")
	// ErrNotSingular: One found more than one row.
	ErrNotSingular = errors.New("velox: more than one row")
	// ErrUnbounded: a query with no Limit that did not say Unbounded. Raised
	// by the guardrails (sub-project 2).
	ErrUnbounded = errors.New("velox: query has no Limit")
	// ErrNoWhere: an update or delete with no Where that did not say All.
	// Raised by the guardrails (sub-project 2).
	ErrNoWhere = errors.New("velox: update or delete has no Where")
	// ErrStale: an entity's struct changed after velox-gen last ran.
	ErrStale = errors.New("velox: generated code is out of date")
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
