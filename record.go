package velox

import (
	"context"
	"iter"
	"slices"
	"sync"

	"github.com/syssam/velox/internal/engine"
)

// Sink receives the Changes of the writes made through a recording handle:
// one call per write (an Exec, a One, an Insert, an InsertAll, a Save, the
// edge writes of a Rel.Add…) with every Change of that write, in order, and
// no call for a write that changed nothing. It runs inside the write's
// transaction, before its COMMIT, or before the RELEASE of the write's
// savepoint in the caller's transaction: an error from Record is the
// write's error, and rolls the write back.
//
// tx is a handle on the write's transaction that does not record: what the
// sink writes through it, audit rows say, commits or rolls back with the
// write, and is not itself recorded. It is valid while that transaction is.
//
// An upsert's old rows, and on MySQL its new rows, are read without the
// scope or the Read policy (see DB.Record), so a sink can see values the
// caller's policy hides.
type Sink interface {
	Record(ctx context.Context, tx *DB, changes []Change) error
}

// RecordFunc is a function as a Sink.
type RecordFunc func(ctx context.Context, tx *DB, changes []Change) error

// Record calls f.
func (f RecordFunc) Record(ctx context.Context, tx *DB, changes []Change) error {
	return f(ctx, tx, changes)
}

// Recorder is a Sink that keeps the Changes it receives, in order. Its zero
// value is ready, and it is safe for concurrent use.
//
// A Recorder drops the Changes recorded in a transaction or savepoint that
// rolls back, and in an attempt of TxRetrySerializable before its retry, so
// after Tx returns it holds what committed. Shared by concurrent
// transactions, it drops only the Changes of the one that rolls back, and
// until the others commit or roll back, All and Len include their Changes.
type Recorder struct {
	mu      sync.Mutex
	changes []Change
	ids     []uint64 // each Change's id, ascending: for drop
	last    uint64   // the last id given
}

// Record keeps changes; it ignores tx and never fails.
func (r *Recorder) Record(_ context.Context, _ *DB, changes []Change) error {
	for _, c := range changes {
		r.keep(c)
	}
	return nil
}

// keep keeps c and returns its id, which grows with each Change kept.
func (r *Recorder) keep(c Change) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last++
	r.changes = append(r.changes, c)
	r.ids = append(r.ids, r.last)
	return r.last
}

// drop drops the Changes of ids, ascending, those of a transaction or
// savepoint that rolled back; an id no longer kept (after Reset) is skipped.
func (r *Recorder) drop(ids []uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, j := 0, 0
	for i, id := range r.ids {
		for j < len(ids) && ids[j] < id {
			j++
		}
		if j < len(ids) && ids[j] == id {
			continue
		}
		r.changes[n], r.ids[n] = r.changes[i], id
		n++
	}
	clear(r.changes[n:])
	r.changes, r.ids = r.changes[:n], r.ids[:n]
}

// All returns the Changes kept when All is called, in order. A Change
// recorded later, during the iteration among them, is not yielded.
func (r *Recorder) All() iter.Seq[Change] {
	r.mu.Lock()
	all := slices.Clone(r.changes)
	r.mu.Unlock()
	return slices.Values(all)
}

// Len returns the number of Changes kept.
func (r *Recorder) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.changes)
}

// Reset drops the Changes kept.
func (r *Recorder) Reset() {
	r.mu.Lock()
	r.changes, r.ids = nil, nil
	r.mu.Unlock()
}

// RecordOption configures a recording handle.
type RecordOption struct {
	owner func() (*EntityInfo, error)
	cols  []int
	keep  bool
}

// RecordIgnore names columns of entity E an update's comparison leaves out:
// an update that changes only these columns records no Change, and
// Change.Diff does not list them. Old, New, Changed, OldRow and NewRow still
// read them. A column an UpdateDefault fills (updated_at) is left out
// without it; RecordKeep takes it back in. For several entities, pass one
// RecordIgnore per entity.
func RecordIgnore[E any](cols ...Column[E]) RecordOption {
	return RecordOption{owner: ownerOf[E], cols: columnIndexes(cols)}
}

// RecordKeep names columns of entity E filled by an UpdateDefault that an
// update's comparison takes in: without it such a column (updated_at) is
// left out, as RecordIgnore leaves a column out, so an update that only
// bumps it records no Change. RecordIgnore wins over RecordKeep.
func RecordKeep[E any](cols ...Column[E]) RecordOption {
	return RecordOption{owner: ownerOf[E], cols: columnIndexes(cols), keep: true}
}

// Record returns a DB like db whose writes report each row they change to
// s: one Change per row, all of a write's in one call, inside the write's
// transaction before it commits (see Sink). Inside a transaction a recorded
// write runs under a savepoint, outside one in a transaction of its own; a
// failed statement or a sink's error rolls it back, so a recorded write
// that returns an error took no effect. Code that acts after the commit
// reads a Recorder once Tx returns.
//
// It records the writes made through it, through its transactions and
// savepoints, its WithoutPolicy and the Mutation.DB of a hook on one of its
// writes; raw SQL, dry runs and the writes of db itself are not recorded.
// Record on a recording DB replaces its sink and its options. velox never
// sees a UseTx transaction end, so a Recorder used through UseTx keeps the
// Changes of one that rolls back.
//
// Below PostgreSQL 18, and on SQLite and MySQL, a recorded update, delete
// or upsert reads the rows it touches, locked, in the same transaction; on
// PostgreSQL 18 and later each is one statement that returns its rows
// before and after it. An upsert's conflicting rows are read without the
// scope or the Read policy, so a sink can see values the caller cannot. A
// recorded write that cannot account for every row it touched fails. The
// guide's "Recording changes" (docs/guide.md) says what is recorded and
// what it costs; ADR 0094 (docs/decisions/0094-change-recording.md) has
// the rules.
//
// The DB it returns shares db's pool and options, and Close on it closes
// nothing. Record panics when s is nil.
func (db *DB) Record(s Sink, opts ...RecordOption) *DB {
	if s == nil {
		panic("velox: Record: the Sink is nil")
	}
	r := &engine.Recording{Sink: func(ctx context.Context, tx *engine.DB, rows []engine.ChangeRow) error {
		changes := make([]Change, len(rows))
		for i, row := range rows {
			changes[i] = changeOf(row)
		}
		return s.Record(ctx, pub(tx), changes)
	}}
	for _, o := range opts {
		in, err := o.owner()
		if err != nil {
			panic("velox: Record: " + err.Error())
		}
		m := &r.Ignore
		if o.keep {
			m = &r.Include
		}
		if *m == nil {
			*m = map[*EntityInfo][]int{}
		}
		(*m)[in] = append((*m)[in], o.cols...)
	}
	if rec, ok := s.(*Recorder); ok {
		r.Keep = func(row engine.ChangeRow) uint64 { return rec.keep(changeOf(row)) }
		r.Drop = rec.drop
	}
	return pub(db.e.Record(r))
}
