package engine

import (
	"context"
	"slices"
	"unsafe"
)

// Masked is what a recorded Change shows for a Sensitive column's value.
const Masked = "***"

// ChangeRow is one row a recorded write changed, as the engine reports it:
// the kind of write, the entity, and the row before and after it. Old is nil
// for an insert and New is nil for a delete. Soft marks a soft delete, an
// update reported with Op OpDelete.
type ChangeRow struct {
	Op       Op
	Soft     bool
	In       *EntityInfo
	Old, New unsafe.Pointer
	// ignore is the recording's RecordIgnore columns, which ChangedFields
	// leaves out; set by emit.
	ignore []string
}

// Recording is what a recording handle does with each ChangeRow: Sink
// receives it, and Ignore names the columns left out of an update's
// comparison. Mark and Truncate are set only when the sink is a
// *velox.Recorder: Mark is how many Changes it holds, and Truncate drops
// those after the first n, for a transaction or savepoint that rolls back.
type Recording struct {
	Sink     func(ctx context.Context, r ChangeRow) error
	Ignore   []string
	Mark     func() int
	Truncate func(n int)
}

// Record returns a DB like db whose writes go to r, as do those of the
// transactions and savepoints it begins and of a hook's Mutation.DB. It
// replaces the recording db already has. The DB it returns shares db's pool
// and configuration, and Close on it closes nothing.
func (db *DB) Record(r *Recording) *DB {
	c := *db
	c.rec, c.owned = r, false
	return &c
}

// Recording returns what db's writes are recorded to; nil when db does not
// record.
func (db *DB) Recording() *Recording { return db.rec }

// Key is the row's primary key: after the write for an insert or an update,
// so an update that assigns the key reports the new one, and before it for a
// delete. It is the key column's value for a one-column key,
// a []any of the key columns' values for a composite key, and nil for an
// entity with no key.
func (r ChangeRow) Key() any {
	p := r.New
	if p == nil {
		p = r.Old
	}
	if r.In == nil || p == nil || len(r.In.PrimaryKey) == 0 {
		return nil
	}
	if len(r.In.PrimaryKey) == 1 {
		return r.value(r.In.PrimaryKey[0], p)
	}
	key := make([]any, len(r.In.PrimaryKey))
	for i, k := range r.In.PrimaryKey {
		key[i] = r.value(k, p)
	}
	return key
}

// OldValues is the row before the write by column name; nil for an insert.
func (r ChangeRow) OldValues() map[string]any { return r.values(r.Old) }

// NewValues is the row after the write by column name; nil for a delete.
func (r ChangeRow) NewValues() map[string]any { return r.values(r.New) }

// ChangedFields is an update's columns whose values differ (EqualValues), in
// column order, but for those the recording ignores; nil for another kind of
// write or when none differ.
func (r ChangeRow) ChangedFields() []FieldChange {
	if r.Op != OpUpdate || r.In == nil || r.Old == nil || r.New == nil {
		return nil
	}
	var out []FieldChange
	for i := range r.In.Columns {
		if slices.Contains(r.ignore, r.In.Columns[i].Name) {
			continue
		}
		a, b := r.value(i, r.Old), r.value(i, r.New)
		if EqualValues(a, b) {
			continue
		}
		if r.In.Columns[i].Sensitive {
			a, b = Masked, Masked
		}
		out = append(out, FieldChange{Name: r.In.Columns[i].Name, Old: a, New: b})
	}
	return out
}

// values is the row at p by column name, a Sensitive column Masked; nil for
// a nil p.
func (r ChangeRow) values(p unsafe.Pointer) map[string]any {
	if r.In == nil || p == nil {
		return nil
	}
	out := make(map[string]any, len(r.In.Columns))
	for i := range r.In.Columns {
		c := &r.In.Columns[i]
		if c.Sensitive {
			out[c.Name] = Masked
			continue
		}
		out[c.Name] = r.value(i, p)
	}
	return out
}

// value is column col of the row at p, plain: nil for NULL, and a nullable
// column's value, not its pointer.
func (r ChangeRow) value(col int, p unsafe.Pointer) any {
	v, _ := plain(r.In.kit.Value(p, col))
	return v
}

// changed reports whether an update's row differs in a column the recording
// does not ignore: an update that changes none is not reported.
func (r ChangeRow) changed() bool {
	for i := range r.In.Columns {
		if !slices.Contains(r.ignore, r.In.Columns[i].Name) && !EqualValues(r.value(i, r.Old), r.value(i, r.New)) {
			return true
		}
	}
	return false
}

