package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"strings"
	"unsafe"
)

// The misuses of Track.
var (
	errTrackInsert = errors.New("an insert's Changes need no Track; Track is for updates and deletes")
	errTrackLate   = errors.New("a Track after next comes too late: velox reads the rows before it writes them")
)

// trackState is what Track asked for, and what the tracked write changed.
type trackState struct {
	all     bool     // Track was called with no names
	cols    []int    // the columns Track named
	changes []Change // what the write changed, for Changes
}

// trackingOf is mu's trackState; nil when no hook called Track.
func trackingOf(mu *MutationState) *trackState {
	ts, _ := mu.Trk.(*trackState)
	return ts
}

// TrackByName is Mutation.Track's work: it marks the columns of the given
// names, or with none the columns the update assigns, or every column of a
// delete, to be read before the write and compared after it. A misuse is
// recorded, and returned.
func TrackByName(mu *MutationState, names ...string) error {
	switch {
	case mu.St == nil:
		return mu.fail(errTrackInsert)
	case mu.Called:
		return mu.fail(errTrackLate)
	}
	cols := make([]int, 0, len(names))
	for _, name := range names {
		i := mu.column("Track", name)
		if i < 0 {
			return mu.Err
		}
		cols = append(cols, i)
	}
	ts := trackingOf(mu)
	if ts == nil {
		ts = &trackState{}
		mu.Trk = ts
	}
	if len(names) == 0 {
		ts.all = true
	}
	for _, c := range cols {
		if !slices.Contains(ts.cols, c) {
			ts.cols = append(ts.cols, c)
		}
	}
	return nil
}

// trackedCols is the columns a tracked write reads, in column order: those
// Track named, and with a Track of no names the columns st assigns, its
// UpdateDefault and Version ones included, or every column of a delete.
func (ts *trackState) trackedCols(st *Statement) []int {
	in := st.Entity
	want := make([]bool, len(in.Columns))
	for _, c := range ts.cols {
		want[c] = true
	}
	if ts.all {
		if st.Op == OpDelete {
			for i := range want {
				want[i] = true
			}
		}
		for _, a := range st.Set {
			want[a.Column] = true
		}
	}
	var cols []int
	for i, w := range want {
		if w {
			cols = append(cols, i)
		}
	}
	return cols
}

// ChangesOf is Mutation.Changes' work: after next, one Change per row the
// write changed. An update or a delete yields those Track read; an insert
// yields every row, each column with no old value.
func ChangesOf(mu *MutationState) iter.Seq[Change] {
	return func(yield func(Change) bool) {
		if !mu.Called || mu.Core != nil || mu.Dry {
			return
		}
		if mu.St != nil {
			ts := trackingOf(mu)
			if ts == nil {
				return
			}
			for _, c := range ts.changes {
				if !yield(c) {
					return
				}
			}
			return
		}
		in := mu.entity()
		for _, p := range mu.Rows {
			if !yield(insertChange(in, p)) {
				return
			}
		}
	}
}

// insertChange is the Change of the inserted row at p: every column, its
// old value nil.
func insertChange(in *EntityInfo, p unsafe.Pointer) Change {
	c := Change{Key: rowKey(in, p), Fields: make([]FieldChange, len(in.Columns))}
	for i := range in.Columns {
		v := loadColumn(&in.Columns[i], p)
		c.Fields[i] = FieldChange{Name: in.Columns[i].Name, New: v, Changed: v != nil}
	}
	return c
}

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
			out = append(out, FieldChange{Name: c.Name, Old: a, New: b, Changed: true})
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

// errTrackBidi refuses Track on an update that sets the key of a
// bidirectional one-to-one edge: velox re-points both partners in
// statements of its own, which Track does not follow.
func errTrackBidi(in *EntityInfo) error {
	return fmt.Errorf("velox: %s: Track on an update that sets the key of a bidirectional one-to-one edge: velox re-points both partners, which Track does not follow", in.Name)
}

