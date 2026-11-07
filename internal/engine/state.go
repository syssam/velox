package engine

import (
	"sync"
	"unsafe"
)

// MetaState is velox's Meta: an entity's metadata and scan plan. velox.Meta
// holds one as its only field, so that package untyped reaches it by a
// pointer conversion, as velox.DB holds a DB.
type MetaState[E any] struct {
	Info *EntityInfo
	Addr func(*E, int) any
	Err  error
	// NilCols marks the columns NilScanner reads; nil when there are none.
	NilCols []bool
	// Tbl is the entity's Table, built by its first TableOf.
	Tbl TableCache
}

// TableCache holds an entity's Table, built on first use: an entity that no
// untyped code reads builds none. It holds the Table as any: a Meta that
// names Table puts Table's methods, and what they reference, into the export
// data of every package of entities (internal/repocheck's compile-cost test).
type TableCache struct {
	once sync.Once
	t    any // a Table
}

// TableOfState is m's Table, built on first use; target names the entity as
// a relation's target.
func TableOfState[E any](m *MetaState[E], target func() RelTarget) Table {
	m.Tbl.once.Do(func() {
		var typ any
		var addr func(p unsafe.Pointer, col int) any
		if m.Err == nil {
			a := m.Addr
			addr = func(p unsafe.Pointer, col int) any { return a((*E)(p), col) }
			typ = ElemType((*E)(nil))
		}
		m.Tbl.t = NewTable(m.Info, m.Err, addr, m.NilCols, typ, target())
	})
	t, _ := m.Tbl.t.(Table)
	return t
}

// QueryState is the part of velox's Query that does not depend on its
// entity: velox.Query holds one as its first field, for the reason MetaState
// is velox.Meta's. It is not generic, so that a Query of each entity adds no
// type of its own.
type QueryState struct {
	DB    *DB
	St    Statement
	Err   error          // a condition's error, returned by every way of running the query
	Loads []LoadSpec     // the relations All loads
	After unsafe.Pointer // After's row, copied: a *E
	Joins *JoinSet       // how to read the joined relations, in the order of St.Joins; nil without Join
}
