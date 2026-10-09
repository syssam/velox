package orm

import (
	"math"
	"testing"
)

func TestIDAs(t *testing.T) {
	type myID int64
	if v, ok := IDAs[int64](int64(7)); !ok || v != 7 {
		t.Errorf("the id as it is: %v %v", v, ok)
	}
	if v, ok := IDAs[int64](7); !ok || v != 7 {
		t.Errorf("int to int64: %v %v", v, ok)
	}
	if v, ok := IDAs[myID](int32(7)); !ok || v != 7 {
		t.Errorf("int32 to a named int64: %v %v", v, ok)
	}
	if v, ok := IDAs[uint8](255); !ok || v != 255 {
		t.Errorf("int to uint8: %v %v", v, ok)
	}
	if v, ok := IDAs[int](uint64(9)); !ok || v != 9 {
		t.Errorf("uint64 to int: %v %v", v, ok)
	}
	if v, ok := IDAs[string]("a"); !ok || v != "a" {
		t.Errorf("a string: %v %v", v, ok)
	}
	// What does not fit is not that id, and the zero value comes back.
	if v, ok := IDAs[int8](300); ok || v != 0 {
		t.Errorf("300 as an int8: %v %v", v, ok)
	}
	if v, ok := IDAs[uint](-1); ok || v != 0 {
		t.Errorf("-1 as a uint: %v %v", v, ok)
	}
	if v, ok := IDAs[uint8](uint64(256)); ok || v != 0 {
		t.Errorf("256 as a uint8: %v %v", v, ok)
	}
	if v, ok := IDAs[int64](uint64(math.MaxUint64)); ok || v != 0 {
		t.Errorf("the largest uint64 as an int64: %v %v", v, ok)
	}
	// A number is not a string, nor a string a number, nor is nil anything.
	if _, ok := IDAs[int]("1"); ok {
		t.Error(`"1" as an int`)
	}
	if _, ok := IDAs[string](1); ok {
		t.Error("1 as a string")
	}
	if _, ok := IDAs[int](nil); ok {
		t.Error("nil as an int")
	}
	if _, ok := IDAs[int](1.0); ok {
		t.Error("a float as an int")
	}
}
