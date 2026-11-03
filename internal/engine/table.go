package engine

import (
	"errors"
	"fmt"
	"reflect"
	"unsafe"
)

// Table is an entity's metadata and scan plan with the entity's type erased:
// what an untyped query (DB.QueryTable) reads, and what the untyped
// conditions are made with, keyed by column and relation indexes. For
// generated code. Code that handles every entity alike, such as
// velox-graphql's runtime, is compiled once on it, where the typed API would
// be compiled once per entity. TableOf returns an entity's. A row is an
// unsafe.Pointer to an E; TableRows is a run of them.
//
// The zero Table is no entity: its queries fail.
type Table struct{ t *table }

// table is a Table's state, built once per entity by TableOf.
type table struct {
	info    *EntityInfo
	err     error // the metadata's error; info is nil with it
	kit     *Kit
	addr    func(p unsafe.Pointer, col int) any // &row.<field of column col>
	nilCols []bool                              // as Meta's
	// typ, slice and vals hold reflect.Types as any: a velox type that
	// reaches reflect.Type puts reflect's method tables into every package
	// of entities (internal/repocheck's compile-cost test).
	typ   any     // E
	slice any     // []E
	size  uintptr // E's size
	// vals is, for each column, the Go type of a value a condition on it
	// compares with (its field's type, without the pointer of a nullable one),
	//
	vals []any
	// target names E as a relation's target, and its Get is E's owner getter
	// for a RelRef.
	target RelTarget
}

// errNoTable is the error of the zero Table's queries.
var errNoTable = errors.New("velox: a zero Table: use TableOf")

// NewTable returns the Table of an entity: its metadata info (or its error
// err, info nil), its field addresses (addr), the columns NilScanner reads
// (nilCols), its reflect.Type typ (as any), and target, which names it as a
// relation's target. velox's TableOf calls it once per entity.
func NewTable(info *EntityInfo, err error, addr func(p unsafe.Pointer, col int) any, nilCols []bool, typ any, target RelTarget) Table {
	t := &table{info: info, err: err, target: target}
	if err == nil {
		rt := typ.(reflect.Type)
		t.kit, t.addr, t.nilCols, t.typ = info.kit, addr, nilCols, rt
		t.slice, t.size = reflect.SliceOf(rt), rt.Size()
		t.vals = make([]any, len(info.Columns))
		for i := range info.Columns {
			ft := GoType(info.Columns[i])
			if ft != nil && ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			t.vals[i] = ft
		}
	}
	return Table{t}
}

// state is t's table, or the zero Table's error state.
func (t Table) state() *table {
	if t.t == nil {
		return &table{err: errNoTable}
	}
	return t.t
}

// Info returns the entity's metadata, or nil when it is invalid (Err).
// It is shared: treat it as read-only.
func (t Table) Info() *EntityInfo { return t.state().info }

// Err returns what is wrong with the entity's metadata, or nil.
func (t Table) Err() error { return t.state().err }

// Field returns a pointer to the field of column col of the row at p, as any:
// a *V for a field of type V. The Table must be valid
// (Err nil) and col one of its columns.
func (t Table) Field(p unsafe.Pointer, col int) any { return t.t.addr(p, col) }

// Value returns the value of column col of the row at p.
// The Table must be valid and col one of its columns.
func (t Table) Value(p unsafe.Pointer, col int) any { return t.t.kit.Value(p, col) }

// Ptr returns the row at p as a *E, in an any. The Table
// must be valid.
func (t Table) Ptr(p unsafe.Pointer) any { return t.t.kit.Ptr(p) }

// New returns a new zero row. The Table must be valid.
func (t Table) New() unsafe.Pointer { return reflect.New(t.t.typ.(reflect.Type)).UnsafePointer() }

// copyRow returns a new row holding what the row at p holds.
func (t *table) copyRow(p unsafe.Pointer) unsafe.Pointer {
	typ := t.typ.(reflect.Type)
	c := reflect.New(typ)
	c.Elem().Set(reflect.NewAt(typ, p).Elem())
	return c.UnsafePointer()
}

