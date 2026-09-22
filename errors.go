package velox

import (
	"errors"
	"fmt"

	"github.com/syssam/velox/dialect/sql/sqlgraph"
)

// Standard sentinel errors for common operations.
var (
	// ErrNotFound is returned when a requested entity does not exist.
	ErrNotFound = errors.New("velox: entity not found")

	// ErrNotSingular is returned when a query that expects exactly one result
	// returns zero or multiple results.
	ErrNotSingular = errors.New("velox: entity not singular")

	// ErrTxStarted is returned when attempting to start a new transaction
	// within an existing transaction.
	ErrTxStarted = errors.New("velox: cannot start a transaction within a transaction")
)

// NotFoundError represents an error when an entity is not found.
type NotFoundError struct {
	label string
}

// Error returns the error string.
func (e *NotFoundError) Error() string {
	return fmt.Sprintf("velox: %s not found", e.label)
}

// Is reports whether the target error matches NotFoundError.
// This allows errors.Is(notFoundErr, ErrNotFound) to return true.
func (e *NotFoundError) Is(err error) bool {
	return err == ErrNotFound
}

// Label returns the entity label.
func (e *NotFoundError) Label() string {
	return e.label
}

// NewNotFoundError returns a new NotFoundError for the given entity type.
func NewNotFoundError(label string) *NotFoundError {
	return &NotFoundError{label: label}
}

// IsNotFound returns true if the error is a NotFoundError.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	var target *NotFoundError
	return errors.As(err, &target) || errors.Is(err, ErrNotFound)
}

// NotSingularError represents an error when a query expects a singular result
// but receives zero or multiple results.
type NotSingularError struct {
	label string
}

// Error returns the error string.
func (e *NotSingularError) Error() string {
	return fmt.Sprintf("velox: %s not singular", e.label)
}

// Is reports whether the target error matches NotSingularError.
// This allows errors.Is(notSingularErr, ErrNotSingular) to return true.
func (e *NotSingularError) Is(err error) bool {
	return err == ErrNotSingular
}

// Label returns the entity label.
func (e *NotSingularError) Label() string {
	return e.label
}

// NewNotSingularError returns a new NotSingularError for the given entity type.
func NewNotSingularError(label string) *NotSingularError {
	return &NotSingularError{label: label}
}

// IsNotSingular returns true if the error is a NotSingularError.
func IsNotSingular(err error) bool {
	if err == nil {
		return false
	}
	var target *NotSingularError
	return errors.As(err, &target) || errors.Is(err, ErrNotSingular)
}

// NotLoadedError represents an error when attempting to access an edge
// that was not loaded (eager-loaded).
type NotLoadedError struct {
	edge string
}

// Error returns the error string.
func (e *NotLoadedError) Error() string {
	return fmt.Sprintf("velox: edge %q was not loaded", e.edge)
}

// Edge returns the name of the edge that was not loaded.
func (e *NotLoadedError) Edge() string {
	return e.edge
}

// NewNotLoadedError returns a new NotLoadedError for the given edge name.
func NewNotLoadedError(edge string) *NotLoadedError {
	return &NotLoadedError{edge: edge}
}

// IsNotLoaded returns true if the error is a NotLoadedError.
func IsNotLoaded(err error) bool {
	if err == nil {
		return false
	}
	var target *NotLoadedError
	return errors.As(err, &target)
}

// ConstraintError represents a database constraint violation error.
type ConstraintError struct {
	msg  string
	wrap error
}

// Error returns the error string.
func (e *ConstraintError) Error() string {
	return fmt.Sprintf("velox: constraint failed: %s", e.msg)
}

// Unwrap returns the underlying error.
func (e *ConstraintError) Unwrap() error {
	return e.wrap
}

// Message returns the constraint violation message.
func (e *ConstraintError) Message() string {
	return e.msg
}

// NewConstraintError returns a new ConstraintError with the given message.
func NewConstraintError(msg string, wrap error) *ConstraintError {
	return &ConstraintError{msg: msg, wrap: wrap}
}

// IsConstraintError returns true if the error is a constraint violation.
// It checks both the typed ConstraintError and raw database driver errors
// (unique, foreign key, check constraint violations).
func IsConstraintError(err error) bool {
	if err == nil {
		return false
	}
	var target *ConstraintError
	return errors.As(err, &target) || sqlgraph.IsConstraintError(err)
}

// ValidationError represents a validation error for field values.
type ValidationError struct {
	Name   string // Field or edge name (kept for backward compat)
	Err    error  // Underlying validation error
	Entity string // Entity type name (e.g., "User")
	Field  string // Field name (e.g., "name") — same as Name for field validations
}

// Error returns the error string.
func (e *ValidationError) Error() string {
	return fmt.Sprintf("velox: validator failed for field %q: %s", e.Name, e.Err)
}

// Unwrap returns the underlying error.
func (e *ValidationError) Unwrap() error {
	return e.Err
}

// NewValidationError returns a new ValidationError for the given field.
func NewValidationError(name string, err error) *ValidationError {
	return &ValidationError{Name: name, Err: err}
}

// IsValidationError returns true if the error is a ValidationError.
func IsValidationError(err error) bool {
	if err == nil {
		return false
	}
	var target *ValidationError
	return errors.As(err, &target)
}
