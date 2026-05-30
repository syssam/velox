// Package compare holds the canonical error taxonomy, structured diff, and
// three-way verdict classification for the parity harness. It is ORM-free: it
// judges executor outputs without depending on velox or ent.
package compare

// ErrCat is the canonical, executor-independent category of an operation's
// outcome. Each executor maps its native error into one of these so the
// comparator can compare error behavior across implementations.
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
)
