package velox

import (
	"unsafe"

	"github.com/syssam/velox/internal/engine"
)

// Named is the handle of a column of entity E whose Go field is of type V,
// found by the column's name with Field. It is for code that cannot name
// the generated handles, such as a mixin's Policy, Hooks or Interceptors,
// which serve many entities. V is the field's type as velox-gen writes it:
// int64, or *time.Time for a Nillable time.
//
// It is a Column[E], so Columns, Omit, Save and OnConflict take it. Its
// zero value names no column: a condition made from it fails the
// statement, and its use as a column, Set and Ptr panic.
type Named[E, V any] struct {
	i     int32 // the column's index plus one; 0 in the zero value
	flags uint8
	off   uintptr // the field's offset in E
}

// The flags of a Named.
const (
	namedNullable uint8 = 1 << iota
	namedImmutable
	namedPointer
)

// Field returns the handle of entity E's column name, whose Go field is of
// type V. The type must be exact: a column that holds NULL through a
// pointer field takes a pointer V, and one whose field is not a pointer
// takes no pointer.
//
// It panics when E is not an entity, when E has no such column, or when the
// column's field is not a V. Called from a Policy, Hooks or Interceptors
// function, which velox calls while it builds E's metadata, the panic is
// E's *SchemaError at its first use, so a wrong name or type is never
// skipped. That call does not wait on the build in progress: velox records
// E's columns before it calls those functions. Called anywhere else, it
// builds E's metadata first when no statement has.
func Field[E, V any](name string) Named[E, V] {
	r, err := engine.FieldByName(engine.ElemType((*E)(nil)), engine.ElemType((*V)(nil)), name, func() error {
		_, err := metaOf[E]()
		return err
	})
	if err != nil {
		panic(err)
	}
	c := Named[E, V]{i: r.Col + 1, off: r.Off}
	if r.Nullable {
		c.flags |= namedNullable
	}
	if r.Immutable {
		c.flags |= namedImmutable
	}
	if r.Pointer {
		c.flags |= namedPointer
	}
	return c
}

func (c Named[E, V]) column(*E) int32 {
	if c.i == 0 {
		panic(engine.ZeroField)
	}
	return c.i - 1
}

// EQ is column = v. For a pointer V, EQ(nil) is column IS NULL, and any other
// pointer compares the value it points to.
func (c Named[E, V]) EQ[_ Lazy[V]](v V) Pred[E] {
	return Pred[E](engine.NewPredicate(engine.FieldCmp(engine.OpEQ, c.i-1, c.flags&namedPointer != 0, v)))
}

// NEQ is column <> v. For a pointer V, NEQ(nil) is column IS NOT NULL, and
// any other pointer compares the value it points to.
func (c Named[E, V]) NEQ[_ Lazy[V]](v V) Pred[E] {
	return Pred[E](engine.NewPredicate(engine.FieldCmp(engine.OpNEQ, c.i-1, c.flags&namedPointer != 0, v)))
}

// In is column IN (vs...). With no values it is false. For a pointer V, each
// pointer compares the value it points to, and a nil one fails the
// statement: use IsNull.
func (c Named[E, V]) In[_ Lazy[V]](vs ...V) Pred[E] {
	return Pred[E](engine.NewPredicate(engine.FieldIn(c.i-1, c.flags&namedPointer != 0, list(vs))))
}

// IsNull is column IS NULL. On a column that cannot hold NULL it fails the
// statement.
func (c Named[E, V]) IsNull[_ Lazy[V]]() Pred[E] {
	return Pred[E](engine.NewPredicate(engine.FieldNull(engine.OpIsNull, c.i-1, c.flags&namedNullable != 0)))
}

// NotNull is column IS NOT NULL. On a column that cannot hold NULL it fails
// the statement.
func (c Named[E, V]) NotNull[_ Lazy[V]]() Pred[E] {
	return Pred[E](engine.NewPredicate(engine.FieldNull(engine.OpNotNull, c.i-1, c.flags&namedNullable != 0)))
}

// Set sets the column to v. For a pointer V, a nil v sets NULL. It panics on
// the zero Named.
func (c Named[E, V]) Set[_ Lazy[V]](v V) Setter[E] {
	return Setter[E]{engine.FieldSet(c.i-1, c.flags&namedPointer != 0, v)}
}

// Ptr returns the address of x's field of the column. It panics on the
// zero Named.
func (c Named[E, V]) Ptr[_ Lazy[V]](x *E) *V {
	if c.i == 0 {
		panic(engine.ZeroField)
	}
	return (*V)(unsafe.Add(unsafe.Pointer(x), c.off))
}

// Immutable reports whether an update may not assign the column.
func (c Named[E, V]) Immutable[_ Lazy[V]]() bool { return c.flags&namedImmutable != 0 }

// Nullable reports whether the column holds NULL.
func (c Named[E, V]) Nullable[_ Lazy[V]]() bool { return c.flags&namedNullable != 0 }
