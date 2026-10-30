package velox

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"unsafe"
)

// EntityInfo is what velox knows about an entity, built once on its first use.
// It is the non-generic view that hooks see in a Statement, and the runtime
// view of the generated Desc it is built from.
//
// One EntityInfo is shared by every query on the entity, in every goroutine,
// so it must be treated as read-only, slices included. A hook that needs
// different metadata for a statement copies it and points Statement.Entity
// at the copy; it never changes the shared one.
type EntityInfo struct {
	Name       string // the Go type, e.g. "billing.Invoice"
	Table      string
	Schema     string // the schema holding Table; empty for the default
	Columns    []ColumnInfo
	PrimaryKey []int // indexes into Columns
	Tenant     int   // index into Columns; -1 when the entity has none
	Indexes    []IndexInfo
	rels       []*relation                // in the order of the generated shape
	kit        *kit                       // set by MetaOnce.Load
	check      func(col int, v any) error // Desc.Check; nil when the entity has no validators
	immutable  bool                       // some column is Immutable
	required   bool                       // some column has a RequiredEdge
	bidi       bool                       // Desc.Bidi: a link is two rows, (a, b) and (b, a)
	bidiKeys   []bidiKey                  // the keys of the EdgeBidi relations, which both partners' rows hold
	hooks      []hookFn                   // the Hooks method's, outermost first

	// The entity's policy, bound once from its Policy method.
	policy *policy // nil when none
}

// policy is an entity's Policy, with E erased.
type policy struct {
	read, write func(ctx context.Context) (Predicate, error) // nil when not declared
	insert      func(ctx context.Context, p unsafe.Pointer) error
}

// Column returns the index of the column with the given name.
func (in *EntityInfo) Column(name string) (int, bool) {
	for i := range in.Columns {
		if in.Columns[i].Name == name {
			return i, true
		}
	}
	return -1, false
}

// ColumnInfo describes one column. It is the runtime view of the generated
// ColumnDesc it is built from.
type ColumnInfo struct {
	Field       string // the Go field name
	Name        string // the column name
	Type        any    // the field's Go type, a reflect.Type: read it with GoType
	Nullable    bool   // the column holds NULL: the field is a pointer, or its ColumnDesc says Nullable
	Default     any
	HasDefault  bool
	DefaultFunc func() any // nil when none
	// UpdateDefault is the function whose result every update writes to the
	// column; an insert does not (Default and DefaultFunc fill an insert's);
	// nil when none.
	UpdateDefault func() any
	Immutable     bool // an update may not assign the column
	Sensitive     bool // the column holds a secret (velox logs no values)
	// RequiredEdge is ColumnDesc.RequiredEdge: the Required edge whose
	// NULL-able key the column holds, which a write may not leave nil; "" when
	// none.
	RequiredEdge string
	// Codec says the column's value is encoded: its ColumnDesc gave Encode
	// and Decode, as velox-gen does for every json field. The package
	// documentation's JSON fields section says how such a column binds and reads.
	Codec  bool
	encode func(v any) ([]byte, error)    // ColumnDesc.Encode
	decode func(data []byte, v any) error // ColumnDesc.Decode
	off    uintptr                        // the field's offset in the entity
	// copyDefault says Default holds memory a row would share: fillDefault
	// gives each row a deep copy of it.
	copyDefault bool
}

// GoType returns the Go type of column c's field. ColumnInfo.Type holds it as
// any, and this is a function rather than a method, so that no velox type
// reaches reflect.Type: every package that reaches it compiles about 134 KiB of
// wrappers for reflect's iterator types.
func GoType(c ColumnInfo) reflect.Type { return rtype(c.Type) }

// rtype returns the reflect.Type that v holds; velox's types hold them as any
// for the reason given on GoType.
func rtype(v any) reflect.Type {
	t, _ := v.(reflect.Type)
	return t
}

// elemType returns, as any, the reflect.Type of what the pointer p points to.
// Generic code calls it with a nil *T in place of reflect.TypeFor[T](), so that
// its body, compiled where it is called, holds no reflect.Type.
func elemType(p any) any { return reflect.TypeOf(p).Elem() }

// IndexInfo describes one index.
type IndexInfo struct {
	Columns []int
	Unique  bool
}

// Meta is an entity's metadata and scan plan. For generated code.
type Meta[E any] struct {
	info *EntityInfo
	addr func(*E, int) any
	err  error
}

// MetaOnce builds an entity's Meta on first use. For generated code. Its zero
// value is ready, so a generated file declares one without running anything at
// program start.
type MetaOnce[E any] struct {
	once sync.Once
	m    *Meta[E]
}

// Entity is the constraint velox's generic API puts on an entity type E: its
// pointer has the method velox-gen writes.
type Entity[E any] interface {
	*E
	VeloxMeta() *Meta[E]
}

// isRelation reports a One or Many field. It goes by the type's name: a marker
// method would give every instantiation a method table, compiled into every
// package that uses it.
func isRelation(t reflect.Type) bool {
	if t.PkgPath() != reflect.TypeFor[holder]().PkgPath() { // not a package-level value: nothing runs at start
		return false
	}
	n := t.Name()
	return strings.HasPrefix(n, "One[") || strings.HasPrefix(n, "Many[")
}

