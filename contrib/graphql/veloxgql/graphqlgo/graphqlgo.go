package graphqlgo

import (
	"context"

	"github.com/syssam/graphql-go"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// With is the executor option that serves velox's generated code on db.
// Give it to every executor whose schema binds generated code:
//
//	exec := graphql.NewExecutor(s, graphqlgo.With(db), graphql.WithMaxDepth(12))
//
// For each operation it puts db, veloxgql.DefaultLimits() (velox's fixed
// backstop: see the package doc) and the selection source in the context,
// where veloxgql.DB reads the DB and generated code finds the field it
// resolves, with a new table of rows, so that edges batch. A
// subscription's source opens with the same state, and each of its events
// gets a table of its own. A mutation runs in a velox session
// (velox.WithSession), so on a DB with replicas it reads its own writes
// from the primary. It runs in no transaction: a resolver that needs one
// opens it (db.Tx, velox.UseTx).
//
// Without With, generated code fails with veloxgql.ErrNoSelectionSource and
// veloxgql.ErrNoDB before any statement. Given twice, the later one's state
// is the one resolvers see, as graphql-go runs the later interceptor
// inside the earlier. It panics on a nil db.
func With(db *velox.DB) graphql.ExecutorOption {
	if db == nil {
		panic("graphqlgo.With: nil DB")
	}
	w := &with{db: db}
	return func(e *graphql.Executor) {
		graphql.WithOperationInterceptor(w)(e)
		graphql.WithSubscriptionInterceptor(w)(e)
	}
}

// with is With's interceptors.
type with struct {
	db *velox.DB
}

// defaultLimits are the limits every With puts in the state, which gqlgraph
// reads: velox's backstop against unbounded reads and filters. Readers copy
// them (veloxgql.LimitsFromContext), so all share one value.
var defaultLimits = veloxgql.DefaultLimits()

var (
	_ graphql.OperationInterceptor    = (*with)(nil)
	_ graphql.SubscriptionInterceptor = (*with)(nil)
)

// install is ctx with the operation's state.
func (w *with) install(ctx context.Context) context.Context {
	return gqlcore.Install(ctx, w.db, &defaultLimits, source{})
}

// InterceptSubscription implements graphql.SubscriptionInterceptor: the
// subscription's source opens with the state in its context. Each event's
// resolvers get the state, and a table of their own, from
// InterceptOperation.
func (w *with) InterceptSubscription(ctx context.Context, oc *graphql.OperationContext, next graphql.SubscriptionHandler) (<-chan *graphql.Response, error) {
	return next(w.install(ctx), oc)
}

// InterceptOperation implements graphql.OperationInterceptor: it puts the
// operation's state and a new table of rows in its context, and runs a
// mutation in a velox session.
func (w *with) InterceptOperation(ctx context.Context, oc *graphql.OperationContext, next graphql.OperationHandler) *graphql.Response {
	ctx = gqlcore.WithTable(w.install(ctx), nil)
	if op := oc.Operation; op != nil && op.Operation == ast.Mutation {
		pin := velox.PinnedUntil(ctx)
		ctx = velox.WithSession(ctx)
		velox.PinUntil(ctx, pin)
	}
	return next(ctx, oc)
}
