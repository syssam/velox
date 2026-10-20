package veloxgql

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"
)

// Time is the Time scalar: an RFC 3339 string with nanoseconds and the zone
// offset. The zero time is "0001-01-01T00:00:00Z", not null.
type Time time.Time

// MarshalGQL writes t as an RFC 3339 string with nanoseconds.
func (t Time) MarshalGQL(w io.Writer) {
	_, _ = io.WriteString(w, strconv.Quote(time.Time(t).Format(time.RFC3339Nano)))
}

// UnmarshalGQL sets t from v, an RFC 3339 string; the fraction of a second
// is optional.
func (t *Time) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return errors.New("veloxgql: Time must be an RFC 3339 string")
	}
	x, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return errors.New("veloxgql: Time must be an RFC 3339 string")
	}
	*t = Time(x)
	return nil
}

// UUID is the UUID scalar: the hyphenated hexadecimal string, lower case on
// output and either case on input.
type UUID [16]byte

// String is u's hyphenated lower-case form.
func (u UUID) String() string { return formatUUID(u) }

// MarshalGQL writes u as its hyphenated lower-case string.
func (u UUID) MarshalGQL(w io.Writer) {
	_, _ = io.WriteString(w, `"`+formatUUID(u)+`"`)
}

// UnmarshalGQL sets u from v, a hyphenated hexadecimal string.
func (u *UUID) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return errors.New("veloxgql: UUID must be a string")
	}
	x, ok := parseUUID(s)
	if !ok {
		return errors.New("veloxgql: UUID must be of the form xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx")
	}
	*u = x
	return nil
}

// formatUUID is u's hyphenated lower-case form, 8-4-4-4-12 hexadecimal
// digits.
func formatUUID(u [16]byte) string {
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

// parseUUID parses the hyphenated form, in either case, and no other.
func parseUUID(s string) ([16]byte, bool) {
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

// Bytes is the Bytes scalar: standard base64, with padding.
type Bytes []byte

// MarshalGQL writes b as a standard base64 string; nil is "".
func (b Bytes) MarshalGQL(w io.Writer) {
	_, _ = io.WriteString(w, `"`+base64.StdEncoding.EncodeToString(b)+`"`)
}

// UnmarshalGQL sets b from v, a standard base64 string with padding.
func (b *Bytes) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return errors.New("veloxgql: Bytes must be a base64 string")
	}
	x, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return errors.New("veloxgql: Bytes must be a standard base64 string with padding")
	}
	*b = x
	return nil
}

// JSON is the JSON scalar: any JSON value, written as it is. A generated
// getter converts a field's Go value to it with json.Marshal.
type JSON json.RawMessage

// MarshalGQL writes j as it is. An empty or invalid j writes null, so that
// the response stays valid JSON.
func (j JSON) MarshalGQL(w io.Writer) {
	if len(j) == 0 || !json.Valid(j) {
		_, _ = io.WriteString(w, "null")
		return
	}
	_, _ = w.Write(j)
}

// UnmarshalGQL sets j to the JSON of v, the value gqlgen decoded from the
// request.
func (j *JSON) UnmarshalGQL(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return errors.New("veloxgql: JSON is not a JSON value")
	}
	*j = b
	return nil
}

// Uint64 is the Uint64 scalar: a decimal string both ways, since a JSON
// number loses precision above 2^53 in many clients.
type Uint64 uint64

// MarshalGQL writes u as a decimal string.
func (u Uint64) MarshalGQL(w io.Writer) {
	_, _ = io.WriteString(w, `"`+strconv.FormatUint(uint64(u), 10)+`"`)
}

// UnmarshalGQL sets u from v, a decimal string. A number is an error: the
// scalar is a string both ways.
func (u *Uint64) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return errors.New("veloxgql: Uint64 must be a decimal string")
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return errors.New("veloxgql: Uint64 must be a decimal string of at most 2^64-1")
	}
	*u = Uint64(n)
	return nil
}
