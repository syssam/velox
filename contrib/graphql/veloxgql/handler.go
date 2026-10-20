package veloxgql

import (
	"context"
	"errors"
	"fmt"
	"sync"
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
// resolvers get the transaction's DB and context, so every statement of the
// operation, its nested fields' too, is in the transaction. A statement a
// mutation's resolver runs on db itself, not on the DB DBFrom reads, fails
// with velox.ErrOutsideTx. A transaction's DB is not safe for concurrent
// use, and gqlgen runs only the mutation's root fields one after another:
// below the root it resolves an object's fields that take a context, edges
// among them, concurrently. So the handler runs a mutation's field resolvers
// one at a time; queries' and subscriptions' still run concurrently. A
// resolver of a mutation must not itself use the DB from more than one
// goroutine.
//
// The transaction commits when the response has no errors. It rolls back
// when the response has any,
// even a field's error under data the other fields filled, and when the
// operation panics, whether gqlgen recovered the panic into an error or the
// panic carries on. A response whose transaction did not commit, rolled
// back or failed to commit, has data null, since nothing it reports was
// kept, and the commit's error, if any, is added to its errors through the
// server's error presenter. WithDeadlines' Tx and MaxTx bound the whole
// operation: its resolvers' context carries the transaction's deadline.
//
// A subscription runs in no transaction: each of its events' statements
// run on db.
//
// A mutation's transaction does not cover @defer: gqlgen resolves a deferred
// fragment after the response with the main data has been made, which for a
// mutation is after the commit, so its resolvers find the transaction done
// and fail. @defer inside a mutation is unsupported; use it in queries.
//
// Every limit of this package (MaxPage, MaxList, MaxFilterDepth,
// MaxFilterWidth, MaxFilterNodes and MaxOrderTerms) bounds one argument or
// one edge, not a request: a query that nests connections reads up to
// MaxPage to the power of its depth rows, and aliases repeat a field as many
// times as the client writes it. A server needs a limit on the cost of the
// whole request as well, such as gqlgen's extension.FixedComplexityLimit
// (srv.Use(extension.FixedComplexityLimit(n))).
//
// Use panics when db is nil, or when a limit of this package is not
// positive: Validate refuses it, naming the limit, since a limit of 0 would
// fail every request that reaches it.
func Handler(db *velox.DB) interface {
	graphql.HandlerExtension
	graphql.OperationInterceptor
	graphql.ResponseInterceptor
	graphql.FieldInterceptor
} {
	return dbHandler{db: db}
}

// dbHandler is Handler's extension. It intercepts the operation to put the
// DB in its context, which the executable schema runs in; the response,
// because a mutation's resolvers run while gqlgen makes its response (inside
// the response interceptors), and the response is where their errors
// collect, to decide between commit and rollback; and a mutation's fields,
// to run their resolvers one at a time.
type dbHandler struct{ db *velox.DB }

// mutationKey is the context key of a mutation operation's *mutation.
type mutationKey struct{}

// mutation is a mutation operation's state.
type mutation struct {
	// started is set once the first response has started. gqlgen runs a
	// mutation's fields in its first response; a transport that asks for
	// more (SSE, websockets) gets none, and runs no transaction for it.
	started atomic.Bool
	// mu is held while one of the operation's field resolvers runs.
	mu sync.Mutex
}

// errRollback is what the transaction's function returns to roll back a
// response with errors.
var errRollback = errors.New("veloxgql: the response has errors")

// ExtensionName implements graphql.HandlerExtension.
func (dbHandler) ExtensionName() string { return "VeloxDB" }

// Validate implements graphql.HandlerExtension: it refuses a nil DB, and a
// limit that is not positive, when the server takes the extension.
func (h dbHandler) Validate(graphql.ExecutableSchema) error {
	if h.db == nil {
		return errors.New("veloxgql.Handler: nil DB")
	}
	for _, l := range []struct {
		name string
		n    int
	}{
		{"MaxPage", MaxPage},
		{"MaxList", MaxList},
		{"MaxFilterDepth", MaxFilterDepth},
		{"MaxFilterWidth", MaxFilterWidth},
		{"MaxFilterNodes", MaxFilterNodes},
		{"MaxOrderTerms", MaxOrderTerms},
	} {
		if l.n <= 0 {
			return fmt.Errorf("veloxgql.Handler: veloxgql.%s is %d, and a limit must be positive", l.name, l.n)
		}
	}
	return nil
}

// InterceptOperation implements graphql.OperationInterceptor: it puts the
// DB in the operation's context, and marks a mutation's.
func (h dbHandler) InterceptOperation(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
	ctx = WithDB(ctx, h.db)
	if op := graphql.GetOperationContext(ctx).Operation; op != nil && op.Operation == ast.Mutation {
		ctx = context.WithValue(ctx, mutationKey{}, new(mutation))
	}
	return next(ctx)
}

// InterceptResponse implements graphql.ResponseInterceptor: it makes a
// mutation's first response in a transaction. Every other response, and an
// error response gqlgen makes without running the operation, passes
// through.
func (h dbHandler) InterceptResponse(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
	m, _ := ctx.Value(mutationKey{}).(*mutation)
	if m == nil || m.started.Swap(true) {
		return next(ctx)
	}
	var resp *graphql.Response
	err := h.db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		resp = next(WithDB(ctx, tx)) // the transaction's ctx: its deadline, and the TxAround hooks' values
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

// InterceptField implements graphql.FieldInterceptor: a mutation's field
// resolvers run one at a time, so that its transaction's DB is never used
// concurrently. The lock cannot nest: gqlgen calls the field interceptors
// around the field's resolver alone (graphql.ResolveField passes the
// resolver to OperationContext.ResolverMiddleware), and resolves the field's
// children after they return.
func (dbHandler) InterceptField(ctx context.Context, next graphql.Resolver) (any, error) {
	m, _ := ctx.Value(mutationKey{}).(*mutation)
	if m == nil {
		return next(ctx)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return next(ctx)
}
