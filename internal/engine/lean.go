package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"unsafe"
)

// An INSERT lists only the columns that carry a value (ADR 0093). A column
// is left out when every row of the statement holds nothing for it: a nil
// in a nullable column, which stores NULL, or the Go zero value in a column
// whose database default the schema declared (ColumnDescriptor.DBDefault),
// which stores that default. The values the database filled are read back
// into the rows: with RETURNING on PostgreSQL and SQLite, with a SELECT by
// key on MySQL.

// dbDefaultBit is the bit of dialect d in ColumnInfo.dbDefault.
func dbDefaultBit(d Dialect) uint8 { return 1 << d }

// HasDBDefault reports whether column c has a database default on dialect
// d that the schema declared, which an insert leaves a zero value to.
func HasDBDefault(c *ColumnInfo, d Dialect) bool { return c.dbDefault&dbDefaultBit(d) != 0 }

// leftToDB reports whether v, the value of column col, leaves the column
// to the database: the Go zero value in a column with a database default.
func leftToDB(in *EntityInfo, d Dialect, col int, v any) bool {
	if !HasDBDefault(&in.Columns[col], d) {
		return false
	}
	rv := reflect.ValueOf(v)
	return !rv.IsValid() || rv.IsZero()
}

// emptyCell reports whether v, the value of column col, puts nothing in the
// column: a NULL (nullCell), or a value left to the database's default.
func emptyCell(in *EntityInfo, d Dialect, col int, v any) bool {
	return leftToDB(in, d, col, v) || nullCell(in, col, v) || nilEncoded(&in.Columns[col], v)
}

// nilEncoded reports whether v is a nil slice, map or pointer in a nullable
// column with a codec, which binds NULL, not what the codec makes of nil
// (colArg).
func nilEncoded(c *ColumnInfo, v any) bool {
	if !c.Nullable || !c.Codec {
		return false
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Invalid:
		return true
	case reflect.Slice, reflect.Map, reflect.Pointer:
		return rv.IsNil()
	}
	return false
}

// LeanInsert removes from st, an insert, each column that every row leaves
// empty (emptyCell), unless the conflict clause names it, and returns the
// columns it removed whose database default fills them, in st's order. It
// keeps at least one column, so that the statement has a row to write. It
// runs after the hooks and before the Rewrite hooks, which see the columns
// that are sent. It compacts st.Columns and st.Rows in place: the caller
// gives a statement that owns them.
func LeanInsert(d Dialect, st *Statement) []int {
	if len(st.Rows) == 0 {
		return nil
	}
	var buf [64]bool
	var drop []bool
	if len(st.Columns) <= len(buf) {
		drop = buf[:len(st.Columns)]
	} else {
		drop = make([]bool, len(st.Columns))
	}
	n := 0
	for i, col := range st.Columns {
		if st.Conflict != nil && slices.Contains(st.Conflict.Columns, col) {
			continue
		}
		all := true
		for _, row := range st.Rows {
			if i >= len(row) || !emptyCell(st.Entity, d, col, row[i]) {
				all = false
				break
			}
		}
		if all {
			drop[i] = true
			n++
		}
	}
	if n == 0 {
		return nil
	}
	if n == len(st.Columns) {
		drop[0] = false // one column stays, written as the rows hold it
	}
	var fill []int
	cols := st.Columns[:0]
	for i, col := range st.Columns {
		if !drop[i] {
			cols = append(cols, col)
		} else if HasDBDefault(&st.Entity.Columns[col], d) {
			fill = append(fill, col)
		}
	}
	for r, row := range st.Rows {
		out := row[:0]
		for i, v := range row {
			if !drop[i] {
				out = append(out, v)
			}
		}
		clear(row[len(out):])
		st.Rows[r] = out
	}
	st.Columns = cols
	return fill
}

// PlanInsert makes st, the insert of rows with the generated key column gen
// (-1 for none), lean (LeanInsert), and sets what it reads back: on
// PostgreSQL and SQLite the key and the columns the database filled, with
// RETURNING, unless the conflict clause keeps the existing row, whose rows
// RETURNING would not match to the rows sent; on MySQL the key comes from
// the driver and the filled columns from a SELECT after the insert. A key
// column that the database fills on MySQL, where no auto-increment value
// tells it, is refused.
func PlanInsert(d Dialect, st *Statement, gen int) error {
	fill := LeanInsert(d, st)
	in := st.Entity
	if InfoBidi(in) {
		// The rows hold each link and its mirror (MirrorRows), which match
		// no entity one to one; such a join entity has no generated key.
		return nil
	}
	if d == MySQL {
		for _, c := range fill {
			if slices.Contains(in.PrimaryKey, c) {
				return fmt.Errorf("velox: %s: MySQL cannot return the key %s that the database's default fills: set it in Go", in.Name, in.Columns[c].Name)
			}
		}
		st.fill = fill
		return nil
	}
	if st.Conflict != nil && st.Conflict.DoNothing && len(st.Rows) > 1 {
		return nil
	}
	var ret []int
	if gen >= 0 {
		ret = append(ret, gen)
	}
	ret = append(ret, fill...)
	st.Returning, st.ret = ret, ret // a Rewrite hook changes a copy of Returning
	return nil
}

