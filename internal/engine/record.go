package engine

import (
	"context"
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

// Key is the row's primary key: before the write for an update or a delete,
// after it for an insert. It is the key column's value for a one-column key,
// a []any of the key columns' values for a composite key, and nil for an
// entity with no key.
func (r ChangeRow) Key() any {
	p := r.Old
	if r.Op == OpInsert {
		p = r.New
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
// column order; nil for another kind of write or when none differ.
func (r ChangeRow) ChangedFields() []FieldChange {
	if r.Op != OpUpdate || r.In == nil || r.Old == nil || r.New == nil {
		return nil
	}
	var out []FieldChange
	for i := range r.In.Columns {
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
