package engine

import (
	"context"
	"reflect"
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
		return sendRecorded(ctx, tx, &cp, rows, func(ctx context.Context, tx *DB, st *Statement) (int64, error) {
			err := sendOne(ctx, tx, p, st, gen, &res)
			return res.Affected, err
		})
	})
	out.Affected = res.Affected
	return err
}

// sendRecorded sends st, the recorded insert of the entities at rows, with
// send, and returns a ChangeRow per row, in order. A plain insert reports
// each row as an insert whose new row is a copy of the entity, taken after
// send read back what the database filled; none when the statement affected
// no row, as when an Around hook kept it from the database. An upsert
// reports as conflictRead says.
func sendRecorded(ctx context.Context, tx *DB, st *Statement, rows []unsafe.Pointer, send insertSend) ([]ChangeRow, error) {
	var cr *conflictRead
	if st.Conflict != nil {
		var err error
		if cr, err = lockConflicts(ctx, tx, st, rows); err != nil {
			return nil, err
		}
	}
	n, err := send(ctx, tx, st)
	if err != nil {
		return nil, err
	}
	if cr != nil {
		return cr.changes(ctx, tx, rows)
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

// conflictRead is a recorded upsert's read, before its INSERT, of the rows
// its rows conflict with: by the conflict columns, every column, locked
// where the dialect can (not on SQLite, whose transaction holds the
// database). A row whose conflict key it found is the update branch's, or
// DoNothing's; any other row is an insert.
//
// Below PostgreSQL 18 this read cannot lock a row that is not there: a row
// another session inserts under a key the read did not find, before the
// INSERT, is reported as an insert although the upsert updated it, with no
// old values. PostgreSQL 18's RETURNING old and new (one statement) closes
// it there. On MySQL, whose ON DUPLICATE KEY UPDATE takes a conflict on any
// unique key, a row that conflicts on a key other than the conflict columns
// is reported the same way.
type conflictRead struct {
	in   *EntityInfo
	t    Table
	cols []int   // the conflict columns
	keys [][]any // each row's conflict key, in order; nil for one holding a NULL, which never conflicts
	q    [][]any // the distinct keys, which the reads look up
	olds map[string]unsafe.Pointer
	keep bool // DoNothing: a conflicting row is kept, and not reported
}

// lockConflicts reads, locked, the rows that rows, the entities st inserts,
// conflict with on st's conflict columns, before st is sent.
func lockConflicts(ctx context.Context, tx *DB, st *Statement, rows []unsafe.Pointer) (*conflictRead, error) {
	in := st.Entity
	cr := &conflictRead{in: in, t: InfoTable(in), cols: st.Conflict.Columns, keep: st.Conflict.DoNothing || len(st.Conflict.Update) == 0,
		keys: make([][]any, len(rows))}
	seen := make(map[string]bool, len(rows))
	for i, p := range rows {
		k := make([]any, len(cr.cols))
		for j, c := range cr.cols {
			v, ok := plain(in.kit.Value(p, c))
			if !ok {
				k = nil
				break
			}
			k[j] = v
		}
		cr.keys[i] = k
		if k != nil && !seen[keyString(k)] {
			seen[keyString(k)] = true
			cr.q = append(cr.q, k)
		}
	}
	olds, err := cr.read(ctx, tx, true)
	if err != nil {
		return nil, err
	}
	cr.olds = olds
	return cr, nil
}

// read reads the rows of cr's keys, every column, by key, locked when lock
// is set and the dialect can. It reads without the scope or the Read
// policy: the database finds a conflict whatever they hide, and the rows
// are those the caller's own insert meets.
func (cr *conflictRead) read(ctx context.Context, tx *DB, lock bool) (map[string]unsafe.Pointer, error) {
	out := make(map[string]unsafe.Pointer, len(cr.q))
	if len(cr.q) == 0 {
		return out, nil
	}
	rdb := tx.WithoutPolicy("velox: the rows a recorded upsert meets")
	q := QueryTable(rdb, cr.t).Where(conflictIn(cr.t, tx.D, cr.cols, cr.q)).Columns(everyColumn(cr.in)...).AllowUnbounded()
	q.st.unscoped = true
	if lock && tx.D != SQLite {
		q = q.ForUpdate()
	}
	got, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	k := make([]any, len(cr.cols))
	for i := range got.n {
		p := got.at(i)
		for j, c := range cr.cols {
			k[j], _ = plain(cr.in.kit.Value(p, c))
		}
		out[keyString(k)] = p
	}
	return out, nil
}

// changes reads the rows the upsert left, by their conflict keys, and
// returns a ChangeRow per row sent, in order: an update, with the old row
// the locked read found and the new row as stored, for a row that
// conflicted, unless DoNothing kept it; an insert, with the row as stored,
// for any other. A row the read-back does not find was not written (an
// Around hook kept it) and is left out, and so is a second row of the same
// key, which wrote the row the first one did.
func (cr *conflictRead) changes(ctx context.Context, tx *DB, rows []unsafe.Pointer) ([]ChangeRow, error) {
	news, err := cr.read(ctx, tx, false)
	if err != nil {
		return nil, err
	}
	out := make([]ChangeRow, 0, len(rows))
	done := make(map[string]bool, len(rows))
	for i, k := range cr.keys {
		if k == nil {
			// A NULL in a conflict column never conflicts: the row was
			// inserted, and the entity holds what was read back.
			out = append(out, ChangeRow{Op: OpInsert, In: cr.in, New: ownCopy(cr.in, rows[i])})
			continue
		}
		ks := keyString(k)
		if done[ks] {
			continue
		}
		done[ks] = true
		cur := news[ks]
		if cur == nil {
			continue
		}
		if old, ok := cr.olds[ks]; ok {
			if !cr.keep {
				out = append(out, ChangeRow{Op: OpUpdate, In: cr.in, Old: old, New: cur})
			}
			continue
		}
		out = append(out, ChangeRow{Op: OpInsert, In: cr.in, New: cur})
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
