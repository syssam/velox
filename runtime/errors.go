package runtime

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect/sql/sqlgraph"
)

type (
	// NotFoundError is an alias for velox.NotFoundError.
	NotFoundError = velox.NotFoundError
	// NotSingularError is an alias for velox.NotSingularError.
	NotSingularError = velox.NotSingularError
	// NotLoadedError is an alias for velox.NotLoadedError.
	NotLoadedError = velox.NotLoadedError
)

// Constructors — delegate to velox root package.
var (
	NewNotFoundError    = velox.NewNotFoundError
	NewNotSingularError = velox.NewNotSingularError
	NewNotLoadedError   = velox.NewNotLoadedError
	NewConstraintError  = velox.NewConstraintError
	NewValidationError  = velox.NewValidationError
)

// ConstraintError is an alias for velox.ConstraintError.
type ConstraintError = velox.ConstraintError

// ValidationError is an alias for velox.ValidationError.
type ValidationError = velox.ValidationError

// Sentinel errors — aliases for the root velox package sentinels.
var (
	ErrNotFound    = velox.ErrNotFound
	ErrNotSingular = velox.ErrNotSingular
	ErrTxStarted   = velox.ErrTxStarted
)

// IsNotFound returns true if the error is a NotFoundError.
// Delegates to velox.IsNotFound which checks both AsType and errors.Is.
func IsNotFound(err error) bool {
	return velox.IsNotFound(err)
}

// IsNotSingular returns true if the error is a NotSingularError.
// Delegates to velox.IsNotSingular which checks both AsType and errors.Is.
func IsNotSingular(err error) bool {
	return velox.IsNotSingular(err)
}

// IsNotLoaded returns true if the error is a NotLoadedError.
func IsNotLoaded(err error) bool {
	return velox.IsNotLoaded(err)
}

// IsConstraintError returns true if the error is a constraint violation.
func IsConstraintError(err error) bool {
	return velox.IsConstraintError(err)
}

// IsValidationError returns true if the error is a ValidationError.
func IsValidationError(err error) bool {
	return velox.IsValidationError(err)
}

// MayWrapConstraintError wraps the error with ConstraintError if it is a
// database constraint violation (duplicate key, FK violation, etc.).
// This ensures velox.IsConstraintError(err) returns true for DB errors.
// Exported for use by generated Create/Update builders that call
// sqlgraph.CreateNode / sqlgraph.UpdateNodes directly (no runtime middleman).
func MayWrapConstraintError(err error) error {
	if sqlgraph.IsConstraintError(err) {
		return NewConstraintError(err.Error(), err)
	}
	return err
}
