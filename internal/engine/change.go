package engine

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
	"unsafe"
)

// rowKey is the primary key of the row at p, a plain value per key column.
func rowKey(in *EntityInfo, p unsafe.Pointer) []any {
	key := make([]any, len(in.PrimaryKey))
	for i, k := range in.PrimaryKey {
		key[i] = loadColumn(&in.Columns[k], p)
	}
	return key
}

// DiffRows compares the rows at before and after of entity in, column by
// column, and returns the columns whose values differ (EqualValues), in
// column order. A nil row holds NULL in every column.
func DiffRows(in *EntityInfo, before, after unsafe.Pointer) []FieldChange {
	if in == nil {
		return nil
	}
	var out []FieldChange
	for i := range in.Columns {
		c := &in.Columns[i]
		var a, b any
		if before != nil {
			a = loadColumn(c, before)
		}
		if after != nil {
			b = loadColumn(c, after)
		}
		if !EqualValues(a, b) {
			out = append(out, FieldChange{Name: c.Name, Old: a, New: b})
		}
	}
	return out
}

// EqualValues reports whether a and b, plain column values, are equal by
// value. Two nils are equal, and nil equals no other value. Values of a type
// with a method Equal(T) bool, as time.Time and the decimal types have, are
// compared with it. Byte slices, json.RawMessage among them, are compared
// with bytes.Equal. Anything else is compared with reflect.DeepEqual.
func EqualValues(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	t := va.Type()
	if t != vb.Type() {
		return false
	}
	if m, ok := t.MethodByName("Equal"); ok && m.Type.NumIn() == 2 && m.Type.In(1) == t &&
		m.Type.NumOut() == 1 && m.Type.Out(0).Kind() == reflect.Bool {
		return va.Method(m.Index).Call([]reflect.Value{vb})[0].Bool()
	}
	if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
		return bytes.Equal(va.Bytes(), vb.Bytes())
	}
	return reflect.DeepEqual(a, b)
}

// trackedWrite sends st, an update or a delete through db, a recording
// handle, and reports the rows it changes (emit). In one transaction (a
// savepoint in db's, else one of its own: recordedTx) it reads the key and the columns cols of the
// rows st's conditions match, under st's scope (a Writable read), locked
// where the dialect can, in key order; sends st narrowed to those keys, a
// chunk at a time; and, for an update, reads the new values: RETURNING on
// PostgreSQL and SQLite, a SELECT by key on MySQL. It sets mu.Affected.
//
// On PostgreSQL 18 and later (returnsOld) st is sent as it is, once, and
// returns its rows before and after it (tracker.both). Outside a
// transaction, a write that pins one row by its key (pinsOneRow), on a DB
// with no Rewrite hook (which could widen it), sends no BEGIN; any other
// runs in a transaction of velox's own, so that a count of rows over
// WithMaxRows rolls it back.
func trackedWrite(ctx context.Context, db *DB, mu *MutationState, cols []int, st *Statement) error {
	in := st.Entity
	if len(in.PrimaryKey) == 0 {
		return fmt.Errorf("velox: %s: recording needs a primary key to find the rows again", in.Name)
	}
	for _, a := range st.Set {
		if a.Op != AssignSet && slices.Contains(in.PrimaryKey, a.Column) {
			return fmt.Errorf("velox: %s: recording cannot follow a row whose key %s the update changes by Add or Sub", in.Name, in.Columns[a.Column].Name)
		}
	}
	tw := tracker{in: in, t: InfoTable(in), cols: cols, old: returnsOld(ctx, db)}
	tw.read = keyAnd(in, tw.cols)
	if tw.old && !db.InTx && len(db.Cfg.Rewrites) == 0 && pinsOneRow(in, st) {
		// One statement on one row: it commits on its own, as it would
		// through a handle that does not record, and the sink runs after.
		mu.Affected = 0
		rows, err := tw.run(ctx, db, mu, st)
		if err != nil {
			return err
		}
		return emit(ctx, db, rows)
	}
	return versionedTx(ctx, db, &tw.old, func(ctx context.Context, tx *DB) error {
		mu.Affected = 0
		rows, err := tw.run(ctx, tx, mu, st)
		if err != nil {
			return err
		}
		return emit(ctx, tx, rows)
	})
}

