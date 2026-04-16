package gqlrelay

import (
	"encoding/base64"
	"fmt"
	"io"
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
func (o OrderDirection) MarshalGQL(w io.Writer) {
	io.WriteString(w, o.String())
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
// Uses streaming msgpack + base64 encoding like Ent for compact cursor tokens.
func (c Cursor) MarshalGQL(w io.Writer) {
	quote := []byte{'"'}
	_, _ = w.Write(quote)
	defer func() { _, _ = w.Write(quote) }()
	wc := base64.NewEncoder(base64.RawStdEncoding, w)
	defer wc.Close()
	_ = msgpack.NewEncoder(wc).Encode(c)
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
