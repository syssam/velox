package velox

import (
	"database/sql"
	"fmt"
	"reflect"
	"unsafe"

	"github.com/syssam/velox/internal/engine"
)

// Meta is an entity's metadata and scan plan. For generated code.
type Meta[E any] struct {
	info *EntityInfo
	addr func(*E, int) any
	err  error
	// nilCols marks the columns engine.NilScanner reads; nil when there are none.
	nilCols []bool
	// tbl is the entity's Table, built by its first TableOf.
	tbl tableState
}

// nilColumns are the columns of a slice or a map that database/sql cannot read a NULL
// into, nil when there are none: it knows of []byte alone, and its error fails each
// page holding the row. A pointer it reads, an encoded column and a type's own Scan
// are told of NULL themselves.
func nilColumns(info *EntityInfo) []bool {
	var cols []bool
	for i := range info.Columns {
		c := &info.Columns[i]
		t, _ := c.Type.(reflect.Type)
		if t == nil || c.Codec || t.Kind() != reflect.Slice && t.Kind() != reflect.Map || t == reflect.TypeFor[[]byte]() ||
			reflect.PointerTo(t).Implements(reflect.TypeFor[sql.Scanner]()) {
			continue
		}
		if cols == nil {
			cols = make([]bool, len(info.Columns))
		}
		cols[i] = true
	}
	return cols
}

// MetaOnce builds an entity's Meta on first use. For generated code. Its zero
// value is ready, so a generated file declares one without running anything at
// program start. A build that fails with a NotLinkedError panic is not kept: the
// next use builds the Meta again.
type MetaOnce[E any] struct {
	once engine.Once
	m    *Meta[E]
}

// NotLinkedError is what generated code panics with when an entity is used before
// the package that binds its schema has run its init. For generated code. The
// entity's Meta holds the error, and MetaOnce does not keep it: the entity
// works once that init has run. Its Error returns the message.
type NotLinkedError = engine.NotLinkedError

// Show is what an entity's String prints for p, a field that is a pointer:
// the value it points to, or nil. For generated code.
func Show[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

// Entity is the constraint velox's generic API puts on an entity type E: its
// pointer has the method velox-gen writes.
type Entity[E any] interface {
	*E
	VeloxMeta() *Meta[E]
}

// CheckEntity builds entity E's metadata and returns what is wrong with it:
// generated code that is out of date or hand-edited, a Policy or Hooks method
// the metadata does not bind, a panic in the metadata. A relation names its
// target where a Rel handle is used, so CheckEntity does not check
// relations: call CheckRelation for each (a test that calls only CheckEntity
// leaves the relations unchecked). velox-gen verify calls CheckEntity for
// every entity and CheckRelation for every relation.
func CheckEntity[E any, PE Entity[E]]() error {
	return PE(nil).VeloxMeta().err
}

// HasReadPolicy reports whether entity E's policy has a Read condition: one
// the schema declares, a scope's, or both. For generated code, such as a
// GraphQL filter that treats a key whose row Read hides as null. It is false
// when E's metadata failed to build; the entity's first statement reports why.
func (m *Meta[E]) HasReadPolicy[_ Lazy[E]]() bool {
	if m == nil || m.info == nil {
		return false
	}
	p := engine.InfoPolicy(m.info)
	return p != nil && p.Read != nil
}

// kitOf builds entity E's kit from its scan plan.
func kitOf[E any](info *EntityInfo, addr func(*E, int) any) *engine.Kit {
	m := &Meta[E]{info: info, addr: addr, nilCols: nilColumns(info)}
	typ := engine.ElemType((*E)(nil))
	return &engine.Kit{
		Value: func(p unsafe.Pointer, col int) any { return engine.Elem(addr((*E)(p), col)) },
		Scan: func(r engine.Rows, cols []int) (any, error) {
			n := len(info.Columns)
			if cols != nil {
				n = len(cols)
			}
			dst := make([]any, n)
			part := engine.NewBlocks(typ)
			for r.Next() {
				if err := scanRow(r, m, cols, dst, (*E)(part.Next())); err != nil {
					return nil, err
				}
			}
			return part, nil
		},
		Pick: func(all []any, ptrs []unsafe.Pointer, idx [][]int) []any {
			// An owner whose rows lie one after another in one of the scan's
			// arrays is that run of it; the others' rows are copied, all into
			// one array, so one allocation, not one per owner. Each owner's
			// slice is capped at its length, so an append to it copies first,
			// and keeps the whole array it is in alive, as the owners loaded
			// together usually live together: an owner in place keeps a scan
			// array of up to blockMax rows, the copied owners' among them.
			flags := make([]bool, len(idx)+len(ptrs))
			keep := flags[:len(idx)]
			buf := make([]E, engine.InPlace(all, idx, keep, flags[len(idx):]))
			out := make([]any, len(idx))
			at := 0
			for i, x := range idx {
				var part []E
				if keep[i] {
					part = unsafe.Slice((*E)(ptrs[x[0]]), len(x))
				} else {
					part = buf[at : at+len(x) : at+len(x)]
					for k, j := range x {
						part[k] = *(*E)(ptrs[j])
					}
					at += len(x)
				}
				out[i] = part
			}
			return out
		},
		Ptr:   func(p unsafe.Pointer) any { return (*E)(p) },
		Empty: func() any { return []E{} },
	}
}

// metaOf returns entity E's Meta, for generic code that cannot name E's
// pointer type in its constraints (a method of Rel[E, T]).
func metaOf[E any]() (*Meta[E], error) {
	p, ok := any((*E)(nil)).(interface{ VeloxMeta() *Meta[E] })
	if !ok {
		return nil, fmt.Errorf("velox: %v is not an entity: run go tool velox-gen", engine.ElemType((*E)(nil)))
	}
	m := p.VeloxMeta()
	return m, m.err
}
