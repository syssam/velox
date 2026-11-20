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
// one-to-one edge is refused, as velox re-points its partner in a statement
// recording does not follow.
func recordInsertRow(ctx context.Context, db *DB, p unsafe.Pointer, st *Statement, gen int, out *Result) error {
	in := st.Entity
	if len(infoBidiKeys(in)) > 0 {
		if keys, _ := bidiRow(in, p); len(keys) > 0 {
			return errRecordBidi(in, OpInsert)
		}
	}
	// The closures work on copies, so that st and out, which no closure
	// holds, stay where the caller put them: an insert through a handle
	// that does not record allocates nothing for this path.
	cp := *st
	rows := []unsafe.Pointer{p}
	var res Result
	err := recordedInsert(ctx, db, cp.Conflict == nil, func(ctx context.Context, tx *DB) ([]ChangeRow, error) {
		cp.InTx = tx.InTx
		return sendRecorded(ctx, tx, &cp, rows, gen, func(ctx context.Context, tx *DB, st *Statement) (int64, error) {
			err := sendOne(ctx, tx, p, st, gen, &res)
			return res.Affected, err
		})
	})
	out.Affected = res.Affected
	return err
}

// sendRecorded sends st, the recorded insert of the entities at rows with
// the generated key column gen (-1 for none), with send, and returns their
// ChangeRows. A plain insert reports each row, in order, as an insert whose
// new row is a copy of the entity, taken after send read back what the
// database filled; none when the statement affected no row. An upsert
// reports as conflictRead says.
func sendRecorded(ctx context.Context, tx *DB, st *Statement, rows []unsafe.Pointer, gen int, send insertSend) ([]ChangeRow, error) {
	var cr *conflictRead
	if st.Conflict != nil {
		var err error
		if cr, err = lockConflicts(ctx, tx, st, rows, gen); err != nil {
			return nil, err
		}
		if tx.D != MySQL && gen >= 0 && len(st.Returning) == 0 && slices.Contains(st.Conflict.Columns, gen) {
			// A DoNothing InsertAll reads nothing back (PlanInsert), but on
			// the generated key, which the INSERT leaves out, no row
			// conflicts: each comes back, in order, and its key finds it.
			st.Returning, st.ret = []int{gen}, []int{gen}
		}
	}
	n, err := send(ctx, tx, st)
	if err != nil {
		return nil, err
	}
	if cr != nil {
		return cr.changes(ctx, tx, st, rows)
	}
	if n == 0 {
		return nil, nil
	}
	in := st.Entity
	out := make([]ChangeRow, len(rows))
	for i, p := range rows {
		out[i] = ChangeRow{Op: OpInsert, In: in, New: ownCopy(in, p)}
	}
	return out, nil
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

// conflictRead is a recorded upsert below PostgreSQL 18: a read, before
// its INSERT, of the rows its rows conflict with, by the conflict columns,
// every column, locked where the dialect can (not on SQLite, whose
// transaction holds the database); and a read after it of the rows as
// stored. The database compares the keys in both, so its collation decides
// what matches, as it does for the conflict. A row of the second read is
// paired with one of the first by its primary key as stored: the pair is an
// update, which emit leaves out when nothing changed (as a row DoNothing
// kept), and a row with no pair is an insert.
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
//   - On MySQL, whose ON DUPLICATE KEY UPDATE takes a conflict on any
//     unique key, a row that conflicts on another key than the conflict
//     columns is not found by the second read, and the write fails.
//   - Rows of one InsertAll that share a conflict key give one Change per
//     row stored.
//   - Both reads see the rows without the scope or the Read policy: the
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
	var keys [][]any
	seen := make(map[string]bool, len(rows))
	for _, p := range rows {
		// A row whose generated key is zero leaves it out of the INSERT,
		// and one with a NULL in a conflict column never conflicts.
		if k, ok := cr.key(p, cr.cols, nil); ok && !seen[keyString(k)] {
			seen[keyString(k)] = true
			keys = append(keys, k)
		}
	}
	got, err := cr.read(ctx, tx, conflictIn(cr.t, tx.D, cr.cols, keys), len(keys) > 0, true)
	if err != nil {
		return nil, err
	}
	cr.olds = make(map[string]unsafe.Pointer, len(got))
	for _, p := range got {
		cr.olds[keyString(cr.stored(p, in.PrimaryKey))] = p
	}
	return cr, nil
}