// validate refuses metadata that would render wrong SQL: two columns of one
// name, a key or index that names no field or one field twice, a tenant
// column that cannot be compared or filled safely, and a primary-key, tenant
// or relation-key column with a codec.
func validate(info *EntityInfo) error {
	var errs []error
	fail := func(field, format string, args ...any) {
		errs = append(errs, &SchemaError{Entity: info.Name, Field: field, Reason: fmt.Sprintf(format, args...)})
	}
	seen := make(map[string]int, len(info.Columns))
	for i, c := range info.Columns {
		if j, ok := seen[c.Name]; ok {
			fail(c.Field, "column name %q is also the name of %s", c.Name, info.Columns[j].Field)
			continue
		}
		seen[c.Name] = i
	}
	set := func(what string, cols []int) {
		if len(cols) == 0 {
			fail("", "%s names no fields", what)
			return
		}
		named := make(map[int]bool, len(cols))
		for _, c := range cols {
			if named[c] {
				fail(info.Columns[c].Field, "%s names the field twice", what)
			}
			named[c] = true
		}
	}
	if info.Tenant >= 0 {
		if c := info.Columns[info.Tenant]; rtype(c.Type).Kind() == reflect.Pointer || !rtype(c.Type).Comparable() {
			fail(c.Field, "the tenant column must be NOT NULL and comparable")
		}
		// Filled by a default, a row would land in a tenant the context
		// never named; and UpdateDefault would move rows between tenants.
		if c := info.Columns[info.Tenant]; c.HasDefault || c.DefaultFunc != nil || c.UpdateDefault != nil {
			fail(c.Field, "the tenant column cannot have Default, DefaultFunc or UpdateDefault: the context's tenant fills it")
		}
	}
	set("the primary key", info.PrimaryKey)
	// A key is compared by every Save, scope and load, and a condition on an
	// encoded column is refused when the statement runs: every use would fail.
	encodedKey := func(what string, col int) {
		if col >= 0 && col < len(info.Columns) && info.Columns[col].Codec {
			fail(info.Columns[col].Field, "%s column %s has a codec (Encode and Decode): an encoded column cannot be compared", what, info.Columns[col].Name)
		}
	}
	for _, k := range info.PrimaryKey {
		encodedKey("the primary key's", k)
	}
	if info.Tenant >= 0 {
		encodedKey("the tenant", info.Tenant)
	}
	for _, r := range info.rels {
		encodedKey("relation "+r.name+"'s key", r.ownerCol)
	}
	for _, ix := range info.Indexes {
		if ix.Unique {
			set("a unique index", ix.Columns)
		} else {
			set("an index", ix.Columns)
		}
	}
	return errors.Join(errs...)
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

// kitOf builds entity E's kit from its scan plan.
func kitOf[E any](info *EntityInfo, addr func(*E, int) any) *kit {
	m := &Meta[E]{info: info, addr: addr}
	typ := elemType((*E)(nil))
	return &kit{
		value: func(p unsafe.Pointer, col int) any { return elem(addr((*E)(p), col)) },
		scan: func(r rows, cols []int) (any, error) {
			n := len(info.Columns)
			if cols != nil {
				n = len(cols)
			}
			dst := make([]any, n)
			part := newBlocks(typ)
			for r.Next() {
				if err := scanRow(r, m, cols, dst, (*E)(part.next())); err != nil {
					return nil, err
				}
			}
			return part, nil
		},
		pick: func(all []any, ptrs []unsafe.Pointer, idx [][]int) []any {
			// An owner whose rows lie one after another in one of the scan's
			// arrays is that run of it; the others' rows are copied, all into
			// one array, so one allocation, not one per owner. Each owner's
			// slice is capped at its length, so an append to it copies first,
			// and keeps the whole array it is in alive, as the owners loaded
			// together usually live together: an owner in place keeps a scan
			// array of up to blockMax rows, the copied owners' among them.
			flags := make([]bool, len(idx)+len(ptrs))
			keep := flags[:len(idx)]
			buf := make([]E, inPlace(all, idx, keep, flags[len(idx):]))
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
		ptr:   func(p unsafe.Pointer) any { return (*E)(p) },
		empty: func() any { return []E{} },
	}
}

// elem is the value p points to. It is not generic, so that the reflect.Value
// it uses is not compiled into the generic code that calls it.
func elem(p any) any { return reflect.ValueOf(p).Elem().Interface() }

// metaOf returns entity E's Meta, for generic code that cannot name E's
// pointer type in its constraints (a method of Rel[E, T]).
func metaOf[E any]() (*Meta[E], error) {
	p, ok := any((*E)(nil)).(interface{ VeloxMeta() *Meta[E] })
	if !ok {
		return nil, fmt.Errorf("velox: %v is not an entity: run go tool velox-gen", elemType((*E)(nil)))
	}
	m := p.VeloxMeta()
	return m, m.err
}

// The functions below reach what EntityInfo keeps private, for the code around
// velox's generic API.
func infoKit(in *EntityInfo) *kit                               { return in.kit }
func setInfoKit(in *EntityInfo, k *kit)                         { in.kit = k }
func setInfoCheck(in *EntityInfo, f func(col int, v any) error) { in.check = f }
func infoHooks(in *EntityInfo) []hookFn                         { return in.hooks }
func infoBidi(in *EntityInfo) bool                              { return in.bidi }
func infoBidiKeys(in *EntityInfo) []bidiKey                     { return in.bidiKeys }
func infoPolicy(in *EntityInfo) *policy                         { return in.policy }
func infoRels(in *EntityInfo) []*relation                       { return in.rels }
func setInfoPolicy(in *EntityInfo, p *policy)                   { in.policy = p }
func setInfoBidi(in *EntityInfo, on bool)                       { in.bidi = on }
