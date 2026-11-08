package veloxgql

import (
	"encoding/json"
	"io"
	"strconv"

	"github.com/99designs/gqlgen/graphql"

	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// A GraphQL ID is the entity's own key, in its own Go type, as in entgql.
// gqlgen binds every ID argument to the first model gqlgen.yml lists for ID,
// so the keys of a schema's GraphQL types are of one Go type, and the model
// is the one that marshals it: Int64ID for int64 keys, UUID for UUID keys,
// gqlgen's graphql.ID for string keys. The package documentation of
// contrib/graphql lists them.

// MarshalInt64ID writes id, an int64 key, as the ID scalar: a JSON string of
// its decimal digits, as the GraphQL specification serializes an ID and
// gqlgen's graphql.IntID does. The model veloxgql.Int64ID in gqlgen.yml binds
// ID to it.
func MarshalInt64ID(id int64) graphql.Marshaler {
	return graphql.WriterFunc(func(w io.Writer) {
		_, _ = io.WriteString(w, `"`+strconv.FormatInt(id, 10)+`"`)
	})
}

// UnmarshalInt64ID reads the ID scalar into an int64 key. It takes a string
// of decimal digits, as MarshalInt64ID writes, or an integer, as the
// specification lets a client send an ID. Anything else, or a number that is
// no int64, is BAD_USER_INPUT.
func UnmarshalInt64ID(v any) (int64, error) {
	switch x := v.(type) {
	case string:
		return parseInt64ID(x)
	case json.Number:
		return parseInt64ID(string(x))
	case int64:
		return x, nil
	case int:
		return int64(x), nil
	case int32:
		return int64(x), nil
	}
	return 0, gqlcore.InputErrorf("ID must be a string of an integer, or an integer")
}

// parseInt64ID is s, the decimal digits of an int64, as an int64.
func parseInt64ID(s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, gqlcore.InputErrorf("ID must be the decimal digits of a 64-bit integer")
	}
	return n, nil
}