// key is the values of cols in the entity at p; id stands for its
// generated key, when not nil. It reports false when one is NULL, or is
// the generated key left zero.
func (cr *conflictRead) key(p unsafe.Pointer, cols []int, id any) ([]any, bool) {
	k := make([]any, len(cols))
	for j, c := range cols {
		if c == cr.gen {
			if id != nil {
				k[j] = id
				continue
			}
			if ZeroInt(fieldAddr(cr.in, p, c)) {
				return nil, false
			}
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

// read reads the rows where, every column, locked when lock is set and the
// dialect can, in the order the database returns them; nothing when some is
// false. It reads without the scope or the Read policy: the database finds
// a conflict whatever they hide, and the rows are those the caller's own
// insert meets.
func (cr *conflictRead) read(ctx context.Context, tx *DB, where Predicate, some, lock bool) ([]unsafe.Pointer, error) {
	if !some {
		return nil, nil
	}
	rdb := tx.WithoutPolicy("velox: the rows a recorded upsert meets")
	q := QueryTable(rdb, cr.t).Where(where).Columns(everyColumn(cr.in)...).AllowUnbounded()
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

// ids is the generated key of each of rows, the entities st inserted, as a
// value of the key's type, where the entity holds none: on MySQL, whose
// upsert sets no key, the driver's LastInsertId and those after it, when
// every row was inserted (MySQL counts an updated row twice), as ent's
// batch insert reads them. nil when there is nothing to stand in.
func (cr *conflictRead) ids(st *Statement, rows []unsafe.Pointer) []any {
	if cr.gen < 0 || !slices.Contains(cr.cols, cr.gen) {
		return nil
	}
	if res := st.sent; !res.HasLastID || res.Affected != int64(len(rows)) || slices.ContainsFunc(rows, func(p unsafe.Pointer) bool {
		return !ZeroInt(fieldAddr(cr.in, p, cr.gen))
	}) {
		return nil
	}
	typ := GoType(cr.in.Columns[cr.gen])
	out := make([]any, len(rows))
	for i := range rows {
		out[i] = reflect.ValueOf(st.sent.LastID + int64(i)).Convert(typ).Interface()
	}
	return out
}

// changes reads the rows the upsert st of rows left, as stored: by the
// conflict columns of each entity after the send, which holds what
// RETURNING or the driver read back, or by its primary key for one with a
// NULL in a conflict column. It pairs each with the locked read's row of
// the same primary key, as stored: an update when there is one, an insert
// when not. The Changes are in the order of rows for those whose key holds
// the values a stored row holds, then the other stored rows, in the order
// read: rows the database's collation matched to another value (as
// "Alice@x" to a stored "alice@x") come out of the order of rows.
//
// It fails closed: a row sent whose key finds no stored row, when no stored
// row is left to be it, is an error, and the write rolls back; never a
// Change silently lost.
func (cr *conflictRead) changes(ctx context.Context, tx *DB, st *Statement, rows []unsafe.Pointer) ([]ChangeRow, error) {
	in := cr.in
	ids := cr.ids(st, rows)
	byKey := make([][]any, len(rows)) // each row's conflict key, nil for none
	byPK := make([][]any, len(rows))  // each row's primary key, for one with no conflict key
	var keys, pks [][]any
	for i, p := range rows {
		var id any
		if ids != nil {
			id = ids[i]
		}
		if k, ok := cr.key(p, cr.cols, id); ok {
			byKey[i], keys = k, append(keys, k)
			continue
		}
		k, ok := cr.key(p, in.PrimaryKey, id)
		if !ok {
			return nil, fmt.Errorf("velox: %s: a recorded upsert cannot find the row it wrote: its conflict columns hold NULL and the database gave its key without saying it", in.Name)
		}
		byPK[i], pks = k, append(pks, k)
	}
	got, err := cr.read(ctx, tx, conflictIn(cr.t, tx.D, cr.cols, keys), len(keys) > 0, false)
	if err != nil {
		return nil, err
	}
	more, err := cr.read(ctx, tx, keysIn(cr.t, pks), len(pks) > 0, false)
	if err != nil {
		return nil, err
	}
	news := append(got, more...)
	exact := make(map[string]unsafe.Pointer, len(news)) // by conflict key and by primary key, as stored
	for _, p := range news {
		exact["k"+keyString(cr.stored(p, cr.cols))] = p
		exact["p"+keyString(cr.stored(p, in.PrimaryKey))] = p
	}
	out := make([]ChangeRow, 0, len(news))
	done := make(map[unsafe.Pointer]bool, len(news))
	add := func(p unsafe.Pointer) {
		if done[p] {
			return // a second row of the key wrote the same stored row
		}
		done[p] = true
		row := ChangeRow{Op: OpInsert, In: in, New: p}
		if old, ok := cr.olds[keyString(cr.stored(p, in.PrimaryKey))]; ok {
			row.Op, row.Old = OpUpdate, old
		}
		out = append(out, row)
	}
	unmatched := 0
	for i := range rows {
		var p unsafe.Pointer
		if byKey[i] != nil {
			p = exact["k"+keyString(byKey[i])]
		} else {
			p = exact["p"+keyString(byPK[i])]
		}
		if p == nil {
			unmatched++
			continue
		}
		add(p)
	}
	left := 0
	for _, p := range news {
		if !done[p] {
			left++
			add(p)
		}
	}
	if unmatched > 0 && left == 0 {
		return nil, fmt.Errorf("velox: %s: a recorded upsert found no stored row for %d of the rows it wrote, so it cannot report them; the write is rolled back", in.Name, unmatched)
	}
	return out, nil
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
