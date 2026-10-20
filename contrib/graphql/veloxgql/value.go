package veloxgql

import (
	"encoding"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"
)

// The ways setValue fails.
var (
	errUnsupported = errors.New("unsupported type")
	errMismatch    = errors.New("value of another type")
)

// text is the key of an ID: text that stands for a number or a string, as the
// key's Go type needs. A JSON value stands for a number only as a
// json.Number, and for a string only as a string.
type text string

// keyText is key in its canonical text, the text of an ID; false when key's
// type is not a key type.
func keyText(key any) (string, bool) {
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
		return formatUUID(k), true
	case UUID:
		return formatUUID(k), true
	case encoding.TextMarshaler:
		b, err := k.MarshalText()
		return string(b), err == nil
	}
	return "", false
}

// jsonValue is v as a cursor holds it in JSON: integers stay numbers, so
// that json.Marshal writes every digit; times are RFC 3339 with nanoseconds
// and the zone offset; UUIDs are strings; floats with no JSON number are the
// strings NaN, +Inf and -Inf.
func jsonValue(v any) any {
	switch x := v.(type) {
	case time.Time:
		return x.Format(time.RFC3339Nano)
	case Time:
		return time.Time(x).Format(time.RFC3339Nano)
	case [16]byte:
		return formatUUID(x)
	case UUID:
		return formatUUID(x)
	case Uint64:
		return uint64(x)
	case Bytes:
		return []byte(x)
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

// setValue sets *p from v, a value of a decoded cursor (a json.Number, a
// string, a bool or nil) or the text of an ID's key. It fails with
// errMismatch when v is not of *p's type, and errUnsupported when p's type is
// none setValue knows.
func setValue(p, v any) error {
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
	case *Uint64:
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
	case *Time:
		return setTime(p, v)
	case *[16]byte:
		return setUUID(p, v)
	case *UUID:
		return setUUID(p, v)
	case *[]byte:
		return setBytes(p, v)
	case *Bytes:
		return setBytes(p, v)
	case encoding.TextUnmarshaler:
		s, ok := stringOf(v)
		if !ok || p.UnmarshalText([]byte(s)) != nil {
			return errMismatch
		}
		return nil
	}
	return errUnsupported
}

func okOrMismatch(ok bool) error {
	if !ok {
		return errMismatch
	}
	return nil
}

// numberOf is the text of v when v stands for a number.
func numberOf(v any) (string, bool) {
	switch v := v.(type) {
	case json.Number:
		return string(v), true
	case text:
		return string(v), true
	}
	return "", false
}

// stringOf is v when v stands for a string.
func stringOf(v any) (string, bool) {
	switch v := v.(type) {
	case string:
		return v, true
	case text:
		return string(v), true
	}
	return "", false
}

func setInt[T int | int8 | int16 | int32 | int64](p *T, v any, bits int) error {
	s, ok := numberOf(v)
	if !ok {
		return errMismatch
	}
	n, err := strconv.ParseInt(s, 10, bits)
	if err != nil {
		return errMismatch
	}
	*p = T(n)
	return nil
}

func setUint[T uint | uint8 | uint16 | uint32 | uint64 | Uint64](p *T, v any, bits int) error {
	s, ok := numberOf(v)
	if !ok {
		return errMismatch
	}
	n, err := strconv.ParseUint(s, 10, bits)
	if err != nil {
		return errMismatch
	}
	*p = T(n)
	return nil
}

func setFloat[T float32 | float64](p *T, v any, bits int) error {
	s, ok := numberOf(v)
	if !ok {
		// The floats JSON has no number for (jsonValue).
		if s, _ = v.(string); s != "NaN" && s != "+Inf" && s != "-Inf" {
			return errMismatch
		}
	}
	f, err := strconv.ParseFloat(s, bits)
	if err != nil {
		return errMismatch
	}
	*p = T(f)
	return nil
}

func setTime[T time.Time | Time](p *T, v any) error {
	s, ok := stringOf(v)
	if !ok {
		return errMismatch
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return errMismatch
	}
	*p = T(t)
	return nil
}

func setUUID[T [16]byte | UUID](p *T, v any) error {
	s, ok := stringOf(v)
	if !ok {
		return errMismatch
	}
	u, ok := parseUUID(s)
	if !ok {
		return errMismatch
	}
	*p = T(u)
	return nil
}

func setBytes[T []byte | Bytes](p *T, v any) error {
	s, ok := stringOf(v)
	if !ok {
		return errMismatch
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return errMismatch
	}
	*p = T(b)
	return nil
}
