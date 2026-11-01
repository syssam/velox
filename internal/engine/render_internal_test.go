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
