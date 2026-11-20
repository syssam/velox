package velox

import (
	"iter"
	"unsafe"

	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/veloxtype"
)

// Change is one row a write changed: its key, and each tracked column's old
// and new values. Mutation.Changes yields it; Changed lists the columns that
// differ.
type Change = veloxtype.Change

// FieldChange is one column of a Change, or of Diff: its name, its old and
// new values, plain as Mutation.Value returns them, and whether they differ.
type FieldChange = veloxtype.FieldChange

// Track asks velox to read the rows an update or a delete will change,
// before it changes them, so that Changes can compare old and new values. A
// hook calls it before next. It names the columns to compare. With no name
// it compares the columns the update assigns when next runs: the caller's,
// the hooks', the UpdateDefault and Version columns. On a delete it then
// compares every column. Track on an insert, after next, or with a name the
// entity has no column of is a misuse, recorded and returned.
//
// A tracked write runs in one transaction: the caller's inside db.Tx, else
// one of its own. velox reads the key and the tracked columns of the rows
// the statement matches, under its interceptors and policies, in key order,
// locked with FOR UPDATE except on SQLite. It sends the statement with its
// conditions and the keys it read, so that it changes exactly those rows. An
// update's new values come back with RETURNING on PostgreSQL and SQLite, and
// from a SELECT by key on MySQL. Long key lists are split under the
// dialect's parameter limit.
//
// The read is bounded by the DB's MaxRows: a tracked write of more rows
// fails with ErrTooManyRows before anything is written. Under SQL(ctx)
// Track reads nothing, and the statement renders as it would untracked. A
// write a hook makes itself (SetAffected), or skips, is not read.
func (m *Mutation) Track(names ...string) error { return engine.TrackByName(&m.mu, names...) }

// Changes returns, once next has returned without error, one Change per row
// the write changed. An update or a delete yields the rows Track read, in
// key order, with the tracked columns; without Track it yields none. An
// insert yields each inserted row, with every column, its old value nil and
// its key as written. Before next, after an error, under SQL(ctx), and when
// a hook skipped next, it yields none.
func (m *Mutation) Changes() iter.Seq[Change] { return engine.ChangesOf(&m.mu) }

// Diff compares two rows of entity E column by column and returns the
// columns whose values differ, in column order. A nil row holds NULL in
// every column, so Diff(nil, x) lists x's non-NULL columns. Values compare
// by their Equal method when their type has one (time.Time, a decimal), as
// bytes when they are a byte slice (json.RawMessage), and else with
// reflect.DeepEqual. A Change's Changed uses the same rule.
func Diff[E any, PE Entity[E]](before, after *E) []FieldChange {
	return engine.DiffRows(stateOf(PE(nil).VeloxMeta()).Info, unsafe.Pointer(before), unsafe.Pointer(after))
}