// everyColumn is the index of each of in's columns, in order.
func everyColumn(in *EntityInfo) []int {
	cols := make([]int, len(in.Columns))
	for i := range cols {
		cols[i] = i
	}
	return cols
}

// keyAnd is in's primary-key columns, then those of cols not in it.
func keyAnd(in *EntityInfo, cols []int) []int {
	out := slices.Clone(in.PrimaryKey)
	for _, c := range cols {
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

// tracker is one tracked write.
type tracker struct {
	in   *EntityInfo
	t    Table
	cols []int // the columns compared
	read []int // the columns read: the key, then the compared ones
	// old says the write reads its rows before and after it in one
	// statement, RETURNING old and new (PostgreSQL 18: returnsOld).
	old bool
}

// pinsOneRow reports whether st, an update or a delete, matches one row at
// most: not All, and its conditions hold, at their top level,
// <column> = v for every primary-key column, v not nil.
func pinsOneRow(in *EntityInfo, st *Statement) bool {
	if st.AllRows || len(in.PrimaryKey) == 0 {
		return false
	}
	for _, k := range in.PrimaryKey {
		if _, ok := pinnedValue(int32(k), st.Where); !ok {
			return false
		}
	}
	return true
}

// run is trackedWrite's work on tx.
func (tw *tracker) run(ctx context.Context, tx *DB, mu *MutationState, st *Statement) ([]ChangeRow, error) {
	if tw.old {
		return tw.both(ctx, tx, mu, st)
	}
	order := make([]OrderTerm, len(tw.in.PrimaryKey))
	for i, k := range tw.in.PrimaryKey {
		order[i] = OrderTerm{Column: k}
	}
	q := QueryTable(tx, tw.t).Where(st.Where...).Columns(tw.read...).OrderBy(order...).Writable().AllowUnbounded()
	if tx.D != SQLite {
		q = q.ForUpdate()
	}
	olds, err := q.All(ctx)
	if errors.Is(err, ErrTooManyRows) {
		return nil, fmt.Errorf("velox: %s: a recorded %s matches more than %d rows, the cap WithMaxRows sets on its read of them: %w", tw.in.Name, st.Op, tx.Cfg.MaxRows, ErrTooManyRows)
	}
	if err != nil {
		return nil, err
	}
	if olds.n == 0 {
		return nil, nil // nothing matches: nothing to send
	}
	keys := make([][]any, olds.n)
	for i := range keys {
		keys[i] = rowKey(tw.in, olds.at(i))
	}
	news := make([]unsafe.Pointer, olds.n) // an update's rows as written, by old row
	returning := st.Op == OpUpdate && tx.D != MySQL
	at := newKeys(tw.in, st, keys) // where each row is after the write
	per := max(1, (keysPer(tx, tw.in, st.Where)-len(st.Set))/len(tw.in.PrimaryKey))
	err = byChunks(len(keys), per, func(lo, hi int) error {
		cp := *st
		cp.Where = append(slices.Clip(st.Where), keysIn(tw.t, keys[lo:hi]))
		cp.InTx = true
		if !returning {
			res, err := ExecWrite(ctx, tx, &cp)
			if err != nil {
				return err
			}
			mu.Affected += res.Affected
			return nil
		}
		cp.Returning = tw.read
		n, err := tw.returned(ctx, tx, &cp, at, lo, hi, news)
		mu.Affected += n
		return err
	})
	if err != nil {
		return nil, err
	}
	if st.Op == OpUpdate && !returning {
		if err := tw.reread(ctx, tx, at, news); err != nil {
			return nil, err
		}
	}
	return tw.changes(st, olds, news), nil
}

// newKeys is each row's key after st, which held keys before it: a key
// column st assigns holds the value assigned.
func newKeys(in *EntityInfo, st *Statement, keys [][]any) [][]any {
	var set []Assignment
	for _, a := range st.Set {
		if slices.Contains(in.PrimaryKey, a.Column) {
			set = append(set, a)
		}
	}
	if len(set) == 0 {
		return keys
	}
	out := make([][]any, len(keys))
	for r, k := range keys {
		k = slices.Clone(k)
		for _, a := range set {
			k[slices.Index(in.PrimaryKey, a.Column)], _ = keyOf(a.Value)
		}
		out[r] = k
	}
	return out
}

// keysIn is the condition that a row's primary key is one of keys: key IN
// (…) for one key column, (a = ? AND b = ?) OR … for several.
func keysIn(t Table, keys [][]any) Predicate {
	return valuesIn(t, t.Info().PrimaryKey, keys)
}

// valuesIn is the condition that a row's columns cols hold one of keys, a
// value per column each: col IN (…) for one column, (a = ? AND b = ?) OR …
// for several.
func valuesIn(t Table, cols []int, keys [][]any) Predicate {
	if len(cols) == 1 {
		vs := make([]any, len(keys))
		for i, k := range keys {
			vs[i] = k[0]
		}
		return t.Pred(cols[0], PredIn, vs)
	}
	ors := make([]Node, len(keys))
	for i, k := range keys {
		ands := make([]Node, len(k))
		for j, col := range cols {
			ands[j] = PredNode(t.Pred(col, PredEQ, k[j]))
		}
		ors[i] = Group(OpAnd, ands)
	}
	if len(ors) == 1 {
		return NewPredicate(ors[0])
	}
	return NewPredicate(Group(OpOr, ors))
}

// keyString is a key as a map key.
func keyString(k []any) string {
	var b strings.Builder
	for i, v := range k {
		if i > 0 {
			b.WriteByte(0)
		}
		fmt.Fprint(&b, v)
	}
	return b.String()
}

// returned sends st, an update with RETURNING of tw.read, and keeps each row
// it returns in news, at the place of the old row whose new key (at, rows lo
// to hi) it holds. It returns the rows that came back.
func (tw *tracker) returned(ctx context.Context, tx *DB, st *Statement, at [][]any, lo, hi int, news []unsafe.Pointer) (int64, error) {
	bumpOnce(st)
	ts := tw.t.state()
	place := make(map[string]int, hi-lo)
	for i := lo; i < hi; i++ {
		place[keyString(at[i])] = i
	}
	var n int64
	read := func(r Rows) error {
		n = 0 // anew, should an Around hook retry
		dst := make([]any, len(tw.read))
		for r.Next() {
			p := ts.newRow()
			bindTableRow(ts, tw.read, dst, p)
			if err := r.Scan(dst...); err != nil {
				return err
			}
			n++
			if i, ok := place[keyString(rowKey(tw.in, p))]; ok {
				news[i] = p
			}
		}
		return nil
	}
	var err error
	if tx.Hooked() {
		err = tx.Run(ctx, st, read, nil)
	} else {
		err = tx.Plain(ctx, st, read, nil)
	}
	return n, err
}

// reread reads the rows an update wrote by their new keys, at, a chunk at a
// time, and keeps each in news at its old row's place. The read adds no row
// condition: the update was scoped, and may have moved the rows out of view.
func (tw *tracker) reread(ctx context.Context, tx *DB, at [][]any, news []unsafe.Pointer) error {
	place := make(map[string]int, len(at))
	for i, k := range at {
		place[keyString(k)] = i
	}
	return byChunks(len(at), max(1, keysPer(tx, tw.in, nil)/len(tw.in.PrimaryKey)), func(lo, hi int) error {
		q := QueryTable(tx, tw.t).Where(keysIn(tw.t, at[lo:hi])).Columns(tw.read...).AllowUnbounded()
		q.st.unscoped = true
		rows, err := q.All(ctx)
		if err != nil {
			return err
		}
		for j := range rows.n {
			p := rows.at(j)
			if i, ok := place[keyString(rowKey(tw.in, p))]; ok {
				news[i] = p
			}
		}
		return nil
	})
}

// changes is a ChangeRow per old row of st: the row before the write and,
// for an update, the row after it. An update's row that no RETURNING or
// re-read found is left out. A soft delete's (MarkSoftDelete) rows are
// deletes, Soft, with both rows.
func (tw *tracker) changes(st *Statement, olds TableRows, news []unsafe.Pointer) []ChangeRow {
	op, soft := st.Op, st.Op == OpUpdate && st.softDelete
	out := make([]ChangeRow, 0, olds.n)
	for i := range olds.n {
		if st.Op == OpUpdate && news[i] == nil {
			continue // a Rewrite or Around hook kept the row from the write
		}
		row := ChangeRow{Op: op, In: tw.in, Old: olds.at(i), New: news[i]}
		if soft {
			row.Op, row.Soft = OpDelete, true
		}
		out = append(out, row)
	}
	return out
}

// both sends st, an update or a delete on PostgreSQL 18 or later, on tx as
// it is, once, with RETURNING old.<read>, new.<read> (a delete: old alone),
// and returns a ChangeRow per row it returns, in key order: the row before
// the write and, for an update, after it. The values are those the write
// itself saw, so no read comes first and no row can change between the
// two. More rows than WithMaxRows fail the write, which trackedWrite runs
// in a transaction when it may match more than one row. It sets
// mu.Affected to the rows returned.
func (tw *tracker) both(ctx context.Context, tx *DB, mu *MutationState, st *Statement) ([]ChangeRow, error) {
	cp := *st
	cp.InTx = st.InTx || tx.InTx
	cp.Returning, cp.returnOld, cp.recorded = tw.read, true, true
	if cp.Op == OpUpdate {
		bumpOnce(&cp)
	}
	ts := tw.t.state()
	update := cp.Op == OpUpdate
	var out []ChangeRow
	read := func(r Rows) error {
		out = out[:0] // anew, should an Around hook retry
		n := len(tw.read)
		width := n
		if update {
			width = 2 * n
		}
		dst := make([]any, width)
		for r.Next() {
			row := ChangeRow{Op: cp.Op, In: tw.in, Old: ts.newRow()}
			bindTableRow(ts, tw.read, dst[:n], row.Old)
			if update {
				row.New = ts.newRow()
				bindTableRow(ts, tw.read, dst[n:], row.New)
			}
			if err := r.Scan(dst...); err != nil {
				return err
			}
			if update && st.softDelete {
				row.Op, row.Soft = OpDelete, true
			}
			out = append(out, row)
		}
		return nil
	}
	var err error
	if tx.Hooked() {
		err = tx.Run(ctx, &cp, read, nil)
	} else {
		err = tx.Plain(ctx, &cp, read, nil)
	}
	if err != nil {
		return nil, err
	}
	mu.Affected += int64(len(out))
	if m := tx.Cfg.MaxRows; m > 0 && len(out) > m {
		return nil, fmt.Errorf("velox: %s: a recorded %s wrote %d rows, more than %d, the cap WithMaxRows sets on the rows it records; the write is rolled back: %w", tw.in.Name, st.Op, len(out), m, ErrTooManyRows)
	}
	// In key order, as the locked read reports them; the database returns
	// them in the order it found them.
	slices.SortStableFunc(out, func(a, b ChangeRow) int {
		return compareKeys(rowKey(tw.in, a.Old), rowKey(tw.in, b.Old))
	})
	return out, nil
}

// compareKeys orders two primary keys column by column: numbers, strings,
// times and byte strings (a UUID's array too, as PostgreSQL orders it) by
// value, anything else by its printed form. Strings compare by byte, which
// a key's collation may not.
func compareKeys(a, b []any) int {
	for i := range min(len(a), len(b)) {
		if c := compareValue(a[i], b[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a), len(b))
}

// compareValue orders two plain column values for compareKeys.
func compareValue(a, b any) int {
	if ta, ok := a.(time.Time); ok {
		if tb, ok := b.(time.Time); ok {
			return ta.Compare(tb)
		}
	}
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	if va.IsValid() && vb.IsValid() && va.Kind() == vb.Kind() {
		switch va.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return cmp.Compare(va.Int(), vb.Int())
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return cmp.Compare(va.Uint(), vb.Uint())
		case reflect.Float32, reflect.Float64:
			return cmp.Compare(va.Float(), vb.Float())
		case reflect.String:
			return strings.Compare(va.String(), vb.String())
		case reflect.Slice, reflect.Array:
			if va.Type().Elem().Kind() == reflect.Uint8 && va.Type() == vb.Type() {
				return bytes.Compare(byteString(va), byteString(vb))
			}
		}
	}
	return strings.Compare(fmt.Sprint(a), fmt.Sprint(b))
}

// byteString is v, a byte slice or array, as a []byte.
func byteString(v reflect.Value) []byte {
	if v.Kind() == reflect.Slice {
		return v.Bytes()
	}
	out := make([]byte, v.Len())
	reflect.Copy(reflect.ValueOf(out), v)
	return out
}