// Fill stores row, a row of target, as the entity of relation edge's field in
// the row at owner, loaded, as a Join does when it reads a row: Rel.Fill,
// untyped. It fails when edge is no relation held in a
// field, or target is not the relation's target.
func (t Table) Fill(owner unsafe.Pointer, edge int, target Table, row unsafe.Pointer) error {
	s, ts := t.state(), target.state()
	if s.err != nil {
		return s.err
	}
	if ts.err != nil {
		return ts.err
	}
	if _, err := (RelRef{Owner: s.target.Get, Target: ts.target, I: int32(edge)}).Link(); err != nil {
		return err
	}
	off, err := RelOffset(s.info, int32(edge))
	if err != nil {
		return err
	}
	h := (*Holder)(unsafe.Add(owner, off))
	h.Loaded, h.V = true, ts.kit.Ptr(row)
	return nil
}

// Filled returns the entity of relation edge's field in the row at owner, a
// row of target, and whether it holds one: Rel.Filled, untyped. A field that
// holds no *T of target's entity, such as a to-many relation's slice, holds
// none.
func (t Table) Filled(owner unsafe.Pointer, edge int, target Table) (unsafe.Pointer, bool) {
	s, ts := t.state(), target.state()
	if s.err != nil || ts.err != nil {
		return nil, false
	}
	off, err := RelOffset(s.info, int32(edge))
	if err != nil {
		return nil, false
	}
	h := (*Holder)(unsafe.Add(owner, off))
	if h.V == nil {
		return nil, false
	}
	// V holds a *T, which an interface keeps in its data word, with *T's
	// type in its type word.
	probe := ts.kit.Ptr(nil)
	w, pw := (*[2]unsafe.Pointer)(unsafe.Pointer(&h.V)), (*[2]unsafe.Pointer)(unsafe.Pointer(&probe))
	if w[0] != pw[0] {
		return nil, false
	}
	return w[1], w[1] != nil
}

// TableRows is a run of rows of one entity, one after another in memory: what an
// untyped query returns. For generated code. RowsOf views them as a []E.
// The zero TableRows is empty.
type TableRows struct {
	p    unsafe.Pointer // the first row
	size uintptr        // a row's size
	n    int
	info *EntityInfo // the rows' entity
}

// Len returns the number of rows.
func (r TableRows) Len() int { return r.n }

// At returns a pointer to row i. It panics when i is out of range.
func (r TableRows) At(i int) unsafe.Pointer {
	if i < 0 || i >= r.n {
		panic(fmt.Sprintf("velox: TableRows.At(%d) of %d rows", i, r.n))
	}
	return unsafe.Add(r.p, uintptr(i)*r.size)
}

// RowsData is r's first row, nil for none, its length, its row size and its
// entity: for velox's RowsOf.
func RowsData(r TableRows) (unsafe.Pointer, int, uintptr, *EntityInfo) {
	return r.p, r.n, r.size, r.info
}

// sliceRows is the TableRows of v, a []E in an any, whose rows are size
// bytes, of the entity info.
func sliceRows(v any, size uintptr, info *EntityInfo) TableRows {
	// A slice in an interface is held by pointer, in its data word.
	h := (*struct {
		p    unsafe.Pointer
		n, c int
	})((*[2]unsafe.Pointer)(unsafe.Pointer(&v))[1])
	if h == nil {
		return TableRows{size: size, info: info}
	}
	return TableRows{p: h.p, size: size, n: h.n, info: info}
}

// PredOp is the operator of an untyped condition on a column (Table.Pred).
// For generated code.
type PredOp uint8

