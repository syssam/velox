package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"time"
	"unsafe"
)

// ChangeRow is one row a recorded write changed, as the engine reports it:
// the kind of write, the entity, and the row before and after it. Old is nil
// for an insert and New is nil for a delete. Soft marks a soft delete, an
// update reported with Op OpDelete.
type ChangeRow struct {
	Op       Op
	Soft     bool
	In       *EntityInfo
	Old, New unsafe.Pointer
	// rec is the recording the row was emitted to, whose ignored columns
	// Diff leaves out; set by emit.
	rec *Recording
}

// Recording is what a recording handle does with the ChangeRows of each
// write: Sink receives them, all of one write in one call, with the write's
// transaction handle (one that does not record). Ignore names, per entity,
// columns left out of an update's comparison; Include names columns with an
// UpdateDefault, which are left out unless named here. Keep and Drop are set
// only when the sink is a *velox.Recorder: Keep keeps a ChangeRow and returns
// its id, unique in the Recorder and growing, and Drop drops the rows of the
// ids given, ascending, for a transaction or savepoint that rolls back. Where
// velox sees the transaction end, flush calls Keep in place of Sink and the
// transaction notes the id (txFrame.kept): rows are dropped by id, not by
// position, so a Recorder shared by concurrent transactions loses only the
// rows of the one that rolls back. A transaction the caller began on its own
// *sql.Tx (UseTx), whose end velox never sees, calls Sink and keeps nothing.
type Recording struct {
	Sink    func(ctx context.Context, tx *DB, rows []ChangeRow) error
	Ignore  map[*EntityInfo][]int
	Include map[*EntityInfo][]int
	Keep    func(r ChangeRow) uint64
	Drop    func(ids []uint64)
}

// ignores reports whether r leaves column col of entity in out of an
// update's comparison: Ignore names it, or it has an UpdateDefault that
// Include does not name.
func (r *Recording) ignores(in *EntityInfo, col int) bool {
	if r != nil && slices.Contains(r.Ignore[in], col) {
		return true
	}
	if in.Columns[col].UpdateDefault == nil {
		return false
	}
	return r == nil || !slices.Contains(r.Include[in], col)
}

