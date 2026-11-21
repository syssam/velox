package graphqlgo

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/google/uuid"
	"github.com/syssam/graphql-go"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// Scalars binds velox's scalars to the Go types velox entities hold, as
// veloxgql's scalar types read and write them: Time to time.Time
// (graphql.Time, an RFC 3339 string), UUID to uuid.UUID (the hyphenated
// lower-case string), Bytes to []byte (standard base64 with padding) and
// Uint64 to uint64 and uint (a decimal string both ways). It binds GraphQL's
// Int to int8, int16, uint8, uint16 and uint32 too, beside graphql-go's own
// int, int32 and int64, so that the generated inputs' fields of those types
// bind: a value outside the Go type's range, or Int's, is the client's
// error. A pointer to each binds with it, nil being null. The schema must
// declare Time, UUID, Bytes and Uint64, as the vocabulary velox-graphql
// writes does; NewSchema fails on one it lacks. gqlc binds Cursor, JSON,
// OrderDirection and PageInfo from their own methods and fields. Give it
// to graphql.NewSchema with the generated bindings.
func Scalars() graphql.SchemaOption {
	return graphql.Options(
		graphql.Time("Time"),
		graphql.Scalar("UUID", func(w *graphql.Writer, u uuid.UUID) error {
			w.String(veloxgql.UUID(u).String())
			return nil
		}, func(v any) (uuid.UUID, error) {
			var u veloxgql.UUID
			err := u.UnmarshalGQL(v)
			return uuid.UUID(u), err
		}),
		graphql.Scalar("Bytes", func(w *graphql.Writer, b []byte) error {
			w.String(base64.StdEncoding.EncodeToString(b))
			return nil
		}, func(v any) ([]byte, error) {
			var b veloxgql.Bytes
			err := b.UnmarshalGQL(v)
			return b, err
		}),
		graphql.Scalar("Uint64", func(w *graphql.Writer, u uint64) error {
			w.String(strconv.FormatUint(u, 10))
			return nil
		}, func(v any) (uint64, error) {
			var u veloxgql.Uint64
			err := u.UnmarshalGQL(v)
			return uint64(u), err
		}),
		graphql.Scalar("Uint64", func(w *graphql.Writer, u uint) error {
			w.String(strconv.FormatUint(uint64(u), 10))
			return nil
		}, func(v any) (uint, error) {
			var u veloxgql.Uint64
			err := u.UnmarshalGQL(v)
			return uint(u), err
		}),
		intScalar[int8](), intScalar[int16](), intScalar[uint8](), intScalar[uint16](), intScalar[uint32](),
	)
}

// smallInt are the integer Go types velox entities hold that graphql-go
// does not bind to Int itself.
type smallInt interface {
	~int8 | ~int16 | ~uint8 | ~uint16 | ~uint32
}

// intScalar binds Int to T, with writeInt and decodeInt.
func intScalar[T smallInt]() graphql.SchemaOption {
	return graphql.Scalar("Int", writeInt[T], decodeInt[T])
}

// writeInt writes v as a JSON number; one over Int's 32 bits is an error,
// as graphql-go's own Int has it.
func writeInt[T smallInt](w *graphql.Writer, v T) error {
	if int64(v) > math.MaxInt32 {
		return fmt.Errorf("graphqlgo: %d is outside Int's 32 bits", v)
	}
	w.Int64(int64(v))
	return nil
}

// decodeInt reads raw, an Int's value as graphql-go gives it to a scalar's
// unmarshal (a json.Number, or an integer), as a T: it must be an integer
// within both T's range and Int's.
func decodeInt[T smallInt](raw any) (T, error) {
	var n int64
	switch v := raw.(type) {
	case json.Number:
		var err error
		if n, err = v.Int64(); err != nil {
			return 0, fmt.Errorf("graphqlgo: an Int must be an integer, not %v", raw)
		}
	case int64:
		n = v
	case int:
		n = int64(v)
	default:
		return 0, fmt.Errorf("graphqlgo: an Int must be an integer, not %v", raw)
	}
	if n < math.MinInt32 || n > math.MaxInt32 || int64(T(n)) != n {
		return 0, fmt.Errorf("graphqlgo: the Int %d does not fit a %T", n, T(0))
	}
	return T(n), nil
}
