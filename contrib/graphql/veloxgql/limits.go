package veloxgql

import (
	"context"
	"time"

	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// Limits caps what a client may ask for. Each bounds one argument or one
// edge. A zero field means the default.
//
// The limits of an operation are its adapter's, which it puts in the
// operation's context, where LimitsFromContext reads them: gqlgenx's those
// gqlgenx.WithLimits gives, so two servers in one process can have
// different limits, and graphqlgo's always DefaultLimits().
type Limits struct {
	// MaxPage is the largest first or last a connection takes, and the page
	// size when neither is given. More is ErrPageTooLarge, not a shorter
	// page.
	MaxPage int

	// MaxList is the most rows a list edge (graphql.Connection(false))
	// reads: the edge's query allows MaxList rows in place of the DB's
	// WithMaxRows cap, which is no cap unless the application sets one. A
	// node with more fails the edge with velox.ErrTooManyRows, in an error
	// that names the edge, rather than reading them all.
	MaxList int

	// MaxFilterDepth is how deep a where input may nest through not, and,
	// or and has<Edge>With. Deeper is ErrFilterTooDeep, before any query
	// runs.
	MaxFilterDepth int

	// MaxFilterWidth is the most entries a list in a where input may have:
	// an and, an or or a has<Edge>With list of inputs, or an In or a NotIn
	// list of values. More is ErrFilterTooWide, before any query runs.
	MaxFilterWidth int

	// MaxFilterNodes is the budget of one where argument: its inputs, the
	// inputs nested in them, the predicates they set and the values of their
	// In and NotIn lists each count one, across the whole argument. More is
	// ErrFilterTooLarge, before any query runs. Depth and width alone do not
	// bound an argument that holds one input many times over, as a GraphQL
	// variable used at many places does. Under velox-graphql's
	// compat=veloxe the budget is 10 times this. Either is held to 30000, so
	// that a statement's parameters stay under every dialect's limit.
	MaxFilterNodes int

	// MaxOrderTerms is the most terms an orderBy list may have, each an
	// ORDER BY term and a term of every cursor. More is ErrBadPagination,
	// before any query runs.
	MaxOrderTerms int

	// MaxCost is the most a request may cost (gqlgenx.Cost): the bound on a whole
	// request that the other limits, each of one argument, do not give. More
	// is ErrTooCostly, before any statement runs. It cannot see a value's size,
	// which is MaxResponseBytes, or how long a statement takes, which is Timeout. It bounds the statements too,
	// at 100 each, so 1500 statements by default. Two levels of full pages with 12 fields in
	// each row cost about 144000, and pass. A list edge costs MaxList for each
	// parent, so under the defaults one full page with two list edges, posts(first:
	// 100) { edges { node { likes { id } tags { id } } } }, costs 200602 and is
	// refused: raise MaxCost, or lower MaxList. The worst shape measured, 128-byte
	// aliases over rows, allocates about 2 KB a unit, so 290 MB at the default.
	// Introspection has its own limit.
	MaxCost int

	// MaxIntrospectionCost is the most the __schema and __type of a request may
	// cost, one for each field of their response. More is ErrTooCostly. The
	// default admits GraphiQL's query on a schema of 150 rich entities (about
	// 210000), or of about 100 with graphql.WhereOps on every field, and no data
	// comes under it: raising it does not loosen MaxCost,
	// nor MaxCost it. A request at this limit allocates about 160 MB through
	// gqlgen. A larger schema raises it, at about 640 bytes a unit; a server open
	// to strangers lowers it, or has no extension.Introspection.
	MaxIntrospectionCost int

	// MaxTokens is the most lexer tokens a request's query may have. More is the
	// parser's error, GRAPHQL_PARSE_FAILED, before any of it is validated.
	// gqlgenx.Extension enforces it, and gqlgenx.Setup gives it to gqlgen too.
	MaxTokens int

	// MaxFields is the most fields a request's query may hold, as written, a
	// repeat and an alias each counting one, and the most spreads and inline
	// fragments. More is ErrRequestTooLarge, BAD_USER_INPUT, before gqlgen validates
	// the query: validation is quadratic in the fields that share a response name,
	// so of those a query may hold 2 times MaxFields pairs that differ, whose errors
	// are the cost, and 200 times MaxFields pairs that agree, each fragment counted
	// where it is spread; and its spreads may reach 40 times MaxFields fields.
	MaxFields int

	// MaxFragments is the most fragments a request's query may name, defined or
	// spread. Validation stores a pair of any two spread together, so this is what
	// bounds them, at about 145 bytes a pair. More is ErrRequestTooLarge.
	MaxFragments int

	// MaxDepth is how deep a request's selections, fragments spread included, its
	// values and its variables may nest. Validation reads a value again at each
	// level above it. More is ErrRequestTooLarge.
	MaxDepth int

	// MaxNameBytes is the longest name or alias a request's query may hold, which
	// each row and error repeats. It is the schema's longest name when that is
	// longer. More is ErrRequestTooLarge.
	MaxNameBytes int

	// MaxLiteralBytes is the longest string literal a request's query may hold: an
	// error repeats it. A variable has no such limit. More is ErrRequestTooLarge.
	MaxLiteralBytes int

	// MaxVariableNodes is the most values and objects a request's variables may
	// hold. An object of an input type weighs 4, and 4 more for each 16 of the
	// type's fields, as gqlgen does work for each of them; a plain value, a list and
	// a JSON scalar's objects weigh 1. Of a query sent by its hash the types are not
	// known yet, and an object weighs as the schema's widest input does. More is ErrRequestTooLarge, before gqlgen
	// reads any of them.
	MaxVariableNodes int

	// MaxErrors is the most errors a response may hold, and one that says there are
	// more. Each pair of fields that differ and each unknown name makes one of
	// validation's, held to this on a server gqlgenx.Setup made; a list of a field that a
	// directive refuses makes one for each row, and over the limit the request stops
	// (ErrTooManyErrors) with data null, under gqlgenx.Extension and gqlgenx.ErrorPresenter.
	MaxErrors int

	// MaxBodyBytes is the most bytes the body of a request may have, its
	// variables and any upload included. More is refused with 413, by the handler
	// gqlgenx.Setup returns, as nothing else bounds it. The same handler holds a websocket's
	// messages to it, and closes the connection of one over.
	MaxBodyBytes int

	// MaxResponseBytes is the most bytes the strings, the bytes and the JSON of a
	// response may write, counted as its fields resolve, a string as it is escaped. gqlgenx.Cost
	// cannot see the size of a value: one stored string of 900 KB under 300 aliases
	// answered with 270 MB. More is ErrResponseTooLarge, with data null, and the fields
	// not yet resolved do not run. A subscription's event has its own count.
	MaxResponseBytes int

	// Timeout is how long a query or a mutation may run: more cancels the context its
	// statements use, and is ErrTimeout, with data null, and a mutation rolls back. A
	// resolver that ignores its context holds the response until it returns. A
	// subscription has no such limit.
	Timeout time.Duration
}

// DefaultLimits returns MaxPage 100, MaxList 1000, MaxFilterDepth 5,
// MaxFilterWidth 100, MaxFilterNodes 1000, MaxOrderTerms 5, MaxCost 150000,
// MaxIntrospectionCost 250000, MaxTokens 15000, MaxFields 5000, MaxFragments 500,
// MaxDepth 64, MaxNameBytes 128, MaxLiteralBytes 64 KiB, MaxVariableNodes 100000,
// MaxErrors 100, MaxBodyBytes 1 MiB, MaxResponseBytes 32 MiB and Timeout 30 s.
func DefaultLimits() Limits {
	return Limits{
		MaxPage: 100, MaxList: 1000, MaxFilterDepth: 5, MaxFilterWidth: 100, MaxFilterNodes: 1000, MaxOrderTerms: 5,
		MaxCost: 150000, MaxIntrospectionCost: 250000, MaxTokens: 15000, MaxFields: 5000, MaxFragments: 500, MaxDepth: 64,
		MaxNameBytes: 128, MaxLiteralBytes: 64 << 10, MaxVariableNodes: 100000, MaxErrors: 100, MaxBodyBytes: 1 << 20,
		MaxResponseBytes: 32 << 20, Timeout: 30 * time.Second,
	}
}

// LimitsFromContext returns the limits an adapter put in ctx, or
// DefaultLimits() when there are none. Under graphqlgo they are always
// DefaultLimits(), and velox enforces only the ones generated code reads
// (MaxPage, MaxList, MaxOrderTerms and the filter limits): the fields that
// limit a whole request (MaxCost, MaxIntrospectionCost and MaxTokens
// through Timeout) are graphql-go's own options to set, and velox does not
// enforce them.
func LimitsFromContext(ctx context.Context) Limits {
	if l, ok := gqlcore.LimitsOf(ctx).(*Limits); ok && l != nil {
		return *l
	}
	return DefaultLimits()
}
