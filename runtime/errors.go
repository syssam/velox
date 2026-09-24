package runtime

import (
	"errors"

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
)

// ConstraintError is an alias for velox.ConstraintError.
type ConstraintError = velox.ConstraintError

// ValidationError is an alias for velox.ValidationError.
type ValidationError = velox.ValidationError

// ErrNodeIDTypeMismatch is returned by a generated NodeResolver when the
// id it was handed is not of that entity's ID type.
//
// Noder/Noders try every registered resolver in turn, so in a schema that
// mixes ID types (int for some entities, uuid.UUID for others) a mismatch
// is the normal outcome for every resolver but one. It must be skipped
// like a not-found error rather than aborting the lookup — otherwise
// resolution succeeds or fails depending on map iteration order.
var ErrNodeIDTypeMismatch = errors.New("velox: NodeResolver: id type mismatch")

// ErrAmbiguousNodeID is returned by ResolveNode (and so by the generated
// Noder/Noders) when an id matches rows in more than one entity type. With
// per-table auto-increment keys, User 1 and Todo 1 both exist; picking one
// would answer node(id:) with an arbitrary type. Relay IDs must be globally
// unique — enable FeatureGlobalID. Test with errors.Is.
var ErrAmbiguousNodeID = errors.New("velox: node id matches more than one type")

// ErrOldValueAfterMutation is returned by OldXxx / OldField when the old
// row is requested after the UPDATE has run. At that point the row holds
// the new values, so returning them as "old" would silently corrupt audit
// and diff logic. Read old values in the hook before calling next.Mutate —
// a value read there stays available afterwards. Test with errors.Is.
var ErrOldValueAfterMutation = errors.New("velox: old values are only available before the mutation runs")

// IsNodeIDTypeMismatch reports whether err came from a NodeResolver that
// does not handle the given id type.
func IsNodeIDTypeMismatch(err error) bool {
	return errors.Is(err, ErrNodeIDTypeMismatch)
}

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
