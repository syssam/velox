// Package shared holds the conversions between a Go value and the JSON value
// of a cursor, or the text of an ID, that veloxgql (IDs) and gqlimpl
// (cursors) both need. It knows base types only (time.Time, [16]byte, uint64,
// []byte): the callers unwrap veloxgql's scalars first. It is not part of the
// API.
package shared

import (
	"encoding"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"
)

// The ways SetValue fails.
var (
	ErrUnsupported = errors.New("unsupported type")
	ErrMismatch    = errors.New("value of another type")
)

// Text is the key of an ID: text that stands for a number or a string, as the
// key's Go type needs. A JSON value stands for a number only as a
// json.Number, and for a string only as a string.
type Text string

// KeyText is key in its canonical text, the text of an ID; false when key's
// type is not a key type.
func KeyText(key any) (string, bool) {
	switch k := key.(type) {
	case int:
		return strconv.FormatInt(int64(k), 10), true
	case int8:
		return strconv.FormatInt(int64(k), 10), true
	case int16:
		return strconv.FormatInt(int64(k), 10), true
	case int32:
		return strconv.FormatInt(int64(k), 10), true
	case int64:
		return strconv.FormatInt(k, 10), true
	case uint:
		return strconv.FormatUint(uint64(k), 10), true
	case uint8:
		return strconv.FormatUint(uint64(k), 10), true
	case uint16:
		return strconv.FormatUint(uint64(k), 10), true
	case uint32:
		return strconv.FormatUint(uint64(k), 10), true
	case uint64:
		return strconv.FormatUint(k, 10), true
	case string:
		return k, true
	case [16]byte:
		return FormatUUID(k), true
	case encoding.TextMarshaler:
		b, err := k.MarshalText()
		return string(b), err == nil
	}
	return "", false
}

// JSONValue is v as a cursor holds it in JSON: integers stay numbers, so
// that json.Marshal writes every digit; times are RFC 3339 with nanoseconds
// and the zone offset; UUIDs are strings; floats with no JSON number are the
// strings NaN, +Inf and -Inf.
func JSONValue(v any) any {
	switch x := v.(type) {
	case time.Time:
		return x.Format(time.RFC3339Nano)
	case [16]byte:
		return FormatUUID(x)
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return strconv.FormatFloat(x, 'g', -1, 64)
		}
	case float32:
		if f := float64(x); math.IsNaN(f) || math.IsInf(f, 0) {
			return strconv.FormatFloat(f, 'g', -1, 32)
		}
	}
	return v
}

// SetValue sets *p from v, a value of a decoded cursor (a json.Number, a
// string, a bool or nil) or the text of an ID's key. It fails with
// ErrMismatch when v is not of *p's type, and ErrUnsupported when p's type is
// none SetValue knows.
func SetValue(p, v any) error {
	switch p := p.(type) {
	case *int:
		return setInt(p, v, strconv.IntSize)
	case *int8:
		return setInt(p, v, 8)
	case *int16:
		return setInt(p, v, 16)
	case *int32:
		return setInt(p, v, 32)
	case *int64:
		return setInt(p, v, 64)
	case *uint:
		return setUint(p, v, strconv.IntSize)
	case *uint8:
		return setUint(p, v, 8)
	case *uint16:
		return setUint(p, v, 16)
	case *uint32:
		return setUint(p, v, 32)
	case *uint64:
		return setUint(p, v, 64)
	case *float64:
		return setFloat(p, v, 64)
	case *float32:
		return setFloat(p, v, 32)
	case *string:
		s, ok := stringOf(v)
		*p = s
		return okOrMismatch(ok)
	case *bool:
		b, ok := v.(bool)
		*p = b
		return okOrMismatch(ok)
	case *time.Time:
		return setTime(p, v)
	case *[16]byte:
		return setUUID(p, v)
	case *[]byte:
		return setBytes(p, v)
	case encoding.TextUnmarshaler:
		s, ok := stringOf(v)
		if !ok || p.UnmarshalText([]byte(s)) != nil {
			return ErrMismatch
		}
		return nil
	}
	return ErrUnsupported
}

