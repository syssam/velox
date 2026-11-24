package gqlcore

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"
	"unicode/utf8"
)

// msgpackVectors are msgpack cursors, with their key and their values for
// an order of as many terms as they hold. The first eight are from a
// running server's responses (compact integers). The next fourteen are of
// the Cursor below, encoded by msgpack v5.4.1 with a time extension of type
// 1, as velox's predecessor wrote them. The last six are entgql's
// Cursor[int] (contrib v0.7.0 pagination.go: ID tagged i, Value tagged
// v,omitempty), encoded by msgpack v5.4.1, whose times are msgpack's
// timestamp extension (type -1).
//
// To regenerate the fourteen: declare a Cursor (fields ID, Value and
// Values, tagged i, v and vs,omitempty) and a cursorTime (MarshalMsgpack of
// []any{t.MarshalBinary(), zone name}, registered with
// msgpack.RegisterExt(1, (*cursorTime)(nil))). Encode each with
// github.com/vmihailenco/msgpack/v5 v5.4.1 (msgpack.Marshal), and write
// base64.RawStdEncoding of the bytes. The Go values, in order:
//
//	Cursor{ID: int64(1)}
//	Cursor{ID: int64(2), Value: "User-4"}
//	Cursor{ID: int64(7), Value: &cursorTime{time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)}}
//	Cursor{ID: int64(8), Value: &cursorTime{time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("EST", -5*3600))}}
//	Cursor{ID: int64(9), Value: []any{"b", int64(30)}}
//	Cursor{ID: int64(10), Value: []any{&cursorTime{time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}, nil}}
//	Cursor{ID: int64(11), Value: 2.5}
//	Cursor{ID: int64(12), Value: true}
//	Cursor{ID: int64(-3), Value: int64(-200)}
//	Cursor{ID: int64(300), Value: int64(1) << 40}
//	Cursor{ID: int64(13), Values: []any{"x", int64(1)}}
//	Cursor{ID: uuid.MustParse("0b9e4f2a-1c3d-4e5f-8a9b-0c1d2e3f4a5b"), Value: "n"}
//	Cursor{ID: int64(14), Value: int64(0)}
//	Cursor{ID: int64(15), Value: float32(1.5)}
//
// entgql's, in order, with ID an int:
//
//	{ID: 1}
//	{ID: 2, Value: "User-4"}
//	{ID: 3, Value: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
//	{ID: 4, Value: time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)}
//	{ID: 5, Value: time.Date(1960, 1, 2, 3, 4, 5, 7, time.UTC)}
//	{ID: 6, Value: time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("EST", -5*3600))}
var msgpackVectors = []struct {
	s      string
	key    any
	values []any // nil: v was nil, the values of an order of no terms
}{
	{"gqFpAaF2wA", json.Number("1"), nil},
	{"gqFpAqF2wA", json.Number("2"), nil},
	{"gqFpBKF2wA", json.Number("4"), nil},
	{"gqFpBaF2wA", json.Number("5"), nil},
	{"gqFpAqF2plVzZXItNA", json.Number("2"), []any{"User-4"}},
	{"gqFpA6F2FA", json.Number("3"), []any{json.Number("20")}},
	{"gqFpBaF2KA", json.Number("5"), []any{json.Number("40")}},
	{"gqFpAaF2qHUxQHAuY29t", json.Number("1"), []any{"u1@p.com"}},
	{"gqFp0wAAAAAAAAABoXbA", json.Number("1"), nil},
	{"gqFp0wAAAAAAAAACoXamVXNlci00", json.Number("2"), []any{"User-4"}},
	{"gqFp0wAAAAAAAAAHoXbHFgGSxA8BAAAADuDpLKUAAAAG//+jVVRD", json.Number("7"), []any{"2026-01-02T03:04:05.000000006Z"}},
	{"gqFp0wAAAAAAAAAIoXbHFgGSxA8BAAAADuDn9lAAAAAA/tSjRVNU", json.Number("8"), []any{"2026-01-01T00:00:00-05:00"}},
	{"gqFp0wAAAAAAAAAJoXaSoWLTAAAAAAAAAB4", json.Number("9"), []any{"b", json.Number("30")}},
	{"gqFp0wAAAAAAAAAKoXaSxxYBksQPAQAAAA7g6SylAAAAAP//o1VUQ8A", json.Number("10"), []any{"2026-01-02T03:04:05Z", nil}},
	{"gqFp0wAAAAAAAAALoXbLQAQAAAAAAAA", json.Number("11"), []any{json.Number("2.5")}},
	{"gqFp0wAAAAAAAAAMoXbD", json.Number("12"), []any{true}},
	{"gqFp0//////////9oXbT/////////zg", json.Number("-3"), []any{json.Number("-200")}},
	{"gqFp0wAAAAAAAAEsoXbTAAABAAAAAAA", json.Number("300"), []any{json.Number("1099511627776")}},
	{"g6Fp0wAAAAAAAAANoXbAonZzkqF40wAAAAAAAAAB", json.Number("13"), []any{"x", json.Number("1")}},
	{"gqFpxBALnk8qHD1OX4qbDB0uP0pboXahbg", "0b9e4f2a-1c3d-4e5f-8a9b-0c1d2e3f4a5b", []any{"n"}},
	{"gqFp0wAAAAAAAAAOoXbTAAAAAAAAAAA", json.Number("14"), []any{json.Number("0")}},
	{"gqFp0wAAAAAAAAAPoXbKP8AAAA", json.Number("15"), []any{json.Number("1.5")}},
	{"gaFpAQ", json.Number("1"), nil},
	{"gqFpAqF2plVzZXItNA", json.Number("2"), []any{"User-4"}},
	{"gqFpA6F21v9pVzWl", json.Number("3"), []any{"2026-01-02T03:04:05Z"}},
	{"gqFpBKF21/8AAAAYaVc1pQ", json.Number("4"), []any{"2026-01-02T03:04:05.000000006Z"}},
	{"gqFpBaF2xwz/AAAAB//////tMYUl", json.Number("5"), []any{"1960-01-02T03:04:05.000000007Z"}},
	{"gqFpBqF21v9pVf9Q", json.Number("6"), []any{"2026-01-01T05:00:00Z"}},
}

