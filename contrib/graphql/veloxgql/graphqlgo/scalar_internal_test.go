package graphqlgo

import (
	"encoding/json"
	"math/bits"
	"testing"
)

// TestDecodeInt: decodeInt takes the forms graphql-go gives a scalar, a
// variable's json.Number and a literal's integer, within both the Go
// type's range and Int's, and refuses anything else.
func TestDecodeInt(t *testing.T) {
	for _, c := range []struct {
		raw  any
		want uint8
		ok   bool
	}{
		{json.Number("255"), 255, true},
		{int64(7), 7, true},
		{8, 8, true},
		{json.Number("256"), 0, false},
		{json.Number("1.5"), 0, false},
		{int64(-1), 0, false},
		{int64(1) << 40, 0, false},
		{"3", 0, false},
		{3.0, 0, false},
	} {
		got, err := decodeInt[uint8](c.raw)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("decodeInt[uint8](%#v) = %d, %v", c.raw, got, err)
		}
	}
	if got, err := decodeInt[int8](int64(-128)); err != nil || got != -128 {
		t.Errorf("decodeInt[int8](-128) = %d, %v", got, err)
	}
}

// TestDecodeUint: decodeUint reads a Uint64's decimal string as a uint and
// refuses what veloxgql.Uint64 refuses. A value over 32 bits fails on a
// 32-bit platform only, where uint holds 32.
func TestDecodeUint(t *testing.T) {
	if got, err := decodeUint("4294967295"); err != nil || got != 4294967295 {
		t.Errorf("decodeUint(4294967295) = %d, %v", got, err)
	}
	if _, err := decodeUint(7); err == nil {
		t.Error("decodeUint takes a number, where Uint64 is a string")
	}
	if _, err := decodeUint("9223372036854775807"); (err == nil) != (bits.UintSize == 64) {
		t.Errorf("decodeUint(max int64): %v on a %d-bit uint", err, bits.UintSize)
	}
}
