package gqlcore

import (
	"encoding/base64"
	"encoding/json"
	"math"
	"strconv"
	"time"
	"unicode/utf8"
)

// LegacyCursor is a cursor veloxe, velox's predecessor, wrote: standard
// base64 without padding of the msgpack map {"i": key, "v": value} or
// {"i": key, "v": nil, "vs": [values…]}. Its key and values are JSON values
// (json.Number, string, bool and, in a value, nil), as DecodeCursor's are,
// so a connection converts them the same way. A time is an RFC 3339 string
// and a 16-byte binary a UUID's string. veloxgql.Cursor's UnmarshalGQL
// gives one as the Cursor's Key.
type LegacyCursor struct {
	// Key is the row's key, a json.Number or a string.
	Key any
	// Err is the error velox's own decoding gave the cursor's text, which a
	// connection that takes no legacy cursor returns.
	Err error

	values []any // the values of v when it is an array, or of vs
	one    any   // v when it is not an array
	array  bool  // v was an array, or vs was given
}

// Values returns the cursor's values for an order of n terms, and false
// when the cursor holds another number. A nil v is the values of an order
// of no terms, or of one term whose value is NULL, as veloxe wrote both.
func (l *LegacyCursor) Values(n int) ([]any, bool) {
	switch {
	case l.array:
		return l.values, len(l.values) == n
	case l.one == nil && n == 0:
		return nil, true
	case n == 1:
		return []any{l.one}, true
	}
	return nil, false
}

// DecodeLegacyCursor decodes s, a cursor veloxe wrote, and reports false for
// any other text. It reads exactly the msgpack veloxe wrote, and nothing
// else: a map of the keys i, v and vs; integers, strings of valid UTF-8,
// nil, booleans, floats, 16-byte binaries and veloxe's time extension (type
// 1) as values; an array of them as v or vs. Every length is checked
// against what is left of the input before anything is allocated.
func DecodeLegacyCursor(s string) (*LegacyCursor, bool) {
	b, err := base64.RawStdEncoding.DecodeString(s)
	if s == "" || err != nil || base64.RawStdEncoding.EncodeToString(b) != s {
		return nil, false
	}
	d := msgDecoder{b: b}
	l, ok := d.cursor()
	if !ok || len(d.b) != 0 {
		return nil, false
	}
	return l, true
}

// msgDecoder reads msgpack from b, which it consumes.
type msgDecoder struct{ b []byte }

// cursor reads the cursor's map.
func (d *msgDecoder) cursor() (*LegacyCursor, bool) {
	n, ok := d.mapLen()
	if !ok || n < 1 || n > 3 {
		return nil, false
	}
	var (
		l                      LegacyCursor
		haveKey, haveV, haveVs bool
		vArray                 bool  // v is an array
		v, vs                  []any // v's elements, or v alone; vs
	)
	for range n {
		k, ok := d.str()
		if !ok {
			return nil, false
		}
		switch {
		case k == "i" && !haveKey:
			haveKey = true
			l.Key, ok = d.scalar()
			switch l.Key.(type) {
			case json.Number, string:
			default:
				ok = false
			}
		case k == "v" && !haveV:
			haveV = true
			if vArray = d.isArray(); vArray {
				v, ok = d.array()
			} else {
				var x any
				x, ok = d.scalar()
				v = []any{x}
			}
		case k == "vs" && !haveVs:
			haveVs = true
			vs, ok = d.array()
		default:
			ok = false
		}
		if !ok {
			return nil, false
		}
	}
	switch {
	case !haveKey:
		return nil, false
	case len(vs) > 0:
		// veloxe's Values: v is nil beside them.
		if vArray || len(v) == 1 && v[0] != nil {
			return nil, false
		}
		l.array, l.values = true, vs
	case vArray:
		l.array, l.values = true, v
	case len(v) == 1:
		l.one = v[0]
	}
	return &l, true
}

// take consumes n bytes, false when fewer are left.
func (d *msgDecoder) take(n uint64) ([]byte, bool) {
	if n > uint64(len(d.b)) {
		return nil, false
	}
	p := d.b[:n]
	d.b = d.b[n:]
	return p, true
}

// uint reads a big-endian unsigned integer of n bytes.
func (d *msgDecoder) uint(n uint64) (uint64, bool) {
	p, ok := d.take(n)
	if !ok {
		return 0, false
	}
	var x uint64
	for _, c := range p {
		x = x<<8 | uint64(c)
	}
	return x, true
}

// head consumes the next type byte: 0xc1, which msgpack never uses, when
// none is left.
func (d *msgDecoder) head() byte {
	if len(d.b) == 0 {
		return 0xc1
	}
	c := d.b[0]
	d.b = d.b[1:]
	return c
}

// mapLen reads a map's header: its number of entries.
func (d *msgDecoder) mapLen() (uint64, bool) {
	switch c := d.head(); {
	case c >= 0x80 && c <= 0x8f:
		return uint64(c & 0x0f), true
	case c == 0xde:
		return d.uint(2)
	case c == 0xdf:
		return d.uint(4)
	}
	return 0, false
}

// isArray reports whether the next value is an array.
func (d *msgDecoder) isArray() bool {
	return len(d.b) > 0 && (d.b[0] >= 0x90 && d.b[0] <= 0x9f || d.b[0] == 0xdc || d.b[0] == 0xdd)
}

