package gqlimpl

import (
	"fmt"
	"reflect"
	"unsafe"
)

// The generated code hands a row to this package as a *E in an any, and gets
// one back the same way: the erasure to an unsafe.Pointer, which the untyped
// velox.Table takes, is here, and the generated code imports no unsafe. It
// types a result with a type assertion: a generic helper to do it would be
// compiled for each entity.

// rowOf is the row x holds, a *E of the entity E of c's Table, as a
// pointer; nil for a nil x. It panics on an x of another type, which only a
// bug in the generated code passes. With an invalid Table, whose
// statements fail, only x's kind is checked.
func rowOf(c *Type, x any) unsafe.Pointer {
	if x == nil {
		return nil
	}
	if t := c.table(); t.Err() == nil {
		// x holds a *E, which an interface keeps in its data word, with the
		// type *E in its type word: that of t.Ptr's.
		probe := t.Ptr(nil)
		w, pw := (*[2]unsafe.Pointer)(unsafe.Pointer(&x)), (*[2]unsafe.Pointer)(unsafe.Pointer(&probe))
		if w[0] == pw[0] {
			return w[1]
		}
		panic(fmt.Sprintf("gqlimpl: %s: a %T is not a row of %s", c.Name, x, t.Info().Name))
	}
	v := reflect.ValueOf(x)
	if v.Kind() != reflect.Pointer {
		panic(fmt.Sprintf("gqlimpl: %s: a %T is not a row", c.Name, x))
	}
	return v.UnsafePointer()
}

// nodeOf is p, a row of c's entity, as a *E in an any; nil for a nil p.
func nodeOf(c *Type, p unsafe.Pointer) any {
	if p == nil {
		return nil
	}
	return c.table().Ptr(p)
}

// nodesOf is ps, rows of c's entity, as a []*E in an any: the slice holds
// the same pointers. A nil ps is an empty slice.
func nodesOf(c *Type, ps []unsafe.Pointer) any {
	elem := reflect.TypeOf(c.table().Ptr(nil))
	out := reflect.MakeSlice(reflect.SliceOf(elem), len(ps), len(ps))
	if len(ps) > 0 {
		copy(unsafe.Slice((*unsafe.Pointer)(out.UnsafePointer()), len(ps)), ps)
	}
	return out.Interface()
}
