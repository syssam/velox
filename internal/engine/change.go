package engine

import (
	"bytes"
	"cmp"
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"slices"
	"strings"
	"sync"
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
// column, and returns the columns whose values differ (equalColumn), in
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
		if !equalColumn(c, a, b) {
			out = append(out, FieldChange{Name: c.Name, Old: a, New: b})
		}
	}
	return out
}

// equalColumn reports whether a and b, plain values of column c, are equal:
// by number in a decimal column (ColumnDescriptor.Decimal), so that "1.50"
// and "1.5" of a decimal type held as text are equal (equalDecimal), and by
// EqualValues in any other.
func equalColumn(c *ColumnInfo, a, b any) bool {
	if c.decimal == [2]uint8{} {
		return EqualValues(a, b)
	}
	return equalDecimal(a, b)
}

// equalDecimal reports whether a and b, plain values of a decimal column,
// are the same number: each read as a big.Rat from the text its Value
// method gives (a string, []byte, int64 or float64), or else its String
// method's. Two nils are equal, and nil equals no other value. A value
// that reads as no number is compared by EqualValues.
func equalDecimal(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	x, okx := decimalRat(a)
	y, oky := decimalRat(b)
	if okx && oky {
		return x.Cmp(y) == 0
	}
	return EqualValues(a, b)
}

// decimalRat is v, a decimal's plain value, as a number (equalDecimal).
func decimalRat(v any) (*big.Rat, bool) {
	var s string
	switch t := v.(type) {
	case driver.Valuer:
		dv, err := t.Value()
		if err != nil {
			return nil, false
		}
		switch d := dv.(type) {
		case string:
			s = d
		case []byte:
			s = string(d)
		case int64:
			return new(big.Rat).SetInt64(d), true
		case float64:
			r := new(big.Rat).SetFloat64(d)
			return r, r != nil
		default:
			return nil, false
		}
	case fmt.Stringer:
		s = t.String()
	default:
		return nil, false
	}
	return new(big.Rat).SetString(strings.TrimSpace(s))
}

// EqualValues reports whether a and b, plain column values, are equal by
// value. Two nils are equal, and nil equals no other value. Values of a type
// with a method Equal(T) bool, as time.Time and the decimal types have, are
// compared with it. A float NaN equals NaN, by value and through a pointer:
// a column that held NaN and still does has not changed. Byte slices,
// json.RawMessage among them, are compared with bytes.Equal. Anything else
// is compared with reflect.DeepEqual.
func EqualValues(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if x, ok := a.(time.Time); ok {
		y, ok := b.(time.Time)
		return ok && x.Equal(y)
	}
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	t := va.Type()
	if t != vb.Type() {
		return false
	}
	if m := equalMethod(t); m >= 0 {
		return va.Method(m).Call([]reflect.Value{vb})[0].Bool()
	}
	switch k := t.Kind(); {
	case k == reflect.Float32 || k == reflect.Float64:
		return sameFloat(va.Float(), vb.Float())
	case k == reflect.Pointer && (t.Elem().Kind() == reflect.Float32 || t.Elem().Kind() == reflect.Float64):
		if va.IsNil() || vb.IsNil() {
			return va.IsNil() && vb.IsNil()
		}
		return sameFloat(va.Elem().Float(), vb.Elem().Float())
	case k == reflect.Slice && t.Elem().Kind() == reflect.Uint8:
		return bytes.Equal(va.Bytes(), vb.Bytes())
	}
	return reflect.DeepEqual(a, b)
}

// sameFloat reports whether x and y are equal, or both NaN.
func sameFloat(x, y float64) bool { return x == y || x != x && y != y }

// equalMethods caches, by type, the index of its method Equal(T) bool, or -1
// for a type without one (equalMethod).
var equalMethods sync.Map // reflect.Type → int

// equalMethod is the index of t's method Equal(T) bool, -1 when it has none.
func equalMethod(t reflect.Type) int {
	if m, ok := equalMethods.Load(t); ok {
		return m.(int)
	}
	i := -1
	if m, ok := t.MethodByName("Equal"); ok && m.Type.NumIn() == 2 && m.Type.In(1) == t &&
		m.Type.NumOut() == 1 && m.Type.Out(0).Kind() == reflect.Bool {
		i = m.Index
	}
	equalMethods.Store(t, i)
	return i
}

