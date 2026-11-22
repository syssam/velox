package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
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

// conflictWritten reports whether column col of an insert is written
// whatever its rows hold: the conflict target, and every column the update
// branch copies from the inserted row. MySQL's new.<col> names only a
// column the INSERT lists, and PostgreSQL's EXCLUDED.<col> would hold the
// column's DEFAULT, not the row's value. A column an ExcludedTerm of the
// update branch names is read from the inserted row too.
func conflictWritten(cf *ConflictClause, col int) bool {
	return cf != nil && (slices.Contains(cf.Columns, col) || slices.Contains(cf.Update, col) || slices.Contains(cf.excludedRefs(), col))
}

// excludedRefs are the columns the ExcludedTerms of cf's update branch, its
// assignments and its condition, name.
func (cf *ConflictClause) excludedRefs() []int {
	var cols []int
	for _, a := range cf.Set {
		if a.Op == AssignExpr {
			cols = excludedCols(cols, exprAssigned(a))
		}
	}
	for _, p := range cf.Where {
		cols = nodeExcluded(cols, p.n)
	}
	return cols
}

// defaultCell reports whether the cell v of column col, in st, an insert
// that LeanInsert gave DEFAULT cells (st.dflt), is written as the DEFAULT
// keyword on dialect d: the zero value in a column with a declared default
// that the conflict clause does not write (conflictWritten). A Rewrite hook
// that puts a value in such a cell gets it written.
func defaultCell(d Dialect, st *Statement, col int, v any) bool {
	return st.dflt && d != SQLite && !conflictWritten(st.Conflict, col) && leftToDB(st.Entity, d, col, v)
}

// LeanInsert removes from st, an insert, each column that every row leaves
// empty (emptyCell), unless the conflict clause names it (conflictWritten),
// and returns the columns whose database default fills a cell, in st's
// order: those it removed and, on PostgreSQL and MySQL, those it kept where
// a row leaves the cell to the database, which is then written as the
// DEFAULT keyword (st.dflt, defaultCell). An insert of one row, or any
// insert on MySQL, may lose every column: it is sent as DEFAULT VALUES or
// () VALUES (). An insert of several rows on PostgreSQL or SQLite keeps one
// column, as no such form writes several rows. It runs after the hooks and
// before the Rewrite hooks, which see the columns that are sent. It compacts
// st.Columns and st.Rows in place: the caller gives a statement that owns
// them.
func LeanInsert(d Dialect, st *Statement) []int {
	if len(st.Rows) == 0 {
		return nil
	}
	var buf, cbuf [64]bool
	var drop, cells []bool
	if len(st.Columns) <= len(buf) {
		drop, cells = buf[:len(st.Columns)], cbuf[:len(st.Columns)]
	} else {
		drop, cells = make([]bool, len(st.Columns)), make([]bool, len(st.Columns))
	}
	// someLeft reports whether a row leaves column i, col, to its default:
	// only an insert of several rows on PostgreSQL or MySQL writes DEFAULT.
	someLeft := func(i, col int) bool {
		if d == SQLite || len(st.Rows) < 2 || !HasDBDefault(&st.Entity.Columns[col], d) {
			return false
		}
		for _, row := range st.Rows {
			if i < len(row) && leftToDB(st.Entity, d, col, row[i]) {
				return true
			}
		}
		return false
	}
	n := 0
	for i, col := range st.Columns {
		if conflictWritten(st.Conflict, col) {
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
		} else {
			cells[i] = someLeft(i, col)
		}
	}
	if n == len(st.Columns) && n > 0 && len(st.Rows) > 1 && d != MySQL {
		// One column stays, written as the rows hold it, or as DEFAULT.
		drop[0], n = false, n-1
		cells[0] = someLeft(0, st.Columns[0])
	}
	var fill []int
	for i, col := range st.Columns {
		if cells[i] || drop[i] && HasDBDefault(&st.Entity.Columns[col], d) {
			fill = append(fill, col)
			st.dflt = st.dflt || cells[i]
		}
	}
	if n > 0 {
		dropColumns(st, drop)
	}
	return fill
}

