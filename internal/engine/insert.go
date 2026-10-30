package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"unsafe"
)

// MaxParams is each dialect's limit on the parameters of one statement.
var MaxParams = map[Dialect]int{Postgres: 65535, MySQL: 65535, SQLite: 32766}

// CheckConflict refuses an upsert clause of entity in without the columns of
// a unique key, one that updates an Immutable column, one that updates on
// the join entity of a bidirectional edge, and an upsert of the row at one
// (nil for InsertAll, whose prepareMany refuses it) that sets the key of a
// bidirectional one-to-one edge. It is not generic, so that every entity's
// InsertQuery does not compile it.
func CheckConflict(in *EntityInfo, cf *ConflictClause, one unsafe.Pointer) error {
	if len(cf.Columns) == 0 {
		return fmt.Errorf("velox: %s: an upsert needs OnConflict with the columns of a unique key", in.Name)
	}
	if one != nil && len(in.bidiKeys) > 0 {
		if err := RefuseBidiInsert(in, one, false); err != nil {
			return err
		}
	}
	if len(cf.Update) > 0 && in.bidi {
		return ErrBidiUpdate(in)
	}
	if in.immutable {
		for _, c := range cf.Update {
			if col := &in.Columns[c]; col.Immutable {
				return fmt.Errorf("velox: %s: %s: %w", in.Name, col.Field, ErrImmutable)
			}
		}
	}
	return nil
}

// FillDefault sets the field that ptr points to as column c's Default or
// DefaultFunc says. A DefaultFunc returns the element type of a pointer field
// (velox-gen's Nillable), which is stored through a new pointer. It is not
// generic, so that the reflect.Value it uses is not compiled into every
// package that inserts.
func FillDefault(c *ColumnInfo, ptr any) {
	v := reflect.ValueOf(ptr).Elem()
	switch {
	case c.HasDefault && v.IsZero():
		if c.copyDefault {
			v.Set(defaultValue(c.Default))
		} else {
			v.Set(reflect.ValueOf(c.Default))
		}
	case c.DefaultFunc != nil && v.IsZero():
		d := reflect.ValueOf(c.DefaultFunc())
		if t := v.Type(); t.Kind() == reflect.Pointer && d.Type() != t {
			p := reflect.New(t.Elem())
			p.Elem().Set(d)
			d = p
		}
		v.Set(d)
	}
}

// defaultValue is a column's static default as a reflect.Value: a deep copy,
// so that entities never share a pointee, a slice's array or a map.
func defaultValue(d any) reflect.Value {
	return cloneValue(reflect.ValueOf(d))
}

// holdsRefs reports whether a value of type t shares memory with its copy:
// whether cloneValue has anything to copy in it.
func holdsRefs(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface:
		return true
	case reflect.Array:
		return t.Len() > 0 && holdsRefs(t.Elem())
	case reflect.Struct:
		for i := range t.NumField() {
			if f := t.Field(i); f.IsExported() && holdsRefs(f.Type) {
				return true
			}
		}
	}
	return false
}

// cloneValue returns a deep copy of v, through pointers, slices, maps, arrays,
// interfaces and exported struct fields; v must not be cyclic. A nil pointer,
// slice, map or interface stays nil. A struct's unexported fields are copied
// with the struct, shallowly; anything else is copied as is.
func cloneValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		r := reflect.New(v.Type()).Elem()
		r.Set(cloneValue(v.Elem()))
		return r
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		p := reflect.New(v.Type().Elem())
		p.Elem().Set(cloneValue(v.Elem()))
		return p
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		s := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		if v.Type().Elem().Kind() == reflect.Uint8 {
			reflect.Copy(s, v)
			return s
		}
		for i := range v.Len() {
			s.Index(i).Set(cloneValue(v.Index(i)))
		}
		return s
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		m := reflect.MakeMapWithSize(v.Type(), v.Len())
		for it := v.MapRange(); it.Next(); {
			m.SetMapIndex(it.Key(), cloneValue(it.Value()))
		}
		return m
	case reflect.Array:
		a := reflect.New(v.Type()).Elem()
		for i := range v.Len() {
			a.Index(i).Set(cloneValue(v.Index(i)))
		}
		return a
	case reflect.Struct:
		s := reflect.New(v.Type()).Elem()
		s.Set(v)
		for i := range v.NumField() {
			if f := s.Field(i); f.CanSet() { // exported
				f.Set(cloneValue(v.Field(i)))
			}
		}
		return s
	}
	return v
}

