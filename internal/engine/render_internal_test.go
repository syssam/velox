package engine

import (
	"math"
	"reflect"
	"testing"
)

func TestDialectString(t *testing.T) {
	if Postgres.String() != "postgres" || MySQL.String() != "mysql" || SQLite.String() != "sqlite" {
		t.Error(Postgres, MySQL, SQLite)
	}
}

// TestIntRange: what arith's guard holds a SQLite column to, and that it guards only
// integers.
func TestIntRange(t *testing.T) {
	for _, c := range []struct {
		t      any
		lo, hi int64
		ok     bool
	}{
		{reflect.TypeFor[int64](), math.MinInt64, math.MaxInt64, true},
		{reflect.TypeFor[*int64](), math.MinInt64, math.MaxInt64, true},
		{reflect.TypeFor[int8](), -128, 127, true},
		{reflect.TypeFor[int32](), math.MinInt32, math.MaxInt32, true},
		{reflect.TypeFor[uint8](), 0, 255, true},
		{reflect.TypeFor[uint64](), 0, math.MaxInt64, true},
		{reflect.TypeFor[float64](), 0, 0, false},
		{nil, 0, 0, false},
	} {
		if lo, hi, ok := intRange(c.t); lo != c.lo || hi != c.hi || ok != c.ok {
			t.Errorf("%v: %d, %d, %v", c.t, lo, hi, ok)
		}
	}
}

// TestInLen: a list pads to the next power of two up to 1024, then to the
// next multiple of 1024 up to 16384, and inFit is the longest list that
// pads to at most n.
func TestInLen(t *testing.T) {
	for n, want := range map[int]int{0: 0, 1: 1, 2: 2, 3: 4, 5: 8, 1000: 1024, 1024: 1024, 1025: 2048, 2049: 3072, 16384: 16384, 16385: 16385, 40000: 40000} {
		if got := inLen(n); got != want {
			t.Errorf("inLen(%d) = %d, want %d", n, got, want)
		}
	}
	for n := -1; n <= 20000; n++ {
		f := inFit(n)
		if f < 1 || (n >= 1 && inLen(f) > n) || (n >= 1 && inLen(f+1) <= n && f+1 <= n) {
			t.Fatalf("inFit(%d) = %d: inLen(%d) = %d, inLen(%d) = %d", n, f, f, inLen(f), f+1, inLen(f+1))
		}
	}
}
