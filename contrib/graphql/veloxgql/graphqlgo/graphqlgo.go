package graphqlgo

import (
	"context"

	"github.com/syssam/graphql-go"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// The package registers its selection source when it is linked.
func init() { gqlcore.Register(source{}) }

// With is an executor option that puts db in every operation's context, for
// code that reads it there (veloxgql.DB), and runs a mutation in a velox
// session. It is optional (decision 0173): generated code needs no option,
// as a query carries its DB, a row the DB that read it, and the package's
// registered source gives the field and the operation's table.
//
//	exec := graphql.NewExecutor(s, graphqlgo.With(db), graphql.WithMaxDepth(12))
//
// For each operation it puts db and veloxgql.DefaultLimits() in the context,
// where veloxgql.DB and veloxgql.LimitsFromContext read them; a
// subscription's source opens with them. A mutation runs in a velox session
// (velox.WithSession), so on a DB with replicas it reads its own writes from
// the primary; without With, a mutation's reads after its writes may go to
// a replica. It runs in no transaction: a resolver that needs one opens it
// (db.Tx, velox.UseTx). Given twice, the later one's DB is the one resolvers
// see, as graphql-go runs the later interceptor inside the earlier. It
// panics on a nil db.
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

// install is ctx with the DB and the limits, and no selection source of its
// own: the registered one gives the field.
func (w *with) install(ctx context.Context) context.Context {
	return gqlcore.InstallDB(ctx, w.db, &defaultLimits)
}

// InterceptSubscription implements graphql.SubscriptionInterceptor: the
// subscription's source opens with the DB in its context. Each event's
// resolvers get it from InterceptOperation.
func (w *with) InterceptSubscription(ctx context.Context, oc *graphql.OperationContext, next graphql.SubscriptionHandler) (<-chan *graphql.Response, error) {
	return next(w.install(ctx), oc)
}

// InterceptOperation implements graphql.OperationInterceptor: it puts the
// DB in the operation's context, and runs a mutation in a velox session.
func (w *with) InterceptOperation(ctx context.Context, oc *graphql.OperationContext, next graphql.OperationHandler) *graphql.Response {
	ctx = w.install(ctx)
	if op := oc.Operation; op != nil && op.Operation == ast.Mutation {
		pin := velox.PinnedUntil(ctx)
		ctx = velox.WithSession(ctx)
		velox.PinUntil(ctx, pin)
	}
	return next(ctx, oc)
}
