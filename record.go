package velox

import (
	"context"
	"iter"
	"slices"
	"sync"
)

// Sink receives the Changes of the writes made through a recording handle,
// one call per Change, in order. An error from Record is the write's error.
type Sink interface {
	Record(ctx context.Context, c Change) error
}

// RecordFunc is a function as a Sink.
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
// that changes only these columns records no Change, and ChangedFields does
// not list them. OldValues and NewValues keep every column.
func RecordIgnore(columns ...string) RecordOption {
	return RecordOption{ignore: slices.Clone(columns)}
}