// TestDecodeMsgpackCursor: each vector decodes to its key, and to its values
// for an order of as many terms; a cursor of a nil value is one of no
// terms, or of one whose value is NULL.
func TestDecodeMsgpackCursor(t *testing.T) {
	for _, v := range msgpackVectors {
		l, ok := DecodeMsgpackCursor(v.s)
		if !ok {
			t.Errorf("%s: not a msgpack cursor", v.s)
			continue
		}
		if l.Key != v.key {
			t.Errorf("%s: key %#v, want %#v", v.s, l.Key, v.key)
		}
		got, ok := l.Values(len(v.values))
		if !ok || !reflect.DeepEqual(got, v.values) && len(got)+len(v.values) > 0 {
			t.Errorf("%s: values %#v %v, want %#v", v.s, got, ok, v.values)
		}
		if v.values == nil {
			if got, ok := l.Values(1); !ok || !reflect.DeepEqual(got, []any{nil}) {
				t.Errorf("%s: values of one term %#v %v, want [nil]", v.s, got, ok)
			}
		}
		if _, ok := l.Values(len(v.values) + 1); ok && v.values != nil {
			t.Errorf("%s: values of %d terms, want refused", v.s, len(v.values)+1)
		}
		if _, ok := l.Values(2); ok && v.values == nil {
			t.Errorf("%s: values of 2 terms, want refused", v.s)
		}
	}
}

