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

// Record returns a DB like db whose writes are reported to s, one Change per
// row, all of a write's in one call, after the write's statements succeed
// and before its transaction commits (see Sink). It records the writes made
// through it, through the transactions and savepoints it begins, through its
// WithoutPolicy, and through the Mutation.DB of a hook on one of its writes;
// raw SQL, dry runs and the writes of db itself are not recorded. Record on a
// recording DB replaces its sink and its options; on a transaction's handle,
// for that handle, and a rollback still drops a Recorder's Changes.
//
// Inside a transaction a recorded write runs under a savepoint that holds
// its statements and the sink's call: a failed statement or a sink's error
// rolls back to it, so a recorded write that returns an error took no
// effect. Outside one it runs in a transaction of its own, a single
// statement's too, and the sink is called before the COMMIT: a sink's error
// rolls the write back. Code that acts after the commit reads a Recorder
// once Tx returns.
//
// velox never sees the end of a transaction the caller began on its own
// *sql.Tx (UseTx): there the Changes go to the sink, a Recorder's too, and
// none is dropped when that transaction rolls back, so a Recorder used
// through UseTx holds every Change delivered; what a sink writes through
// its tx there rolls back with the transaction.
//
// Below PostgreSQL 18, and on SQLite and MySQL, a recorded update or delete
// first reads its rows, locked, then writes them by key. A delete there
// reports only the rows the database deleted: it returns their keys
// (RETURNING), and on MySQL a count of rows short of those read fails the
// write. On PostgreSQL 18
// and later it is one statement, RETURNING the rows before and after it
// (old and new), and so is an upsert, which reads nothing first. The
// recording handle asks the server its version (SHOW server_version_num,
// which no hook sees) once per pool, at its first recorded write: before
// the BEGIN of a transaction of its own, or in the caller's transaction
// inside a savepoint (the write's), so that a failure to read it rolls back
// to the savepoint and leaves the transaction usable; such a write then takes
// the locked read, and a later one asks again. A count of rows over
// WithMaxRows rolls the write back. An update's or a
// delete's Changes come in key order as Go compares the key values: the
// database's order for numeric and UUID keys, but a text key under a
// collation other than C may order otherwise. An upsert's Changes come in
// the order the database returns its rows.
//
// Below PostgreSQL 18 a recorded upsert reads the rows its rows conflict
// with, locked, before its INSERT, without the scope or the Read policy, as the database finds a
// conflict whatever they hide: a sink can see values the caller's policy
// hides. The database names the rows the upsert touched: on PostgreSQL and
// SQLite the INSERT returns every column of them (a row DoNothing kept is
// not returned, and not reported); on MySQL a read by the conflict columns
// follows, and a count of affected rows it does not account for fails the
// write, as when a row conflicted on another unique key (this needs the
// clientFoundRows=true that Connect requires). A row with NULL in a conflict
// column, or a generated key left zero in the conflict columns, cannot be
// found after the INSERT on MySQL, which returns no keys, so a recorded
// upsert there refuses it: insert such rows without OnConflict. A Rewrite
// hook may not change the RETURNING of a recorded write. Below PostgreSQL 18 the
// first read cannot lock a row that is not there, so a row another session
// inserts under the same key at once is reported as an insert although the
// upsert updated it. On MySQL that read takes gap locks: two recorded
// upserts of new keys in the same gap, at once, can deadlock, and one fails
// with ER_LOCK_DEADLOCK, which TxRetrySerializable retries. Rows of one
// InsertAll that share a conflict key give one Change per stored row (on
// MySQL, the count fails the write).
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
