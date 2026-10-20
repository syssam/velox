package gen

import (
	"context"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
)

// LikeConnection is the GraphQL type LikeConnection, a connection to a list of
// Like. It holds the query of the whole list; gqlgen calls its methods for the
// fields a query selects, and each runs only then.
type LikeConnection struct {
	query velox.Query[Like] // the whole list, before paging
}

// TotalCount is the number of Like nodes in the whole connection, not only on
// this page. It counts the rows when gqlgen asks for it, which is only when the
// field totalCount is selected.
func (c *LikeConnection) TotalCount(ctx context.Context) (int, error) {
	n, err := c.query.Count(ctx)
	return int(n), err
}

// LikeEdge is the GraphQL type LikeEdge, an edge in a connection to a list of
// Like.
type LikeEdge struct {
	// Node is the Like at the end of the edge.
	Node *Like
	// Cursor is the cursor of the edge, for paging.
	Cursor veloxgql.Cursor
}
