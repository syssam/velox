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
// bidirectional one-to-one edge, and one whose update branch writes such a key.
// It is not generic, so that every entity's InsertQuery does not compile it.
func CheckConflict(in *EntityInfo, cf *ConflictClause, one unsafe.Pointer) error {
	if len(cf.Columns) == 0 {
		return fmt.Errorf("velox: %s: an upsert needs OnConflict with the columns of a unique key", in.Name)
	}
	if one != nil && len(in.bidiKeys) > 0 {
		if err := refuseBidiInsert(in, one, false); err != nil {
			return err
		}
	}
	if err := refuseBidiUpsert(in, cf); err != nil {
		return err
	}
	if len(cf.Update) > 0 && in.bidi {
		return errBidiUpdate(in)
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
		for f := range t.Fields() {
			if f.IsExported() && holdsRefs(f.Type) {
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

// InsertAllowed asks the policy's Insert about the row of in at p, prepared
// and validated, unless WithoutPolicy.
func InsertAllowed(ctx context.Context, in *EntityInfo, p unsafe.Pointer) error {
	if pol := in.policy; pol != nil && pol.Insert != nil && PolicyBypassReason(ctx) == "" {
		if err := pol.Insert(ctx, p); err != nil {
			if m, ok := err.(*rowMisuseError); ok {
				return newStageError(in.Name, "policy", m.err)
			}
			return &PolicyError{Entity: in.Name, Op: OpInsert, Err: err}
		}
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

// ErrNothingToInsert is the error of an insert into in when its only column is
// the generated key.
func ErrNothingToInsert(in *EntityInfo) error {
	return fmt.Errorf("velox: %s: nothing to insert: the only column is the generated key", in.Name)
}

// fieldAddr is a pointer to the field of column col of the row of in at p,
// as Meta's addr returns it.
func fieldAddr(in *EntityInfo, p unsafe.Pointer, col int) any {
	c := &in.Columns[col]
	return reflect.NewAt(GoType(*c), unsafe.Add(p, c.off)).Interface()
}

// setKey stores id in the integer field ptr points to, column gen of in. It
// is not generic for the reason given on FillDefault.
func setKey(in *EntityInfo, gen int, ptr any, id int64) error {
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
		if !selfLink(r[0], r[1]) {
			rows = append(rows, []any{r[1], r[0]})
		}
	}
	st.Rows = rows
}

// selfLink reports whether the key values a and b, of one Go type, are equal:
// bytes.Equal for a byte slice of any named type, == otherwise. checkBidi and
// checkEdgeBidi refuse, through linkKey, every other key type that == could
// panic on: an interface, and a struct or an array that holds one.
func selfLink(a, b any) bool {
	if x, ok := a.([]byte); ok {
		y, _ := b.([]byte)
		return bytes.Equal(x, y)
	}
	if v := reflect.ValueOf(a); v.Kind() == reflect.Slice {
		return bytes.Equal(v.Bytes(), reflect.ValueOf(b).Bytes())
	}
	return a == b
}

// errBidiUpdate is the error of an update, a Save or an upsert that updates,
// on the join entity of a bidirectional edge: it would change one row of a
// link.
func errBidiUpdate(in *EntityInfo) error {
	return fmt.Errorf("velox: %s: the join entity of a bidirectional edge cannot be updated; delete the link and insert it again", in.Name)
}

// InsertRow sends st, the insert of the row at p that oneStatement made with
// its generated key column gen, and sets out.Affected. A row that sets the
// key of a bidirectional one-to-one edge (EdgeBidi) is inserted with the
// statement that links its partner (bidiInsert). It is not generic, so that
// it is compiled once; the transaction's closure works on copies, made only
// for such a row, so that no other insert allocates for it.
func InsertRow(ctx context.Context, db *DB, p unsafe.Pointer, st *Statement, gen int, out *Result) error {
	if in := st.Entity; len(infoBidiKeys(in)) > 0 {
		if keys, vs := bidiRow(in, p); len(keys) > 0 {
			cp := *st
			setStmtBidi(&cp, &bidiStep{Keys: keys, Vals: vs, Insert: true})
			var res Result
			err := bidiInsert(ctx, db, in, p, keys, vs, gen >= 0, cp.Conflict != nil, func(ctx context.Context, tx *DB) error {
				return sendOne(ctx, tx, p, &cp, gen, &res)
			})
			if err == nil {
				out.Affected = res.Affected
			}
			return err
		}
	}
	return sendOne(ctx, db, p, st, gen, out)
}

// sendOne sends st, the insert of the row at p with its generated key column
// gen, on db, reads that key back into the row, and sets out.Affected.
func sendOne(ctx context.Context, db *DB, p unsafe.Pointer, st *Statement, gen int, out *Result) error {
	in := st.Entity
	if gen < 0 {
		res, err := ExecWrite(ctx, db, st)
		if err != nil {
			return err
		}
		out.Affected = res.Affected
		return CheckUpserted(st, res.Affected)
	}
	if db.D != MySQL {
		var back int64 // the rows that came back
		read := func(r Rows) error {
			if !r.Next() {
				return nil
			}
			back = 1
			// A hook may have redirected Returning: then the row does not
			// hold the key.
			if len(st.Returning) == 1 && st.Returning[0] == gen {
				return r.Scan(ScanDst(in, gen, fieldAddr(in, p, gen)))
			}
			return nil
		}
		var err error
		if db.Hooked() {
			err = db.Run(ctx, st, read, nil)
		} else {
			err = db.Plain(ctx, st, read, nil)
		}
		if err != nil {
			return err
		}
		out.Affected = back
		if len(st.Returning) == 0 && !stmtUpsertPolicy(st) {
			// Without RETURNING (a hook dropped it), no row tells nothing; a
			// policy-conditioned upsert was rendered with it, as prepare
			// checks, so no row back is a hidden row.
			return nil
		}
		return CheckUpserted(st, back)
	}
	res, err := ExecWrite(ctx, db, st)
	if err != nil {
		return err
	}
	out.Affected = res.Affected
	// st.Conflict, not cf: a rewrite hook may have added or removed the clause.
	// Without a conflict clause a successful insert wrote its row, whether
	// the driver reported the count or not (-1).
	if st.Conflict == nil && res.HasLastID && res.Affected != 0 {
		return setKey(in, gen, fieldAddr(in, p, gen), res.LastID)
	}
	return nil
}

// ConditionedUpsert predicts, before the scope runs, whether it will give
// the update branch of an InsertAll of n rows of entity in the conditions of
// its interceptors or of its policy's Read or Write. A row those conditions hide fails the upsert once the
// statement has written the others, so such an InsertAll of several rows runs
// in a transaction, which the failure rolls back. The scope conditions an
// upsert that updates, on any dialect but MySQL (which refuses it), of an
// entity with a Read or Write policy, unless WithoutPolicy.
func ConditionedUpsert(ctx context.Context, db *DB, in *EntityInfo, cf *ConflictClause, n int) bool {
	return n > 1 && cf != nil && !cf.DoNothing && len(cf.Update) > 0 && db.D != MySQL &&
		(len(in.filters) > 0 || InfoPolicy(in) != nil && (InfoPolicy(in).Read != nil || InfoPolicy(in).Write != nil) && PolicyBypassReason(ctx) == "")
}
