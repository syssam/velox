package veloxgql

import (
	"context"

	"github.com/syssam/velox2/contrib/graphql/veloxgql/internal/shared"
)

// Batch makes rows one batch for their edges: the first edge field gqlgen
// resolves on any of them reads that edge for all of them, in one statement.
// The generated code batches its pages and nodes; Batch is for the rows an
// application's own resolver returns. Outside a gqlgen operation, or a
// resolver's field, it does nothing.
//
// The rows are the operation's: an edge of theirs reads them as they are
// when the edge is first resolved, under that resolver's context, and a row
// given to Batch again leaves its earlier batch. rows itself is not kept,
// and the rows are let go of with the response (a subscription's event).
func Batch[E any](ctx context.Context, rows []*E) {
	shared.Add(ctx, rows)
}
