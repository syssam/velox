package velox

import (
	"context"
	"iter"
	"slices"
	"sync"

	"github.com/syssam/velox/internal/engine"
)

// Sink receives the Changes of the writes made through a recording handle,
// one call per Change, in order. An error from Record is the write's error.
//
// A sink that writes, an audit row say, must write with the ctx it is given,
// or through a handle that does not record: a write through the recording
// handle under that ctx is not recorded, but one under another context is,
// and its Change comes back to the sink.
//
// An upsert's old rows, and on MySQL its new rows, are read without the
// scope or the Read policy (see DB.Record), so a sink can see values the
// caller's policy hides.
type Sink interface {
	Record(ctx context.Context, c Change) error
}

// RecordFunc is a function as a Sink. Like any Sink, a RecordFunc that
// writes must write with the ctx it is given (see Sink).
type RecordFunc func(ctx context.Context, c Change) error

// Record calls f.
func (f RecordFunc) Record(ctx context.Context, c Change) error { return f(ctx, c) }

// Recorder is a Sink that keeps the Changes it receives, in order. Its zero
// value is ready, and it is safe for concurrent use.
type Recorder struct {
	mu      sync.Mutex
	changes []Change
}

// Record keeps c. It never fails.
func (r *Recorder) Record(_ context.Context, c Change) error {
	r.mu.Lock()
	r.changes = append(r.changes, c)
	r.mu.Unlock()
	return nil
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
	r.changes = nil
	r.mu.Unlock()
}

// mark is the number of Changes kept, for truncate to return to.
func (r *Recorder) mark() int { return r.Len() }

// truncate drops the Changes kept after the first n.
func (r *Recorder) truncate(n int) {
	r.mu.Lock()
	if n < len(r.changes) {
		clear(r.changes[n:])
		r.changes = r.changes[:n]
	}
	r.mu.Unlock()
}

// RecordOption configures a recording handle.
type RecordOption struct {
	ignore []string
}

// RecordIgnore names columns an update's comparison leaves out: an update
// that changes only these columns records no Change, and Change.Diff does
// not list them. Old, New, Changed, OldRow and NewRow still read them.
func RecordIgnore(columns ...string) RecordOption {
	return RecordOption{ignore: slices.Clone(columns)}
}

// Record returns a DB like db whose writes are reported to s, one Change per
// row, after the write's statements succeed. It records the writes made
// through it, through the transactions and savepoints it begins, through its
// WithoutPolicy, and through the Mutation.DB of a hook on one of its writes;
// raw SQL, dry runs and the writes of db itself are not recorded. Record on a
// recording DB replaces its sink and its options.
//
// Inside a transaction a recorded write runs under a savepoint that holds
// its statements and the sink's calls: a failed statement or a sink's error
// rolls back to it, so a recorded write that returns an error took no
// effect. Outside one it runs in a transaction of its own, but for an
// insert of one statement, which commits by itself, and the sink is called
// after the COMMIT: a sink's error is returned and undoes nothing.
// The sink's writes are recorded only when made under another context than
// the one it was given (see Sink).
//
// A recorded upsert reads the rows its rows conflict with, locked, before
// its INSERT, without the scope or the Read policy, as the database finds a
// conflict whatever they hide: a sink can see values the caller's policy
// hides. The database names the rows the upsert touched: on PostgreSQL and
// SQLite the INSERT returns every column of them (a row DoNothing kept is
// not returned, and not reported); on MySQL a read by the conflict columns
// follows, and a count of affected rows it does not account for fails the
// write, as when a row conflicted on another unique key (this needs the
// clientFoundRows=true that Open asks for). A row with NULL in a conflict
// column, or a generated key left zero in the conflict columns, cannot be
// found after the INSERT on MySQL, which returns no keys, so a recorded
// upsert there refuses it: insert such rows without OnConflict. A Rewrite
// hook may not change the RETURNING of a recorded insert or upsert. Below PostgreSQL 18 the
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
	r := &engine.Recording{Sink: func(ctx context.Context, row engine.ChangeRow) error {
		return s.Record(ctx, changeOf(row))
	}}
	for _, o := range opts {
		r.Ignore = append(r.Ignore, o.ignore...)
	}
	if rec, ok := s.(*Recorder); ok {
		r.Mark, r.Truncate = rec.mark, rec.truncate
	}
	return pub(db.e.Record(r))
}
