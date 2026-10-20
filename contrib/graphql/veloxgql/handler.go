package veloxgql

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox2"
)

// Handler returns the gqlgen handler extension that serves a schema on db.
// Use it first, so that it is the outermost of the server's extensions:
//
//	srv := handler.New(graph.NewExecutableSchema(cfg))
//	srv.Use(veloxgql.Handler(db))
//
// Every operation's context gets db, which DBFrom reads, so queries and
// subscriptions run on db, and a DB with replicas routes their reads to
// them.
//
// A mutation operation runs in one db.Tx, on the primary, and its
// resolvers get the transaction's DB. Its root fields run one after another,
// as gqlgen runs a mutation's, so every statement of the operation, its
// nested fields' too, is in the transaction. The transaction commits when
// the response has no errors. It rolls back when the response has any,
// even a field's error under data the other fields filled, and when the
// operation panics, whether gqlgen recovered the panic into an error or the
// panic carries on. A response whose transaction did not commit, rolled
// back or failed to commit, has data null, since nothing it reports was
// kept, and the commit's error, if any, is added to its errors through the
// server's error presenter. WithDeadlines' Tx and MaxTx bound the whole
// operation.
//
// A subscription runs in no transaction: each of its events' statements
// run on db.
//
// Use panics when db is nil.
func Handler(db *velox.DB) interface {
	graphql.HandlerExtension
	graphql.OperationInterceptor
	graphql.ResponseInterceptor
} {
	return dbHandler{db: db}
}

// dbHandler is Handler's extension. It intercepts the operation to put the
// DB in its context, which the executable schema runs in; and the response,
// because a mutation's resolvers run while gqlgen makes its response (inside
// the response interceptors), and the response is where their errors
// collect, to decide between commit and rollback.
type dbHandler struct{ db *velox.DB }

// mutationKey is the context key of a mutation operation's state: a
// *atomic.Bool set once its first response has started. gqlgen runs a
// mutation's fields in its first response; a transport that asks for more
// (SSE, websockets) gets none, and runs no transaction for it.
type mutationKey struct{}

// errRollback is what the transaction's function returns to roll back a
// response with errors.
var errRollback = errors.New("veloxgql: the response has errors")

// ExtensionName implements graphql.HandlerExtension.
func (dbHandler) ExtensionName() string { return "VeloxDB" }

// Validate implements graphql.HandlerExtension: it refuses a nil DB when
// the server takes the extension.
func (h dbHandler) Validate(graphql.ExecutableSchema) error {
	if h.db == nil {
		return errors.New("veloxgql.Handler: nil DB")
	}
	return nil
}

// InterceptOperation implements graphql.OperationInterceptor: it puts the
// DB in the operation's context, and marks a mutation's.
func (h dbHandler) InterceptOperation(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
	ctx = WithDB(ctx, h.db)
	if op := graphql.GetOperationContext(ctx).Operation; op != nil && op.Operation == ast.Mutation {
		ctx = context.WithValue(ctx, mutationKey{}, new(atomic.Bool))
	}
	return next(ctx)
}

// InterceptResponse implements graphql.ResponseInterceptor: it makes a
// mutation's first response in a transaction. Every other response, and an
// error response gqlgen makes without running the operation, passes
// through.
func (h dbHandler) InterceptResponse(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
	started, _ := ctx.Value(mutationKey{}).(*atomic.Bool)
	if started == nil || started.Swap(true) {
		return next(ctx)
	}
	var resp *graphql.Response
	err := h.db.Tx(ctx, func(tx *velox.DB) error {
		resp = next(WithDB(ctx, tx))
		if resp != nil && len(resp.Errors) > 0 {
			return errRollback
		}
		return nil
	})
	if err == nil {
		return resp
	}
	out := &graphql.Response{}
	if resp != nil {
		*out = *resp
		out.Data = nil
	}
	if !errors.Is(err, errRollback) {
		// Begin or commit failed, or the transaction's deadline or the
		// request's context ended it.
		rctx := graphql.WithFreshResponseContext(ctx)
		graphql.AddError(rctx, err)
		out.Errors = append(out.Errors, graphql.GetErrors(rctx)...)
	}
	return out
}