// trackedWrite is writeCore's send for st, an update or a delete whose hook
// called Track. In one transaction (db's, else one of its own) it reads the
// key and the tracked columns of the rows st's conditions match, under st's
// scope (a Writable read), locked where the dialect can, in key order; sends
// st narrowed to those keys, a chunk at a time; and, for an update, reads
// the new values: RETURNING on PostgreSQL and SQLite, a SELECT by key on
// MySQL. It keeps a Change per row in ts.changes and sets mu.Affected.
func trackedWrite(ctx context.Context, db *DB, mu *MutationState, ts *trackState, st *Statement) error {
	in := st.Entity
	if len(in.PrimaryKey) == 0 {
		return fmt.Errorf("velox: %s: Track needs a primary key to find the rows again", in.Name)
	}
	for _, a := range st.Set {
		if a.Op != AssignSet && slices.Contains(in.PrimaryKey, a.Column) {
			return fmt.Errorf("velox: %s: Track cannot follow a row whose key %s the update changes by Add or Sub", in.Name, in.Columns[a.Column].Name)
		}
	}
	tw := tracker{in: in, t: InfoTable(in), cols: ts.trackedCols(st)}
	tw.read = keyAnd(in, tw.cols)
	run := func(ctx context.Context, tx *DB) error {
		ts.changes, mu.Affected = nil, 0
		return tw.run(ctx, tx, mu, ts, st)
	}
	if db.InTx {
		return run(ctx, db)
	}
	return db.Tx(ctx, run)
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
	cols []int // the tracked columns
	read []int // the columns read: the key, then the tracked ones
}

// run is trackedWrite's work on tx.
func (tw *tracker) run(ctx context.Context, tx *DB, mu *MutationState, ts *trackState, st *Statement) error {
	order := make([]OrderTerm, len(tw.in.PrimaryKey))
	for i, k := range tw.in.PrimaryKey {
		order[i] = OrderTerm{Column: k}
	}
	q := QueryTable(tx, tw.t).Where(st.Where...).Columns(tw.read...).OrderBy(order...).Writable().AllowUnbounded()
	if tx.D != SQLite {
		q = q.ForUpdate()
	}
	olds, err := q.All(ctx)
	if err != nil {
		return err
	}
	if olds.n == 0 {
		return nil // nothing matches: nothing to send
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
		return err
	}
	if st.Op == OpUpdate && !returning {
		if err := tw.reread(ctx, tx, at, news); err != nil {
			return err
		}
	}
	ts.changes = tw.changes(st.Op, olds, keys, news)
	return nil
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
	in := t.Info()
	if len(in.PrimaryKey) == 1 {
		vs := make([]any, len(keys))
		for i, k := range keys {
			vs[i] = k[0]
		}
		return t.Pred(in.PrimaryKey[0], PredIn, vs)
	}
	ors := make([]Node, len(keys))
	for i, k := range keys {
		ands := make([]Node, len(k))
		for j, col := range in.PrimaryKey {
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

// changes is a Change per old row: its key before the write, and each
// tracked column's old and new value. A delete's new values are nil, and so
// are an update's for a row no RETURNING or re-read found.
func (tw *tracker) changes(op Op, olds TableRows, keys [][]any, news []unsafe.Pointer) []Change {
	out := make([]Change, 0, olds.n)
	for i := range olds.n {
		if op == OpUpdate && news[i] == nil {
			continue // a Rewrite or Around hook kept the row from the write
		}
		c := Change{Key: keys[i], Fields: make([]FieldChange, len(tw.cols))}
		for j, col := range tw.cols {
			ci := &tw.in.Columns[col]
			f := FieldChange{Name: ci.Name, Old: loadColumn(ci, olds.at(i))}
			if op == OpUpdate {
				f.New = loadColumn(ci, news[i])
			}
			f.Changed = !EqualValues(f.Old, f.New)
			c.Fields[j] = f
		}
		out = append(out, c)
	}
	return out
}
