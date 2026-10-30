package engine

import (
	"reflect"
	"slices"
	"testing"
)

// TestInPlace: an owner's rows are used in place only when they are a run of
// one of the scan's arrays that no other owner in place has.
func TestInPlace(t *testing.T) {
	b := NewBlocks(reflect.TypeFor[int64]())
	for range 10 { // arrays of 4 and 8 rows: rows 0 to 3, then 4 to 9
		b.Next()
	}
	all, ptrs := JoinBlocks([]any{b})
	idx := [][]int{
		{0, 1, 2}, // a run in the first array
		{3, 4},    // straddles the arrays
		{5, 7},    // not a run
		nil,       // no rows
		{8, 9},    // a run in the second array
		{8, 9},    // the same rows as an owner in place
		{6},       // one row
	}
	keep := make([]bool, len(idx))
	if n := InPlace(all, idx, keep, make([]bool, len(ptrs))); n != 6 {
		t.Errorf("%d rows to copy, want 6", n)
	}
	want := []bool{true, false, false, false, true, false, true}
	if !slices.Equal(keep, want) {
		t.Errorf("in place %v, want %v", keep, want)
	}
}

// TestKeyRowsFill: each key's rows are its targets' indexes, in order; a
// target with no key, or a key no owner has, is in none.
func TestKeyRowsFill(t *testing.T) {
	group := map[any]int{}
	for _, k := range []any{int64(7), int64(3), int64(9)} {
		AddKey(group, k)
	}
	if g, added := AddKey(group, int64(3)); g != 1 || added {
		t.Errorf("adding 3 again gives %d, %v; want 1, false", g, added)
	}
	keys := []any{int64(3), nil, int64(7), int64(3), int64(5), int64(7), int64(3)}
	var x KeyRows
	x.Fill(group, len(keys), func(i int) (any, bool) { return keys[i], keys[i] != nil })
	for g, want := range [][]int{{2, 5}, {0, 3, 6}, nil} {
		got := x.Of(g)
		if !slices.Equal(got, want) || cap(got) != len(got) {
			t.Errorf("key %d: rows %v cap %d, want %v capped", g, got, cap(got), want)
		}
	}
	if got := x.Of(-1); got != nil {
		t.Errorf("no key: rows %v, want none", got)
	}
}
