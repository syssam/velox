package gqlimpl

import (
	"context"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
)

// Paginate is paginate for the tests: NewConnection and Page in one call.
func Paginate[E any](ctx context.Context, q velox.Query[E], typ string, args Args, order, key []OrderTerm[E]) (*Page[E], error) {
	return paginate(ctx, q, typ, args, order, key)
}

// CursorOrder, CheckCursor and CursorValue export the cursor's unexported
// checks to the tests.
type CursorOrder = cursorOrder

func CheckCursor(c veloxgql.Cursor, typ string, order []CursorOrder) error {
	return checkCursor(c, typ, order)
}

func CursorValue[T any](v any) (T, error) { return cursorValue[T](v) }
