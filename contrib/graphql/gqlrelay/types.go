package gqlrelay

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"

	"github.com/vmihailenco/msgpack/v5"

	velox "github.com/syssam/velox"
	"github.com/syssam/velox/dialect/sql"
)

// OrderDirection defines the ordering direction for pagination.
type OrderDirection string

// Order direction constants.
const (
	OrderDirectionAsc  OrderDirection = "ASC"
	OrderDirectionDesc OrderDirection = "DESC"
)

// String returns the string representation of OrderDirection.
func (o OrderDirection) String() string {
	return string(o)
}

// Validate validates the OrderDirection value.
func (o OrderDirection) Validate() error {
	if o != OrderDirectionAsc && o != OrderDirectionDesc {
		return fmt.Errorf("invalid order direction: %q", o)
	}
	return nil
}

// Reverse returns the reverse direction.
func (o OrderDirection) Reverse() OrderDirection {
	if o == OrderDirectionAsc {
		return OrderDirectionDesc
	}
	return OrderDirectionAsc
}

// OrderTermOption returns the SQL order term option for this direction.
func (o OrderDirection) OrderTermOption() sql.OrderTermOption {
	if o == OrderDirectionDesc {
		return sql.OrderDesc()
	}
	return sql.OrderAsc()
}

// MarshalGQL implements the graphql.Marshaler interface.
// Emits the value as a JSON-quoted string (e.g. `"ASC"`) so the surrounding
// GraphQL response is valid JSON. Matches Ent's entgql.OrderDirection.MarshalGQL.
func (o OrderDirection) MarshalGQL(w io.Writer) {
	io.WriteString(w, strconv.Quote(o.String()))
}

// UnmarshalGQL implements the graphql.Unmarshaler interface.
func (o *OrderDirection) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("order direction must be a string")
	}
	*o = OrderDirection(s)
	return o.Validate()
}

// Cursor is a pagination cursor.
type Cursor struct {
	ID     any           `msgpack:"i"`
	Value  velox.Value   `msgpack:"v,omitempty"`
	Values []velox.Value `msgpack:"vs,omitempty"`
}

// MarshalGQL implements the graphql.Marshaler interface.
// Uses msgpack + base64 encoding like Ent for compact cursor tokens.
//
// Encode through a local buffer instead of streaming directly into `w`, so
// that an encoding failure (unencodable Value type — a programming bug)
// produces an empty cursor token `""` rather than a half-flushed base64
// stream that decodes to garbage msgpack on the next request. UnmarshalGQL
// treats `""` as a no-op cursor, so the failure mode is "this response has
// no cursor" instead of "the next paginated request fails with a confusing
// decode error far from the source".
func (c Cursor) MarshalGQL(w io.Writer) {
	quote := []byte{'"'}
	var body bytes.Buffer
	enc := base64.NewEncoder(base64.RawStdEncoding, &body)
	if err := msgpack.NewEncoder(enc).Encode(c); err != nil {
		// Close to release the base64 writer's internal buffer; we don't
		// use its contents because we're writing an empty token on failure.
		_ = enc.Close()
		slog.Error("gqlrelay: cursor msgpack encode failed; emitting empty token",
			"err", err)
		_, _ = w.Write(quote)
		_, _ = w.Write(quote)
		return
	}
	if err := enc.Close(); err != nil {
		slog.Error("gqlrelay: cursor base64 close failed; emitting empty token",
			"err", err)
		_, _ = w.Write(quote)
		_, _ = w.Write(quote)
		return
	}
	_, _ = w.Write(quote)
	_, _ = w.Write(body.Bytes())
	_, _ = w.Write(quote)
}

// UnmarshalGQL implements the graphql.Unmarshaler interface.
func (c *Cursor) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("cursor must be a string")
	}
	if s == "" {
		return nil
	}
	if err := msgpack.NewDecoder(
		base64.NewDecoder(
			base64.RawStdEncoding,
			strings.NewReader(s),
		),
	).Decode(c); err != nil {
		return fmt.Errorf("cannot decode cursor: %w", err)
	}
	return nil
}

// PageInfo is pagination information.
type PageInfo struct {
	HasNextPage     bool    `json:"hasNextPage"`
	HasPreviousPage bool    `json:"hasPreviousPage"`
	StartCursor     *Cursor `json:"startCursor,omitempty"`
	EndCursor       *Cursor `json:"endCursor,omitempty"`
}

// Exported field name constants for use in collected field checks.
const (
	EdgesField      = "edges"
	NodeField       = "node"
	PageInfoField   = "pageInfo"
	TotalCountField = "totalCount"
)
