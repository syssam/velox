package graphqlgo

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/syssam/graphql-go"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// Scalars binds velox's scalars to the Go types velox entities hold, as
// veloxgql's scalar types read and write them: Time to time.Time
// (graphql.Time, an RFC 3339 string), UUID to uuid.UUID (the hyphenated
// lower-case string), Bytes to []byte (standard base64 with padding; nil,
// a NULL column, is null, as under gqlgen) and
// Uint64 to uint64 and uint (a decimal string both ways). It binds GraphQL's
// Int to int8, int16, uint8, uint16 and uint32 too, beside graphql-go's own
// int, int32 and int64, so that the generated inputs' fields of those types
// bind: a value outside the Go type's range, or Int's, is the client's
// error. A pointer to each binds with it, nil being null. The schema must
// declare Time, UUID, Bytes and Uint64, as the vocabulary velox-graphql
// writes does; NewSchema fails on one it lacks, and ScalarsOnly binds some
// of them. gqlc binds Cursor, JSON, OrderDirection and PageInfo from their
// own methods and fields. Give it to graphql.NewSchema with the generated
// bindings.
func Scalars() graphql.SchemaOption {
	return ScalarsOnly(scalarNames...)
}

// ScalarsOnly is Scalars of the named scalars alone, of Time, UUID, Bytes
// and Uint64, for a schema that does not declare them all: velox-graphql's
// apptypes= leaves a scalar to the application, and a schema served in
// slices may lack any of them.
// graphql-go's NewSchema refuses a binding of a scalar the schema does not
// declare, and an option cannot see the schema, so the caller names them,
// as gqlc lists the scalars that need a binding:
//
//	s, err := graph.NewSchema(app.Bindings(r), graphqlgo.ScalarsOnly("Time"))
//
// GraphQL's Int binds to the small integer types whatever the names. It
// panics on any other name.
func ScalarsOnly(names ...string) graphql.SchemaOption {
	opts := []graphql.SchemaOption{
		intScalar[int8](), intScalar[int16](), intScalar[uint8](), intScalar[uint16](), intScalar[uint32](),
	}
	for _, name := range names {
		switch name {
		case "Time":
			opts = append(opts, graphql.Time("Time"))
		case "UUID":
			opts = append(opts, uuidScalar())
		case "Bytes":
			opts = append(opts, bytesScalar())
		case "Uint64":
			opts = append(opts, uint64Scalars())
		default:
			panic(fmt.Sprintf("graphqlgo.ScalarsOnly: %q is not one of velox's scalars: %s", name, strings.Join(scalarNames, ", ")))
		}
	}
	return graphql.Options(opts...)
}

// scalarNames are the scalars Scalars binds, which ScalarsOnly takes.
var scalarNames = []string{"Time", "UUID", "Bytes", "Uint64"}

// uuidScalar binds UUID to uuid.UUID.
func uuidScalar() graphql.SchemaOption {
	return graphql.Scalar("UUID", func(w *graphql.Writer, u uuid.UUID) error {
		w.String(veloxgql.UUID(u).String())
		return nil
	}, func(v any) (uuid.UUID, error) {
		var u veloxgql.UUID
		err := u.UnmarshalGQL(v)
		return uuid.UUID(u), err
	})
}

// bytesScalar binds Bytes to []byte.
func bytesScalar() graphql.SchemaOption {
	return graphql.Scalar("Bytes", func(w *graphql.Writer, b []byte) error {
		if b == nil {
			w.Null() // a NULL column, as gqlgen writes it
			return nil
		}
		w.String(base64.StdEncoding.EncodeToString(b))
		return nil
	}, func(v any) ([]byte, error) {
		var b veloxgql.Bytes
		err := b.UnmarshalGQL(v)
		return b, err
	})
}

// uint64Scalars binds Uint64 to uint64 and uint.
func uint64Scalars() graphql.SchemaOption {
	return graphql.Options(
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
		}, decodeUint),
	)
}

// decodeUint reads a Uint64 as a uint, which holds 32 bits on a 32-bit
// platform: a value it does not hold is the client's error.
func decodeUint(v any) (uint, error) {
	var u veloxgql.Uint64
	if err := u.UnmarshalGQL(v); err != nil {
		return 0, err
	}
	if uint64(uint(u)) != uint64(u) {
		return 0, fmt.Errorf("graphqlgo: the Uint64 %d does not fit a uint", uint64(u))
	}
	return uint(u), nil
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
