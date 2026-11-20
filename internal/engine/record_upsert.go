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
// returns its Changes, and emits them, under recordedTx: outside a
// transaction in one of velox's own, a single INSERT's too, and in the
// caller's in a savepoint; either holds the statements and the sink, which
// runs before the COMMIT or the RELEASE.
//
// old is nil for a plain insert. For an upsert it says whether it reads its
// old values in the INSERT (PostgreSQL 18: returnsOld); in the caller's
// transaction, with the server version not known yet, versionedTx learns it
// inside the write's savepoint and sets it.
func recordedInsert(ctx context.Context, db *DB, old *bool, fn func(ctx context.Context, tx *DB) ([]ChangeRow, error)) error {
	run := func(ctx context.Context, tx *DB) error {
		rows, err := fn(ctx, tx)
		if err != nil {
			return err
		}
		return emit(tx, rows)
	}
	if old != nil {
		return versionedTx(ctx, db, old, run)
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
	cp.returnOld = cp.Conflict != nil && returnsOld(ctx, db)
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
		if cp.Conflict != nil {
			primeVersion(ctx, db) // before bidiInsert's transaction
			cp.returnOld = returnsOld(ctx, db)
		}
		setStmtBidi(&cp, &bidiStep{Keys: keys, Vals: vs, Insert: true})
		err = bidiInsert(ctx, db, in, p, keys, vs, gen >= 0, cp.Conflict != nil, send)
	} else {
		var old *bool
		if cp.Conflict != nil {
			old = &cp.returnOld
		}
		err = recordedInsert(ctx, db, old, send)
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
// and the rows the statement affected. A plain insert reports each row, in
// order, as an insert whose new row is the row as stored: on PostgreSQL and
// SQLite it is sent by insertReturning, on MySQL with send and a read by
// key (insertRead). An upsert on PostgreSQL 18 or later,
// st.returnOld, is sent by returningBoth; below, it reports as conflictRead
// says: on PostgreSQL and SQLite it is sent by conflictRead.returning, on
// MySQL with send.
func sendRecorded(ctx context.Context, tx *DB, st *Statement, rows []unsafe.Pointer, gen int, send insertSend) ([]ChangeRow, int64, error) {
	if st.Conflict != nil && st.returnOld {
		st.recorded = true
		cs, err := returningBoth(ctx, tx, st, rows)
		return cs, int64(len(cs)), err
	}
	st.returnOld = false
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
	if tx.D != MySQL {
		return insertReturning(ctx, tx, st, rows)
	}
	return insertRead(ctx, tx, st, rows, send)
}

// insertReturning sends st, the recorded plain insert of rows on PostgreSQL
// or SQLite, with RETURNING every column instead of what PlanInsert set
// (st.ret), and reports each row that comes back, in its order, as an
// insert whose new row is the row as stored: a value the database rounds
// or pads (a decimal's scale, a time's microseconds, a CHAR) is reported as
// it holds it. The rows come back in the order of the VALUES (ADR 0093): the
// entities get the columns PlanInsert would have read back, and nothing
// else, so that they end as a handle that does not record leaves them. On
// the join entity of a bidirectional edge the rows hold each link and its
// mirror (MirrorRows), and the entities read nothing back.
func insertReturning(ctx context.Context, tx *DB, st *Statement, rows []unsafe.Pointer) ([]ChangeRow, int64, error) {
	in, base := st.Entity, st.ret
	cols := everyColumn(in)
	st.Returning, st.ret = cols, cols
	var news []unsafe.Pointer
	err := returnedRows(ctx, tx, st, InfoTable(in), cols, false, false, func(got, _ []unsafe.Pointer) error {
		news = got
		if InfoBidi(in) || len(base) == 0 {
			return nil
		}
		if len(got) != len(rows) {
			return fmt.Errorf("velox: %s: a recorded insert of %d rows returned %d; the write is rolled back", in.Name, len(rows), len(got))
		}
		for i, p := range got {
			copyColumns(in, base, rows[i], p)
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	out := make([]ChangeRow, len(news))
	for i, p := range news {
		out[i] = ChangeRow{Op: OpInsert, In: in, New: p}
	}
	return out, int64(len(news)), nil
}

// insertRead sends st, the recorded plain insert of rows on MySQL, with
// send, and reports each row, in order, as an insert whose new row is the
// row as stored, read by its key after the INSERT, every column, in the
// write's transaction: MySQL has no RETURNING. That one read also gives the
// entities the columns the database's defaults filled (st.fill), which
// send then does not read (readFilled). A row the read cannot find (an
// entity without a key, a key the driver did not report) is reported as a
// copy of the entity. On the join entity of a bidirectional edge each link
// reports its mirror row after it.
func insertRead(ctx context.Context, tx *DB, st *Statement, rows []unsafe.Pointer, send insertSend) ([]ChangeRow, int64, error) {
	fill := st.fill
	st.fill = nil
	n, err := send(ctx, tx, st)
	st.fill = fill
	if err != nil || n == 0 {
		return nil, n, err
	}
	in := st.Entity
	bidi := InfoBidi(in)
	stored, err := readStored(ctx, tx, in, rows, bidi)
	if err != nil {
		return nil, 0, err
	}
	// row is the stored row of key k, or fallback() when the read did not find it.
	row := func(k []any, fallback func() unsafe.Pointer) unsafe.Pointer {
		if p := stored[keyString(k)]; p != nil && len(k) > 0 {
			return p
		}
		return fallback()
	}
	out := make([]ChangeRow, 0, len(rows))
	for _, p := range rows {
		k := rowKey(in, p)
		cur := row(k, func() unsafe.Pointer { return ownCopy(in, p) })
		copyColumns(in, fill, p, cur)
		out = append(out, ChangeRow{Op: OpInsert, In: in, New: cur})
		if bidi && !selfLink(Elem(fieldAddr(in, p, 0)), Elem(fieldAddr(in, p, 1))) {
			// The join entity of a bidirectional edge: the INSERT wrote the
			// link's mirror row too (MirrorRows).
			m := k
			if mk, ok := mirrorKey(in.PrimaryKey, k); ok {
				m = mk
			}
			out = append(out, ChangeRow{Op: OpInsert, In: in, New: row(m, func() unsafe.Pointer { return mirrorCopy(in, p) })})
		}
	}
	return out, n, nil
}

// readStored reads the rows of rows, entities of in just inserted, every
// column, by their primary keys, and returns them by keyString of their
// keys; with bidi, the keys of their mirror rows too. It reads without the
// Read policy or the interceptors, as readFilled does. An entity without a
// key reads nothing.
func readStored(ctx context.Context, tx *DB, in *EntityInfo, rows []unsafe.Pointer, bidi bool) (map[string]unsafe.Pointer, error) {
	if len(in.PrimaryKey) == 0 {
		return nil, nil
	}
	keys := make([][]any, 0, len(rows))
	for _, p := range rows {
		k := rowKey(in, p)
		keys = append(keys, k)
		if m, ok := mirrorKey(in.PrimaryKey, k); bidi && ok {
			keys = append(keys, m)
		}
	}
	t := InfoTable(in)
	rdb := tx.WithoutPolicy("velox: the rows a recorded insert wrote")
	q := QueryTable(rdb, t).Where(keysIn(t, keys)).Columns(everyColumn(in)...).AllowUnbounded()
	q.st.unscoped = true
	got, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]unsafe.Pointer, got.n)
	for i := range got.n {
		p := got.at(i)
		out[keyString(rowKey(in, p))] = p
	}
	return out, nil
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
		copyColumn(in, i, dst, p)
	}
	return dst
}

// copyColumns copies the columns cols of the row of in at src into the row
// at dst, a deep copy of each field that holds memory, as ownCopy does: a
// recorded write that reads a row back gives the caller's entity its own
// values, which share no memory with the Change's row.
func copyColumns(in *EntityInfo, cols []int, dst, src unsafe.Pointer) {
	for _, c := range cols {
		copyColumn(in, c, dst, src)
	}
}

// copyColumn copies column col of the row of in at src into the row at
// dst, deeply when its field holds memory (holdsRefs).
func copyColumn(in *EntityInfo, col int, dst, src unsafe.Pointer) {
	c := &in.Columns[col]
	typ := GoType(*c)
	from := reflect.NewAt(typ, unsafe.Add(src, c.off)).Elem()
	to := reflect.NewAt(typ, unsafe.Add(dst, c.off)).Elem()
	if holdsRefs(typ) {
		to.Set(cloneValue(from))
	} else {
		to.Set(from)
	}
}

// conflictRead is a recorded upsert below PostgreSQL 18, or with
// WithoutReturningOld. Before its INSERT
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
//     values. PostgreSQL 18 and later close it: there the upsert is one
//     statement, RETURNING old and new (returningBoth), and no read comes
//     first.
//   - The first read matches the conflict columns by =, which NULL never
//     matches: on a PostgreSQL unique index with NULLS NOT DISTINCT, a row
//     with NULL in a conflict column that conflicts is reported as an
//     insert although the upsert updated it.
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
// have read back are copied into the entities (fillUpserted). It checks the count as
// sendOne and SendBatch do (CheckUpserted).
func (cr *conflictRead) returning(ctx context.Context, tx *DB, st *Statement, rows []unsafe.Pointer) ([]unsafe.Pointer, error) {
	in, base := cr.in, st.ret
	cols := everyColumn(in)
	st.Returning, st.ret = cols, cols
	var news []unsafe.Pointer
	err := returnedRows(ctx, tx, st, cr.t, cols, false, false, func(got, _ []unsafe.Pointer) error {
		news = got
		return fillUpserted(in, st, base, news, rows)
	})
	if err != nil {
		return nil, err
	}
	return news, CheckUpserted(st, int64(len(news)))
}

// fillUpserted copies the columns base, those PlanInsert would have read
// back, from news, the rows a recorded upsert of rows returned (every
// column), into the entities they were written from (placeUpserted), so
// that the caller's entities end as a handle that does not record leaves
// them.
func fillUpserted(in *EntityInfo, st *Statement, base []int, news, rows []unsafe.Pointer) error {
	if len(base) == 0 || len(news) == 0 {
		return nil
	}
	dst, err := placeUpserted(in, st.Conflict.Columns, news, rows)
	if err != nil {
		return err
	}
	for i, p := range news {
		copyColumns(in, base, dst[i], p)
	}
	return nil
}

// returningBoth sends st, the upsert of rows on PostgreSQL 18 or later, with
// RETURNING old and new of every column instead of what PlanInsert set
// (st.ret), and returns a ChangeRow per row that comes back, in its order:
// an update, whose old row is the row as the conflict found it, or, when
// old's primary key is NULL, an insert. A row DoNothing kept, or that the
// update branch's condition left, is not returned. No read comes first: the
// values are those the INSERT itself saw. The columns PlanInsert would have
// read back are copied into the entities (fillUpserted). It checks the count as sendOne and
// SendBatch do (CheckUpserted).
func returningBoth(ctx context.Context, tx *DB, st *Statement, rows []unsafe.Pointer) ([]ChangeRow, error) {
	in, base := st.Entity, st.ret
	cols := everyColumn(in)
	st.Returning, st.ret = cols, cols
	var out []ChangeRow
	err := returnedRows(ctx, tx, st, InfoTable(in), cols, true, true, func(olds, news []unsafe.Pointer) error {
		out = make([]ChangeRow, len(olds))
		for i, old := range olds {
			out[i] = ChangeRow{Op: OpUpdate, In: in, Old: old, New: news[i]}
			if old == nil {
				out[i].Op = OpInsert
			}
		}
		return fillUpserted(in, st, base, news, rows)
	})
	if err != nil {
		return nil, err
	}
	return out, CheckUpserted(st, int64(len(out)))
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
// column (equalColumn).
func (cr *conflictRead) same(a, b unsafe.Pointer) bool {
	for i := range cr.in.Columns {
		x, _ := plain(cr.in.kit.Value(a, i))
		y, _ := plain(cr.in.kit.Value(b, i))
		if !equalColumn(&cr.in.Columns[i], x, y) {
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
