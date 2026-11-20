package engine

import (
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"
)

// valuer is a driver.Valuer that gives v, or err.
type valuer struct {
	v   driver.Value
	err error
}

func (x valuer) Value() (driver.Value, error) { return x.v, x.err }

// stringer has only a String method.
type stringer string

func (s stringer) String() string { return string(s) }

func TestEqualDecimalForms(t *testing.T) {
	for _, c := range []struct {
		name string
		a, b any
		want bool
	}{
		{"two nils", nil, nil, true},
		{"nil and a value", nil, valuer{v: "1"}, false},
		{"a value and nil", valuer{v: "1"}, nil, false},
		{"text, trailing zero", valuer{v: "1.50"}, valuer{v: "1.5"}, true},
		{"text and bytes", valuer{v: "2"}, valuer{v: []byte("2.0")}, true},
		{"int64 and text", valuer{v: int64(3)}, valuer{v: "3.00"}, true},
		{"float64 and text", valuer{v: 0.5}, valuer{v: "0.50"}, true},
		{"different numbers", valuer{v: "1.5"}, valuer{v: "1.6"}, false},
		{"String method", stringer(" 4.0 "), stringer("4"), true},
		{"Valuer and Stringer", valuer{v: "5"}, stringer("5.0"), true},
		{"a Valuer that fails reads as no number", valuer{err: errors.New("x")}, valuer{v: "1"}, false},
		{"a bool Value reads as no number", valuer{v: true}, valuer{v: true}, true},
		{"text that is no number", stringer("abc"), stringer("abc"), true},
		{"text that is no number, unequal", stringer("abc"), stringer("abd"), false},
		{"a plain int is no decimal", 7, 7, true},
		{"infinity is no number", valuer{v: float64Inf()}, valuer{v: float64Inf()}, true},
	} {
		if got := equalDecimal(c.a, c.b); got != c.want {
			t.Errorf("%s: equalDecimal = %v, want %v", c.name, got, c.want)
		}
	}
	for _, v := range []any{valuer{err: errors.New("x")}, valuer{v: true}, 7, nil, stringer("")} {
		if r, ok := decimalRat(v); ok {
			t.Errorf("decimalRat(%#v) = %v, want no number", v, r)
		}
	}
}

// float64Inf is +Inf, which big.Rat cannot hold.
func float64Inf() float64 {
	var z float64
	return 1 / z
}

