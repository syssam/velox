package engine

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"unsafe"
)

// insertSend sends st, a recorded insert of rows, on tx, and returns the
// rows it affected (-1 when the driver did not say): sendOne or SendBatch,
// with the caller's bookkeeping.
type insertSend func(ctx context.Context, tx *DB, st *Statement) (int64, error)

// recordedInsert runs fn, which sends a recorded insert's statements and
// returns its Changes, and emits them. A single INSERT outside a transaction
// is sent as it is: it commits on its own, so the sink, which runs after it,
// sees a committed write, and the handle sends what one that does not record
// sends. Anything else runs under recordedTx: an upsert, whose locked read
// and INSERT must be one, and an InsertAll of several statements, in a
// transaction of velox's own, whose COMMIT the sink waits for; and, in the
// caller's transaction, every insert, in a savepoint that holds its
// statements and the sink.
func recordedInsert(ctx context.Context, db *DB, single bool, fn func(ctx context.Context, tx *DB) ([]ChangeRow, error)) error {
	run := func(ctx context.Context, tx *DB) error {
		rows, err := fn(ctx, tx)
		if err != nil {
			return err
		}
		return emit(ctx, tx, rows)
	}
	if single && !db.InTx {
		return run(ctx, db)
	}
	return recordedTx(ctx, db, run)
}

// recordInsertRow is InsertRow on a recording handle: the insert of the row
// at p, and its Change. A row that sets the key of a bidirectional
// one-to-one edge is inserted with the link of its partner (bidiInsert),
// whose row is reported after the insert's. An upsert on MySQL that cannot
// find its row is refused (refuseUnfindable).
func recordInsertRow(ctx context.Context, db *DB, p unsafe.Pointer, st *Statement, gen int, out *Result) error {
	in := st.Entity
	if err := refuseUnfindable(db.D, in, st.Conflict, gen, []unsafe.Pointer{p}); err != nil {
		return err
	}
	// The closures work on copies, so that st and out, which no closure
	// holds, stay where the caller put them: an insert through a handle
	// that does not record allocates nothing for this path.
	cp := *st
	cp.recorded = true
	rows := []unsafe.Pointer{p}
	var n int64
	send := func(ctx context.Context, tx *DB) ([]ChangeRow, error) {
		cp.InTx = tx.InTx
		cs, k, err := sendRecorded(ctx, tx, &cp, rows, gen, func(ctx context.Context, tx *DB, st *Statement) (int64, error) {
			var res Result
			err := sendOne(ctx, tx, p, st, gen, &res)
			return res.Affected, err
		})
		n = k
		return cs, err
	}
	var err error
	if keys, vs := bidiRow(in, p); len(keys) > 0 {
		setStmtBidi(&cp, &bidiStep{Keys: keys, Vals: vs, Insert: true})
		err = bidiInsert(ctx, db, in, p, keys, vs, gen >= 0, cp.Conflict != nil, send)
	} else {
		err = recordedInsert(ctx, db, cp.Conflict == nil, send)
	}
	out.Affected = n
	return err
}

// refuseUnfindable refuses, on MySQL, a recorded upsert of rows (gen the
// generated key column, -1 for none) holding a row with NULL in a conflict
// column, or the generated key left zero in the conflict columns: such a row
// cannot be found after the INSERT on MySQL, which returns no keys, and
// whose read after it is by the conflict values. That covers a row whose
// key the caller set (a UUID) as well.
func refuseUnfindable(d Dialect, in *EntityInfo, cf *ConflictClause, gen int, rows []unsafe.Pointer) error {
	if d != MySQL || cf == nil {
		return nil
	}
	for _, p := range rows {
		for _, c := range cf.Columns {
			why := ""
			if c == gen && ZeroInt(fieldAddr(in, p, c)) {
				why = "is the generated key, left zero"
			} else if _, ok := plain(in.kit.Value(p, c)); !ok {
				why = "is NULL"
			}
			if why != "" {
				return fmt.Errorf("velox: %s: a row whose conflict column %s %s cannot be found after the INSERT on MySQL, which returns no keys, so a recorded upsert refuses it: insert such rows without OnConflict", in.Name, in.Columns[c].Name, why)
			}
		}
	}
	return nil
}

