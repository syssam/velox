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

func TestMustAndCheck(t *testing.T) {
	if v := Must(7, nil); v != 7 {
		t.Errorf("Must returned %v", v)
	}
	Check(nil)
	for name, f := range map[string]func(){
		"Must":  func() { Must(0, ErrNotFound) },
		"Check": func() { Check(ErrNotFound) },
	} {
		func() {
			defer func() {
				if r := recover(); r != ErrNotFound { //nolint:errorlint // the very value that was passed
					t.Errorf("%s panicked with %v", name, r)
				}
			}()
			f()
			t.Errorf("%s did not panic", name)
		}()
	}
}

func TestOne(t *testing.T) {
	if err := One(1, nil); err != nil {
		t.Errorf("one row: %v", err)
	}
	if err := One(0, nil); err != ErrNotFound { //nolint:errorlint // the sentinel itself
		t.Errorf("no row: %v", err)
	}
	if err := One(0, ErrTxDone); err != ErrTxDone { //nolint:errorlint // the error that was passed
		t.Errorf("an error: %v", err)
	}
}

func TestApply(t *testing.T) {
	type q struct{ n int }
	got := Apply(&q{}, func(x *q) { x.n++ }, nil, func(x *q) { x.n += 10 })
	if got.n != 11 {
		t.Errorf("got %d", got.n)
	}
	if same := &(q{n: 3}); Apply(same) != same || same.n != 3 {
		t.Error("Apply of nothing changed the query")
	}
}

func TestIDAs_StringKinds(t *testing.T) {
	type slug string
	if v, ok := IDAs[slug]("abc"); !ok || v != "abc" {
		t.Errorf("a string as an id type declared on string: %v %v", v, ok)
	}
	if v, ok := IDAs[string](slug("abc")); !ok || v != "abc" {
		t.Errorf("the reverse: %v %v", v, ok)
	}
	if _, ok := IDAs[slug](1); ok {
		t.Error("a number as a string id")
	}
}
