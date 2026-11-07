package engine

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"unsafe"
)

// Table is an entity's metadata and scan plan with the entity's type erased:
// what an untyped query (untyped.Query) reads, and what the untyped
// conditions are made with, keyed by column and relation indexes. For
// generated code. Code that handles every entity alike, such as
// velox-graphql's runtime, is compiled once on it, where the typed API would
// be compiled once per entity. TableOf returns an entity's. A row is a *E
// in an any, which each method checks is of the Table's entity; TableRows
// is a run of them.
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
	// ptrw is *E's type word: an any whose type word it is holds a row.
	ptrw unsafe.Pointer
	// vals is, for each column, the Go type of a value a condition on it
	// compares with (its field's type, without the pointer of a nullable one),
	//
	vals []any
	// target names E as a relation's target, and its Get is E's owner getter
	// for a RelRef.
	target RelTarget
}

// errNoTable is the error of the zero Table's queries.
var errNoTable = errors.New("velox: a zero Table: use untyped.TableOf")

// NewTable returns the Table of an entity: its metadata info (or its error
// err, info nil), its field addresses (addr), the columns NilScanner reads
// (nilCols), its reflect.Type typ (as any), and target, which names it as a
// relation's target. untyped.TableOf calls it once per entity.
func NewTable(info *EntityInfo, err error, addr func(p unsafe.Pointer, col int) any, nilCols []bool, typ any, target RelTarget) Table {
	t := &table{info: info, err: err, target: target}
	if err == nil {
		rt := typ.(reflect.Type)
		t.kit, t.addr, t.nilCols, t.typ = info.kit, addr, nilCols, rt
		t.slice, t.size = reflect.SliceOf(rt), rt.Size()
		t.ptrw = typeWord(info.kit.Ptr(nil))
		t.vals = make([]any, len(info.Columns))
		for i := range info.Columns {
			t.vals[i] = info.Columns[i].val
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

// typeWord is the type word of v.
func typeWord(v any) unsafe.Pointer { return (*[2]unsafe.Pointer)(unsafe.Pointer(&v))[0] }

// rowOf is the row row holds: a non-nil *E of t's entity. Another value is an
// error, as is an invalid Table.
func (t *table) rowOf(row any) (unsafe.Pointer, error) {
	if t.err != nil {
		return nil, t.err
	}
	w := (*[2]unsafe.Pointer)(unsafe.Pointer(&row))
	if w[0] != t.ptrw || w[1] == nil {
		return nil, fmt.Errorf("velox: %s: a row is a non-nil *%v, not %T", t.info.Name, t.typ, row)
	}
	return w[1], nil
}

// rowsOf is rowOf of each of rows.
func (t *table) rowsOf(rows []any) ([]unsafe.Pointer, error) {
	out := make([]unsafe.Pointer, len(rows))
	for i, r := range rows {
		p, err := t.rowOf(r)
		if err != nil {
			return nil, err
		}
		out[i] = p
	}
	return out, nil
}

// Field returns a pointer to the field of column col of row, as any: a *V
// for a field of type V. It is nil when row is not a non-nil *E of the
// Table's entity, the Table is invalid, or col is not one of its columns.
func (t Table) Field(row any, col int) any {
	s := t.state()
	p, err := s.rowOf(row)
	if err != nil || col < 0 || col >= len(s.info.Columns) {
		return nil
	}
	return s.addr(p, col)
}

// Value returns the value of column col of row. It is nil when Field is.
func (t Table) Value(row any, col int) any {
	s := t.state()
	p, err := s.rowOf(row)
	if err != nil || col < 0 || col >= len(s.info.Columns) {
		return nil
	}
	return s.kit.Value(p, col)
}

// New returns a new zero row, a *E in an any. It is nil when the Table is
// invalid.
func (t Table) New() any {
	s := t.state()
	if s.err != nil {
		return nil
	}
	return reflect.New(s.typ.(reflect.Type)).Interface()
}

// newRow returns a new zero row.
func (t *table) newRow() unsafe.Pointer { return reflect.New(t.typ.(reflect.Type)).UnsafePointer() }

// copyRow returns a new row holding what the row at p holds.
func (t *table) copyRow(p unsafe.Pointer) unsafe.Pointer {
	typ := t.typ.(reflect.Type)
	c := reflect.New(typ)
	c.Elem().Set(reflect.NewAt(typ, p).Elem())
	return c.UnsafePointer()
}

// Fill stores row, a row of target, as the entity of relation edge's field in
// the row at owner, loaded, as a Join does when it reads a row: Rel.Fill,
// untyped. It fails when edge is no relation held in a field, target is not
// the relation's target, or owner or row is not a row of its entity.
func (t Table) Fill(owner any, edge int, target Table, row any) error {
	s, ts := t.state(), target.state()
	op, err := s.rowOf(owner)
	if err != nil {
		return err
	}
	rp, err := ts.rowOf(row)
	if err != nil {
		return err
	}
	if _, err := (RelRef{Owner: s.target.Get, Target: ts.target, I: int32(edge)}).Link(); err != nil {
		return err
	}
	off, err := RelOffset(s.info, int32(edge))
	if err != nil {
		return err
	}
	h := (*Holder)(unsafe.Add(op, off))
	h.Loaded, h.V = true, ts.kit.Ptr(rp)
	return nil
}

// Filled returns the entity of relation edge's field in the row at owner, a
// row of target, as a *T in an any, and whether it holds one: Rel.Filled,
// untyped. A field that holds no *T of target's entity, such as a to-many
// relation's slice, holds none, as does an owner that is not a row of the
// Table's entity.
func (t Table) Filled(owner any, edge int, target Table) (any, bool) {
	s, ts := t.state(), target.state()
	if ts.err != nil {
		return nil, false
	}
	op, err := s.rowOf(owner)
	if err != nil {
		return nil, false
	}
	off, err := RelOffset(s.info, int32(edge))
	if err != nil {
		return nil, false
	}
	h := (*Holder)(unsafe.Add(op, off))
	if typeWord(h.V) != ts.ptrw {
		return nil, false
	}
	if _, err := ts.rowOf(h.V); err != nil {
		return nil, false // a nil *T
	}
	return h.V, true
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

// At returns row i, a *E in an any, pointing into the rows. It panics when i
// is out of range.
func (r TableRows) At(i int) any { return r.info.kit.Ptr(r.at(i)) }

// at is a pointer to row i. It panics when i is out of range.
func (r TableRows) at(i int) unsafe.Pointer {
	if i < 0 || i >= r.n {
		panic(fmt.Sprintf("velox: TableRows.At(%d) of %d rows", i, r.n))
	}
	return unsafe.Add(r.p, uintptr(i)*r.size)
}

// RowsData is r's first row, nil for none, its length, its row size and its
// entity: for untyped.RowsOf.
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
// the column's own type is returned as it is; a v of another kind fails. A
// number the column's type cannot hold (300 for an int8, -1 for a uint64,
// 1e300 for a float32) fails with ErrOutOfRange.
func (t Table) Convert(col int, v any) (any, error) {
	s := t.state()
	if s.err != nil {
		return nil, s.err
	}
	if col < 0 || col >= len(s.info.Columns) {
		return nil, fmt.Errorf("velox: %s: a value of column %d, of %d", s.info.Name, col, len(s.info.Columns))
	}
	want, _ := s.vals[col].(reflect.Type)
	switch out, res := coerce(want, v); res {
	case coerced:
		return out, nil
	case overflow:
		return nil, fmt.Errorf("velox: %s: %v for %s, a %v: %w", s.info.Name, v, s.info.Columns[col].Field, want, ErrOutOfRange)
	}
	return nil, checkValue(s.info, &s.info.Columns[col], want, v)
}

// overflows reports whether v, a number, does not fit want, a number type
// of the same kind class.
func overflows(v reflect.Value, want reflect.Type) bool {
	z := reflect.New(want).Elem()
	switch {
	case v.CanInt():
		x := v.Int()
		if z.CanUint() {
			return x < 0 || z.OverflowUint(uint64(x))
		}
		return z.OverflowInt(x)
	case v.CanUint():
		x := v.Uint()
		if z.CanInt() {
			return x > math.MaxInt64 || z.OverflowInt(int64(x))
		}
		return z.OverflowUint(x)
	case v.CanFloat():
		x := v.Float()
		return !math.IsInf(x, 0) && !math.IsNaN(x) && z.OverflowFloat(x)
	}
	return false
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

// HasRowFilter reports whether the entity's rows are filtered for reads, by
// an interceptor or a Read policy, which the schema declares or a scope
// adds: Meta.HasRowFilter.
func (t Table) HasRowFilter() bool {
	s := t.state()
	if s.info == nil {
		return false
	}
	return HasRowFilter(s.info)
}

// Has is true when relation edge holds at least one entity of target that
// meets every condition of ps: Rel.HasWith, untyped. ps
// are conditions on target. Like HasWith it reads no metadata: a relation
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
	byName := false
	for i, p := range ps {
		if err := PredErr(PredNode(p)); err != nil {
			return NewPredicate(Node{Op: OpInvalid, V: err})
		}
		kids[i] = PredNode(p)
		byName = byName || named(kids[i])
	}
	return NewPredicate(Node{Op: OpExists, ByName: byName, Col: int32(edge), V: RelRef{Owner: s.target.Get, Target: ts.target, I: int32(edge)}, Kids: kids})
}

// Of is true for the rows of target related through relation edge to any of
// owners, rows of the entity: Rel.Of, untyped, of the relation as its handle
// declares it, without a Where. A relation that does not resolve, or whose
// target is not target, is an invalid condition.
func (t Table) Of(edge int, target Table, owners ...any) Predicate {
	s, ts := t.state(), target.state()
	for _, err := range []error{s.err, ts.err} {
		if err != nil {
			return NewPredicate(Node{Op: OpInvalid, V: err})
		}
	}
	ps, err := s.rowsOf(owners)
	if err != nil {
		return NewPredicate(Node{Op: OpInvalid, V: err})
	}
	return NewPredicate(OfNode(RelRef{Owner: s.target.Get, Target: ts.target, I: int32(edge)}, nil, nil, ps))
}
