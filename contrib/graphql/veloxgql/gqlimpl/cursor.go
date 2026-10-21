package gqlimpl

import (
	"errors"
	"fmt"

	"github.com/syssam/velox2/contrib/graphql/veloxgql"
	"github.com/syssam/velox2/contrib/graphql/veloxgql/internal/shared"
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
		return fmt.Errorf("veloxgql: %s cursor: %w: the key is an %w", typ, veloxgql.ErrBadCursor, veloxgql.ErrWrongIDType)
	}
	if len(c.Terms) != len(order) {
		return fmt.Errorf("veloxgql: %s cursor: %w: taken under another order", typ, veloxgql.ErrBadCursor)
	}
	for i, t := range c.Terms {
		if t.Field != order[i].Field || t.Direction != order[i].Direction {
			return fmt.Errorf("veloxgql: %s cursor: %w: taken under another order", typ, veloxgql.ErrBadCursor)
		}
	}
	return nil
}

// cursorValue converts v, a term's value or the key of a decoded cursor, to
// T, the Go type of its column: a Go integer or veloxgql.Uint64 from a
// number, a float from a number (or NaN, +Inf, -Inf), a string, a bool, a
// time.Time or veloxgql.Time from an RFC 3339 string, a [16]byte or
// veloxgql.UUID from the UUID string form, []byte or veloxgql.Bytes from
// standard base64, or a type whose pointer has UnmarshalText from a string.
// A v of another type fails with veloxgql.ErrBadCursor.
func cursorValue[T any](v any) (T, error) {
	var t T
	switch err := shared.SetValue(&t, v); {
	case errors.Is(err, shared.ErrUnsupported):
		var zero T
		return zero, fmt.Errorf("veloxgql: CursorValue: %s is not a supported type", shared.TypeName[T]())
	case err != nil:
		var zero T
		return zero, fmt.Errorf("veloxgql: %w: a value is not a %s", veloxgql.ErrBadCursor, shared.TypeName[T]())
	}
	return t, nil
}