// arrayLen reads an array's header: its number of elements, each at least
// one byte, so never more than are left.
func (d *msgDecoder) arrayLen() (uint64, bool) {
	var n uint64
	ok := true
	switch c := d.head(); {
	case c >= 0x90 && c <= 0x9f:
		n = uint64(c & 0x0f)
	case c == 0xdc:
		n, ok = d.uint(2)
	case c == 0xdd:
		n, ok = d.uint(4)
	default:
		return 0, false
	}
	return n, ok && n <= uint64(len(d.b))
}

// array reads an array of scalars.
func (d *msgDecoder) array() ([]any, bool) {
	n, ok := d.arrayLen()
	if !ok {
		return nil, false
	}
	vs := make([]any, n)
	for i := range vs {
		if vs[i], ok = d.scalar(); !ok {
			return nil, false
		}
	}
	return vs, true
}

// str reads a string of valid UTF-8.
func (d *msgDecoder) str() (string, bool) {
	return d.strBody(d.head())
}

// strBody reads the rest of a string whose type byte is c.
func (d *msgDecoder) strBody(c byte) (string, bool) {
	var n uint64
	ok := true
	switch {
	case c >= 0xa0 && c <= 0xbf:
		n = uint64(c & 0x1f)
	case c == 0xd9:
		n, ok = d.uint(1)
	case c == 0xda:
		n, ok = d.uint(2)
	case c == 0xdb:
		n, ok = d.uint(4)
	default:
		return "", false
	}
	if !ok {
		return "", false
	}
	p, ok := d.take(n)
	if !ok || !utf8.Valid(p) {
		return "", false
	}
	return string(p), true
}

// bin reads a binary.
func (d *msgDecoder) bin() ([]byte, bool) {
	return d.binBody(d.head())
}

// binBody reads the rest of a binary whose type byte is c.
func (d *msgDecoder) binBody(c byte) ([]byte, bool) {
	var n uint64
	var ok bool
	switch c {
	case 0xc4:
		n, ok = d.uint(1)
	case 0xc5:
		n, ok = d.uint(2)
	case 0xc6:
		n, ok = d.uint(4)
	}
	if !ok {
		return nil, false
	}
	return d.take(n)
}

// scalar reads a value that is not an array or a map, as a JSON value.
func (d *msgDecoder) scalar() (any, bool) {
	switch c := d.head(); {
	case c <= 0x7f:
		return json.Number(strconv.FormatUint(uint64(c), 10)), true
	case c >= 0xe0:
		return json.Number(strconv.FormatInt(int64(int8(c)), 10)), true
	case c >= 0xa0 && c <= 0xbf, c >= 0xd9 && c <= 0xdb:
		return d.strBody(c)
	case c >= 0xc4 && c <= 0xc6:
		b, ok := d.binBody(c)
		if !ok || len(b) != 16 {
			return nil, false
		}
		return FormatUUID([16]byte(b)), true
	case c >= 0xc7 && c <= 0xc9, c >= 0xd4 && c <= 0xd8:
		return d.ext(c)
	case c == 0xc0:
		return nil, true
	case c == 0xc2:
		return false, true
	case c == 0xc3:
		return true, true
	case c >= 0xcc && c <= 0xcf:
		x, ok := d.uint(1 << (c - 0xcc))
		return json.Number(strconv.FormatUint(x, 10)), ok
	case c >= 0xd0 && c <= 0xd3:
		n := uint64(1) << (c - 0xd0)
		x, ok := d.uint(n)
		// Sign-extend the n bytes.
		shift := 64 - 8*n
		return json.Number(strconv.FormatInt(int64(x<<shift)>>shift, 10)), ok
	case c == 0xca:
		x, ok := d.uint(4)
		f := float64(math.Float32frombits(uint32(x)))
		return floatValue(strconv.FormatFloat(f, 'g', -1, 32), f), ok
	case c == 0xcb:
		x, ok := d.uint(8)
		f := math.Float64frombits(x)
		return floatValue(strconv.FormatFloat(f, 'g', -1, 64), f), ok
	}
	return nil, false
}

// floatValue is f as a cursor's JSON value: the number s, or for NaN and
// the infinities their string, as JSONValue writes them.
func floatValue(s string, f float64) any {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return s
	}
	return json.Number(s)
}

// ext reads the rest of an extension whose type byte is c: veloxe's time,
// type 1, whose payload is the msgpack array [MarshalBinary of the time,
// its zone's name]. It is the time as RFC 3339 with nanoseconds and the
// zone offset, as JSONValue writes a time.
func (d *msgDecoder) ext(c byte) (any, bool) {
	var n uint64
	ok := true
	switch {
	case c >= 0xd4 && c <= 0xd8:
		n = 1 << (c - 0xd4)
	case c == 0xc7:
		n, ok = d.uint(1)
	case c == 0xc8:
		n, ok = d.uint(2)
	case c == 0xc9:
		n, ok = d.uint(4)
	}
	if !ok {
		return nil, false
	}
	if d.head() != 1 {
		return nil, false
	}
	p, ok := d.take(n)
	if !ok {
		return nil, false
	}
	e := msgDecoder{b: p}
	if k, ok := e.arrayLen(); !ok || k != 2 {
		return nil, false
	}
	bin, ok := e.bin()
	if !ok {
		return nil, false
	}
	if _, ok := e.str(); !ok || len(e.b) != 0 {
		return nil, false
	}
	var t time.Time
	if t.UnmarshalBinary(bin) != nil {
		return nil, false
	}
	return t.Format(time.RFC3339Nano), true
}
