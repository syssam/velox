package veloxgql

import "errors"

// Errors veloxgql returns. Each is returned wrapped, in an error whose text
// starts "veloxgql: " and says what was expected, so a sentinel's own text is
// a bare phrase; test for them with errors.Is. None of them echoes what the
// client sent: the message names only what the server wanted.
//
// Each but ErrTooCostly, ErrResponseTooLarge, ErrTooManyErrors and
// ErrTimeout is an error in the client's input, and is returned in an error
// with the code CodeBadUserInput, which an adapter's error presenter shows
// the client. ErrTooCostly, ErrResponseTooLarge and ErrTooManyErrors have
// the code CodeTooCostly, and ErrTimeout CodeTimeout.
var (
	// ErrBadCursor: a cursor that does not decode, or one taken on another
	// type or under another order than the query's, or whose values are not
	// of the order's types.
	ErrBadCursor = errors.New("malformed cursor")
	// ErrPageTooLarge: first or last over the MaxPage of
	// the limits (LimitsOf).
	ErrPageTooLarge = errors.New("page is over the maximum")
	// ErrBadPagination: first or last below 0, both given, another
	// combination gqlgraph.Args refuses, or an orderBy over the MaxOrderTerms
	// of the limits (LimitsOf).
	ErrBadPagination = errors.New("bad pagination arguments")
	// ErrFilterTooDeep: a where input nested deeper than the MaxFilterDepth
	// of the limits (LimitsOf).
	ErrFilterTooDeep = errors.New("filter is nested too deep")
	// ErrFilterTooWide: a list of a where input (and, or, has<Edge>With, In,
	// NotIn) with more than the MaxFilterWidth of the limits (LimitsOf)
	// entries.
	ErrFilterTooWide = errors.New("filter list is too long")
	// ErrFilterTooLarge: a where argument over its budget of the
	// MaxFilterNodes of the limits (LimitsOf) inputs, predicates and values.
	ErrFilterTooLarge = errors.New("filter is too large")
	// ErrEmptyWhereInput: under gqlgraph.Type's LenientFilters
	// (filters=lenient), a where input that sets no predicate, nested in an
	// or, a not or a has<Edge>With, as entgql's generated
	// ErrEmpty<T>WhereInput. An and leaves such an input out.
	ErrEmptyWhereInput = errors.New("empty predicate where input")
	// ErrTooCostly: a request whose gqlgenx.Cost is over the MaxCost of the
	// adapter's limits. It is returned with the code CodeTooCostly, before
	// any statement runs.
	ErrTooCostly = errors.New("request is too costly")
	// ErrRequestTooLarge: a request whose query is over the MaxFields of the
	// limits, or whose body is over gqlgenx.MaxBodyBytes. Nothing of it was
	// validated.
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
)