func TestEachRefused(t *testing.T) {
	for _, c := range []struct {
		name string
		st   Statement
		want string
	}{
		{"none", Statement{}, ""},
		{"Limit", Statement{Limit: 1}, "Limit; Rel.LimitEach limits each owner's rows"},
		{"Offset", Statement{Offset: 1}, "Offset; page each owner's rows with After"},
		{"ForUpdate", Statement{Lock: LockForUpdate}, "ForUpdate; a per-owner read locks nothing"},
		{"ForShare", Statement{Lock: LockForShare}, "ForShare; a per-owner read locks nothing"},
		{"SkipLocked", Statement{LockWait: LockSkipLocked}, "SkipLocked; a per-owner read locks nothing"},
		{"NoWait", Statement{LockWait: LockNoWait}, "NoWait; a per-owner read locks nothing"},
	} {
		if got := eachRefused(&c.st); got != c.want {
			t.Errorf("%s: eachRefused = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDiffRowsNilEntity(t *testing.T) {
	if got := DiffRows(nil, nil, nil); got != nil {
		t.Errorf("DiffRows of no entity = %v, want nil", got)
	}
}

func TestCompareKeys(t *testing.T) {
	t1 := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name string
		a, b []any
		want int
	}{
		{"equal", []any{int64(1), "a"}, []any{int64(1), "a"}, 0},
		{"first column decides", []any{int64(2), "a"}, []any{int64(1), "z"}, 1},
		{"second column decides", []any{int64(1), "a"}, []any{int64(1), "b"}, -1},
		{"shorter first", []any{int64(1)}, []any{int64(1), int64(0)}, -1},
		{"longer after", []any{int64(1), int64(0)}, []any{int64(1)}, 1},
		{"ints", []any{int32(-3)}, []any{int32(2)}, -1},
		{"uints", []any{uint16(9)}, []any{uint16(3)}, 1},
		{"floats", []any{1.5}, []any{2.5}, -1},
		{"times", []any{t1.Add(time.Hour)}, []any{t1}, 1},
		{"time and non-time", []any{t1}, []any{"x"}, -1},
		{"byte slices", []any{[]byte{1, 2}}, []any{[]byte{1, 3}}, -1},
		{"byte arrays", []any{[2]byte{9, 9}}, []any{[2]byte{9, 1}}, 1},
		{"slices of different types print", []any{[]byte("b")}, []any{[]int{1}}, 1},
		{"other slices print", []any{[]int{2}}, []any{[]int{10}}, 1},
		{"different kinds print", []any{int64(10)}, []any{"9"}, -1},
		{"nil and a value print", []any{nil}, []any{"a"}, -1},
		{"two nils", []any{nil}, []any{nil}, 0},
	} {
		got := compareKeys(c.a, c.b)
		if (got < 0) != (c.want < 0) || (got > 0) != (c.want > 0) {
			t.Errorf("%s: compareKeys = %d, want sign %d", c.name, got, c.want)
		}
	}
}

func TestByteString(t *testing.T) {
	if got := byteString(reflect.ValueOf([]byte("ab"))); string(got) != "ab" {
		t.Errorf("slice: %q", got)
	}
	if got := byteString(reflect.ValueOf([3]byte{1, 2, 3})); !reflect.DeepEqual(got, []byte{1, 2, 3}) {
		t.Errorf("array: %v", got)
	}
}

type hasExported struct {
	P *int
	S []int
}

type noRefs struct {
	A int
	_ *int // unexported: not cloned, so not a reference to follow
}

func TestHoldsRefs(t *testing.T) {
	for _, c := range []struct {
		name string
		typ  reflect.Type
		want bool
	}{
		{"int", reflect.TypeFor[int](), false},
		{"string", reflect.TypeFor[string](), false},
		{"pointer", reflect.TypeFor[*int](), true},
		{"slice", reflect.TypeFor[[]byte](), true},
		{"map", reflect.TypeFor[map[string]int](), true},
		{"interface", reflect.TypeFor[any](), true},
		{"array of ints", reflect.TypeFor[[3]int](), false},
		{"empty array of pointers", reflect.TypeFor[[0]*int](), false},
		{"array of pointers", reflect.TypeFor[[2]*int](), true},
		{"struct with an exported slice", reflect.TypeFor[hasExported](), true},
		{"struct with an unexported pointer only", reflect.TypeFor[noRefs](), false},
		{"time.Time", reflect.TypeFor[time.Time](), false},
	} {
		if got := holdsRefs(c.typ); got != c.want {
			t.Errorf("%s: holdsRefs = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCloneValue(t *testing.T) {
	n := 5
	s := []int{1, 2}
	b := []byte("xy")
	m := map[string][]int{"k": {7}}
	var iface any = &n
	in := struct {
		P    *int
		S    []int
		B    []byte
		M    map[string][]int
		A    [2][]int
		I    any
		NP   *int
		NS   []int
		NM   map[string]int
		NI   any
		priv []int
	}{P: &n, S: s, B: b, M: m, A: [2][]int{{3}, nil}, I: iface, priv: s}
	got := cloneValue(reflect.ValueOf(in)).Interface().(struct {
		P    *int
		S    []int
		B    []byte
		M    map[string][]int
		A    [2][]int
		I    any
		NP   *int
		NS   []int
		NM   map[string]int
		NI   any
		priv []int
	})
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("clone = %+v, want %+v", got, in)
	}
	if got.P == in.P || &got.S[0] == &s[0] || &got.B[0] == &b[0] || &got.M["k"][0] == &m["k"][0] || &got.A[0][0] == &in.A[0][0] || got.I.(*int) == &n {
		t.Error("the clone shares memory with the original")
	}
	if &got.priv[0] != &s[0] {
		t.Error("an unexported field is copied shallowly, with the struct")
	}
	*got.P = 99
	if n != 5 {
		t.Error("writing through the clone's pointer changed the original")
	}
	if got.NP != nil || got.NS != nil || got.NM != nil || got.NI != nil {
		t.Errorf("nil pointer, slice, map and interface stay nil: %+v", got)
	}
	if v := defaultValue(int64(4)); v.Int() != 4 {
		t.Errorf("defaultValue(4) = %v", v)
	}
	if v := defaultValue([]string{"a"}); v.Len() != 1 || v.Index(0).String() != "a" {
		t.Errorf("defaultValue(slice) = %v", v)
	}
}
