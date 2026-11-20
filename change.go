package velox

import (
	"strings"
	"unsafe"

	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/veloxtype"
)

// Change is one row a recorded write changed: an insert, an update or a
// delete of a row of the entity named Entity, the Go type's name without its
// package ("Invoice"). Key is the row's primary key, after the write for an
// insert or an update (an update that assigns the key reports the new one;
// the key handle's Old gives the old) and before it for a delete: the key
// column's value, or a []any of values for a composite key. Soft marks a
// soft delete, whose Op is OpDelete although the row was updated.
//
// The rows before and after the write are read typed, a column's with its
// handle's Old and New (I.Total.Old(c)) and with Changed, or whole with
// RowsOf, or for any entity with OldRow, NewRow and Diff. Their values are plain Go values, as
// Mutation.Value gives them, and real: a Sensitive column's included, as it
// is the caller's data, as a struct field is. Sensitive keeps its meaning,
// not printable and not serializable: OldRow and NewRow marshal without it,
// as the entity's JSON does, and Diff leaves it out.
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

// Changed reports whether an update's old and new values differ in any of
// cols, compared as Diff compares them, a Sensitive or RecordIgnore column's
// included. It is false for an insert or a delete, and for another entity's
// handles.
func (c Change) Changed[E any](cols ...Column[E]) bool {
	if _, ok := c.row.Row(c.row.New).(*E); !ok {
		return false
	}
	for _, col := range cols {
		if c.row.Differs(int(col.column(nil))) {
			return true
		}
	}
	return false
}

// changeValue is column col of ch's row at p, Old or New, as a V; the zero
// V when p is nil, the column holds NULL, or ch is not of entity E. For the
// handles' Old and New.
func changeValue[E, V any](ch Change, p unsafe.Pointer, col int32) V {
	var v V
	if _, ok := ch.row.Row(p).(*E); !ok {
		return v
	}
	v, _ = ch.row.Value(int(col), p).(V)
	return v
}

// OldRow returns the row before the write, an *E of c's entity; nil for an
// insert. Its Sensitive fields marshal to nothing, as the entity's JSON tags
// say (json:"-").
func (c Change) OldRow() any { return c.row.Row(c.row.Old) }

// NewRow returns the row after the write, as OldRow does; nil for a delete.
func (c Change) NewRow() any { return c.row.Row(c.row.New) }

// Diff returns an update's columns whose old and new values differ, in
// column order, but neither Sensitive columns nor those the recording
// ignores: those RecordIgnore names, and those an UpdateDefault fills unless
// RecordKeep names them.
// Values compare by their Equal method when their type has one (time.Time,
// a decimal), as bytes when they are a byte slice, and else with
// reflect.DeepEqual. It is nil for an insert or a delete.
func (c Change) Diff() []FieldChange { return c.row.Diff() }

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

// FieldChange is one column that differs between two rows, in Change.Diff
// or in Diff: its name and its old and new values, plain as
// Mutation.Value returns them.
type FieldChange = veloxtype.FieldChange

// Diff compares two rows of entity E column by column and returns the
// columns whose values differ, in column order. A nil row holds NULL in
// every column, so Diff(nil, x) lists x's non-NULL columns. Values compare
// as in Change.Diff, but Diff lists a Sensitive column, with its values.
func Diff[E any, PE Entity[E]](before, after *E) []FieldChange {
	return engine.DiffRows(stateOf(PE(nil).VeloxMeta()).Info, unsafe.Pointer(before), unsafe.Pointer(after))
}