// scanReturned scans the row r holds, the RETURNING columns of st, into the
// entity at p. A Rewrite hook that changed Returning leaves the entity as
// it was: the row then holds other columns.
func scanReturned(r Rows, st *Statement, p unsafe.Pointer) error {
	if !slices.Equal(st.Returning, st.ret) {
		return nil
	}
	in := st.Entity
	if len(st.ret) == 1 {
		c := st.ret[0]
		return r.Scan(ScanDst(in, c, fieldAddr(in, p, c)))
	}
	dst := make([]any, len(st.ret))
	for i, c := range st.ret {
		dst[i] = ScanDst(in, c, fieldAddr(in, p, c))
	}
	return r.Scan(dst...)
}

// sendReturning sends st, which reads back st.Returning, on db, and scans
// the rows that come back into the entities, in order: one, when it is not
// nil, else rows. It returns how many came back.
func sendReturning(ctx context.Context, db *DB, st *Statement, one unsafe.Pointer, rows []unsafe.Pointer) (int64, error) {
	var back int64
	read := func(r Rows) error {
		for r.Next() {
			dst := one
			if dst == nil && int(back) < len(rows) {
				dst = rows[back]
			}
			if dst != nil && (one == nil || back == 0) {
				if err := scanReturned(r, st, dst); err != nil {
					return err
				}
			}
			back++
		}
		return nil
	}
	var err error
	if db.Hooked() {
		err = db.Run(ctx, st, read, nil)
	} else {
		err = db.Plain(ctx, st, read, nil)
	}
	return back, err
}

// readFilled reads the columns st.fill, which the database's defaults
// filled, of the rows just inserted, by their keys, into rows: MySQL's way,
// as it has no RETURNING. It reads without the Read policy, as the rows are
// the caller's own; an Interceptor still filters, and a row it hides keeps
// its Go values.
func readFilled(ctx context.Context, db *DB, st *Statement, rows []unsafe.Pointer) error {
	in := st.Entity
	if len(st.fill) == 0 || len(in.PrimaryKey) == 0 {
		return nil
	}
	t := InfoTable(in)
	rdb := db.WithoutPolicy("velox: the defaults of the rows an insert wrote")
	copyIn := func(dst, src unsafe.Pointer) {
		for _, c := range st.fill {
			SetElem(fieldAddr(in, dst, c), Elem(fieldAddr(in, src, c)))
		}
	}
	if len(in.PrimaryKey) == 1 && len(rows) > 1 {
		k := in.PrimaryKey[0]
		keys := make([]any, len(rows))
		at := make(map[any]unsafe.Pointer, len(rows))
		for i, p := range rows {
			v, _ := plain(in.kit.Value(p, k))
			keys[i], at[v] = v, p
		}
		got, err := QueryTable(rdb, t).Where(t.Pred(k, PredIn, keys)).Columns(append([]int{k}, st.fill...)...).AllowUnbounded().All(ctx)
		if err != nil {
			return err
		}
		for i := range got.n {
			src := got.at(i)
			v, _ := plain(in.kit.Value(src, k))
			if dst, ok := at[v]; ok {
				copyIn(dst, src)
			}
		}
		return nil
	}
	for _, p := range rows {
		key := make([]Predicate, len(in.PrimaryKey))
		for i, k := range in.PrimaryKey {
			v, _ := plain(in.kit.Value(p, k))
			key[i] = t.Pred(k, PredEQ, v)
		}
		src, err := QueryTable(rdb, t).Where(key...).Columns(st.fill...).one(ctx)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		copyIn(p, src)
	}
	return nil
}

// DefaultRuns splits rows 0 to n of an InsertAll into runs of consecutive
// rows that leave the same columns of cols to the database's defaults
// (leftToDB), so that each row stores what it would alone. at(i, col) is
// the value of column col in row i. It returns the end of each run.
func DefaultRuns(in *EntityInfo, d Dialect, cols []int, n int, at func(i, col int) any) []int {
	var dcols []int
	for _, c := range cols {
		if HasDBDefault(&in.Columns[c], d) {
			dcols = append(dcols, c)
		}
	}
	if len(dcols) == 0 || n == 0 {
		return []int{n}
	}
	same := func(i, j int) bool {
		for _, c := range dcols {
			if leftToDB(in, d, c, at(i, c)) != leftToDB(in, d, c, at(j, c)) {
				return false
			}
		}
		return true
	}
	var ends []int
	for i := 1; i < n; i++ {
		if !same(i-1, i) {
			ends = append(ends, i)
		}
	}
	return append(ends, n)
}

// SendBatch sends st, the insert of the entities at rows with the
// generated key column gen (-1 for none), planned by PlanInsert, and reads
// back what the database assigned: the keys and the filled columns. It
// returns the rows the statement affected, -1 when the driver did not say.
func SendBatch(ctx context.Context, db *DB, st *Statement, rows []unsafe.Pointer, gen int) (int64, error) {
	if len(st.Returning) > 0 && db.D != MySQL {
		back, err := sendReturning(ctx, db, st, nil, rows)
		if err != nil {
			return 0, err
		}
		return back, CheckUpserted(st, back)
	}
	res, err := ExecWrite(ctx, db, st)
	if err != nil {
		return 0, err
	}
	if err := CheckUpserted(st, res.Affected); err != nil {
		return 0, err
	}
	if db.D != MySQL || st.Conflict != nil {
		return res.Affected, nil
	}
	if gen >= 0 && res.HasLastID && res.Affected == int64(len(rows)) {
		// MySQL gives a multi-row INSERT consecutive auto-increment values,
		// from the first row's, as ent's batch insert reads them.
		for i, p := range rows {
			if err := setKey(st.Entity, gen, fieldAddr(st.Entity, p, gen), res.LastID+int64(i)); err != nil {
				return 0, err
			}
		}
	}
	return res.Affected, readFilled(ctx, db, st, rows)
}
