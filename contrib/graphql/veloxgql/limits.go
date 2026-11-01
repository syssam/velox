package veloxgql

import (
	"cmp"
	"context"
)

// Limits caps what a client may ask for. Each bounds one argument or one
// edge. A zero field means the default.
//
// The limits of an operation are the Extension's (WithLimits), which it puts in
// the operation's context, where LimitsFromContext reads them: two servers in
// one process can have different limits.
type Limits struct {
	// MaxPage is the largest first or last a connection takes, and the page
	// size when neither is given. More is ErrPageTooLarge, not a shorter
	// page. It bounds the IDs of the root field nodes too.
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
	// variable used at many places does.
	MaxFilterNodes int

	// MaxOrderTerms is the most terms an orderBy list may have, each an
	// ORDER BY term and a term of every cursor. More is ErrBadPagination,
	// before any query runs.
	MaxOrderTerms int

	// MaxCost is the most a request may cost (Cost): the bound on a whole
	// request that the other limits, each of one argument, do not give. More
	// is ErrTooCostly, before any statement runs. It bounds the statements too,
	// at 100 each. A list edge costs MaxList for each parent, so under the
	// defaults one full page with a list edge, posts(first: 100) { edges { node
	// { likes { id } } } }, costs 100402 and is refused: raise MaxCost, or lower
	// MaxList. Introspection has its own limit.
	MaxCost int

	// MaxIntrospectionCost is the most the __schema and __type of a request may
	// cost, one for each field of their response. More is ErrTooCostly. The
	// default admits GraphiQL's query on a schema of 500 rich entities (about
	// 700000), and no data comes under it: raising it does not loosen MaxCost,
	// nor MaxCost it. A request at this limit allocates about 640 MB through
	// gqlgen: give a server open to strangers a lower one, or no
	// extension.Introspection.
	MaxIntrospectionCost int

	// MaxTokens is the most lexer tokens a request's query may have. More is the
	// parser's error, GRAPHQL_PARSE_FAILED, before any of it is validated. The
	// Extension enforces it, and Setup gives it to gqlgen too.
	MaxTokens int

	// MaxFields is the most fields a request's query may hold, as written, a
	// repeat and an alias each counting one. More is ErrRequestTooLarge,
	// BAD_USER_INPUT, before gqlgen validates the query: validation is quadratic
	// in the fields that share a response name, so 5 times MaxFields is the most
	// pairs of those a query may hold, each fragment counted where it is spread,
	// and 100 times MaxFields the most fields its spreads reach.
	MaxFields int

	// MaxBodyBytes is the most bytes the body of a request may have, its
	// variables and any upload included. More is refused with 413, by the handler
	// Setup returns, as nothing else bounds it.
	MaxBodyBytes int
}

// DefaultLimits returns MaxPage 100, MaxList 1000, MaxFilterDepth 5,
// MaxFilterWidth 100, MaxFilterNodes 1000, MaxOrderTerms 5, MaxCost 100000,
// MaxIntrospectionCost 1000000, MaxTokens 15000, MaxFields 2000 and MaxBodyBytes
// 1 MiB.
func DefaultLimits() Limits {
	return Limits{
		MaxPage: 100, MaxList: 1000, MaxFilterDepth: 5, MaxFilterWidth: 100, MaxFilterNodes: 1000, MaxOrderTerms: 5,
		MaxCost: 100000, MaxIntrospectionCost: 1000000, MaxTokens: 15000, MaxFields: 2000, MaxBodyBytes: 1 << 20,
	}
}

// LimitsFromContext returns the limits the extension put in ctx, or
// DefaultLimits() when there are none.
func LimitsFromContext(ctx context.Context) Limits {
	if s, ok := ctx.Value(stateKey{}).(*state); ok {
		return s.limits
	}
	return DefaultLimits()
}

// orDefault is l with each zero field taken from DefaultLimits().
func (l Limits) orDefault() Limits {
	d := DefaultLimits()
	return Limits{
		MaxPage:        cmp.Or(l.MaxPage, d.MaxPage),
		MaxList:        cmp.Or(l.MaxList, d.MaxList),
		MaxFilterDepth: cmp.Or(l.MaxFilterDepth, d.MaxFilterDepth),
		MaxFilterWidth: cmp.Or(l.MaxFilterWidth, d.MaxFilterWidth),
		MaxFilterNodes: cmp.Or(l.MaxFilterNodes, d.MaxFilterNodes),
		MaxOrderTerms:  cmp.Or(l.MaxOrderTerms, d.MaxOrderTerms),
		MaxCost:        cmp.Or(l.MaxCost, d.MaxCost),

		MaxIntrospectionCost: cmp.Or(l.MaxIntrospectionCost, d.MaxIntrospectionCost),
		MaxTokens:            cmp.Or(l.MaxTokens, d.MaxTokens),
		MaxFields:            cmp.Or(l.MaxFields, d.MaxFields),
		MaxBodyBytes:         cmp.Or(l.MaxBodyBytes, d.MaxBodyBytes),
	}
}

// fields are l's fields by name, in declaration order.
func (l Limits) fields() []struct {
	name string
	n    int
} {
	return []struct {
		name string
		n    int
	}{
		{"MaxPage", l.MaxPage},
		{"MaxList", l.MaxList},
		{"MaxFilterDepth", l.MaxFilterDepth},
		{"MaxFilterWidth", l.MaxFilterWidth},
		{"MaxFilterNodes", l.MaxFilterNodes},
		{"MaxOrderTerms", l.MaxOrderTerms},
		{"MaxCost", l.MaxCost},
		{"MaxIntrospectionCost", l.MaxIntrospectionCost},
		{"MaxTokens", l.MaxTokens},
		{"MaxFields", l.MaxFields},
		{"MaxBodyBytes", l.MaxBodyBytes},
	}
}