// Record returns a DB like db whose writes go to r, as do those of the
// transactions and savepoints it begins and of a hook's Mutation.DB. It
// replaces the recording db already has. The DB it returns shares db's pool
// and configuration, and Close on it closes nothing.
func (db *DB) Record(r *Recording) *DB {
	c := *db
	c.rec, c.owned, c.batch = r, false, nil
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

// Row is the row at p, Old or New, as an *E; nil for a nil p.
func (r ChangeRow) Row(p unsafe.Pointer) any {
	if r.In == nil || p == nil {
		return nil
	}
	return r.In.kit.Ptr(p)
}

// Value is column col of the row at p, Old or New, plain: nil for NULL, a
// nil p or a col out of range, and a nullable column's value, not its
// pointer.
func (r ChangeRow) Value(col int, p unsafe.Pointer) any {
	if r.In == nil || p == nil || col < 0 || col >= len(r.In.Columns) {
		return nil
	}
	return r.value(col, p)
}

// Differs reports whether an update's old and new rows differ in column col
// (EqualValues); false for another kind of write or a col out of range.
func (r ChangeRow) Differs(col int) bool {
	if r.Op != OpUpdate || r.In == nil || r.Old == nil || r.New == nil || col < 0 || col >= len(r.In.Columns) {
		return false
	}
	return !EqualValues(r.value(col, r.Old), r.value(col, r.New))
}

// Diff is an update's columns whose values differ (EqualValues), in column
// order, but for Sensitive columns and those the recording ignores; nil for
// another kind of write or when none differ.
func (r ChangeRow) Diff() []FieldChange {
	if r.Op != OpUpdate || r.In == nil || r.Old == nil || r.New == nil {
		return nil
	}
	var out []FieldChange
	for i := range r.In.Columns {
		c := &r.In.Columns[i]
		if c.Sensitive || r.rec.ignores(r.In, i) {
			continue
		}
		if a, b := r.value(i, r.Old), r.value(i, r.New); !EqualValues(a, b) {
			out = append(out, FieldChange{Name: c.Name, Old: a, New: b})
		}
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
		if !r.rec.ignores(r.In, i) && !EqualValues(r.value(i, r.Old), r.value(i, r.New)) {
			return true
		}
	}
	return false
}

// batch is the Changes of one recorded write, a caller's call (an Exec, an
// Insert, a Rel.Add…), collected across its statements and handed to the
// sink in one call before the write's frame ends (flush): before the RELEASE
// of its savepoint in the caller's transaction, or the COMMIT of the
// transaction velox opened for it. own says velox opened that transaction.
// The handle of the write's frame, and those derived from it and of the
// savepoints within it, carry it (DB.batch), so that the write's parts join
// it; a handle Record returns carries none.
type batch struct {
	rows []ChangeRow
	own  bool
}

// len is the number of Changes collected; 0 for a nil b.
func (b *batch) len() int {
	if b == nil {
		return 0
	}
	return len(b.rows)
}

// truncate drops the Changes after the first n, those of a savepoint within
// the write that rolled back. A nil b holds none.
func (b *batch) truncate(n int) {
	if b != nil && n < len(b.rows) {
		clear(b.rows[n:])
		b.rows = b.rows[:n]
	}
}

// keptRow is a ChangeRow a Recorder kept in a transaction: its recording and
// the id Keep returned, for Drop on a rollback.
type keptRow struct {
	r  *Recording
	id uint64
}

// unkeep drops from their Recorders the rows kept in f's transaction after
// the first n, those of a transaction or savepoint that rolled back, and
// forgets them. f is the transaction's root frame.
func (f *txFrame) unkeep(n int) {
	if n >= len(f.kept) {
		return
	}
	for rest := f.kept[n:]; len(rest) > 0; {
		// One recording's ids at a time; a tx handle may switch recording.
		r, ids, others := rest[0].r, make([]uint64, 0, len(rest)), []keptRow(nil)
		for _, k := range rest {
			if k.r == r {
				ids = append(ids, k.id)
			} else {
				others = append(others, k)
			}
		}
		r.Drop(ids)
		rest = others
	}
	clear(f.kept[n:])
	f.kept = f.kept[:n]
}

// emit adds rows, the rows a write through db changed, to the write's batch:
// each update that changed a column the recording does not ignore, and every
// other row, in order. Their times are put in UTC first (utcTimes). The sink
// gets them when the write's frame ends (flush).
func emit(db *DB, rows []ChangeRow) error {
	r := db.rec
	if r == nil {
		return nil
	}
	b := db.batch
	if b == nil {
		return errors.New("velox: internal error: a recorded write reported its changes outside its transaction")
	}
	for _, row := range rows {
		row.rec = r
		utcTimes(row.In, row.Old)
		utcTimes(row.In, row.New)
		if row.Op == OpUpdate && !row.changed() {
			continue
		}
		b.rows = append(b.rows, row)
	}
	return nil
}

var (
	timeType    = reflect.TypeFor[time.Time]()
	timePtrType = reflect.TypeFor[*time.Time]()
)

// utcTimes puts each time.Time and *time.Time column of the row of in at p,
// a recorded row the engine owns (a row read back, or ownCopy of an entity),
// in UTC, its monotonic reading dropped, so that rows read and rows written
// marshal a time in one form. A *time.Time gets a new pointer: what the old
// one points to is left as it was. A nil p is left alone. A column of a
// named time type (type T time.Time) is not normalised.
func utcTimes(in *EntityInfo, p unsafe.Pointer) {
	if in == nil || p == nil {
		return
	}
	for i := range in.Columns {
		c := &in.Columns[i]
		switch GoType(*c) {
		case timeType:
			t := (*time.Time)(unsafe.Add(p, c.off))
			*t = t.UTC()
		case timePtrType:
			if t := (**time.Time)(unsafe.Add(p, c.off)); *t != nil {
				u := (*t).UTC()
				*t = &u
			}
		}
	}
}

// flush hands b's Changes, those of the write whose frame tx is, to tx's
// recording, before the frame's RELEASE or COMMIT: a Recorder keeps them,
// and the transaction notes their ids, to drop on a rollback; any other sink
// gets them in one call, with a handle on tx's transaction that does not
// record, so that what the sink writes through it joins the write's
// transaction and is not recorded. In a transaction the caller began on its
// own *sql.Tx (UseTx) a Recorder gets them as any sink does, as velox never
// sees that transaction end. A write that changed nothing calls no sink.
func (b *batch) flush(ctx context.Context, tx *DB) error {
	r := tx.rec
	if len(b.rows) == 0 || r == nil {
		return nil
	}
	if r.Keep != nil && !tx.txRoot.adopted {
		for _, row := range b.rows {
			tx.txRoot.kept = append(tx.txRoot.kept, keptRow{r: r, id: r.Keep(row)})
		}
		return nil
	}
	h := *tx
	h.rec, h.batch = nil, nil
	return r.Sink(ctx, &h, b.rows)
}

// batched is fn as the body of a recorded write's frame, own when velox
// opened its transaction: it gives the frame's handle a batch, runs fn, and
// flushes the batch. It is fn as it is when db does not record, or when a
// write of db is under way already (db.batch): fn's writes join its batch.
func batched(db *DB, own bool, fn func(ctx context.Context, tx *DB) error) func(ctx context.Context, tx *DB) error {
	if db.rec == nil || db.batch != nil {
		return fn
	}
	return func(ctx context.Context, tx *DB) error {
		b := &batch{own: own}
		tx.batch = b
		if err := fn(ctx, tx); err != nil {
			return err
		}
		return b.flush(ctx, tx)
	}
}

// ownTx runs fn on db when db is in a transaction, the caller's or velox's.
// Otherwise it runs fn in a transaction of its own; when db records, fn is
// one recorded write (batched), whose sink runs before the COMMIT, so that a
// sink's error rolls the write back. A recorded write in fn, in the caller's
// transaction, runs in a savepoint of its own (recordedTx).
//
// Before the BEGIN of a transaction for a recording handle it learns the
// server version (primeVersion), where a failure to read it cannot abort
// the transaction: the recorded writes in fn only look at it.
func ownTx(ctx context.Context, db *DB, fn func(ctx context.Context, tx *DB) error) error {
	return ownTxPrimed(ctx, db, false, fn)
}

// ownTxPrimed is ownTx; primed says the caller has asked the server version
// already, for this write, and ownTx does not ask again.
func ownTxPrimed(ctx context.Context, db *DB, primed bool, fn func(ctx context.Context, tx *DB) error) error {
	if db.InTx {
		return fn(ctx, db)
	}
	if db.rec == nil {
		return db.Tx(ctx, fn)
	}
	if !primed {
		primeVersion(ctx, db)
	}
	return db.Tx(ctx, batched(db, true, fn))
}

// inOwnTx reports whether db is in a transaction velox opened for a recorded
// write, which is under way.
func inOwnTx(db *DB) bool { return db.batch != nil && db.batch.own }

// recordedTx runs fn, a recorded write's statements and its emit, so that
// a write that fails took no effect and its sink runs before its frame ends.
// In the caller's transaction it runs fn in a savepoint: an error from a
// statement or from the sink rolls back to it. In a transaction velox opened
// for the write (ownTx) it runs fn there, as that transaction rolls back on
// an error. Outside a transaction it is ownTx.
func recordedTx(ctx context.Context, db *DB, fn func(ctx context.Context, tx *DB) error) error {
	if db.InTx && !inOwnTx(db) {
		return db.Tx(ctx, batched(db, false, fn))
	}
	// Its writes asked the server version already, when they needed it.
	return ownTxPrimed(ctx, db, true, fn)
}

// versionedTx is recordedTx for a recorded write whose path depends on the
// server version, *old (returnsOld, which in a transaction only looks at a
// version already known). In the caller's transaction, with the version not
// known yet, it asks the server inside the write's savepoint, before fn,
// and sets *old. A failed read rolls that savepoint back, so the caller's
// transaction stays usable, and fn runs on the locked-read path in a fresh
// savepoint. In a transaction velox opened, the version was asked before
// its BEGIN (primeVersion), and outside one returnsOld asked it.
func versionedTx(ctx context.Context, db *DB, old *bool, fn func(ctx context.Context, tx *DB) error) error {
	if *old || !db.InTx || inOwnTx(db) || !versionUnknown(ctx, db) {
		return recordedTx(ctx, db, fn)
	}
	var verr error
	err := recordedTx(ctx, db, func(ctx context.Context, tx *DB) error {
		v, err := loadVersion(ctx, tx)
		switch {
		case errors.Is(err, errVersionBusy): // nothing sent: the locked read
		case err != nil:
			verr = err
			return err
		default:
			*old = v >= returningOld
		}
		return fn(ctx, tx)
	})
	if verr != nil && errors.Is(err, verr) {
		*old = false
		return recordedTx(ctx, db, fn)
	}
	return err
}

// atomically runs fn in a savepoint when db is in a transaction, and else
// in a transaction of velox's own, as ownTx: in both, fn's writes commit or
// roll back together. Through a recording handle fn is one recorded write
// (batched): one sink call, before the RELEASE or the COMMIT, has the
// Changes of all of fn's statements.
func atomically(ctx context.Context, db *DB, fn func(ctx context.Context, tx *DB) error) error {
	if db.InTx {
		return db.Tx(ctx, batched(db, false, fn))
	}
	return ownTx(ctx, db, fn)
}