// ZeroInt reports whether ptr points to an integer holding zero.
func ZeroInt(ptr any) bool {
	v := reflect.ValueOf(ptr).Elem()
	return (v.CanInt() || v.CanUint()) && v.IsZero()
}

// InsertColumns lists the columns an INSERT writes: every column but skip.
func InsertColumns(in *EntityInfo, skip int) []int {
	cols := make([]int, 0, len(in.Columns))
	for i := range in.Columns {
		if i != skip {
			cols = append(cols, i)
		}
	}
	return cols
}

// rowTenant is what an insert fills or checks its rows' tenant with: the
// context's tenant converted to the tenant column's type (tenantFor), and the
// WithAllTenants reason.
type rowTenant struct {
	id  any
	all string
}

// InsertTenant returns the rowTenant of an insert of entity in, converted
// once for all its rows.
func InsertTenant(ctx context.Context, in *EntityInfo) (rowTenant, error) {
	if in.Tenant < 0 {
		return rowTenant{}, nil
	}
	id, all := TenantFrom(ctx)
	if id != nil {
		var err error
		if id, err = tenantFor(in, id); err != nil {
			return rowTenant{}, err
		}
	}
	return rowTenant{id, all}, nil
}

// PrepareOne readies the one row at p, of entity in, as PrepareRow does.
func PrepareOne(ctx context.Context, in *EntityInfo, p unsafe.Pointer) error {
	tn, err := InsertTenant(ctx, in)
	if err != nil {
		return err
	}
	return PrepareRow(in, p, tn)
}

// PrepareRow readies the row at p, of entity in, to be inserted, once its
// defaults are filled: it fills or checks the tenant column with tn, from
// InsertTenant. The validators, then the policy's Insert (InsertAllowed), run
// after it. It changes the caller's entity, and the changes stay when the
// insert then fails.
func PrepareRow(in *EntityInfo, p unsafe.Pointer, tn rowTenant) error {
	if in.Tenant >= 0 {
		return fillTenant(in, p, tn.id, tn.all)
	}
	return nil
}

// InsertAllowed asks the policy's Insert about the row of in at p, prepared
// and validated, unless WithoutPolicy.
func InsertAllowed(ctx context.Context, in *EntityInfo, p unsafe.Pointer) error {
	if pol := in.policy; pol != nil && pol.Insert != nil && PolicyBypassReason(ctx) == "" {
		if err := pol.Insert(ctx, p); err != nil {
			return &PolicyError{Entity: in.Name, Op: OpInsert, Err: err}
		}
	}
	return nil
}

// tenantField returns the tenant field of the row of in at p.
func tenantField(in *EntityInfo, p unsafe.Pointer) reflect.Value {
	c := &in.Columns[in.Tenant]
	return reflect.NewAt(GoType(*c), unsafe.Add(p, c.off)).Elem()
}

// fillTenant sets the row's zero tenant field to the context's tenant id,
// which InsertTenant converted to the field's type, and checks one already
// set. Under WithAllTenants (all) the row must name its tenant.
func fillTenant(in *EntityInfo, p unsafe.Pointer, id any, all string) error {
	if id != nil {
		if v := tenantField(in, p); v.IsZero() {
			v.Set(reflect.ValueOf(id))
			return nil
		}
	}
	return checkRowTenant(in, p, id, all, "the row has")
}

// checkRowTenant checks the row's tenant field against the context's tenant id,
// converted to the field's type: under WithTenant the field must hold id, under
// WithAllTenants (all) it must not be zero, and with neither the row has no
// tenant. who says what put the value in the field.
func checkRowTenant(in *EntityInfo, p unsafe.Pointer, id any, all, who string) error {
	col := in.Columns[in.Tenant].Name
	v := tenantField(in, p)
	switch {
	case all != "":
		if v.IsZero() {
			return fmt.Errorf("velox: %s: %w: under WithAllTenants the row names its tenant, and its %s is zero", in.Name, ErrNoTenant, col)
		}
		return nil
	case id == nil:
		return fmt.Errorf("velox: %s: %w", in.Name, ErrNoTenant)
	case !v.Equal(reflect.ValueOf(id)):
		return fmt.Errorf("velox: %s: %w: %s %s %v, the tenant is %v", in.Name, ErrTenantMismatch, who, col, v, id)
	}
	return nil
}

// errHiddenRow is why an upsert that affected fewer rows than it sent is
// denied: the policy's condition kept a conflicting row from being updated.
var errHiddenRow = errors.New("the existing row is hidden from this upsert")

