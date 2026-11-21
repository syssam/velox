package veloxgql

import (
	"context"

	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// Batch makes rows one batch for their edges: the first edge field resolved
// on any of them reads that edge for all of them, in one statement. The
// generated code batches its pages and nodes; Batch is for the rows an
// application's own resolver returns. Outside an adapter's operation, or a
// resolver's field, it does nothing.
//
// The rows are the operation's: an edge of theirs reads them as they are
// when the edge is first resolved, under that resolver's context, and a row
// given to Batch again leaves its earlier batch. rows itself is not kept,
// and the rows are let go of with the response (a subscription's event).
//
// An edge's call joins the batch only from the edge's own field (the generated
// method the engine calls for it, not from an application's other field) and
// with the arguments of the batch's first call; any other call reads alone. The
// batch is read under its first caller's context: velox's row scope
// (velox.RowScopeFromContext: the scope values, the bypass, the shard), the DB and
// the limits are in its key, so callers that differ in them never share a read.
// So is the SQL that each caller's context would read the target with, after
// the Read policy, the interceptors and the Rewrite hooks: callers whose
// viewer or role those read differently never share a read, whatever context
// values they read. Only an Around hook that reads a context value of its own
// needs WithBatchScope.
func Batch[E any](ctx context.Context, rows []*E) {
	xs := make([]any, len(rows))
	for i, r := range rows {
		xs[i] = r
	}
	gqlcore.Add(ctx, xs)
}
