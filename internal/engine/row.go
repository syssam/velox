package engine

import (
	"errors"
	"fmt"
	"iter"
	"reflect"
	"time"
	"unsafe"
)

// The field types a mutation by name reads and stores without reflect: a
// ColumnInfo's fast. fastPtr marks a pointer to one.
const (
	fastInt64 uint8 = 1 + iota
	fastInt32
	fastInt
	fastString
	fastBool
	fastFloat64
	fastTime
	fastPtr uint8 = 0x80
)

// fastOf is the fast code of a field of type ft; 0 when it has none.
func fastOf(ft reflect.Type) uint8 {
	var ptr uint8
	if ft.Kind() == reflect.Pointer {
		ft, ptr = ft.Elem(), fastPtr
	}
	var f uint8
	switch ft {
	case reflect.TypeFor[int64]():
		f = fastInt64
	case reflect.TypeFor[int32]():
		f = fastInt32
	case reflect.TypeFor[int]():
		f = fastInt
	case reflect.TypeFor[string]():
		f = fastString
	case reflect.TypeFor[bool]():
		f = fastBool
	case reflect.TypeFor[float64]():
		f = fastFloat64
	case reflect.TypeFor[time.Time]():
		f = fastTime
	default:
		return 0
	}
	return f | ptr
}

// storeFast stores v, of the column's value type, in the field at p, whose
// fast code is f, and reports whether it could: false for a field without
// one, which store sets through reflect.
func storeFast(f uint8, p unsafe.Pointer, v any) bool {
	if f&fastPtr != 0 {
		var q unsafe.Pointer
		switch x := v.(type) {
		case int64:
			q = unsafe.Pointer(&x)
		case int32:
			q = unsafe.Pointer(&x)
		case int:
			q = unsafe.Pointer(&x)
		case string:
			q = unsafe.Pointer(&x)
		case bool:
			q = unsafe.Pointer(&x)
		case float64:
			q = unsafe.Pointer(&x)
		case time.Time:
			q = unsafe.Pointer(&x)
		default:
			return false
		}
		*(*unsafe.Pointer)(p) = q
		return true
	}
	switch f {
	case fastInt64:
		*(*int64)(p) = v.(int64)
	case fastInt32:
		*(*int32)(p) = v.(int32)
	case fastInt:
		*(*int)(p) = v.(int)
	case fastString:
		*(*string)(p) = v.(string)
	case fastBool:
		*(*bool)(p) = v.(bool)
	case fastFloat64:
		*(*float64)(p) = v.(float64)
	case fastTime:
		*(*time.Time)(p) = v.(time.Time)
	default:
		return false
	}
	return true
}

// field is the address of column c's field in the row at row.
func field(c *ColumnInfo, row unsafe.Pointer) unsafe.Pointer { return unsafe.Add(row, c.off) }

// storeColumn sets column c's field in the row at row to v, converted to
// the column's value type; nil sets NULL on a nullable column.
func storeColumn(c *ColumnInfo, row unsafe.Pointer, v any) error {
	cv, err := columnValue(c, v)
	if err != nil {
		return err
	}
	storeValue(c, row, cv)
	return nil
}

// columnValue is v as column c's value type (namedValue), or nil, which
// only a nullable column takes.
func columnValue(c *ColumnInfo, v any) (any, error) {
	if v == nil {
		if !c.Nullable {
			return nil, fmt.Errorf("%s is not nullable", c.Name)
		}
		return nil, nil
	}
	return namedValue(c, v)
}

// storeValue sets column c's field in the row at row to v, a value
// columnValue returned: nil zeroes the field.
func storeValue(c *ColumnInfo, row unsafe.Pointer, v any) {
	p := field(c, row)
	if v == nil {
		reflect.NewAt(GoType(*c), p).Elem().SetZero()
		return
	}
	if c.fast != 0 && storeFast(c.fast, p, v) {
		return
	}
	cv := v
	f := reflect.NewAt(GoType(*c), p).Elem()
	if f.Kind() == reflect.Pointer {
		n := reflect.New(f.Type().Elem())
		n.Elem().Set(reflect.ValueOf(cv))
		f.Set(n)
		return
	}
	f.Set(reflect.ValueOf(cv))
}

// storeAll sets column c's field in each of rows to v, as storeValue does:
// for the common types, with one type assertion for all of them.
func storeAll(c *ColumnInfo, rows []unsafe.Pointer, v any) {
	switch x := v.(type) {
	case int64:
		if c.fast == fastInt64 {
			for _, p := range rows {
				*(*int64)(field(c, p)) = x
			}
			return
		}
	case string:
		if c.fast == fastString {
			for _, p := range rows {
				*(*string)(field(c, p)) = x
			}
			return
		}
	}
	for _, p := range rows {
		storeValue(c, p, v)
	}
}

