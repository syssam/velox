package gqlrelay

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

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

// cursorTimeExt is the msgpack extension a cursor encodes time values with.
// msgpack's own time extension keeps only the instant and decodes it in the
// local zone. SQLite stores times as text (modernc writes t.String(), e.g.
// "2026-01-01 00:00:00 -0500 EST") and compares them as text, so the next
// page's `(col, id) > (?, ?)` must bind the same instant with the same
// offset AND zone name, or it compares unequal strings for equal times — an
// empty or endlessly repeated page. The extension keeps all three.
const cursorTimeExt int8 = 1

// cursorTime carries a time.Time through a cursor with its offset and zone
// name: time.MarshalBinary keeps the instant and offset, the name is stored
// beside it, and time.FixedZone(name, offset) prints as the original did.
type cursorTime struct{ t time.Time }

func (c *cursorTime) MarshalMsgpack() ([]byte, error) {
	bin, err := c.t.MarshalBinary()
	if err != nil {
		return nil, err
	}
	name, _ := c.t.Zone()
	return msgpack.Marshal([]any{bin, name})
}

func (c *cursorTime) UnmarshalMsgpack(b []byte) error {
	var parts struct {
		_msgpack struct{} `msgpack:",as_array"`
		Bin      []byte
		Name     string
	}
	if err := msgpack.Unmarshal(b, &parts); err != nil {
		return err
	}
	if err := c.t.UnmarshalBinary(parts.Bin); err != nil {
		return err
	}
	if _, offset := c.t.Zone(); parts.Name != "" && c.t.Location() != time.UTC {
		c.t = c.t.In(time.FixedZone(parts.Name, offset))
	}
	return nil
}

func init() { msgpack.RegisterExt(cursorTimeExt, (*cursorTime)(nil)) }

// wrapTime and unwrapTime convert cursor order values to and from their
// encoded form; every other value passes through unchanged.
// A multi-order cursor holds its values as a []any in Value, so both walk
// into slices.
func wrapTime(v velox.Value) velox.Value {
	switch t := v.(type) {
	case time.Time:
		return &cursorTime{t: t}
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = wrapTime(e)
		}
		return out
	}
	return v
}

func unwrapTime(v velox.Value) velox.Value {
	switch t := v.(type) {
	case *cursorTime:
		return t.t
	case cursorTime:
		return t.t
	case []any:
		for i, e := range t {
			t[i] = unwrapTime(e)
		}
		return t
	}
	return v
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
	c.Value = wrapTime(c.Value)
	if len(c.Values) > 0 {
		values := make([]velox.Value, len(c.Values))
		for i, v := range c.Values {
			values[i] = wrapTime(v)
		}
		c.Values = values
	}
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
	c.Value = unwrapTime(c.Value)
	for i, v := range c.Values {
		c.Values[i] = unwrapTime(v)
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