// sendRecorded sends st, the recorded insert of the entities at rows with
// the generated key column gen (-1 for none), and returns their ChangeRows
// and the rows the statement affected. A plain insert is sent with send,
// and reports each row, in order, as an insert whose new row is a copy of
// the entity, taken after send read back what the database filled; none
// when the statement affected no row. An upsert reports as conflictRead
// says: on PostgreSQL and SQLite it is sent by conflictRead.returning, on
// MySQL with send.
func sendRecorded(ctx context.Context, tx *DB, st *Statement, rows []unsafe.Pointer, gen int, send insertSend) ([]ChangeRow, int64, error) {
	if st.Conflict != nil {
		st.recorded = true
		cr, err := lockConflicts(ctx, tx, st, rows, gen)
		if err != nil {
			return nil, 0, err
		}
		if tx.D != MySQL {
			news, err := cr.returning(ctx, tx, st, rows)
			if err != nil {
				return nil, 0, err
			}
			return cr.paired(news, rows, cr.in.PrimaryKey), int64(len(news)), nil
		}
		n, err := send(ctx, tx, st)
		if err != nil {
			return nil, 0, err
		}
		cs, err := cr.counted(ctx, tx, rows, n)
		return cs, n, err
	}
	st.recorded = true
	n, err := send(ctx, tx, st)
	if err != nil {
		return nil, 0, err
	}
	if n == 0 {
		return nil, n, nil
	}
	in := st.Entity
	out := make([]ChangeRow, 0, len(rows))
	for _, p := range rows {
		out = append(out, ChangeRow{Op: OpInsert, In: in, New: ownCopy(in, p)})
		if InfoBidi(in) && !selfLink(Elem(fieldAddr(in, p, 0)), Elem(fieldAddr(in, p, 1))) {
			// The join entity of a bidirectional edge: the INSERT wrote the
			// link's mirror row too (MirrorRows).
			out = append(out, ChangeRow{Op: OpInsert, In: in, New: mirrorCopy(in, p)})
		}
	}
	return out, n, nil
}

// mirrorCopy is ownCopy of the row of in, the join entity of a
// bidirectional edge, at p, with its columns 0 and 1 exchanged: the mirror
// row of the link (MirrorRows).
func mirrorCopy(in *EntityInfo, p unsafe.Pointer) unsafe.Pointer {
	dst := ownCopy(in, p)
	a, b := &in.Columns[0], &in.Columns[1]
	typ := GoType(*a)
	x := reflect.NewAt(typ, unsafe.Add(dst, a.off)).Elem()
	y := reflect.NewAt(typ, unsafe.Add(dst, b.off)).Elem()
	tmp := reflect.New(typ).Elem()
	tmp.Set(x)
	x.Set(y)
	y.Set(tmp)
	return dst
}

// ownCopy is a copy of the columns of the row of in at p, the engine's own:
// a deep copy of each field that holds memory (a pointer, a slice, a map),
// so that what the caller later does to its entity, or to what its fields
// point to, never reaches a recorded Change.
func ownCopy(in *EntityInfo, p unsafe.Pointer) unsafe.Pointer {
	dst := InfoTable(in).state().newRow()
	for i := range in.Columns {
		c := &in.Columns[i]
		typ := GoType(*c)
		src := reflect.NewAt(typ, unsafe.Add(p, c.off)).Elem()
		to := reflect.NewAt(typ, unsafe.Add(dst, c.off)).Elem()
		if holdsRefs(typ) {
			to.Set(cloneValue(src))
		} else {
			to.Set(src)
		}
	}
	return dst
}

// conflictRead is a recorded upsert below PostgreSQL 18. Before its INSERT
// it reads the rows its rows conflict with, by the conflict columns, every
// column, locked where the dialect can (not on SQLite, whose transaction
// holds the database); the database compares the keys, so its collation
// decides what matches, as it does for the conflict. The database then
// names the rows the upsert touched:
//
//   - PostgreSQL and SQLite: the INSERT returns every column of each row it
//     inserted or updated (a row DoNothing kept is not returned, and is not
//     reported).
//   - MySQL, which returns nothing: a read after the INSERT by the conflict
//     columns, checked against the rows the INSERT reports affected (an
//     inserted row counts 1, an updated one 2, and one matched and left as
//     it was 1, under the clientFoundRows=true velox requires). A count
//     that differs, as when a row conflicted on another unique key than the
//     conflict columns, fails the write, which rolls back. A row with NULL
//     in a conflict column, or a generated key left zero in the conflict
//     columns, cannot be found after the INSERT on MySQL, and is refused
//     before anything is sent (refuseUnfindable).
//
// A touched row is paired with a row of the first read by its primary key
// as stored: the pair is an update, which emit leaves out when nothing
// changed, and a row with no pair is an insert.
//
// Known limits:
//   - A read cannot lock a row that is not there: a row another session
//     inserts under a key the first read did not find, before the INSERT,
//     is reported as an insert although the upsert updated it, with no old
//     values. PostgreSQL 18's RETURNING old and new (one statement) closes
//     it there.
//   - On MySQL the locked read takes gap (next-key) locks: two recorded
//     upserts of new keys in the same gap, at once, can deadlock, and one
//     fails with ER_LOCK_DEADLOCK, which TxRetrySerializable retries.
//   - Rows of one InsertAll that share a conflict key give one Change per
//     row stored; on MySQL their count differs, and the write fails.
//   - The reads see the rows without the scope or the Read policy: the
//     database finds a conflict whatever they hide, so a sink can see
//     values the caller's policy hides.
type conflictRead struct {
	in   *EntityInfo
	t    Table
	cols []int // the conflict columns
	gen  int   // the generated key column, -1 for none
	olds map[string]unsafe.Pointer
}

