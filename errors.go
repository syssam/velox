package velox

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
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

// MaskNotFound returns nil for an error that is ErrNotFound and err for
// anything else: where a missing row is an answer and not a failure, such as
// a to-one edge without a neighbor, which resolves to null.
func MaskNotFound(err error) error {
	if IsNotFound(err) {
		return nil
	}
	return err
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

// The kinds of a ConstraintError, for errors.Is: a handler that turns a
// duplicate into "already exists" tests errors.Is(err, velox.ErrUnique). The
// kind is read from the database's own error, which a ConstraintError wraps.
var (
	ErrUnique     = errors.New("velox: unique constraint violated")
	ErrForeignKey = errors.New("velox: foreign key constraint violated")
	ErrCheck      = errors.New("velox: check constraint violated")
)

// Is reports whether target is the sentinel of the error's kind.
func (e *ConstraintError) Is(target error) bool {
	if e.wrap == nil {
		return false
	}
	switch target {
	case ErrUnique:
		return sqlgraph.IsUniqueConstraintError(e.wrap)
	case ErrForeignKey:
		return sqlgraph.IsForeignKeyConstraintError(e.wrap)
	case ErrCheck:
		return sqlgraph.IsCheckConstraintError(e.wrap)
	}
	return false
}

var (
	sqliteConstraint = regexp.MustCompile(`constraint failed: ([^()]+?)\s*(?:\(|$)`)
	mysqlKey         = regexp.MustCompile(`for key '([^']+)'`)
)

// Constraint returns what the database names as violated, when its driver
// says: the constraint on Postgres (users_email_key), the columns on SQLite
// (users.email), the key on MySQL. It is for messages and logs; its form
// differs by database, and it is empty when the driver does not say.
func (e *ConstraintError) Constraint() string {
	if e.wrap == nil {
		return ""
	}
	// lib/pq and pgx carry the name in a field of their error.
	for err := e.wrap; err != nil; err = errors.Unwrap(err) {
		rv := reflect.Indirect(reflect.ValueOf(err))
		if rv.Kind() != reflect.Struct {
			continue
		}
		for _, name := range [...]string{"Constraint", "ConstraintName"} {
			if f := rv.FieldByName(name); f.IsValid() && f.Kind() == reflect.String && f.String() != "" {
				return f.String()
			}
		}
	}
	msg := e.wrap.Error()
	if m := sqliteConstraint.FindStringSubmatch(msg); m != nil {
		return strings.TrimSpace(m[1])
	}
	if m := mysqlKey.FindStringSubmatch(msg); m != nil {
		return m[1]
	}
	return ""
}

// IsRetryable reports whether err is a failure the database asks to be tried
// again: a serialization failure or a deadlock (SQLSTATE 40001 and 40P01 on
// Postgres, errors 1213 and 1205 on MySQL), or a database that was busy or
// locked (SQLite). The whole transaction is what is tried again, from its
// first statement, and only when running it twice is harmless; a constraint
// violation or a validation error is never retryable.
func IsRetryable(err error) bool {
	for e := err; e != nil; e = errors.Unwrap(e) {
		switch x := e.(type) {
		case interface{ SQLState() string }:
			if state := x.SQLState(); state == "40001" || state == "40P01" {
				return true
			}
		case interface{ Code() int }:
			// SQLite's primary result code: SQLITE_BUSY, SQLITE_LOCKED.
			if code := x.Code() & 0xff; code == 5 || code == 6 {
				return true
			}
		}
		// go-sql-driver/mysql carries the server's error number in a field.
		if rv := reflect.Indirect(reflect.ValueOf(e)); rv.Kind() == reflect.Struct {
			if f := rv.FieldByName("Number"); f.IsValid() && f.CanUint() && (f.Uint() == 1213 || f.Uint() == 1205) {
				return true
			}
		}
	}
	return false
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

	// refused says that no validator of the schema ran: the value of the
	// field or edge is refused for what it is (missing though required,
	// set though immutable). NewFieldError sets it, and the message then
	// names the field and gives the reason, without "validator failed".
	refused bool
}

// Error returns the error string.
func (e *ValidationError) Error() string {
	if e.refused {
		if e.Entity != "" {
			return fmt.Sprintf("velox: %s.%s: %s", e.Entity, e.Name, e.Err)
		}
		return fmt.Sprintf("velox: %s: %s", e.Name, e.Err)
	}
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

// NewFieldError returns the ValidationError of a field or an edge of an
// entity whose value is refused before it reaches the database for another
// reason than a validator of the schema: a required one that is missing, an
// immutable one that is set, a value that is none of an enum's. It reads
// "velox: User.name: missing required field".
func NewFieldError(entity, name string, err error) *ValidationError {
	return &ValidationError{Entity: entity, Name: name, Field: name, Err: err, refused: true}
}

// IsValidationError returns true if the error is a ValidationError.
func IsValidationError(err error) bool {
	if err == nil {
		return false
	}
	var target *ValidationError
	return errors.As(err, &target)
}
