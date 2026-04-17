package velox

import (
	"errors"
	"fmt"
	"strings"

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
	id    any // Optional: the ID that was searched for
}

// Error returns the error string.
func (e *NotFoundError) Error() string {
	if e.id != nil {
		return fmt.Sprintf("velox: %s not found (id=%v)", e.label, e.id)
	}
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

// ID returns the ID that was searched for, if available.
func (e *NotFoundError) ID() any {
	return e.id
}

// NewNotFoundError returns a new NotFoundError for the given entity type.
func NewNotFoundError(label string) *NotFoundError {
	return &NotFoundError{label: label}
}

// NewNotFoundErrorWithID returns a new NotFoundError with the ID that was searched for.
func NewNotFoundErrorWithID(label string, id any) *NotFoundError {
	return &NotFoundError{label: label, id: id}
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
	count int // Number of results returned (-1 if unknown)
}

// Error returns the error string.
func (e *NotSingularError) Error() string {
	if e.count >= 0 {
		return fmt.Sprintf("velox: %s not singular (got %d results, expected 1)", e.label, e.count)
	}
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

// Count returns the number of results, or -1 if unknown.
func (e *NotSingularError) Count() int {
	return e.count
}

// NewNotSingularError returns a new NotSingularError for the given entity type.
func NewNotSingularError(label string) *NotSingularError {
	return &NotSingularError{label: label, count: -1}
}

// NewNotSingularErrorWithCount returns a new NotSingularError with the result count.
func NewNotSingularErrorWithCount(label string, count int) *NotSingularError {
	return &NotSingularError{label: label, count: count}
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

// RollbackError wraps an error that occurred during a transaction rollback.
type RollbackError struct {
	Err error // Original error that triggered rollback
}

// Error returns the error string.
func (e *RollbackError) Error() string {
	return fmt.Sprintf("velox: rollback failed: %v", e.Err)
}

// Unwrap returns the underlying error.
func (e *RollbackError) Unwrap() error {
	return e.Err
}

// NewRollbackError returns a new RollbackError wrapping the given error.
func NewRollbackError(err error) *RollbackError {
	return &RollbackError{Err: err}
}

// IsRollbackError returns true if the error is a RollbackError.
func IsRollbackError(err error) bool {
	if err == nil {
		return false
	}
	var target *RollbackError
	return errors.As(err, &target)
}

// AggregateError represents multiple errors collected during an operation.
type AggregateError struct {
	Errors []error
}

// Unwrap returns the list of errors for use with errors.Is and errors.As (Go 1.20+).
func (e *AggregateError) Unwrap() []error {
	return e.Errors
}

// Error returns the error string.
func (e *AggregateError) Error() string {
	if len(e.Errors) == 0 {
		return "velox: no errors"
	}
	if len(e.Errors) == 1 {
		return e.Errors[0].Error()
	}
	var sb strings.Builder
	sb.WriteString("velox: multiple errors:")
	for i, err := range e.Errors {
		fmt.Fprintf(&sb, "\n  [%d] %v", i+1, err)
	}
	return sb.String()
}

// NewAggregateError returns a new AggregateError if there are errors,
// otherwise returns nil.
func NewAggregateError(errs ...error) error {
	var filtered []error
	for _, err := range errs {
		if err != nil {
			filtered = append(filtered, err)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return &AggregateError{Errors: filtered}
}

// QueryError wraps a query error with additional context.
type QueryError struct {
	Entity string // Entity type being queried
	Op     string // Operation (e.g., "select", "count", "exist")
	Err    error  // Underlying error
}

// Error returns the error string.
func (e *QueryError) Error() string {
	if e.Op != "" {
		return fmt.Sprintf("velox: querying %s (%s): %v", e.Entity, e.Op, e.Err)
	}
	return fmt.Sprintf("velox: querying %s: %v", e.Entity, e.Err)
}

// Unwrap returns the underlying error.
func (e *QueryError) Unwrap() error {
	return e.Err
}

// NewQueryError returns a new QueryError.
func NewQueryError(entity, op string, err error) *QueryError {
	return &QueryError{Entity: entity, Op: op, Err: err}
}

// IsQueryError returns true if the error is a QueryError.
func IsQueryError(err error) bool {
	if err == nil {
		return false
	}
	var target *QueryError
	return errors.As(err, &target)
}

// MutationError wraps a mutation error with additional context.
type MutationError struct {
	Entity string // Entity type being mutated
	Op     string // Operation (e.g., "create", "update", "delete")
	Err    error  // Underlying error
}

// Error returns the error string.
func (e *MutationError) Error() string {
	return fmt.Sprintf("velox: %s %s: %v", e.Op, e.Entity, e.Err)
}

// Unwrap returns the underlying error.
func (e *MutationError) Unwrap() error {
	return e.Err
}

// NewMutationError returns a new MutationError.
func NewMutationError(entity, op string, err error) *MutationError {
	return &MutationError{Entity: entity, Op: op, Err: err}
}

// IsMutationError returns true if the error is a MutationError.
func IsMutationError(err error) bool {
	if err == nil {
		return false
	}
	var target *MutationError
	return errors.As(err, &target)
}

// PrivacyError represents a privacy policy violation.
type PrivacyError struct {
	Entity string // Entity type
	Op     string // Operation (query or mutation)
	Rule   string // Rule that denied the operation
	Cause  error  // Optional underlying error
}

// Error returns the error string.
func (e *PrivacyError) Error() string {
	if e.Rule != "" {
		return fmt.Sprintf("velox: privacy denied %s on %s (rule: %s)", e.Op, e.Entity, e.Rule)
	}
	return fmt.Sprintf("velox: privacy denied %s on %s", e.Op, e.Entity)
}

// Unwrap returns the underlying cause, enabling errors.Is/errors.As chains.
func (e *PrivacyError) Unwrap() error { return e.Cause }

// NewPrivacyError returns a new PrivacyError.
func NewPrivacyError(entity, op, rule string) *PrivacyError {
	return &PrivacyError{Entity: entity, Op: op, Rule: rule}
}

// NewPrivacyErrorWithCause returns a new PrivacyError wrapping a cause.
func NewPrivacyErrorWithCause(entity, op, rule string, cause error) *PrivacyError {
	return &PrivacyError{Entity: entity, Op: op, Rule: rule, Cause: cause}
}

// IsPrivacyError returns true if the error is a PrivacyError.
func IsPrivacyError(err error) bool {
	if err == nil {
		return false
	}
	var target *PrivacyError
	return errors.As(err, &target)
}
