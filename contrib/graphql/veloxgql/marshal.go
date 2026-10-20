package veloxgql

import (
	"errors"
	"io"
	"math"
	"strconv"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/google/uuid"
)

// The function marshalers bind the scalars to the Go types velox entities
// hold, which gqlgen binds only to a scalar model of their own type. gqlgen
// looks for Marshal<Name> and Unmarshal<Name> in the package of a model
// named <Name>, before the type of that name: the model veloxgql.Time binds
// a time.Time through MarshalTime, and the types Time, UUID, Bytes and
// Uint64 are left to values that implement graphql.Marshaler themselves.
// gqlgen's own graphql.Int8 to graphql.Uint32 bind the narrow integers to
// Int; the gqlgen.yml in the package documentation lists them all.

// MarshalTime writes t as the Time scalar does.
func MarshalTime(t time.Time) graphql.Marshaler { return Time(t) }

// UnmarshalTime reads the Time scalar.
func UnmarshalTime(v any) (time.Time, error) {
	var t Time
	err := t.UnmarshalGQL(v)
	return time.Time(t), err
}

// MarshalUUID writes u as the UUID scalar does.
func MarshalUUID(u uuid.UUID) graphql.Marshaler { return UUID(u) }

// UnmarshalUUID reads the UUID scalar.
func UnmarshalUUID(v any) (uuid.UUID, error) {
	var u UUID
	err := u.UnmarshalGQL(v)
	return uuid.UUID(u), err
}

// MarshalUint64 writes u as the Uint64 scalar does, a decimal string.
func MarshalUint64(u uint64) graphql.Marshaler { return Uint64(u) }

// UnmarshalUint64 reads the Uint64 scalar, a decimal string.
func UnmarshalUint64(v any) (uint64, error) {
	var u Uint64
	err := u.UnmarshalGQL(v)
	return uint64(u), err
}

// MarshalUint writes u as the Uint64 scalar, a decimal string: a uint field
// is a Uint64, as a uint is 64 bits wide where velox runs.
func MarshalUint(u uint) graphql.Marshaler { return Uint64(u) }

// UnmarshalUint reads the Uint64 scalar into a uint.
func UnmarshalUint(v any) (uint, error) {
	n, err := UnmarshalUint64(v)
	if err == nil && uint64(uint(n)) != n {
		return 0, errors.New("veloxgql: Uint64 is over the largest uint")
	}
	return uint(n), err
}

// MarshalBytes writes b as the Bytes scalar does.
func MarshalBytes(b []byte) graphql.Marshaler { return Bytes(b) }

// UnmarshalBytes reads the Bytes scalar.
func UnmarshalBytes(v any) ([]byte, error) {
	var b Bytes
	err := b.UnmarshalGQL(v)
	return b, err
}

// MarshalFloat32 writes f as a Float, with the fewest digits that read back
// as f. An infinity or NaN, which JSON has no number for, writes null.
// gqlgen has no float32 marshaler: its Float is a float64.
func MarshalFloat32(f float32) graphql.Marshaler {
	return graphql.WriterFunc(func(w io.Writer) {
		if math.IsInf(float64(f), 0) || math.IsNaN(float64(f)) {
			_, _ = io.WriteString(w, "null")
			return
		}
		_, _ = io.WriteString(w, strconv.FormatFloat(float64(f), 'g', -1, 32))
	})
}

// UnmarshalFloat32 reads a Float into a float32. A value beyond the range
// of a float32 is an error, not an infinity.
func UnmarshalFloat32(v any) (float32, error) {
	f, err := graphql.UnmarshalFloat(v)
	switch {
	case err != nil:
		return 0, errors.New("veloxgql: Float must be a number")
	case math.Abs(f) > math.MaxFloat32:
		return 0, errors.New("veloxgql: Float is beyond the range of a float32")
	}
	return float32(f), nil
}