// lockConflicts reads, locked, the rows that rows, the entities st inserts
// with the generated key column gen, conflict with on st's conflict
// columns, before st is sent, and keeps them by primary key.
func lockConflicts(ctx context.Context, tx *DB, st *Statement, rows []unsafe.Pointer, gen int) (*conflictRead, error) {
	in := st.Entity
	cr := &conflictRead{in: in, t: InfoTable(in), cols: st.Conflict.Columns, gen: gen}
	keys := cr.keys(rows)
	got, err := cr.read(ctx, tx, keys, true)
	if err != nil {
		return nil, err
	}
	cr.olds = make(map[string]unsafe.Pointer, len(got))
	for _, p := range got {
		cr.olds[keyString(cr.stored(p, in.PrimaryKey))] = p
	}
	return cr, nil
}

// keys is the distinct conflict keys of rows that can conflict: not a row
// whose generated key is zero, which the INSERT leaves out, nor one with a
// NULL in a conflict column. On the join entity of a bidirectional edge,
// whose INSERT writes each link's mirror row too (MirrorRows), they include
// the mirror rows' keys.
func (cr *conflictRead) keys(rows []unsafe.Pointer) [][]any {
	var keys [][]any
	seen := make(map[string]bool, len(rows))
	add := func(k []any) {
		if !seen[keyString(k)] {
			seen[keyString(k)] = true
			keys = append(keys, k)
		}
	}
	bidi := InfoBidi(cr.in)
	for _, p := range rows {
		k, ok := cr.key(p, cr.cols)
		if !ok {
			continue
		}
		add(k)
		if bidi {
			if m, ok := mirrorKey(cr.cols, k); ok {
				add(m)
			}
		}
	}
	return keys
}

// mirrorKey is k, the values of cols, with the values of columns 0 and 1
// exchanged: the key of a bidirectional join row's mirror. It reports false
// when cols does not hold both.
func mirrorKey(cols []int, k []any) ([]any, bool) {
	a, b := slices.Index(cols, 0), slices.Index(cols, 1)
	if a < 0 || b < 0 {
		return nil, false
	}
	m := slices.Clone(k)
	m[a], m[b] = k[b], k[a]
	return m, true
}

// key is the values of cols in the entity at p. It reports false when one
// is NULL, or is the generated key left zero.
func (cr *conflictRead) key(p unsafe.Pointer, cols []int) ([]any, bool) {
	k := make([]any, len(cols))
	for j, c := range cols {
		if c == cr.gen && ZeroInt(fieldAddr(cr.in, p, c)) {
			return nil, false
		}
		v, ok := plain(cr.in.kit.Value(p, c))
		if !ok {
			return nil, false
		}
		k[j] = v
	}
	return k, true
}

// stored is the values of cols in the row at p, as the database returned
// it.
func (cr *conflictRead) stored(p unsafe.Pointer, cols []int) []any {
	k := make([]any, len(cols))
	for j, c := range cols {
		k[j], _ = plain(cr.in.kit.Value(p, c))
	}
	return k
}

// read reads the rows whose conflict columns hold one of keys, every
// column, locked when lock is set and the dialect can, in the order the
// database returns them. It reads without the scope or the Read policy:
// the database finds a conflict whatever they hide, and the rows are those
// the caller's own insert meets.
func (cr *conflictRead) read(ctx context.Context, tx *DB, keys [][]any, lock bool) ([]unsafe.Pointer, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	rdb := tx.WithoutPolicy("velox: the rows a recorded upsert meets")
	q := QueryTable(rdb, cr.t).Where(conflictIn(cr.t, tx.D, cr.cols, keys)).Columns(everyColumn(cr.in)...).AllowUnbounded()
	q.st.unscoped = true
	if lock && tx.D != SQLite {
		q = q.ForUpdate()
	}
	got, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]unsafe.Pointer, got.n)
	for i := range out {
		out[i] = got.at(i)
	}
	return out, nil
}

