package engine

import (
	"math"
	"reflect"
	"testing"
)

// TestOverflows: each integer and float kind takes its bounds and refuses
// one past them, from a value of another size or signedness; NaN and the
// infinities fit a float.
func TestOverflows(t *testing.T) {
	type c struct {
		v    any
		want any // a value of the target type
		over bool
	}
	cases := []c{
		{int64(math.MaxInt8), int8(0), false}, {int64(math.MaxInt8 + 1), int8(0), true},
		{int64(math.MinInt8), int8(0), false}, {int64(math.MinInt8 - 1), int8(0), true},
		{int64(300), int8(0), true},
		{int64(math.MaxInt16), int16(0), false}, {int64(math.MaxInt16 + 1), int16(0), true},
		{int64(math.MinInt16), int16(0), false}, {int64(math.MinInt16 - 1), int16(0), true},
		{int64(math.MaxInt32), int32(0), false}, {int64(math.MaxInt32 + 1), int32(0), true},
		{int64(math.MinInt32), int32(0), false}, {int64(math.MinInt32 - 1), int32(0), true},
		{uint64(math.MaxInt64), int64(0), false}, {uint64(math.MaxInt64 + 1), int64(0), true},
		{uint64(math.MaxUint64), int(0), true},
		{int64(math.MinInt64), int64(0), false},
		{int64(math.MaxUint8), uint8(0), false}, {int64(math.MaxUint8 + 1), uint8(0), true},
		{int64(math.MaxUint16), uint16(0), false}, {int64(math.MaxUint16 + 1), uint16(0), true},
		{int64(math.MaxUint32), uint32(0), false}, {int64(math.MaxUint32 + 1), uint32(0), true},
		{int64(0), uint64(0), false}, {int64(-1), uint64(0), true}, {int8(-1), uint(0), true},
		{uint64(math.MaxUint64), uint64(0), false}, {int64(math.MaxInt64), uint64(0), false},
		{uint64(math.MaxUint64), uint32(0), true},
		{float64(math.MaxFloat32), float32(0), false}, {float64(1e300), float32(0), true},
		{float64(-1e300), float32(0), true}, {math.Inf(1), float32(0), false}, {math.NaN(), float32(0), false},
		{float64(1e300), float64(0), false}, {float32(1), float64(0), false},
	}
	for _, c := range cases {
		if got := overflows(reflect.ValueOf(c.v), reflect.TypeOf(c.want)); got != c.over {
			t.Errorf("%T %v to %T: overflows %v, want %v", c.v, c.v, c.want, got, c.over)
		}
	}
}