// The operators of Table.Pred, each that of the column handles' method of
// the same name.
const (
	PredEQ        PredOp = iota + 1 // column = v
	PredNEQ                         // column <> v
	PredIn                          // column IN (v...), v a []any; false for none
	PredNotIn                       // column NOT IN (v...), v a []any; true for none
	PredGT                          // column > v
	PredGTE                         // column >= v
	PredLT                          // column < v
	PredLTE                         // column <= v
	PredContains                    // column LIKE %v%, v's LIKE characters escaped; a text column
	PredHasPrefix                   // column LIKE v%; a text column
	PredHasSuffix                   // column LIKE %v; a text column
	PredIsNull                      // column IS NULL; v nil; a nullable column
	PredNotNull                     // column IS NOT NULL; v nil; a nullable column
)

// predOps are the engine's operators of the PredOps that compare.
var predOps = [...]Node{
	PredEQ: {Op: OpEQ}, PredNEQ: {Op: OpNEQ}, PredIn: {Op: OpIn}, PredNotIn: {Op: OpNotIn},
	PredGT: {Op: OpGT}, PredGTE: {Op: OpGTE}, PredLT: {Op: OpLT}, PredLTE: {Op: OpLTE},
	PredIsNull: {Op: OpIsNull}, PredNotNull: {Op: OpNotNull},
}

// Pred is the condition op on column col of the entity, with the value v: the
// column handles' EQ, NEQ, In, NotIn, GT, GTE, LT, LTE, Contains, HasPrefix,
// HasSuffix, IsNull and NotNull, untyped.
//
// v must be of the column's Go type, as the handle's method takes it: the
// field's type, or for a nullable field (a pointer) the type it points to;
// for In and NotIn, a []any of such values. A condition the column cannot
// take (a value of another type, a text operator on a column that is not
// text, IsNull on a column that is not nullable, a column or an operator out
// of range) is an invalid condition, whose error fails the statement that
// holds it before anything is sent: never a condition on another value.
func (t Table) Pred(col int, op PredOp, v any) Predicate {
	n, err := t.pred(col, op, v)
	if err != nil {
		return NewPredicate(Node{Op: OpInvalid, V: err})
	}
	return NewPredicate(n)
}

func (t Table) pred(col int, op PredOp, v any) (Node, error) {
	s := t.state()
	if s.err != nil {
		return Node{}, s.err
	}
	if col < 0 || col >= len(s.info.Columns) {
		return Node{}, fmt.Errorf("velox: %s: a condition on column %d, of %d", s.info.Name, col, len(s.info.Columns))
	}
	c := &s.info.Columns[col]
	want, _ := s.vals[col].(reflect.Type)
	check := func(v any) error { return checkValue(s.info, c, want, v) }
	switch op {
	case PredEQ, PredNEQ, PredGT, PredGTE, PredLT, PredLTE:
		if err := check(v); err != nil {
			return Node{}, err
		}
		n := predOps[op]
		n.Col, n.V, n.W = int32(col), v, s.info
		return n, nil
	case PredIn, PredNotIn:
		vs, ok := v.([]any)
		if !ok && v != nil {
			return Node{}, fmt.Errorf("velox: %s: In and NotIn on %s take a []any, not a %T", s.info.Name, c.Field, v)
		}
		for _, x := range vs {
			if err := check(x); err != nil {
				return Node{}, err
			}
		}
		if vs == nil {
			vs = []any{}
		}
		n := predOps[op]
		n.Col, n.V, n.W = int32(col), vs, s.info
		return n, nil
	case PredContains, PredHasPrefix, PredHasSuffix:
		if want.Kind() != reflect.String {
			return Node{}, fmt.Errorf("velox: %s: %s is not a text column", s.info.Name, c.Field)
		}
		if err := check(v); err != nil {
			return Node{}, err
		}
		s := EscapeLike(reflect.ValueOf(v).String())
		switch op {
		case PredContains:
			s = "%" + s + "%"
		case PredHasPrefix:
			s += "%"
		default:
			s = "%" + s
		}
		return Node{Op: OpLike, Col: int32(col), V: s, W: t.t.info}, nil
	case PredIsNull, PredNotNull:
		if !c.Nullable {
			return Node{}, fmt.Errorf("velox: %s: %s is not nullable", s.info.Name, c.Field)
		}
		if v != nil {
			return Node{}, fmt.Errorf("velox: %s: IsNull and NotNull on %s take no value", s.info.Name, c.Field)
		}
		n := predOps[op]
		n.Col, n.W = int32(col), s.info
		return n, nil
	}
	return Node{}, fmt.Errorf("velox: %s: no condition operator %d", s.info.Name, op)
}