// dropColumns removes from st, an insert, each column drop marks, and its
// value from every row, in place.
func dropColumns(st *Statement, drop []bool) {
	cols := st.Columns[:0]
	for i, col := range st.Columns {
		if !drop[i] {
			cols = append(cols, col)
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
}

// PlanInsert makes st, the insert of rows with the generated key column gen
// (-1 for none), lean (LeanInsert), and sets what it reads back: on
// PostgreSQL and SQLite the key and the columns the database filled, with
// RETURNING, unless the conflict clause keeps the existing rows of an
// insert of several, whose rows RETURNING would not match to the rows sent;
// on MySQL the key comes from the driver and the filled columns from a
// SELECT after the insert (readFilled, or readUpserted for a one-row
// upsert). A key column that the database fills on MySQL, where no
// auto-increment value tells it, is refused.
//
// The columns an upsert's update branch assigns with Set (UpdateSet) are
// read back too, as the row then holds the database's value, not the
// entity's: with RETURNING, and on MySQL for a one-row upsert.
func PlanInsert(d Dialect, st *Statement, gen int) error {
	fill := LeanInsert(d, st)
	if cf := st.Conflict; cf != nil && !keepsRow(cf) && (d != MySQL || len(st.Rows) == 1) {
		for _, a := range cf.Set {
			if !slices.Contains(fill, a.Column) && !slices.Contains(st.Entity.PrimaryKey, a.Column) {
				fill = append(fill, a.Column)
			}
		}
	}
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
	// A conflict that keeps the row (DoNothing, or an empty Update) leaves
	// that row out of RETURNING, so a multi-row read-back by position would
	// give the next row's values to the wrong entities.
	if st.Conflict != nil && keepsRow(st.Conflict) && len(st.Rows) > 1 {
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
// entity at p, through dst, the destinations, which it returns for the next
// row: one slice serves every row, as Scan's arguments escape. A Rewrite
// hook that changed Returning leaves the entity as it was: the row then
// holds other columns.
func scanReturned(r Rows, st *Statement, p unsafe.Pointer, dst []any) ([]any, error) {
	if !slices.Equal(st.Returning, st.ret) {
		return dst, nil
	}
	in := st.Entity
	if len(dst) != len(st.ret) {
		dst = make([]any, len(st.ret))
	}
	for i, c := range st.ret {
		dst[i] = ScanDst(in, c, fieldAddr(in, p, c))
	}
	return dst, r.Scan(dst...)
}

// sendReturning sends st, which reads back st.Returning, on db, and scans
// the rows that come back into the entities, in order: one, when it is not
// nil, else rows. An upsert of several rows whose update branch has a
// condition returns only the rows it wrote: they are matched to rows by
// their conflict columns (placeByKey). It returns how many came back.
func sendReturning(ctx context.Context, db *DB, st *Statement, one unsafe.Pointer, rows []unsafe.Pointer) (int64, error) {
	var back int64
	read := func(r Rows) error {
		back = 0 // anew, should an Around hook retry
		if one == nil && placeByKey(st, len(rows)) {
			n, err := readPlaced(r, st, rows)
			back = n
			return err
		}
		var scan []any
		for r.Next() {
			dst := one
			if dst == nil && int(back) < len(rows) {
				dst = rows[back]
			}
			if dst != nil && (one == nil || back == 0) {
				var err error
				if scan, err = scanReturned(r, st, dst, scan); err != nil {
					return err
				}
			}
			back++
		}
		return nil
	}
	err := sendRead(ctx, db, st, read)
	return back, err
}

// placeByKey reports whether the rows st, the insert of n entities, returns
// are matched to the entities by their conflict columns rather than by
// position: an upsert of several rows whose update branch has a condition
// (the policy's, an interceptor's or a Rewrite hook's), which returns only
// the rows it inserted or updated, a subset of the rows sent.
func placeByKey(st *Statement, n int) bool {
	return n > 1 && st.Conflict != nil && len(st.Conflict.Where) > 0 && len(st.ret) > 0 && slices.Equal(st.Returning, st.ret)
}

// keyReturning adds st's conflict columns to the RETURNING that PlanInsert
// set, when its rows will be matched to the entities by them (placeByKey).
// It runs after the Rewrite hooks and the scope, which may add the
// condition, and before rendering. A recorded upsert returns every column.
func keyReturning(d Dialect, st *Statement) {
	if st.Op != OpInsert || d == MySQL || st.recorded || st.keyed > 0 || !placeByKey(st, len(st.Rows)) {
		return
	}
	ret := slices.Clip(st.ret)
	n := 0
	for _, c := range st.Conflict.Columns {
		if !slices.Contains(ret, c) {
			ret = append(ret, c)
			n++
		}
	}
	st.Returning, st.ret, st.keyed = ret, ret, int16(n)
}

// readPlaced reads the rows r holds, the RETURNING columns of st, each into
// a row of its own, places them among rows by their conflict columns
// (placeUpserted), and copies into each entity the columns PlanInsert read
// back. It returns how many rows came back.
func readPlaced(r Rows, st *Statement, rows []unsafe.Pointer) (int64, error) {
	in := st.Entity
	ts := InfoTable(in).state()
	var (
		got  []unsafe.Pointer
		scan []any
		err  error
	)
	for r.Next() {
		p := ts.newRow()
		if scan, err = scanReturned(r, st, p, scan); err != nil {
			return 0, err
		}
		got = append(got, p)
	}
	dst, err := placeUpserted(in, st.Conflict.Columns, got, rows)
	if err != nil {
		return 0, err
	}
	cols := st.ret[:len(st.ret)-int(st.keyed)]
	for i, p := range got {
		for _, c := range cols {
			SetElem(fieldAddr(in, dst[i], c), Elem(fieldAddr(in, p, c)))
		}
	}
	return int64(len(got)), nil
}

// placeUpserted returns, for each row of got, the rows an upsert of rows
// returned in their order, the entity of rows it was written from. When
// every row came back they are in the order of rows (ADR 0093). When fewer
// did, as when the update branch's condition rejected a conflict, each is
// the one entity whose conflict columns cols hold exactly the values the
// row holds (placeKey: a time by its instant); a row that matches no
// entity, more than one, or one another row took, is an error rather than
// a guess: the database may have stored a value in another form (a
// collation's, a padded CHAR, a time cut to microseconds), and a misplaced
// row would give an entity another row's key.
func placeUpserted(in *EntityInfo, cols []int, got, rows []unsafe.Pointer) ([]unsafe.Pointer, error) {
	if len(got) == len(rows) {
		return rows, nil
	}
	if len(got) > len(rows) {
		return nil, fmt.Errorf("velox: %s: cannot place upserted row: %d rows came back for %d sent", in.Name, len(got), len(rows))
	}
	key := func(p unsafe.Pointer) string {
		var b strings.Builder
		for _, c := range cols {
			v, _ := plain(in.kit.Value(p, c))
			v = placeKey(v)
			fmt.Fprintf(&b, "%T\x00%v\x00", v, v)
		}
		return b.String()
	}
	at := make(map[string][]int, len(rows))
	for i, p := range rows {
		k := key(p)
		at[k] = append(at[k], i)
	}
	used := make([]bool, len(rows))
	dst := make([]unsafe.Pointer, len(got))
	for j, p := range got {
		m := at[key(p)]
		if len(m) != 1 || used[m[0]] {
			var vals []string
			for _, c := range cols {
				v, _ := plain(in.kit.Value(p, c))
				vals = append(vals, fmt.Sprintf("%s=%v", in.Columns[c].Name, v))
			}
			return nil, fmt.Errorf("velox: %s: cannot place upserted row (%s): it matches %d of the rows sent by the exact values of its conflict columns; the write is rolled back", in.Name, strings.Join(vals, ", "), len(m))
		}
		used[m[0]] = true
		dst[j] = rows[m[0]]
	}
	return dst, nil
}

// placeKey is v, a conflict column's value, as placeUpserted compares it: a
// time.Time in UTC without its monotonic reading, so that an entity's time
// in another zone matches the instant the database returns; any other
// value as it is.
func placeKey(v any) any {
	if t, ok := v.(time.Time); ok {
		return t.UTC().Round(0)
	}
	return v
}

// readFilled reads the columns st.fill, which the database's defaults
// filled, of the rows just inserted, by their keys, into rows: MySQL's way,
// as it has no RETURNING. It reads without the Read policy or the
// interceptors, as the rows are the caller's own.
func readFilled(ctx context.Context, db *DB, st *Statement, rows []unsafe.Pointer) error {
	in := st.Entity
	if len(st.fill) == 0 || len(in.PrimaryKey) == 0 {
		return nil
	}
	t := InfoTable(in)
	// The read adds no row condition (unscoped): the insert was checked, and an
	// interceptor must not hide the row it wrote.
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
		q := QueryTable(rdb, t).Where(t.Pred(k, PredIn, keys)).Columns(append([]int{k}, st.fill...)...).AllowUnbounded()
		q.st.unscoped = true
		got, err := q.All(ctx)
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
		q := QueryTable(rdb, t).Where(key...).Columns(st.fill...)
		q.st.unscoped = true
		src, err := q.one(ctx)
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

// readUpserted reads the generated key column gen (-1 for none) and the
// columns st.fill, which the database's defaults filled, of the row the
// upsert st of the entity at p inserted or updated, by its conflict values,
// into the entity: MySQL's way, as it has no RETURNING and its LastInsertId
// does not name an updated row. The entity so gets the stored row's key,
// as RETURNING gives it on PostgreSQL and SQLite. The database compares
// the values, as it did for the conflict. InsertRow runs the upsert and the
// read in one transaction. It reads without the Read policy or the
// interceptors, as readFilled does; a row whose conflict values cannot find
// it (a NULL, a generated key left zero) keeps its Go values.
func readUpserted(ctx context.Context, db *DB, st *Statement, p unsafe.Pointer, gen int) error {
	in := st.Entity
	cols := st.fill
	if gen >= 0 {
		cols = append([]int{gen}, st.fill...)
	}
	if len(cols) == 0 {
		return nil
	}
	t := InfoTable(in)
	key := make([]Predicate, len(st.Conflict.Columns))
	for i, c := range st.Conflict.Columns {
		v, ok := plain(in.kit.Value(p, c))
		if !ok || c == gen && ZeroInt(fieldAddr(in, p, c)) {
			return nil
		}
		key[i] = t.Pred(c, PredEQ, v)
	}
	rdb := db.WithoutPolicy("velox: the key and defaults of the row an upsert wrote")
	q := QueryTable(rdb, t).Where(key...).Columns(cols...)
	q.st.unscoped = true
	src, err := q.one(ctx)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, c := range cols {
		SetElem(fieldAddr(in, p, c), Elem(fieldAddr(in, src, c)))
	}
	return nil
}

// DefaultRuns splits rows 0 to n of an InsertAll on SQLite into runs of
// consecutive rows that leave the same columns of cols to the database's
// defaults (leftToDB), so that each row stores what it would alone: SQLite
// has no DEFAULT keyword in VALUES. A row that leaves every column empty
// and one to a default is a run of its own, sent as DEFAULT VALUES. On
// PostgreSQL and MySQL a cell left to a default is written as DEFAULT
// (LeanInsert), and the rows are one run. at(i, col) is the value of column
// col in row i. It returns the end of each run.
func DefaultRuns(in *EntityInfo, d Dialect, cols []int, n int, at func(i, col int) any) []int {
	if d != SQLite {
		return []int{n}
	}
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
	// alone reports whether row i leaves every column empty and one to its
	// default: several such rows have no column to share.
	alone := func(i int) bool {
		left := false
		for _, c := range cols {
			v := at(i, c)
			if !emptyCell(in, d, c, v) {
				return false
			}
			left = left || leftToDB(in, d, c, v)
		}
		return left
	}
	var ends []int
	for i := 1; i < n; i++ {
		if !same(i-1, i) || alone(i) || alone(i-1) {
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
	step := int64(1)
	if db.D == MySQL && st.Conflict == nil && gen >= 0 && len(rows) > 1 && !IsDryRun(ctx) {
		var err error
		if step, err = autoIncrementStep(ctx, db); err != nil {
			return 0, stmtErr(st, fmt.Errorf("the keys of a multi-row insert need MySQL's @@auto_increment_increment, which could not be read: %w", err))
		}
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
		// MySQL gives a multi-row INSERT auto-increment values from the first
		// row's, @@auto_increment_increment apart, as ent's batch insert reads
		// them. The step is read before the INSERT, so that a failed read
		// sends nothing.
		for i, p := range rows {
			if err := setKey(st.Entity, gen, fieldAddr(st.Entity, p, gen), res.LastID+step*int64(i)); err != nil {
				return 0, err
			}
		}
	}
	return res.Affected, readFilled(ctx, db, st, rows)
}
