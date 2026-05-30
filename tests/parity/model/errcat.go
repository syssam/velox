package model

// ErrCat is the canonical, executor-independent category of an operation's
// outcome. Each executor maps its native error into one of these so the
// comparator can compare error behavior across implementations.
//
// The canonical definition lives here (the leaf package the comparator depends
// on) so that package compare can import model for its Diff types without an
// import cycle. Package compare re-exports these as aliases, so the public
// taxonomy surface is compare.ErrCat / compare.ErrOK / ... as documented.
type ErrCat string

const (
	// ErrOK means the operation succeeded with no error.
	ErrOK ErrCat = "ok"
	// ErrNotFound means the target row did not exist (e.g. update/delete a
	// missing or already-deleted handle).
	ErrNotFound ErrCat = "not_found"
	// ErrUnique means a unique constraint was violated.
	ErrUnique ErrCat = "unique_violation"
	// ErrFK means a foreign-key constraint was violated.
	ErrFK ErrCat = "fk_violation"
	// ErrValidation means input failed a validation/constraint check.
	ErrValidation ErrCat = "validation"
	// ErrNotLoaded means an edge was accessed without being loaded.
	ErrNotLoaded ErrCat = "not_loaded"
	// ErrInternal means the operation failed with an unexpected/internal error
	// that does not map to any known category. It must NOT be conflated with
	// ErrValidation: relabeling an unexpected error as a validation failure on a
	// validation-expected op would let a genuine crash falsely Pass.
	ErrInternal ErrCat = "internal"
)
