package graphqlgo

import (
	"encoding/base64"
	"strconv"

	"github.com/google/uuid"
	"github.com/syssam/graphql-go"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// Scalars binds velox's scalars to the Go types velox entities hold, as
// veloxgql's scalar types read and write them: Time to time.Time
// (graphql.Time, an RFC 3339 string), UUID to uuid.UUID (the hyphenated
// lower-case string), Bytes to []byte (standard base64 with padding) and
// Uint64 to uint64 (a decimal string both ways). A pointer to each binds
// with it, nil being null. The schema must declare the four, as the
// vocabulary velox-graphql writes does; NewSchema fails on one it lacks.
// gqlc binds Cursor, JSON, OrderDirection and PageInfo from their own
// methods and fields.
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
	)
}
