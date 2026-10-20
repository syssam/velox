package veloxgql

import "errors"

// Errors veloxgql returns. Each is returned wrapped, in an error whose text
// starts "veloxgql: " and says what was expected, so a sentinel's own text is
// a bare phrase; test for them with errors.Is. None of them echoes the key of
// an ID: the client sent it, and the message names only what the server
// wanted.
var (
	// ErrBadID: an ID that is not base64url without padding of
	// "<Type>:<key>", or whose key is not the canonical text of a key of the
	// expected Go type.
	ErrBadID = errors.New("malformed ID")
	// ErrWrongIDType: a well-formed ID of another GraphQL type than the one
	// expected, such as a Post ID given where a User ID is. Nothing is looked
	// up with it.
	ErrWrongIDType = errors.New("ID of another type")
	// ErrBadCursor: a cursor that does not decode, or one taken on another
	// type or under another order than the query's (Cursor.Check), or whose
	// values are not of the order's types (CursorValue).
	ErrBadCursor = errors.New("malformed cursor")
	// ErrPageTooLarge: first or last over MaxPage.
	ErrPageTooLarge = errors.New("page is over the maximum")
	// ErrBadPagination: first or last below 0, or both given.
	ErrBadPagination = errors.New("bad pagination arguments")
	// ErrFilterTooDeep: a where input nested deeper than MaxFilterDepth.
	ErrFilterTooDeep = errors.New("filter is nested too deep")
	// ErrFilterTooWide: an and or an or list of a where input with more than
	// MaxFilterWidth entries.
	ErrFilterTooWide = errors.New("filter list is too long")
	// ErrNoDB: a resolver ran with no DB in its context, which the handler
	// puts there.
	ErrNoDB = errors.New("no DB in the context")
)
