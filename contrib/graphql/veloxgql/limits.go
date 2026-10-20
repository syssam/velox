package veloxgql

// The limits on what a client may ask for. They are variables so that an
// application can change them, but only at start-up, before the server
// handles a request: nothing guards them, and a change while requests run is
// a data race.
var (
	// MaxPage is the largest first or last a connection takes, and the page
	// size when neither is given. More is ErrPageTooLarge, not a shorter
	// page.
	MaxPage = 100
	// MaxFilterDepth is how deep a where input may nest through not, and, or
	// and has<Edge>With. Deeper is ErrFilterTooDeep, before any query runs.
	MaxFilterDepth = 5
	// MaxFilterWidth is the most entries an and or an or list may have. More
	// is ErrFilterTooWide, before any query runs.
	MaxFilterWidth = 100
	// MaxOrderTerms is the most terms an orderBy list may have, each an
	// ORDER BY term and a term of every cursor. More is ErrBadPagination,
	// before any query runs.
	MaxOrderTerms = 5
)
