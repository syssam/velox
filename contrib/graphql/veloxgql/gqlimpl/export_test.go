package gqlimpl

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// Paginate is NewConnection and Page in one call, for the tests.
func Paginate[E any](ctx context.Context, q velox.Query[E], typ string, args Args, order, key []OrderTerm[E]) (*Page[E], error) {
	c, err := NewConnection(ctx, q, typ, args, order, key)
	if err != nil {
		return nil, err
	}
	return c.Page(ctx)
}

// CursorOrder, CheckCursor and CursorValue export the cursor's unexported
// checks to the tests.
type CursorOrder = cursorOrder

func CheckCursor(c veloxgql.Cursor, typ string, order []CursorOrder) error {
	return checkCursor(c, typ, order)
}

func CursorValue[T any](v any) (T, error) { return cursorValue[T](v) }
