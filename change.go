package velox

import (
	"strings"
	"unsafe"

	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/veloxtype"
)

// Change is one row a recorded write changed: an insert, an update or a
// delete of a row of the entity named Entity, the Go type's name without its
// package ("Invoice"). Key is the row's primary key,
// before the write for an update or a delete and after it for an insert: the
// key column's value, or a []any of values for a composite key. Soft marks a
// soft delete, whose Op is OpDelete although the row was updated. The rows
// before and after the write are read with OldValues and NewValues, or typed
// with RowsOf.
type Change struct {
	Entity string
	Op     Op
	Key    any
	Soft   bool
	row    engine.ChangeRow
}

// changeOf is the Change of the engine's row r.
func changeOf(r engine.ChangeRow) Change {
	c := Change{Op: r.Op, Key: r.Key(), Soft: r.Soft, row: r}
	if r.In != nil {
		c.Entity = r.In.Name[strings.LastIndexByte(r.In.Name, '.')+1:] // "billing.Invoice" is "Invoice"
	}
	return c
}

// OldValues returns the row before the write by column name: plain values,
// as Mutation.Value gives them, nil for NULL. A Sensitive column's value is
// "***". It is nil for an insert.
func (c Change) OldValues() map[string]any { return c.row.OldValues() }

// NewValues returns the row after the write by column name, as OldValues
// does; an insert's includes what the database filled. It is nil for a
// delete.
func (c Change) NewValues() map[string]any { return c.row.NewValues() }

// ChangedFields returns an update's columns whose old and new values differ,
// in column order. Values compare by their Equal method when their type has
// one (time.Time, a decimal), as bytes when they are a byte slice, and else
// with reflect.DeepEqual. A Sensitive column's values are "***". It is nil
// for an insert or a delete.
func (c Change) ChangedFields() []FieldChange { return c.row.ChangedFields() }

// RowsOf returns c's rows as entity E: old before the write, nil for an
// insert, and cur after it, nil for a delete. Their values are real, a
// Sensitive column's included. ok is false, and both rows nil, when c is not
// a Change of E.
func RowsOf[E any, PE Entity[E]](c Change) (old, cur *E, ok bool) {
	if c.row.In == nil || c.row.In != stateOf(PE(nil).VeloxMeta()).Info {
		return nil, nil, false
	}
	return (*E)(c.row.Old), (*E)(c.row.New), true
}

// FieldChange is one column that differs between two rows, in a Change's
// ChangedFields or in Diff: its name and its old and new values, plain as
// Mutation.Value returns them.
type FieldChange = veloxtype.FieldChange

// Diff compares two rows of entity E column by column and returns the
// columns whose values differ, in column order. A nil row holds NULL in
// every column, so Diff(nil, x) lists x's non-NULL columns. Values compare
// as in Change.ChangedFields, but Diff shows a Sensitive column's values.
func Diff[E any, PE Entity[E]](before, after *E) []FieldChange {
	return engine.DiffRows(stateOf(PE(nil).VeloxMeta()).Info, unsafe.Pointer(before), unsafe.Pointer(after))
}