// loadColumn is the value of column c's field in the row at row: nil for
// NULL, and for a nullable field the value, not its pointer.
func loadColumn(c *ColumnInfo, row unsafe.Pointer) any {
	p := field(c, row)
	if c.fast&fastPtr != 0 {
		q := *(*unsafe.Pointer)(p)
		if q == nil {
			return nil
		}
		p = q
	}
	switch c.fast &^ fastPtr {
	case fastInt64:
		return *(*int64)(p)
	case fastInt32:
		return *(*int32)(p)
	case fastInt:
		return *(*int)(p)
	case fastString:
		return *(*string)(p)
	case fastBool:
		return *(*bool)(p)
	case fastFloat64:
		return *(*float64)(p)
	case fastTime:
		return *(*time.Time)(p)
	}
	f := reflect.NewAt(GoType(*c), p).Elem()
	switch f.Kind() {
	case reflect.Pointer:
		if f.IsNil() {
			return nil
		}
		return f.Elem().Interface()
	case reflect.Slice, reflect.Map:
		if c.Nullable && f.IsNil() {
			return nil
		}
	}
	return f.Interface()
}

// zeroColumn reports whether column c's field in the row at row holds its
// Go zero value: nil, for a pointer.
func zeroColumn(c *ColumnInfo, row unsafe.Pointer) bool {
	p := field(c, row)
	if c.fast&fastPtr != 0 {
		return *(*unsafe.Pointer)(p) == nil
	}
	switch c.fast {
	case fastInt64:
		return *(*int64)(p) == 0
	case fastInt32:
		return *(*int32)(p) == 0
	case fastInt:
		return *(*int)(p) == 0
	case fastString:
		return *(*string)(p) == ""
	case fastBool:
		return !*(*bool)(p)
	case fastFloat64:
		return *(*float64)(p) == 0
	case fastTime:
		return (*(*time.Time)(p)).IsZero()
	}
	return reflect.NewAt(GoType(*c), p).Elem().IsZero()
}

// Row is a row of an insert, by its columns' names: what a hook ranges
// over in an insert's rows, and what an Insert policy checks. It is a
// value, cheap to copy, and is valid while the write runs.
//
// A wrong name, a value of another kind, or a Set in an Insert policy is a
// misuse, which fails the write.
type Row struct {
	in  *EntityInfo
	p   unsafe.Pointer
	rec *error // where a misuse is recorded, keeping the first
	ro  bool   // an Insert policy's row: Set is a misuse
}

// NewRow is the row at p of entity in, which records a misuse in *rec,
// keeping the first. A read-only row refuses Set.
func NewRow(in *EntityInfo, p unsafe.Pointer, rec *error, readOnly bool) Row {
	return Row{in: in, p: p, rec: rec, ro: readOnly}
}

// record records err, keeping the first, and returns it.
func (r Row) record(err error) error {
	if r.rec != nil && *r.rec == nil {
		*r.rec = err
	}
	return err
}

// column is the column of the given name, or the misuse of a name the
// entity has no column of, recorded.
func (r Row) column(what, name string) (*ColumnInfo, error) {
	if r.in == nil {
		return nil, r.record(errors.New("the zero Row has no columns"))
	}
	i, ok := r.in.names[name]
	if !ok {
		return nil, r.record(fmt.Errorf("%s(%q): no column %q", what, name, name))
	}
	return &r.in.Columns[i], nil
}

// Value returns the column's value: nil for NULL, and for a nullable
// column the value, not a pointer to it.
func (r Row) Value(name string) any {
	c, err := r.column("Value", name)
	if err != nil {
		return nil
	}
	return loadColumn(c, r.p)
}

// Set sets the column to v, converted as a condition's value is: a value
// of another type of the same kind is converted, and one of another kind
// is an error. nil sets NULL on a nullable column. In an Insert policy it
// is a misuse: a policy decides, and does not change the row.
func (r Row) Set(name string, v any) error {
	if r.ro {
		return r.record(fmt.Errorf("Set(%q) in an Insert policy: a policy decides and does not change the row; change it in a hook", name))
	}
	c, err := r.column("Set", name)
	if err != nil {
		return err
	}
	if err := storeColumn(c, r.p, v); err != nil {
		return r.record(fmt.Errorf("Set(%q, %v): %w", name, v, err))
	}
	return nil
}

// IsZero reports whether the column holds its Go zero value: nil, for a
// nullable column.
func (r Row) IsZero(name string) bool {
	c, err := r.column("IsZero", name)
	if err != nil {
		return false
	}
	return zeroColumn(c, r.p)
}

