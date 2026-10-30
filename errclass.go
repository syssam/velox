package velox

import (
	"reflect"

	"github.com/syssam/velox2/internal/engine"
)

// as is errors.As over walk: it returns the first error in err's tree of
// type T, or that an As method gives as T, which is not a nil pointer.
func as[T error](err error) (T, bool) {
	var found T
	ok := engine.Walk(err, func(e error) bool {
		if t, ok := e.(T); ok {
			found = t
			return true
		}
		x, ok := e.(interface{ As(any) bool })
		if !ok {
			return false
		}
		var t T
		if !x.As(&t) {
			return false
		}
		if v := reflect.ValueOf(t); !v.IsValid() || v.Kind() == reflect.Pointer && v.IsNil() {
			return false
		}
		found = t
		return true
	})
	return found, ok
}
