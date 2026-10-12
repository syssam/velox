package velox

import (
	"errors"
	"fmt"
	"io"
	"strconv"
)

// This file is the vocabulary of a paged list, whatever serves it (REST,
// GraphQL, RPC): what the Paginate of generated code takes and returns.

// Cursor is the position of a row in an ordering: an opaque string a page
// returns with each row and takes back as After or Before. A cursor made for
// one ordering is refused under another.
type Cursor string

// MarshalGQL and UnmarshalGQL are the methods a GraphQL engine binds a
// scalar by (gqlgen's and graphql-go's Marshaler); nothing here imports one.
func (c Cursor) MarshalGQL(w io.Writer) { _, _ = io.WriteString(w, strconv.Quote(string(c))) }

// UnmarshalGQL takes the cursor a client sent back.
func (c *Cursor) UnmarshalGQL(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("%w: it is a %T, not a string", ErrBadCursor, v)
	}
	*c = Cursor(s)
	return nil
}

// PageInfo says where a page lies in the whole: Relay's PageInfo.
type PageInfo struct {
	HasNextPage     bool    `json:"hasNextPage"`
	HasPreviousPage bool    `json:"hasPreviousPage"`
	StartCursor     *Cursor `json:"startCursor"`
	EndCursor       *Cursor `json:"endCursor"`
}

// ErrBadCursor is returned for a cursor that this query did not produce: it
// is malformed, or it belongs to another ordering.
var ErrBadCursor = errors.New("velox: invalid cursor")
