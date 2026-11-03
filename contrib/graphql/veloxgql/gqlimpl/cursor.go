package gqlimpl

import (
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// cursorOrder is one term of a query's order, as checkCursor compares it
// with a cursor's.
type cursorOrder struct {
	Field     string
	Direction veloxgql.OrderDirection
}

// checkCursor reports whether c may be used on a connection of the GraphQL
// type typ ordered by order, the query's terms without the primary key. A
// cursor of another type fails with ErrBadCursor and, since its key is
// another type's, ErrWrongIDType; one taken under another order (other
// fields, other directions or another number of terms) with ErrBadCursor.
// Neither error's text holds the cursor's type or fields.
func checkCursor(c veloxgql.Cursor, typ string, order []cursorOrder) error {
	if c.Type != typ {
		return InputErrorf("%s cursor: %w: the key is an %w", typ, veloxgql.ErrBadCursor, veloxgql.ErrWrongIDType)
	}
	if len(c.Terms) != len(order) {
		return InputErrorf("%s cursor: %w: taken under another order", typ, veloxgql.ErrBadCursor)
	}
	for i, t := range c.Terms {
		if t.Field != order[i].Field || t.Direction != order[i].Direction {
			return InputErrorf("%s cursor: %w: taken under another order", typ, veloxgql.ErrBadCursor)
		}
	}
	return nil
}