// returning sends st, the upsert of rows on PostgreSQL or SQLite, with
// RETURNING every column instead of what PlanInsert set (st.ret), and
// returns the rows that come back, as stored. The columns PlanInsert would
// have read back are copied into the entities as sendReturning scans them,
// the n-th row into the n-th entity, so that the caller's entities end as a
// handle that does not record leaves them. It checks the count as
// sendOne and SendBatch do (CheckUpserted).
func (cr *conflictRead) returning(ctx context.Context, tx *DB, st *Statement, rows []unsafe.Pointer) ([]unsafe.Pointer, error) {
	in, base := cr.in, st.ret
	cols := everyColumn(in)
	st.Returning, st.ret = cols, cols
	ts := cr.t.state()
	var news []unsafe.Pointer
	read := func(r Rows) error {
		news = news[:0] // anew, should an Around hook retry
		dst := make([]any, len(cols))
		for r.Next() {
			p := ts.newRow()
			bindTableRow(ts, cols, dst, p)
			if err := r.Scan(dst...); err != nil {
				return err
			}
			if i := len(news); len(base) > 0 && i < len(rows) {
				for _, c := range base {
					SetElem(fieldAddr(in, rows[i], c), Elem(fieldAddr(in, p, c)))
				}
			}
			news = append(news, p)
		}
		return nil
	}
	var err error
	if tx.Hooked() {
		err = tx.Run(ctx, st, read, nil)
	} else {
		err = tx.Plain(ctx, st, read, nil)
	}
	if err != nil {
		return nil, err
	}
	return news, CheckUpserted(st, int64(len(news)))
}

// counted is the Changes of rows, the upsert on MySQL that affected n rows:
// it reads the rows by the conflict columns of the entities, as stored, and
// checks n against them (see conflictRead). A count that differs is an
// error, and the write rolls back: a row was touched that the read cannot
// see, and no Change of it may be lost.
func (cr *conflictRead) counted(ctx context.Context, tx *DB, rows []unsafe.Pointer, n int64) ([]ChangeRow, error) {
	news, err := cr.read(ctx, tx, cr.keys(rows), false)
	if err != nil {
		return nil, err
	}
	var want int64
	for _, p := range news {
		old, ok := cr.olds[keyString(cr.stored(p, cr.in.PrimaryKey))]
		switch {
		case !ok:
			want++ // inserted
		case cr.same(old, p):
			want++ // matched, left as it was: clientFoundRows counts it
		default:
			want += 2 // updated
		}
	}
	if n != want {
		return nil, fmt.Errorf("velox: %s: a recorded upsert affected %d rows by MySQL's count, and the %d rows it finds by the conflict columns account for %d: a row conflicted on another unique key, or two rows on one key, or the DSN lacks clientFoundRows=true; the write is rolled back", cr.in.Name, n, len(news), want)
	}
	return cr.paired(news, rows, cr.cols), nil
}

// same reports whether the rows at a and b hold the same values in every
// column.
func (cr *conflictRead) same(a, b unsafe.Pointer) bool {
	for i := range cr.in.Columns {
		x, _ := plain(cr.in.kit.Value(a, i))
		y, _ := plain(cr.in.kit.Value(b, i))
		if !EqualValues(x, y) {
			return false
		}
	}
	return true
}

// paired is a ChangeRow per row of news, the rows the upsert of rows
// touched, as stored: an update, with the old row of the same primary key,
// when the locked read found one, and an insert when not. The Changes are
// in the order of rows for those whose values in by (the primary key, or
// the conflict columns) are exactly those of a row of news, then the other
// rows of news, in their order: rows the database's collation matched to
// another value (as "Alice@x" to a stored "alice@x"), and rows whose key
// the entity does not hold, come after.
func (cr *conflictRead) paired(news, rows []unsafe.Pointer, by []int) []ChangeRow {
	at := make(map[string]unsafe.Pointer, len(news))
	for _, p := range news {
		at[keyString(cr.stored(p, by))] = p
	}
	out := make([]ChangeRow, 0, len(news))
	done := make(map[unsafe.Pointer]bool, len(news))
	add := func(p unsafe.Pointer) {
		if done[p] {
			return // a second row of the key wrote the same stored row
		}
		done[p] = true
		row := ChangeRow{Op: OpInsert, In: cr.in, New: p}
		if old, ok := cr.olds[keyString(cr.stored(p, cr.in.PrimaryKey))]; ok {
			row.Op, row.Old = OpUpdate, old
		}
		out = append(out, row)
	}
	for _, r := range rows {
		if k, ok := cr.key(r, by); ok {
			if p := at[keyString(k)]; p != nil {
				add(p)
			}
		}
	}
	for _, p := range news {
		add(p)
	}
	return out
}

// conflictIn is the condition that a row's columns cols hold one of keys:
// col IN (…) for one column; for several, PostgreSQL's row value
// (a, b) IN ((?, ?), …), and (a = ? AND b = ?) OR … elsewhere, as keysIn
// writes a composite key.
func conflictIn(t Table, d Dialect, cols []int, keys [][]any) Predicate {
	if len(cols) > 1 && d == Postgres {
		return NewPredicate(Node{Op: opRowIn, V: cols, W: keys})
	}
	return valuesIn(t, cols, keys)
}
