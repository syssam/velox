package veloxtest

import (
	"reflect"
	"unsafe"

	"github.com/syssam/velox"
)

// sourceType is the type of an entity's hidden field, which Equal ignores.
var sourceType = reflect.TypeFor[velox.Source]()

// Equal reports whether x and y are deeply equal, as reflect.DeepEqual
// does, except that it ignores the velox.Source every generated entity
// holds: the DB that read or wrote it, which its edge queries run on. Two
// entities read through different DBs, or one read and one built by hand,
// are Equal when their fields and loaded relations are:
//
//	got, err := db.Query[veloxgen.User]().Where(veloxgen.Users.ID.EQ(id)).One(ctx)
//	if !veloxtest.Equal(*got, veloxgen.User{ID: id, Name: "ada"}) { … }
//
// With go-cmp, cmpopts.IgnoreUnexported(veloxgen.User{}) or
// cmp.Comparer(func(a, b velox.Source) bool { return true }) does the same.
func Equal(x, y any) bool {
	if x == nil || y == nil {
		return x == y
	}
	vx, vy := reflect.ValueOf(x), reflect.ValueOf(y)
	if vx.Type() != vy.Type() {
		return false
	}
	return equal(vx, vy, map[visit]bool{})
}

// visit is a pair of pointers compared, as reflect.DeepEqual keeps them, so
// that a cycle ends.
type visit struct {
	x, y unsafe.Pointer
	typ  reflect.Type
}

// equal is Equal of two values of one type.
func equal(x, y reflect.Value, seen map[visit]bool) bool {
	if !x.IsValid() || !y.IsValid() {
		return x.IsValid() == y.IsValid()
	}
	if done, eq := visited(x, y, seen); done {
		return eq
	}
	switch x.Kind() {
	case reflect.Pointer, reflect.Interface:
		if x.Kind() == reflect.Interface && (x.IsNil() || y.IsNil()) {
			return x.IsNil() == y.IsNil()
		}
		if x.Kind() == reflect.Interface && x.Elem().Type() != y.Elem().Type() {
			return false
		}
		return equal(x.Elem(), y.Elem(), seen)
	case reflect.Struct:
		if x.Type() == sourceType {
			return true
		}
		for i := range x.NumField() {
			if !equal(x.Field(i), y.Field(i), seen) {
				return false
			}
		}
		return true
	case reflect.Slice, reflect.Array:
		if x.Len() != y.Len() {
			return false
		}
		for i := range x.Len() {
			if !equal(x.Index(i), y.Index(i), seen) {
				return false
			}
		}
		return true
	case reflect.Map:
		if x.Len() != y.Len() {
			return false
		}
		it := x.MapRange()
		for it.Next() {
			w := y.MapIndex(it.Key())
			if !w.IsValid() || !equal(it.Value(), w, seen) {
				return false
			}
		}
		return true
	case reflect.Func:
		return x.IsNil() && y.IsNil()
	}
	return equalScalar(x, y)
}

// visited settles a pointer, map or slice that is nil, of another length,
// or compared already, with done true and eq the answer; it marks the pair
// compared otherwise.
func visited(x, y reflect.Value, seen map[visit]bool) (done, eq bool) {
	switch x.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice:
	default:
		return false, false
	}
	if x.IsNil() || y.IsNil() {
		return true, x.IsNil() == y.IsNil()
	}
	if x.Kind() == reflect.Slice && x.Len() != y.Len() {
		return true, false
	}
	v := visit{x.UnsafePointer(), y.UnsafePointer(), x.Type()}
	if seen[v] {
		return true, true
	}
	seen[v] = true
	return false, false
}

// equalScalar is equal of two values of a kind that holds no other value.
func equalScalar(x, y reflect.Value) bool {
	switch x.Kind() {
	case reflect.Bool:
		return x.Bool() == y.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return x.Int() == y.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return x.Uint() == y.Uint()
	case reflect.Float32, reflect.Float64:
		return x.Float() == y.Float()
	case reflect.Complex64, reflect.Complex128:
		return x.Complex() == y.Complex()
	case reflect.String:
		return x.String() == y.String()
	case reflect.Chan, reflect.UnsafePointer:
		return x.Pointer() == y.Pointer()
	}
	return false
}