// TestDecodeMsgpackCursorRefuses: a string that is not a msgpack cursor,
// exactly in the subset such a cursor holds, is not one.
func TestDecodeMsgpackCursorRefuses(t *testing.T) {
	enc := func(b ...byte) string { return base64.RawStdEncoding.EncodeToString(b) }
	for name, s := range map[string]string{
		"empty":                 "",
		"velox's cursor":        "eyJ2IjoxLCJ0IjoiUGVyc29uIiwibyI6W10sImsiOjF9",
		"not base64":            "garbage!!",
		"padded":                "gqFpAaF2wA==",
		"stray bits":            "gqFpAaF2wB",
		"base64url":             "gqFpxBALnk8qHD1OX4qbDB0uP0pboXahbg-_",
		"truncated":             "gqFpAaF2",
		"data after":            enc(0x82, 0xa1, 'i', 0x01, 0xa1, 'v', 0xc0, 0x00),
		"not a map":             enc(0x92, 0x01, 0xc0),
		"empty map":             enc(0x80),
		"four entries":          enc(0x84, 0xa1, 'i', 0x01, 0xa1, 'v', 0xc0, 0xa2, 'v', 's', 0x90, 0xa1, 'x', 0xc0),
		"unknown key":           enc(0x82, 0xa1, 'i', 0x01, 0xa1, 'x', 0xc0),
		"duplicate key":         enc(0x82, 0xa1, 'i', 0x01, 0xa1, 'i', 0x02),
		"no key":                enc(0x81, 0xa1, 'v', 0xc0),
		"nil key":               enc(0x82, 0xa1, 'i', 0xc0, 0xa1, 'v', 0xc0),
		"bool key":              enc(0x82, 0xa1, 'i', 0xc3, 0xa1, 'v', 0xc0),
		"array key":             enc(0x82, 0xa1, 'i', 0x91, 0x01, 0xa1, 'v', 0xc0),
		"time key":              enc(0x82, 0xa1, 'i', 0xd4, 0x01, 0x00, 0xa1, 'v', 0xc0),
		"non-string map key":    enc(0x82, 0x01, 0x01, 0xa1, 'v', 0xc0),
		"nested array":          enc(0x82, 0xa1, 'i', 0x01, 0xa1, 'v', 0x91, 0x91, 0x01),
		"vs not an array":       enc(0x83, 0xa1, 'i', 0x01, 0xa1, 'v', 0xc0, 0xa2, 'v', 's', 0x01),
		"v and vs both":         enc(0x83, 0xa1, 'i', 0x01, 0xa1, 'v', 0x01, 0xa2, 'v', 's', 0x91, 0x01),
		"map value":             enc(0x82, 0xa1, 'i', 0x01, 0xa1, 'v', 0x80),
		"another extension":     enc(0x82, 0xa1, 'i', 0x01, 0xa1, 'v', 0xd4, 0x02, 0x00),
		"a bad time":            enc(0x82, 0xa1, 'i', 0x01, 0xa1, 'v', 0xd6, 0x01, 0x92, 0xc4, 0x00, 0xa0),
		"time of three parts":   enc(0x82, 0xa1, 'i', 0x01, 0xa1, 'v', 0xd6, 0x01, 0x93, 0xc4, 0x00, 0xa0),
		"time not bin":          enc(0x82, 0xa1, 'i', 0x01, 0xa1, 'v', 0xd6, 0x01, 0x92, 0xa0, 0xa0),
		"time zone not a str":   enc(0x82, 0xa1, 'i', 0x01, 0xa1, 'v', 0xd6, 0x01, 0x92, 0xc4, 0x00, 0x01),
		"time with data after":  enc(0x82, 0xa1, 'i', 0x01, 0xa1, 'v', 0xd6, 0x01, 0x92, 0xc4, 0x00, 0xa0, 0x00),
		"short bin":             enc(0x82, 0xa1, 'i', 0xc4, 0x03, 1, 2, 3, 0xa1, 'v', 0xc0),
		"invalid UTF-8":         enc(0x82, 0xa1, 'i', 0x01, 0xa1, 'v', 0xa1, 0xff),
		"long string length":    enc(0x82, 0xa1, 'i', 0x01, 0xa1, 'v', 0xdb, 0xff, 0xff, 0xff, 0xff),
		"long array length":     enc(0x82, 0xa1, 'i', 0x01, 0xa1, 'v', 0xdd, 0xff, 0xff, 0xff, 0xff),
		"long extension length": enc(0x82, 0xa1, 'i', 0x01, 0xa1, 'v', 0xc9, 0xff, 0xff, 0xff, 0xff, 0x01),
		"reserved byte":         enc(0x82, 0xa1, 'i', 0x01, 0xa1, 'v', 0xc1),
		"truncated map length":  enc(0xde, 0x00),
		"truncated key length":  enc(0x81, 0xd9),
		"truncated int":         enc(0x81, 0xa1, 'i', 0xcd, 0x01),
		"truncated bin length":  enc(0x81, 0xa1, 'i', 0xc5, 0x00),
		"truncated ext length":  enc(0x82, 0xa1, 'i', 0x01, 0xa1, 'v', 0xc8, 0x00),
		"short ext payload":     enc(0x82, 0xa1, 'i', 0x01, 0xa1, 'v', 0xc7, 0x05, 0x01, 0x92),
		"time bin truncated":    enc(0x82, 0xa1, 'i', 0x01, 0xa1, 'v', 0xd5, 0x01, 0x92, 0xc4),
		"timestamp of 2 bytes":  enc(0x82, 0xa1, 'i', 0x01, 0xa1, 'v', 0xd5, 0xff, 0x00, 0x01),
		"timestamp of 16 bytes": enc(0x82, 0xa1, 'i', 0x01, 0xa1, 'v', 0xd8, 0xff, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1),
		"timestamp nsec 1e9":    enc(0x82, 0xa1, 'i', 0x01, 0xa1, 'v', 0xc7, 0x0c, 0xff, 0x3b, 0x9a, 0xca, 0x00, 0, 0, 0, 0, 0, 0, 0, 1),
		"timestamp truncated":   enc(0x82, 0xa1, 'i', 0x01, 0xa1, 'v', 0xd7, 0xff, 0x00, 0x00),
	} {
		if l, ok := DecodeMsgpackCursor(s); ok {
			t.Errorf("%s: %q decodes to %+v, want refused", name, s, l)
		}
	}
}

