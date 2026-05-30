// Package model is the ground-truth reference interpreter for the parity
// harness. It executes an op.Program over in-memory slices and produces one
// model.Result per op. It is ORM-free: it judges nothing, it only defines the
// correct observable outcome that the velox and ent executors (A3) are checked
// against.
package model

import "velox.test/parity/compare"

// Value is the normalized cell type shared by every executor. Its allowed
// dynamic types are exactly: nil, int, string, bool, []string (JSON labels),
// and Ref (a foreign entity reference normalized to its creation handle). The
// comparator panics on any other dynamic type, so this set is closed by
// contract.
type Value any

// Ref is a foreign entity reference normalized to its creation handle (the
// program index of the Create* op that made it) — never a raw database id.
type Ref struct {
	Handle int
}

// Row is one observed record: column name -> normalized Value. Every returned
// Row carries "id": Ref{Handle: <own handle>}.
type Row map[string]Value

// Result is the observable outcome of exactly one op.
type Result struct {
	Rows   []Row          // for queries / paginate / load; nil for non-returning ops
	Scalar *int           // for count / sum
	Page   *PageInfo      // for paginate ops only
	Err    compare.ErrCat // canonical error category; ErrOK when no error
}

// PageInfo is the Relay page metadata for a paginate op.
type PageInfo struct {
	HasNext, HasPrev bool
	// StartHandle/EndHandle are the creation handles of the first/last row, or
	// nil for an empty page. Cursors are compared by handle, not opaque bytes.
	StartHandle, EndHandle *int
}