func okOrMismatch(ok bool) error {
	if !ok {
		return ErrMismatch
	}
	return nil
}

// numberOf is the text of v when v stands for a number.
func numberOf(v any) (string, bool) {
	switch v := v.(type) {
	case json.Number:
		return string(v), true
	case Text:
		return string(v), true
	}
	return "", false
}

// stringOf is v when v stands for a string.
func stringOf(v any) (string, bool) {
	switch v := v.(type) {
	case string:
		return v, true
	case Text:
		return string(v), true
	}
	return "", false
}

func setInt[T int | int8 | int16 | int32 | int64](p *T, v any, bits int) error {
	s, ok := numberOf(v)
	if !ok {
		return ErrMismatch
	}
	n, err := strconv.ParseInt(s, 10, bits)
	if err != nil {
		return ErrMismatch
	}
	*p = T(n)
	return nil
}

func setUint[T uint | uint8 | uint16 | uint32 | uint64](p *T, v any, bits int) error {
	s, ok := numberOf(v)
	if !ok {
		return ErrMismatch
	}
	n, err := strconv.ParseUint(s, 10, bits)
	if err != nil {
		return ErrMismatch
	}
	*p = T(n)
	return nil
}

func setFloat[T float32 | float64](p *T, v any, bits int) error {
	s, ok := numberOf(v)
	if !ok {
		// The floats JSON has no number for (jsonValue).
		if s, _ = v.(string); s != "NaN" && s != "+Inf" && s != "-Inf" {
			return ErrMismatch
		}
	}
	f, err := strconv.ParseFloat(s, bits)
	if err != nil {
		return ErrMismatch
	}
	*p = T(f)
	return nil
}

func setTime[T time.Time](p *T, v any) error {
	s, ok := stringOf(v)
	if !ok {
		return ErrMismatch
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return ErrMismatch
	}
	*p = T(t)
	return nil
}

func setUUID[T [16]byte](p *T, v any) error {
	s, ok := stringOf(v)
	if !ok {
		return ErrMismatch
	}
	u, ok := ParseUUID(s)
	if !ok {
		return ErrMismatch
	}
	*p = T(u)
	return nil
}

func setBytes[T []byte](p *T, v any) error {
	s, ok := stringOf(v)
	if !ok {
		return ErrMismatch
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return ErrMismatch
	}
	*p = T(b)
	return nil
}

// TypeName is T's name, an interface type's too, which %T of a zero T would
// print as <nil>.
func TypeName[T any]() string {
	return fmt.Sprintf("%T", (*T)(nil))[1:]
}

// FormatUUID is u's hyphenated lower-case form, 8-4-4-4-12 hexadecimal
// digits.
func FormatUUID(u [16]byte) string {
	var b [36]byte
	hex.Encode(b[0:8], u[0:4])
	b[8] = '-'
	hex.Encode(b[9:13], u[4:6])
	b[13] = '-'
	hex.Encode(b[14:18], u[6:8])
	b[18] = '-'
	hex.Encode(b[19:23], u[8:10])
	b[23] = '-'
	hex.Encode(b[24:36], u[10:16])
	return string(b[:])
}

// ParseUUID parses the hyphenated form, in either case, and no other.
func ParseUUID(s string) ([16]byte, bool) {
	var u [16]byte
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return u, false
	}
	var digits [32]byte
	n := copy(digits[:], s[0:8])
	n += copy(digits[n:], s[9:13])
	n += copy(digits[n:], s[14:18])
	n += copy(digits[n:], s[19:23])
	copy(digits[n:], s[24:36])
	if _, err := hex.Decode(u[:], digits[:]); err != nil {
		return u, false
	}
	return u, true
}
