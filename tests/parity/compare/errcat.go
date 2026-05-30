// Package compare holds the canonical error taxonomy surface, structured diff,
// and three-way verdict classification for the parity harness. It is ORM-free:
// it judges executor outputs without depending on velox or ent.
//
// The error category type is defined canonically in package model (the leaf the
// comparator's Diff operates over) and re-exported here as aliases, so the
// public taxonomy is compare.ErrCat / compare.ErrOK / ... while compare can
// still import model for Diff without forming an import cycle.
package compare

import "velox.test/parity/model"

// ErrCat is the canonical, executor-independent category of an operation's
// outcome (alias of model.ErrCat).
type ErrCat = model.ErrCat

const (
	// ErrOK means the operation succeeded with no error.
	ErrOK = model.ErrOK
	// ErrNotFound means the target row did not exist.
	ErrNotFound = model.ErrNotFound
	// ErrUnique means a unique constraint was violated.
	ErrUnique = model.ErrUnique
	// ErrFK means a foreign-key constraint was violated.
	ErrFK = model.ErrFK
	// ErrValidation means input failed a validation/constraint check.
	ErrValidation = model.ErrValidation
	// ErrNotLoaded means an edge was accessed without being loaded.
	ErrNotLoaded = model.ErrNotLoaded
)