// Convert returns v as column col's Go type, the one Pred takes, when v is
// of another type of the same kind: a value as a GraphQL input holds it, in
// the base type its scalar binds to, for a column of the application's own
// Go type. Integers of any size count as one kind, and so do floats. A v of
// the column's own type is returned as it is; a v of another kind fails.
func (t Table) Convert(col int, v any) (any, error) {
	s := t.state()
	if s.err != nil {
		return nil, s.err
	}
	if col < 0 || col >= len(s.info.Columns) {
		return nil, fmt.Errorf("velox: %s: a value of column %d, of %d", s.info.Name, col, len(s.info.Columns))
	}
	want, _ := s.vals[col].(reflect.Type)
	got := reflect.TypeOf(v)
	if got == want {
		return v, nil
	}
	if got == nil || want == nil || kindClass(got.Kind()) != kindClass(want.Kind()) || !got.ConvertibleTo(want) {
		return nil, checkValue(s.info, &s.info.Columns[col], want, v)
	}
	return reflect.ValueOf(v).Convert(want).Interface(), nil
}

// kindClass is k, but one kind for the integers and one for the floats.
func kindClass(k reflect.Kind) reflect.Kind {
	switch k {
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8,
		reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return reflect.Int
	case reflect.Float32:
		return reflect.Float64
	}
	return k
}

// checkValue reports a value v of a condition on column c of in that is not
// of want, the column's Go type.
func checkValue(in *EntityInfo, c *ColumnInfo, want reflect.Type, v any) error {
	if reflect.TypeOf(v) != want {
		return fmt.Errorf("velox: %s: a condition on %s takes a %v, not a %T", in.Name, c.Field, want, v)
	}
	return nil
}

// HasReadPolicy reports whether the entity has a Read policy, which the
// schema declares or a scope adds: Meta.HasReadPolicy.
func (t Table) HasReadPolicy() bool {
	s := t.state()
	if s.info == nil {
		return false
	}
	p := InfoPolicy(s.info)
	return p != nil && p.Read != nil
}

// Has is true when relation edge holds at least one entity of target that
// meets every condition of ps: Rel.HasWhere, untyped. ps
// are conditions on target. Like HasWhere it reads no metadata: a relation
// that does not resolve, or whose target is not target, fails the statement
// that holds the condition.
func (t Table) Has(edge int, target Table, ps ...Predicate) Predicate {
	s, ts := t.state(), target.state()
	for _, err := range []error{s.err, ts.err} {
		if err != nil {
			return NewPredicate(Node{Op: OpInvalid, V: err})
		}
	}
	kids := make([]Node, len(ps))
	for i, p := range ps {
		if err := PredErr(PredNode(p)); err != nil {
			return NewPredicate(Node{Op: OpInvalid, V: err})
		}
		kids[i] = PredNode(p)
	}
	return NewPredicate(Node{Op: OpExists, Col: int32(edge), V: RelRef{Owner: s.target.Get, Target: ts.target, I: int32(edge)}, Kids: kids})
}

// Of is true for the rows of target related through relation edge to any of
// owners, rows of the entity: Rel.Of, untyped, of the relation as its handle
// declares it, without a Where. A relation that does not resolve, or whose
// target is not target, is an invalid condition.
func (t Table) Of(edge int, target Table, owners ...unsafe.Pointer) Predicate {
	s, ts := t.state(), target.state()
	for _, err := range []error{s.err, ts.err} {
		if err != nil {
			return NewPredicate(Node{Op: OpInvalid, V: err})
		}
	}
	return NewPredicate(OfNode(RelRef{Owner: s.target.Get, Target: ts.target, I: int32(edge)}, nil, nil, owners))
}
