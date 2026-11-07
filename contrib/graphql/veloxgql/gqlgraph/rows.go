package gqlgraph

import "reflect"

// A row is a *E in an any, as untyped.Table takes and returns it: the
// generated code hands one to this package so, and gets one back so. velox
// checks it is of the Table's entity wherever it is used. The generated code
// types a result with a type assertion: a generic helper to do it would be
// compiled for each entity.

// rowOf is x, a row, or nil for no row: a nil x or a nil pointer.
func rowOf(x any) any {
	if x == nil {
		return nil
	}
	if v := reflect.ValueOf(x); v.Kind() == reflect.Pointer && v.IsNil() {
		return nil
	}
	return x
}

// nodesOf is ps, rows of c's entity, as a []*E in an any: the slice holds
// the same pointers. A nil ps is an empty slice. It is nil when c's Table is
// invalid.
func nodesOf(c *Type, ps []any) any {
	var elem reflect.Type
	if len(ps) > 0 {
		elem = reflect.TypeOf(ps[0])
	} else if x := c.table().New(); x != nil {
		elem = reflect.TypeOf(x)
	} else {
		return nil
	}
	out := reflect.MakeSlice(reflect.SliceOf(elem), len(ps), len(ps))
	for i, p := range ps {
		out.Index(i).Set(reflect.ValueOf(p))
	}
	return out.Interface()
}
