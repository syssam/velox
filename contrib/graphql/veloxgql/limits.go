package veloxgql

// The limits on what a client may ask for. They are variables so that an
// application can change them, but only at start-up, before the server
// handles a request: nothing guards them, and a change while requests run is
// a data race. Each must be positive: a limit of 0 or less refuses every
// request it bounds, and Handler refuses to serve with one.
var (
	// MaxPage is the largest first or last a connection takes, and the page
	// size when neither is given. More is ErrPageTooLarge, not a shorter
	// page.
	MaxPage = 100
	// MaxList is the most rows a list edge (graphql.Connection(false)) reads:
	// the edge's query allows MaxList rows in place of the DB's WithMaxRows
	// cap, which is no cap unless the application sets one. A node with more
	// fails the edge with velox.ErrTooManyRows, in an error that names the
	// edge, rather than reading them all.
	MaxList = 1000
	// MaxFilterDepth is how deep a where input may nest through not, and, or
	// and has<Edge>With. Deeper is ErrFilterTooDeep, before any query runs.
	MaxFilterDepth = 5
	// MaxFilterWidth is the most entries a list in a where input may have:
	// an and, an or or a has<Edge>With list of inputs, or an In or a NotIn
	// list of values. More is ErrFilterTooWide, before any query runs.
	MaxFilterWidth = 100
	// MaxFilterNodes is the budget of one where argument: its inputs, the
	// inputs nested in them, the predicates they set and the values of their
	// In and NotIn lists each count one, across the whole argument. More is
	// ErrFilterTooWide, before any query runs. Depth and width alone do not
	// bound an argument that holds one input many times over, as a GraphQL
	// variable used at many places does.
	MaxFilterNodes = 1000
	// MaxOrderTerms is the most terms an orderBy list may have, each an
	// ORDER BY term and a term of every cursor. More is ErrBadPagination,
	// before any query runs.
	MaxOrderTerms = 5
)