// ErrUnknownAffected is why an upsert conditioned on the policy is denied
// when the driver did not report its count (-1).
var ErrUnknownAffected = errors.New("the driver did not report the rows the upsert affected, so a hidden row cannot be told apart")

// CheckUpserted denies an upsert whose update branch the scope conditioned on
// the policy, when it affected fewer rows than it sent: a conflicting row the
// policy hides was neither inserted nor updated. Without the count (-1) a
// hidden row cannot be told apart, so it fails closed.
func CheckUpserted(st *Statement, affected int64) error {
	switch {
	case !st.upsertPolicy:
		return nil
	case affected < 0:
		return &PolicyError{Entity: st.Entity.Name, Op: OpUpdate, Err: ErrUnknownAffected}
	case affected < int64(st.upsertRows):
		return &PolicyError{Entity: st.Entity.Name, Op: OpUpdate, Err: errHiddenRow}
	}
	return nil
}

func ErrNothingToInsert(in *EntityInfo) error {
	return fmt.Errorf("velox: %s: nothing to insert: the only column is the generated key", in.Name)
}

// FieldAddr is a pointer to the field of column col of the row of in at p,
// as Meta's Addr returns it.
func FieldAddr(in *EntityInfo, p unsafe.Pointer, col int) any {
	c := &in.Columns[col]
	return reflect.NewAt(GoType(*c), unsafe.Add(p, c.off)).Interface()
}

// SetKey stores id in the integer field ptr points to, column gen of in. It
// is not generic for the reason given on FillDefault.
func SetKey(in *EntityInfo, gen int, ptr any, id int64) error {
	v := reflect.ValueOf(ptr).Elem()
	if v.CanInt() {
		if v.OverflowInt(id) {
			return fmt.Errorf("velox: %s: LastInsertId %d overflows the key field %s", in.Name, id, in.Columns[gen].Field)
		}
		v.SetInt(id)
		return nil
	}
	if id < 0 || v.OverflowUint(uint64(id)) {
		return fmt.Errorf("velox: %s: LastInsertId %d overflows the key field %s", in.Name, id, in.Columns[gen].Field)
	}
	v.SetUint(uint64(id))
	return nil
}

// PerStatement is the most rows of n columns of entity in that one statement
// holds on dialect d: half as many on the join entity of a bidirectional
// edge, which also writes each row's mirror (MirrorRows).
func PerStatement(d Dialect, in *EntityInfo, n int) int {
	if in.bidi {
		n *= 2
	}
	return max(1, MaxParams[d]/n)
}

// MirrorRows gives each row of st, an insert on the join entity of a
// bidirectional edge, its mirror after it: (b, a) for (a, b), unless a and b
// are equal, as a self-link is one row. ent's addM2MEdges writes the mirror
// the same way, as integrity maintenance: the entity's hooks and
// Policy.Insert, which see the caller's rows, never judge it. A row holds the
// two columns in order, as InsertColumns lists them for an entity whose key
// is not generated. It is not generic, and the generic callers only call it,
// so that the entities that are no join entity compile none of it.
func MirrorRows(st *Statement) {
	rows := make([][]any, 0, 2*len(st.Rows))
	for _, r := range st.Rows {
		rows = append(rows, r)
		if !SelfLink(r[0], r[1]) {
			rows = append(rows, []any{r[1], r[0]})
		}
	}
	st.Rows = rows
}

// SelfLink reports whether the key values a and b, of one Go type, are equal:
// bytes.Equal for a byte slice of any named type, == otherwise. checkBidi and
// checkEdgeBidi refuse, through linkKey, every other key type that == could
// panic on: an interface, and a struct or an array that holds one.
func SelfLink(a, b any) bool {
	if x, ok := a.([]byte); ok {
		y, _ := b.([]byte)
		return bytes.Equal(x, y)
	}
	if v := reflect.ValueOf(a); v.Kind() == reflect.Slice {
		return bytes.Equal(v.Bytes(), reflect.ValueOf(b).Bytes())
	}
	return a == b
}

// ErrBidiUpdate is the error of an update, a Save or an upsert that updates,
// on the join entity of a bidirectional edge: it would change one row of a
// link.
func ErrBidiUpdate(in *EntityInfo) error {
	return fmt.Errorf("velox: %s: the join entity of a bidirectional edge cannot be updated; delete the link and insert it again", in.Name)
}
