package engine

import (
	"reflect"
	"testing"
)

// TestInterfaceWords: typeWord and dataWord read an interface as reflect
// does, for a pointer, a slice, a nil pointer and nil.
func TestInterfaceWords(t *testing.T) {
	x := 7
	s := []int{1, 2}
	for _, v := range []any{&x, s, (*int)(nil)} {
		rv := reflect.ValueOf(v)
		if rv.Kind() == reflect.Pointer && dataWord(v) != rv.UnsafePointer() {
			t.Errorf("dataWord(%T) is not the pointer", v)
		}
		if rv.Kind() == reflect.Slice && (*[3]uintptr)(dataWord(v))[0] != uintptr(rv.UnsafePointer()) {
			t.Errorf("dataWord(%T) does not hold the slice's header", v)
		}
		if typeWord(v) == nil || typeWord(v) != typeWord(reflect.Zero(rv.Type()).Interface()) {
			t.Errorf("typeWord(%T) is not its type's", v)
		}
	}
	if typeWord(&x) == typeWord(&s) {
		t.Error("typeWord of *int and *[]int are the same")
	}
	if typeWord(nil) != nil || dataWord(nil) != nil {
		t.Error("nil's words are not nil")
	}
}
