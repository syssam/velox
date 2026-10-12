package gqlinput

import (
	"math"
	"reflect"
)

// IDAs returns id as a T: the id as it is when it is one, and an integer of
// another integer type converted when T holds its value. Anything else is
// not a T (a string is never a number, nor a number a string: "1" and 1 are
// the ids of different rows).
//
// It is for an id whose type is not known where it arrives, the argument of
// a GraphQL node(id:) field, against the id type of each entity in turn.
func IDAs[T any](id any) (T, bool) {
	if v, ok := id.(T); ok {
		return v, true
	}
	var zero T
	from, to := reflect.ValueOf(id), reflect.ValueOf(&zero).Elem()
	if !from.IsValid() {
		return zero, false
	}
	switch {
	case from.CanInt() && to.CanInt():
		if to.OverflowInt(from.Int()) {
			return zero, false
		}
		to.SetInt(from.Int())
	case from.CanInt() && to.CanUint():
		u := uint64(from.Int()) //nolint:gosec // a negative id is refused on the next line
		if from.Int() < 0 || to.OverflowUint(u) {
			return zero, false
		}
		to.SetUint(u)
	case from.CanUint() && to.CanUint():
		if to.OverflowUint(from.Uint()) {
			return zero, false
		}
		to.SetUint(from.Uint())
	case from.Kind() == reflect.String && to.Kind() == reflect.String:
		// An id type declared on string, and a plain string.
		to.SetString(from.String())
	case from.CanUint() && to.CanInt():
		i := int64(from.Uint()) //nolint:gosec // an id above MaxInt64 is refused on the next line
		if from.Uint() > math.MaxInt64 || to.OverflowInt(i) {
			return zero, false
		}
		to.SetInt(i)
	default:
		return zero, false
	}
	return zero, true
}