// TestDecodeMsgpackCursorEncodings: every msgpack encoding of the types
// msgpack cursors hold decodes, the longer lengths of strings, arrays and
// maps included.
func TestDecodeMsgpackCursorEncodings(t *testing.T) {
	enc := func(b ...byte) string { return base64.RawStdEncoding.EncodeToString(b) }
	val := func(v ...byte) string {
		return enc(append([]byte{0x82, 0xa1, 'i', 0x01, 0xa1, 'v'}, v...)...)
	}
	for _, c := range []struct {
		s    string
		want any
	}{
		{val(0xff), json.Number("-1")},
		{val(0xcc, 0xc8), json.Number("200")},
		{val(0xcd, 0x01, 0x00), json.Number("256")},
		{val(0xce, 0x00, 0x01, 0x00, 0x00), json.Number("65536")},
		{val(0xcf, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff), json.Number("18446744073709551615")},
		{val(0xd0, 0x80), json.Number("-128")},
		{val(0xd1, 0x80, 0x00), json.Number("-32768")},
		{val(0xd2, 0x80, 0x00, 0x00, 0x00), json.Number("-2147483648")},
		{val(0xc2), false},
		{val(0xd9, 0x01, 'a'), "a"},
		{val(0xda, 0x00, 0x01, 'b'), "b"},
		{val(0xdb, 0x00, 0x00, 0x00, 0x01, 'c'), "c"},
		{val(0xcb, 0x7f, 0xf8, 0, 0, 0, 0, 0, 1), "NaN"},
		{val(0xcb, 0x7f, 0xf0, 0, 0, 0, 0, 0, 0), "+Inf"},
		{val(0xca, 0xff, 0x80, 0, 0), "-Inf"},
		{val(0xc5, 0x00, 0x10, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1), "00000000-0000-0000-0000-000000000001"},
		{val(0xc6, 0x00, 0x00, 0x00, 0x10, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2), "00000000-0000-0000-0000-000000000002"},
		{val(0xdc, 0x00, 0x01, 0x07), json.Number("7")},
		{val(0xdd, 0x00, 0x00, 0x00, 0x01, 0x08), json.Number("8")},
		{enc(0xde, 0x00, 0x02, 0xa1, 'i', 0x01, 0xa1, 'v', 0x09), json.Number("9")},
		{enc(0xdf, 0x00, 0x00, 0x00, 0x02, 0xa1, 'i', 0x01, 0xa1, 'v', 0x0a), json.Number("10")},
		{enc(0x81, 0xa1, 'i', 0x01), nil},
		{enc(0x82, 0xd9, 0x01, 'i', 0x01, 0xa1, 'v', 0x0b), json.Number("11")},
	} {
		l, ok := DecodeMsgpackCursor(c.s)
		if !ok {
			t.Errorf("%s: not a msgpack cursor", c.s)
			continue
		}
		if got, ok := l.Values(1); !ok || got[0] != c.want {
			t.Errorf("%s: values %#v %v, want [%#v]", c.s, got, ok, c.want)
		}
	}
	// A time with no zone name, a payload of 19 bytes, in an ext 8, an ext
	// 16 and an ext 32.
	head := []byte{0x82, 0xa1, 'i', 0x01, 0xa1, 'v'}
	payload := []byte{0x92, 0xc4, 0x0f, 0x01, 0x00, 0x00, 0x00, 0x0e, 0xe0, 0xe9, 0x2c, 0xa5, 0x00, 0x00, 0x00, 0x00, 0xff, 0xff, 0xa0}
	for _, ext := range [][]byte{{0xc7, 0x13, 0x01}, {0xc8, 0x00, 0x13, 0x01}, {0xc9, 0x00, 0x00, 0x00, 0x13, 0x01}} {
		x := append(append(append([]byte{}, head...), ext...), payload...)
		if l, ok := DecodeMsgpackCursor(enc(x...)); !ok {
			t.Errorf("% x: not a msgpack cursor", ext)
		} else if got, _ := l.Values(1); got[0] != "2026-01-02T03:04:05Z" {
			t.Errorf("% x: %#v", ext, got)
		}
	}
}

// FuzzMsgpackCursor: no input panics DecodeMsgpackCursor; a cursor it decodes
// has a key that is a number or a string, values that are JSON values, and
// encodes back to the text it was read from.
func FuzzMsgpackCursor(f *testing.F) {
	for _, v := range msgpackVectors {
		b, _ := base64.RawStdEncoding.DecodeString(v.s)
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		s := base64.RawStdEncoding.EncodeToString(b)
		l, ok := DecodeMsgpackCursor(s)
		if !ok {
			return
		}
		switch l.Key.(type) {
		case json.Number, string:
		default:
			t.Fatalf("% x: key %#v", b, l.Key)
		}
		for n := range 4 {
			vs, ok := l.Values(n)
			if ok && len(vs) != n {
				t.Fatalf("% x: %d values for %d terms", b, len(vs), n)
			}
			for _, v := range vs {
				switch x := v.(type) {
				case json.Number, bool, nil:
				case string:
					if !utf8.ValidString(x) {
						t.Fatalf("% x: a value of invalid UTF-8", b)
					}
				default:
					t.Fatalf("% x: value %#v", b, v)
				}
			}
		}
	})
}