// Entity returns the row as its *E, in an any, for code that can name E.
func (r Row) Entity() any {
	if r.in == nil || r.in.kit == nil {
		return nil
	}
	return r.in.kit.Ptr(r.p)
}

// entity is the mutation's entity: the statement's, or the insert's.
func (mu *MutationState) entity() *EntityInfo {
	if mu.St != nil {
		return mu.St.Entity
	}
	return mu.In
}

// op is the write's kind.
func (mu *MutationState) op() Op {
	if mu.Op != 0 {
		return mu.Op
	}
	return mu.St.Op
}

// fail records the misuse err, keeping the first, and returns it.
func (mu *MutationState) fail(err error) error {
	mu.Record(err)
	return err
}

// column is the index of the column of the given name, or -1 after
// recording the misuse of a name the entity has no column of.
func (mu *MutationState) column(what, name string) int {
	in := mu.entity()
	if in == nil {
		mu.Record(fmt.Errorf("%s(%q): the mutation has no entity", what, name))
		return -1
	}
	i, ok := in.names[name]
	if !ok {
		mu.Record(fmt.Errorf("%s(%q): no column %q", what, name, name))
		return -1
	}
	return int(i)
}

// SetByName sets the column of the given name: in every row of an insert,
// or in an update's assignment, which it replaces. v is converted as a
// condition's value is, and nil sets NULL on a nullable column. On a delete
// it is a misuse. A misuse is recorded, and returned.
func (mu *MutationState) SetByName(name string, v any) error {
	i := mu.column("Set", name)
	if i < 0 {
		return mu.Err
	}
	in := mu.entity()
	c := &in.Columns[i]
	switch mu.op() {
	case OpInsert:
		cv, err := columnValue(c, v)
		if err != nil {
			return mu.fail(fmt.Errorf("Set(%q, %v): %w", name, v, err))
		}
		storeAll(c, mu.Rows, cv)
		return nil
	case OpDelete:
		return mu.fail(fmt.Errorf("Set(%q) on a delete: a delete assigns no columns", name))
	}
	a := Assignment{Column: i, Op: AssignSet}
	if v != nil {
		cv, err := namedValue(c, v)
		if err != nil {
			return mu.fail(fmt.Errorf("Set(%q, %v): %w", name, v, err))
		}
		a.Value = cv
	} else if !c.Nullable {
		return mu.fail(fmt.Errorf("Set(%q, nil): %s is not nullable", name, name))
	}
	MutateSet(mu, a)
	return nil
}

// FillByName sets the column of the given name to v in each row of an
// insert whose column holds its Go zero value, as SetByName sets it. On an
// update or a delete it is a misuse.
func (mu *MutationState) FillByName(name string, v any) error {
	i := mu.column("Fill", name)
	if i < 0 {
		return mu.Err
	}
	if mu.op() != OpInsert {
		return mu.fail(fmt.Errorf("Fill(%q) on an update or a delete: Fill sets an insert's rows; Set sets an update's assignment", name))
	}
	c := &mu.entity().Columns[i]
	cv, err := columnValue(c, v)
	if err != nil {
		return mu.fail(fmt.Errorf("Fill(%q, %v): %w", name, v, err))
	}
	for _, p := range mu.Rows {
		if zeroColumn(c, p) {
			storeValue(c, p, cv)
		}
	}
	return nil
}

// ValueByName returns the value an update assigns to the column of the
// given name with Set: nil for NULL, and for a nullable column the value,
// not a pointer to it. It reports false when the update does not assign
// the column, or assigns it with Add or Sub, and on a delete. On an insert
// it is a misuse: an insert's values are its rows'.
func (mu *MutationState) ValueByName(name string) (any, bool) {
	i := mu.column("Value", name)
	if i < 0 {
		return nil, false
	}
	if mu.St == nil {
		mu.Record(fmt.Errorf("Value(%q) on an insert: read an insert's values from its rows", name))
		return nil, false
	}
	v, ok := Assigned(mu.St, i)
	if !ok {
		return nil, false
	}
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Pointer && rtype(mu.St.Entity.Columns[i].val) != rv.Type() {
		// Save assigns a nullable column its field, a pointer.
		if rv.IsNil() {
			return nil, true
		}
		return rv.Elem().Interface(), true
	}
	return v, true
}

// RowsByName returns an insert's rows by their columns' names; none for an
// update or a delete.
func (mu *MutationState) RowsByName() iter.Seq[Row] {
	return func(yield func(Row) bool) {
		in := mu.entity()
		for _, p := range mu.Rows {
			if !yield(Row{in: in, p: p, rec: &mu.Err}) {
				return
			}
		}
	}
}