// sinkKey marks the context a Recording's sink runs under. A write through a
// handle recording to r, under a context so marked, is not recorded: the
// audit row a sink writes for a Change is not itself audited.
type sinkKey struct{ r *Recording }

// recording is what db's write under ctx is recorded to: nil when db does
// not record, or when ctx is that of its recording's sink.
func (db *DB) recording(ctx context.Context) *Recording {
	if db.rec == nil || ctx.Value(sinkKey{db.rec}) != nil {
		return nil
	}
	return db.rec
}

// pending is the Changes the recorded writes in a transaction velox opened
// for them made, each with its recording, waiting for the COMMIT.
type pending struct {
	rows []pendingRow
}

type pendingRow struct {
	r   *Recording
	row ChangeRow
}

// len is the number of Changes waiting; 0 for a nil p.
func (p *pending) len() int {
	if p == nil {
		return 0
	}
	return len(p.rows)
}

// truncate drops the Changes after the first n, those of a savepoint that
// rolled back. A nil p holds none.
func (p *pending) truncate(n int) {
	if p != nil && n < len(p.rows) {
		clear(p.rows[n:])
		p.rows = p.rows[:n]
	}
}

// emit reports rows, the rows a write through db changed, to db's recording:
// each update that changed a column the recording does not ignore, and every
// other row, in order. In a transaction velox opened for the write (ownTx),
// they wait for its COMMIT. Otherwise each goes to the sink now: in the
// caller's transaction, when db is in one, after the write's statements and
// in the savepoint recordedTx holds them in. The
// first error a sink returns is emit's, and the rest are not reported.
func emit(ctx context.Context, db *DB, rows []ChangeRow) error {
	r := db.rec
	if r == nil {
		return nil
	}
	var pend *pending
	if db.txRoot != nil {
		pend = db.txRoot.pend
	}
	var sctx context.Context
	for _, row := range rows {
		row.ignore = r.Ignore
		if row.Op == OpUpdate && !row.changed() {
			continue
		}
		if pend != nil {
			pend.rows = append(pend.rows, pendingRow{r: r, row: row})
			continue
		}
		if sctx == nil {
			sctx = context.WithValue(ctx, sinkKey{r}, true)
		}
		if err := r.Sink(sctx, row); err != nil {
			return err
		}
	}
	return nil
}

// ownTx runs fn on db when db is in a transaction, the caller's. Otherwise
// it runs fn in a transaction of its own, and, when db records, the Changes
// emitted in it go to their sinks after its COMMIT: the write is committed
// when a sink runs, and a sink's error, which ownTx returns, undoes nothing.
// A recorded write in fn, on db's transaction, runs in a savepoint of its own
// (recordedTx).
func ownTx(ctx context.Context, db *DB, fn func(ctx context.Context, tx *DB) error) error {
	if db.InTx {
		return fn(ctx, db)
	}
	if db.rec == nil {
		return db.Tx(ctx, fn)
	}
	p := &pending{}
	err := db.Tx(ctx, func(ctx context.Context, tx *DB) error {
		p.truncate(0) // anew, on a retry
		tx.txRoot.pend = p
		return fn(ctx, tx)
	})
	if err != nil {
		return err
	}
	for _, pr := range p.rows {
		if err := pr.r.Sink(context.WithValue(ctx, sinkKey{pr.r}, true), pr.row); err != nil {
			return err
		}
	}
	return nil
}

// recordedTx runs fn, a recorded write's statements and its emit, so that
// a write that fails took no effect. In the caller's transaction it runs fn
// in a savepoint: an error from a statement or from a sink rolls back to it.
// In a transaction velox opened (ownTx) it runs fn there, as that
// transaction rolls back on an error. Outside a transaction it is ownTx.
func recordedTx(ctx context.Context, db *DB, fn func(ctx context.Context, tx *DB) error) error {
	if db.InTx && db.txRoot.pend == nil {
		return db.Tx(ctx, fn)
	}
	return ownTx(ctx, db, fn)
}

// atomically runs fn in a savepoint when db is in a transaction, and else
// in a transaction of velox's own, as ownTx: in both, fn's writes commit or
// roll back together.
func atomically(ctx context.Context, db *DB, fn func(ctx context.Context, tx *DB) error) error {
	if db.InTx {
		return db.Tx(ctx, fn)
	}
	return ownTx(ctx, db, fn)
}