// trackedWrite sends st, an update or a delete through db, a recording
// handle, and reports the rows it changes (emit). In one transaction (a
// savepoint in db's, else one of its own: recordedTx) it reads the key and the columns cols of the
// rows st's conditions match, under st's scope (a Writable read), locked
// where the dialect can, in key order; sends st narrowed to those keys, a
// chunk at a time; and, for an update, reads the new values: RETURNING on
// PostgreSQL and SQLite, a SELECT by key on MySQL. A delete returns the keys
// it deleted on PostgreSQL and SQLite, and on MySQL its count of rows must
// match the chunk's, so that a row a hook or a trigger kept is not reported
// as deleted. It sets mu.Affected.
//
// On PostgreSQL 18 and later (returnsOld) st is sent as it is, once, and
// returns its rows before and after it (tracker.both), in the same
// transaction, so that a count of rows over WithMaxRows, or a sink's error,
// rolls it back.
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
	return versionedTx(ctx, db, &tw.old, func(ctx context.Context, tx *DB) error {
		mu.Affected = 0
		rows, err := tw.run(ctx, tx, mu, st)
		if err != nil {
			return err
		}
		return emit(tx, rows)
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
	// news holds, by old row, an update's row as written, or a delete's key
	// as deleted: a row the write left alone (a Rewrite or Around hook, a
	// trigger) stays nil and is not reported.
	news := make([]unsafe.Pointer, olds.n)
	returning := tx.D != MySQL
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
			if st.Op == OpDelete {
				// MySQL returns no keys: its count must account for every
				// row, or recording cannot tell which it deleted.
				if res.Affected != int64(hi-lo) {
					return fmt.Errorf("velox: %s: a recorded delete of %d locked rows deleted %d by MySQL's count: a hook or a trigger kept rows from it, and recording cannot tell which; the write is rolled back", tw.in.Name, hi-lo, res.Affected)
				}
				for i := lo; i < hi; i++ {
					news[i] = olds.at(i)
				}
			}
			return nil
		}
		cp.Returning = tw.read
		if st.Op == OpDelete {
			// The keys of the rows the database deleted, to report those alone.
			cp.Returning, cp.recorded = tw.in.PrimaryKey, true
		}
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

// keyString is a key as a map key. A time is written by its instant
// (placeKey), so that a key held in another zone, or with a monotonic
// reading, matches the UTC value a database returns.
func keyString(k []any) string {
	var b strings.Builder
	for i, v := range k {
		if i > 0 {
			b.WriteByte(0)
		}
		fmt.Fprint(&b, placeKey(v))
	}
	return b.String()
}

// sendRead sends st on db and reads its rows with read: through the
// pipeline's hooks when db has any.
func sendRead(ctx context.Context, db *DB, st *Statement, read func(Rows) error) error {
	if db.Hooked() {
		return db.Run(ctx, st, read, nil)
	}
	return db.Plain(ctx, st, read, nil)
}

// returnedRows sends st on db and scans each row its RETURNING returns into
// new rows of t, the table of st's entity: the columns cols into one, or,
// with pair (PostgreSQL 18's RETURNING old.<cols>, new.<cols>), into a row
// before the write and again into a row after it. With inserts (an upsert's, which
// pair), the row before may be none: its NULLs leave zero values, and a row
// before whose key is NULL in every column, an inserted row's, is nil. done
// gets the rows in the order they came back (after is nil unless pair); it
// runs inside the read, so an Around hook that retries runs it anew. Every
// recorded write reads what it wrote back through it: an update's and a
// delete's chunk (tracker.returned), PostgreSQL 18's old and new
// (tracker.both, returningBoth), and an upsert's every column
// (conflictRead.returning).
func returnedRows(ctx context.Context, db *DB, st *Statement, t Table, cols []int, pair, inserts bool, done func(rows, after []unsafe.Pointer) error) error {
	in := st.Entity
	ts := t.state()
	n := len(cols)
	width := n
	if pair {
		width = 2 * n
	}
	return sendRead(ctx, db, st, func(r Rows) error {
		var rows, after []unsafe.Pointer
		dst := make([]any, width)
		for r.Next() {
			p := ts.newRow()
			bindTableRow(ts, cols, dst[:n], p)
			var keyNulls, nulls int // the row before's NULLs: an inserted row's are all
			if inserts {
				for i, c := range cols {
					cnt := &nulls
					if slices.Contains(in.PrimaryKey, c) {
						cnt = &keyNulls
					}
					dst[i] = &NullProbe{Dst: dst[i], Nulls: cnt}
				}
			}
			var q unsafe.Pointer
			if pair {
				q = ts.newRow()
				bindTableRow(ts, cols, dst[n:], q)
			}
			if err := r.Scan(dst...); err != nil {
				return err
			}
			if inserts && keyNulls == len(in.PrimaryKey) {
				p = nil
			}
			rows = append(rows, p)
			if pair {
				after = append(after, q)
			}
		}
		return done(rows, after)
	})
}

// returned sends st, an update or a delete with RETURNING (an update's of
// tw.read, a delete's of the key), and keeps each row it returns in news, at
// the place of the old row whose new key (at, rows lo to hi) it holds. A row
// that holds none, as when the database stores an assigned key in another
// form (a truncated time, a padded CHAR), takes the place of the chunk's one
// row, or fails the write: an unreported write is the worst failure of a
// recording. It returns the rows that came back.
func (tw *tracker) returned(ctx context.Context, tx *DB, st *Statement, at [][]any, lo, hi int, news []unsafe.Pointer) (int64, error) {
	if st.Op == OpUpdate {
		bumpOnce(st)
	}
	place := make(map[string]int, hi-lo)
	for i := lo; i < hi; i++ {
		place[keyString(at[i])] = i
	}
	var n int64
	err := returnedRows(ctx, tx, st, tw.t, st.Returning, false, false, func(rows, _ []unsafe.Pointer) error {
		n = int64(len(rows))
		clear(news[lo:hi])
		var stray []unsafe.Pointer
		for _, p := range rows {
			if i, ok := place[keyString(rowKey(tw.in, p))]; ok {
				news[i] = p
			} else {
				stray = append(stray, p)
			}
		}
		return tw.placeStray(st.Op, stray, news[lo:hi])
	})
	return n, err
}

// placeStray places stray, the rows a write returned or a re-read found
// whose key matched no row of the chunk news holds: the chunk's one row
// takes the one stray, as the write was narrowed to its key; any other
// stray fails the write.
func (tw *tracker) placeStray(op Op, stray []unsafe.Pointer, news []unsafe.Pointer) error {
	switch {
	case len(stray) == 0:
		return nil
	case len(stray) == 1 && len(news) == 1 && news[0] == nil:
		news[0] = stray[0]
		return nil
	}
	return fmt.Errorf("velox: %s: a recorded %s found %d rows whose key matches none it was sent for (the database may store an assigned key in another form than the value given); the write is rolled back", tw.in.Name, op, len(stray))
}

// reread reads the rows an update wrote by their new keys, at, a chunk at a
// time, and keeps each in news at its old row's place (a row whose key
// matches none: placeStray). The read adds no row condition: the update was
// scoped, and may have moved the rows out of view. It must find every row
// the locked read found: one it misses, as when MySQL stores an assigned
// key in another form, fails the write rather than go unreported.
func (tw *tracker) reread(ctx context.Context, tx *DB, at [][]any, news []unsafe.Pointer) error {
	place := make(map[string]int, len(at))
	for i, k := range at {
		place[keyString(k)] = i
	}
	if err := tw.rereadChunks(ctx, tx, at, place, news); err != nil {
		return err
	}
	found := 0
	for _, p := range news {
		if p != nil {
			found++
		}
	}
	if found != len(news) {
		return fmt.Errorf("velox: %s: a recorded update found %d of the %d rows it updated when it read them again by their keys (the database may store an assigned key in another form than the value given); the write is rolled back", tw.in.Name, found, len(news))
	}
	return nil
}

// rereadChunks is reread's reads, a chunk at a time; place is the index of
// each new key in at.
func (tw *tracker) rereadChunks(ctx context.Context, tx *DB, at [][]any, place map[string]int, news []unsafe.Pointer) error {
	return byChunks(len(at), max(1, keysPer(tx, tw.in, nil)/len(tw.in.PrimaryKey)), func(lo, hi int) error {
		q := QueryTable(tx, tw.t).Where(keysIn(tw.t, at[lo:hi])).Columns(tw.read...).AllowUnbounded()
		q.st.unscoped = true
		rows, err := q.All(ctx)
		if err != nil {
			return err
		}
		var stray []unsafe.Pointer
		for j := range rows.n {
			p := rows.at(j)
			if i, ok := place[keyString(rowKey(tw.in, p))]; ok && i >= lo && i < hi {
				news[i] = p
			} else {
				stray = append(stray, p)
			}
		}
		return tw.placeStray(OpUpdate, stray, news[lo:hi])
	})
}

// changes is a ChangeRow per old row of st that the write changed (news[i]
// set): the row before the write and, for an update, the row after it. A
// row that no RETURNING, re-read or count found is left out. A soft
// delete's (MarkSoftDelete) rows are deletes, Soft, with both rows.
func (tw *tracker) changes(st *Statement, olds TableRows, news []unsafe.Pointer) []ChangeRow {
	op, soft := st.Op, st.Op == OpUpdate && st.softDelete
	out := make([]ChangeRow, 0, olds.n)
	for i := range olds.n {
		if news[i] == nil {
			continue // a Rewrite or Around hook, or a trigger, kept the row from the write
		}
		row := ChangeRow{Op: op, In: tw.in, Old: olds.at(i)}
		if op == OpUpdate {
			row.New = news[i]
		}
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
	update := cp.Op == OpUpdate
	var out []ChangeRow
	err := returnedRows(ctx, tx, &cp, tw.t, tw.read, update, false, func(olds, news []unsafe.Pointer) error {
		out = make([]ChangeRow, len(olds))
		for i, p := range olds {
			out[i] = ChangeRow{Op: cp.Op, In: tw.in, Old: p}
			if update {
				out[i].New = news[i]
				if st.softDelete {
					out[i].Op, out[i].Soft = OpDelete, true
				}
			}
		}
		return nil
	})
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
