package veloxgql

import "errors"

// Errors veloxgql returns. Each is returned wrapped, in an error whose text
// starts "veloxgql: " and says what was expected, so a sentinel's own text is
// a bare phrase; test for them with errors.Is. None of them echoes the key of
// an ID: the client sent it, and the message names only what the server
// wanted.
//
// Each but ErrNoDB, ErrTooCostly, ErrResponseTooLarge, ErrTooManyErrors and ErrTimeout
// is an error in the client's input, and is returned in an error with the code
// CodeBadUserInput, which ErrorPresenter shows the client. ErrTooCostly,
// ErrResponseTooLarge and ErrTooManyErrors have the code CodeTooCostly, and ErrTimeout
// CodeTimeout. ErrNoDB is a programming error, which has no code: ErrorPresenter logs it
// and shows "internal error".
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
	// type or under another order than the query's, or whose values are not
	// of the order's types.
	ErrBadCursor = errors.New("malformed cursor")
	// ErrPageTooLarge: first or last over the MaxPage of
	// LimitsFromContext(ctx).
	ErrPageTooLarge = errors.New("page is over the maximum")
	// ErrBadPagination: first or last below 0, both given, another
	// combination gqlimpl.Args refuses, or an orderBy over the MaxOrderTerms
	// of LimitsFromContext(ctx).
	ErrBadPagination = errors.New("bad pagination arguments")
	// ErrFilterTooDeep: a where input nested deeper than the MaxFilterDepth
	// of LimitsFromContext(ctx).
	ErrFilterTooDeep = errors.New("filter is nested too deep")
	// ErrFilterTooWide: a list of a where input (and, or, has<Edge>With, In,
	// NotIn) with more than the MaxFilterWidth of LimitsFromContext(ctx)
	// entries.
	ErrFilterTooWide = errors.New("filter list is too long")
	// ErrFilterTooLarge: a where argument over its budget of the
	// MaxFilterNodes of LimitsFromContext(ctx) inputs, predicates and values.
	ErrFilterTooLarge = errors.New("filter is too large")
	// ErrTooCostly: a request whose Cost is over the MaxCost of the
	// Extension's limits. It is returned with the code CodeTooCostly, before
	// any statement runs.
	ErrTooCostly = errors.New("request is too costly")
	// ErrRequestTooLarge: a request whose query is over the MaxFields, or whose
	// body is over the MaxBodyBytes, of the limits. Nothing of it was validated.
	ErrRequestTooLarge = errors.New("request is too large")
	// ErrResponseTooLarge: the strings and bytes of a response are over the
	// MaxResponseBytes of the limits. The request stops there, and its data is null.
	ErrResponseTooLarge = errors.New("response is too large")
	// ErrTooManyErrors: a response made more than the MaxErrors of the limits. The
	// request stops there, and its data is null.
	ErrTooManyErrors = errors.New("response has too many errors")
	// ErrTimeout: a query or a mutation ran over the Timeout of the limits, whose
	// statements were cancelled. Its data is null.
	ErrTimeout = errors.New("request timed out")
	// ErrNoDB: a resolver ran with no DB in its context, which the
	// Extension puts there.
	ErrNoDB = errors.New("no DB in the context")
)
