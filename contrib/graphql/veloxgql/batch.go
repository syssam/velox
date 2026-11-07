package veloxgql

import (
	"context"

	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
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
//
// An edge's call joins the batch only from the edge's own field (the generated
// method called by gqlgen for it, not from an application's other field) and
// with the arguments of the batch's first call; any other call reads alone. The
// batch is read under its first caller's context: velox's row scope
// (velox.RowScopeFromContext: the scope values, the bypass, the shard), the DB and
// the limits are in its key, so callers that differ in them never share a read.
// Any other context value a Read policy or a Rewrite or Around hook reads (the
// viewer, a role) must be the same for every row of a field, or be returned by
// WithBatchScope: a directive or a field middleware that varies the context per
// parent row needs it.
func Batch[E any](ctx context.Context, rows []*E) {
	xs := make([]any, len(rows))
	for i, r := range rows {
		xs[i] = r
	}
	gqlcore.Add(ctx, xs)
}
