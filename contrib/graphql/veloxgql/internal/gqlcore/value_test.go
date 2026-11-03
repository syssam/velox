package gqlcore

import (
	"encoding/json"
	"errors"
	"math"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// TestKeyText: each key type's text, and false for another type.
func TestKeyText(t *testing.T) {
	u := [16]byte{0xab, 1}
	addr := netip.MustParseAddr("10.0.0.1")
	for _, c := range []struct {
		key  any
		want string
	}{
		{int(-1), "-1"}, {int8(-8), "-8"}, {int16(16), "16"}, {int32(32), "32"}, {int64(math.MinInt64), "-9223372036854775808"},
		{uint(1), "1"}, {uint8(8), "8"}, {uint16(16), "16"}, {uint32(32), "32"}, {uint64(math.MaxUint64), "18446744073709551615"},
		{"s", "s"}, {u, FormatUUID(u)}, {addr, "10.0.0.1"},
	} {
		if got, ok := KeyText(c.key); !ok || got != c.want {
			t.Errorf("KeyText(%T %v) = %q %v, want %q", c.key, c.key, got, ok, c.want)
		}
	}
	if _, ok := KeyText(1.5); ok {
		t.Error("KeyText of a float")
	}
}

// TestJSONValue: times, UUIDs and the floats with no JSON number become
// strings; other values stay as they are.
func TestJSONValue(t *testing.T) {
	when := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	for _, c := range []struct {
		v, want any
	}{
		{when, "2026-01-02T03:04:05.000000006Z"}, {[16]byte{}, "00000000-0000-0000-0000-000000000000"},
		{math.NaN(), "NaN"}, {math.Inf(1), "+Inf"}, {math.Inf(-1), "-Inf"}, {1.5, 1.5},
		{float32(math.Inf(-1)), "-Inf"}, {float32(2), float32(2)}, {int64(3), int64(3)},
	} {
		if got := JSONValue(c.v); got != c.want {
			t.Errorf("JSONValue(%v) = %#v, want %#v", c.v, got, c.want)
		}
	}
}

// TestSetValue: each type takes the values that stand for it, refuses
// others with ErrMismatch, and an unknown type is ErrUnsupported.
func TestSetValue(t *testing.T) {
	var (
		i   int
		i8  int8
		i16 int16
		i32 int32
		i64 int64
		u   uint
		u8  uint8
		u16 uint16
		u32 uint32
		u64 uint64
		f64 float64
		f32 float32
		s   string
		b   bool
		tm  time.Time
		id  [16]byte
		bs  []byte
		ad  netip.Addr
	)
	ok := []struct {
		p, v any
	}{
		{&i, json.Number("-1")}, {&i8, Text("127")}, {&i16, json.Number("16")}, {&i32, json.Number("32")}, {&i64, json.Number("64")},
		{&u, json.Number("1")}, {&u8, Text("255")}, {&u16, json.Number("16")}, {&u32, json.Number("32")}, {&u64, json.Number("18446744073709551615")},
		{&f64, json.Number("1.5")}, {&f32, "NaN"}, {&f64, "-Inf"},
		{&s, "x"}, {&s, Text("y")}, {&b, true},
		{&tm, "2026-01-02T03:04:05Z"}, {&id, "000000ab-0000-0000-0000-000000000001"}, {&bs, "AAE="},
		{&ad, "10.0.0.1"},
	}
	for _, c := range ok {
		if err := SetValue(c.p, c.v); err != nil {
			t.Errorf("SetValue(%T, %#v): %v", c.p, c.v, err)
		}
	}
	if i8 != 127 || u64 != math.MaxUint64 || !math.IsInf(f64, -1) || !math.IsNaN(float64(f32)) || s != "y" || !b ||
		tm.Year() != 2026 || id[3] != 0xab || len(bs) != 2 || ad.String() != "10.0.0.1" {
		t.Errorf("the values set: %v %v %v %v %q %v %v %v %v %v", i8, u64, f64, f32, s, b, tm, id, bs, ad)
	}
	bad := []struct {
		p, v any
	}{
		{&i, "1"}, {&i8, json.Number("128")}, {&u, json.Number("-1")}, {&u8, "1"}, {&f64, true}, {&f32, json.Number("x")},
		{&s, json.Number("1")}, {&b, "true"}, {&tm, 1}, {&tm, "yesterday"}, {&id, 1}, {&id, "not-a-uuid"},
		{&bs, 1}, {&bs, "!"}, {&ad, 1}, {&ad, "not an address"},
	}
	for _, c := range bad {
		if err := SetValue(c.p, c.v); !errors.Is(err, ErrMismatch) {
			t.Errorf("SetValue(%T, %#v): %v, want ErrMismatch", c.p, c.v, err)
		}
	}
	if err := SetValue(new(complex64), json.Number("1")); !errors.Is(err, ErrUnsupported) {
		t.Errorf("an unknown type: %v", err)
	}
}

// TestUUID: FormatUUID and ParseUUID are inverses, in either case, and
// ParseUUID refuses any other form.
func TestUUID(t *testing.T) {
	u := [16]byte{0xde, 0xad, 0xbe, 0xef, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 0xff}
	s := FormatUUID(u)
	if s != "deadbeef-0102-0304-0506-0708090a0bff" {
		t.Errorf("FormatUUID: %s", s)
	}
	for _, in := range []string{s, strings.ToUpper(s)} {
		if got, ok := ParseUUID(in); !ok || got != u {
			t.Errorf("ParseUUID(%s) = %v %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "deadbeef01020304050607080090a0bff", "deadbeef-0102-0304-0506-0708090a0bfg", "deadbeef_0102-0304-0506-0708090a0bff"} {
		if _, ok := ParseUUID(in); ok {
			t.Errorf("ParseUUID(%q) is ok", in)
		}
	}
	if TypeName[error]() != "error" || TypeName[[16]byte]() != "[16]uint8" {
		t.Errorf("TypeName: %s %s", TypeName[error](), TypeName[[16]byte]())
	}
}
