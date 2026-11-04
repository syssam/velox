package engine

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
)

// layouts holds each entity's EntityInfo, keyed by the entity's reflect.Type.
// buildDescriptor stores it once the columns are known, before Load binds
// the entity's policy, hooks and interceptors. So a column found by name
// inside that binding does not wait on the build in progress. Only the
// columns are read from it, and they do not change once stored.
var layouts sync.Map

// unlinked holds, as keys, the reflect.Types of the entities whose last
// build panicked with a NotLinkedError before their layout was stored.
var unlinked sync.Map

// publishLayout stores info as entity t's layout. A later build of t, a
// retry after a NotLinkedError, replaces it with columns of the same shape.
func publishLayout(t reflect.Type, info *EntityInfo) {
	layouts.Store(t, info)
	unlinked.Delete(t)
}

// FieldRef is a column of an entity found by its name, for velox.Field.
type FieldRef struct {
	Col       int32   // the column's index
	Off       uintptr // the field's offset in the entity
	Nullable  bool    // the column holds NULL
	Immutable bool    // an update may not assign the column
	Pointer   bool    // the field is a pointer
}

// FieldByName finds entity typ's column name, whose field must be of the Go
// type v. typ and v are reflect.Types held as any (ElemType). When typ's
// layout is not stored yet, build builds the entity's metadata. A failure
// that a NotLinkedError caused is returned as a NotLinkedError, so that a
// build that calls FieldByName is retried too.
//
// It is not generic, so that velox.Field, compiled where it is called, holds
// no reflect.Type.
func FieldByName(typ, v any, name string, build func() error) (FieldRef, error) {
	t, vt := rtype(typ), rtype(v)
	x, ok := layouts.Load(t)
	if !ok {
		if err := build(); err != nil {
			if _, retry := unlinked.Load(t); retry {
				return FieldRef{}, NotLinkedError(err.Error())
			}
			return FieldRef{}, err
		}
		if x, ok = layouts.Load(t); !ok {
			return FieldRef{}, fmt.Errorf("velox: %v has no metadata layout: its metadata was not built by MetaOnce.Load", t)
		}
	}
	info := x.(*EntityInfo)
	i, ok := info.Column(name)
	if !ok {
		return FieldRef{}, &SchemaError{Entity: info.Name, Reason: fmt.Sprintf("Field: no column is named %q", name)}
	}
	c := &info.Columns[i]
	if ft := rtype(c.Type); ft != vt {
		return FieldRef{}, &SchemaError{Entity: info.Name, Field: c.Field,
			Reason: fmt.Sprintf("Field: column %q is of type %v, not %v", name, ft, vt)}
	}
	return FieldRef{Col: int32(i), Off: c.off, Nullable: c.Nullable, Immutable: c.Immutable,
		Pointer: vt.Kind() == reflect.Pointer}, nil
}

// ZeroField is what the zero velox.Named panics with where it cannot fail
// the statement instead: as a column, in an assignment, and in Ptr.
const ZeroField = "velox: the zero Named names no column: make it with Field"

// zeroField is the invalid condition of the zero velox.Named, whose col is
// -1.
func zeroField() Node { return Node{Op: OpInvalid, V: errors.New(ZeroField)} }

// FieldCmp is the condition column col o v, o OpEQ or OpNEQ, for a
// handle found by name. When the field is a pointer (ptr), v is one: nil
// is IS NULL for OpEQ and IS NOT NULL for OpNEQ, and any other pointer is
// compared by the value it points to.
func FieldCmp(o op, col int32, ptr bool, v any) Node {
	if col < 0 {
		return zeroField()
	}
	if ptr {
		rv := reflect.ValueOf(v)
		if rv.IsNil() {
			if o == OpEQ {
				return Node{Op: OpIsNull, Col: col}
			}
			return Node{Op: OpNotNull, Col: col}
		}
		v = rv.Elem().Interface()
	}
	return Node{Op: o, Col: col, V: v}
}

// FieldIn is the condition column col IN (vs...) for a handle found by
// name. When the field is a pointer (ptr), each v is compared by the value
// it points to, and a nil pointer makes the condition invalid: a NULL never
// equals anything, so it is IsNull's.
func FieldIn(col int32, ptr bool, vs []any) Node {
	if col < 0 {
		return zeroField()
	}
	if ptr {
		for i, v := range vs {
			rv := reflect.ValueOf(v)
			if rv.IsNil() {
				return Node{Op: OpInvalid, V: fmt.Errorf("velox: In was given a nil pointer at %d; use IsNull", i)}
			}
			vs[i] = rv.Elem().Interface()
		}
	}
	return Node{Op: OpIn, Col: col, V: vs}
}

// FieldNull is the condition column col IS NULL (OpIsNull) or IS NOT NULL
// (OpNotNull) for a handle found by name. On a column that cannot hold NULL
// it is invalid, which fails the statement: such a condition would be
// constant.
func FieldNull(o op, col int32, nullable bool) Node {
	if col < 0 {
		return zeroField()
	}
	if !nullable {
		return Node{Op: OpInvalid, V: fmt.Errorf("velox: IsNull or NotNull on column %d, which cannot be NULL", col)}
	}
	return Node{Op: o, Col: col}
}

// FieldSet is the assignment of v to column col for a handle found by name.
// When the field is a pointer (ptr), a nil v sets NULL and any other sets
// the value it points to. It panics with ZeroField when col is -1.
func FieldSet(col int32, ptr bool, v any) Assignment {
	if col < 0 {
		panic(ZeroField)
	}
	if ptr {
		rv := reflect.ValueOf(v)
		if rv.IsNil() {
			return Assignment{Column: int(col), Op: AssignSet}
		}
		v = rv.Elem().Interface()
	}
	return Assignment{Column: int(col), Op: AssignSet, Value: v}
}
