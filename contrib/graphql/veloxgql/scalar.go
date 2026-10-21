package veloxgql

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/99designs/gqlgen/graphql"

	"github.com/syssam/velox2/contrib/graphql/veloxgql/internal/shared"
)

// The scalars, the Cursor and the OrderDirection enum are gqlgen marshalers.
var (
	_ graphql.Marshaler   = (*Time)(nil)
	_ graphql.Unmarshaler = (*Time)(nil)
	_ graphql.Marshaler   = (*UUID)(nil)
	_ graphql.Unmarshaler = (*UUID)(nil)
	_ graphql.Marshaler   = (*Bytes)(nil)
	_ graphql.Unmarshaler = (*Bytes)(nil)
	_ graphql.Marshaler   = (*JSON)(nil)
	_ graphql.Unmarshaler = (*JSON)(nil)
	_ graphql.Marshaler   = (*Uint64)(nil)
	_ graphql.Unmarshaler = (*Uint64)(nil)
	_ graphql.Marshaler   = (*Cursor)(nil)
	_ graphql.Unmarshaler = (*Cursor)(nil)
	_ graphql.Marshaler   = (*OrderDirection)(nil)
	_ graphql.Unmarshaler = (*OrderDirection)(nil)
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
func (u UUID) String() string { return shared.FormatUUID(u) }

// MarshalGQL writes u as its hyphenated lower-case string.
func (u UUID) MarshalGQL(w io.Writer) {
	_, _ = io.WriteString(w, `"`+shared.FormatUUID(u)+`"`)
}

// UnmarshalGQL sets u from v, a hyphenated hexadecimal string.
func (u *UUID) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return errors.New("veloxgql: UUID must be a string")
	}
	x, ok := shared.ParseUUID(s)
	if !ok {
		return errors.New("veloxgql: UUID must be of the form xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx")
	}
	*u = x
	return nil
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

// base is v with a scalar of this package unwrapped to its base type (Time to
// time.Time, UUID to [16]byte, Uint64 to uint64, Bytes to []byte), which is
// what package shared handles; any other v is returned as it is.
func base(v any) any {
	switch x := v.(type) {
	case Time:
		return time.Time(x)
	case UUID:
		return [16]byte(x)
	case Uint64:
		return uint64(x)
	case Bytes:
		return []byte(x)
	}
	return v
}

// basePtr is base for a pointer to set.
func basePtr(p any) any {
	switch x := p.(type) {
	case *Time:
		return (*time.Time)(x)
	case *UUID:
		return (*[16]byte)(x)
	case *Uint64:
		return (*uint64)(x)
	case *Bytes:
		return (*[]byte)(x)
	}
	return p
}
