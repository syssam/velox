package gqlgenx

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// Transactioner is the gqlgen handler extension that runs each mutation
// operation in one transaction of DB, as entgql's Transactioner does. It is
// opt-in: a server pages, batches and reads without it (decision 0173), and
// a mutation then runs in no transaction.
//
//	srv := handler.New(graph.NewExecutableSchema(cfg))
//	srv.Use(gqlgenx.Transactioner{DB: db})
//
// Its resolvers get the transaction's context, so a statement a mutation's
// resolver runs on DB, the DB its Resolver holds, runs in the transaction
// (velox.DB.Tx), and so do its nested fields' statements: a resolver needs
// only the DB it holds. One on another DB fails with velox.ErrOutsideTx.
// Under velox.WithoutTx(ctx) a statement runs outside the transaction, as an
// audit row that must survive a rollback does. The transaction runs on the
// primary, so on a DB with replicas the mutation reads its own writes.
//
// A transaction's DB is not safe for concurrent use, and gqlgen runs only a
// mutation's root fields one after another: below the root it resolves an
// object's fields that take a context, edges among them, concurrently. So
// Transactioner runs a mutation's field resolvers one at a time; queries'
// and subscriptions' still run concurrently. A resolver of a mutation must
// not itself use the DB from more than one goroutine.
//
// The transaction commits when the response has no errors. It rolls back
// when the response has any, even a field's error under data the other
// fields filled, when ResponseLimit stopped it, and when the operation
// panics, whether gqlgen recovered the panic into an error or the panic
// carries on. A response whose transaction did not commit has data null,
// since nothing it reports was kept, and the commit's error, if any, is
// added to its errors through the server's error presenter, which
// ErrorPresenter makes "internal error". The DB's velox.WithDeadlines bound
// the whole operation: its resolvers' context carries the transaction's
// deadline.
//
// A query and a subscription run in no transaction. A mutation's transaction
// does not cover @defer: gqlgen resolves a deferred fragment after the
// response with the main data has been made, which for a mutation is after
// the commit, so its resolvers find the transaction done and fail. @defer
// inside a mutation is unsupported; use it in queries.
//
// The server's Use panics when DB is nil or Options holds
// velox.TxRetryRolledBack: gqlgen makes the response once, so the
// operation cannot run again. For the same reason the transaction does not
// retry under a TxRetryRolledBack of the DB's velox.WithTxDefaults.
type Transactioner struct {
	// DB is the DB the transactions begin on: the one the Resolver holds.
	DB *velox.DB
	// Options are the options of each transaction, such as velox.TxIsolation.
	Options []velox.TxOption
}

var (
	_ graphql.HandlerExtension     = Transactioner{}
	_ graphql.OperationInterceptor = Transactioner{}
	_ graphql.ResponseInterceptor  = Transactioner{}
)

// mutationKey is the context key of a mutation operation's *mutation.
type mutationKey struct{}

// mutation is a mutation operation's state under a Transactioner.
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
func (Transactioner) ExtensionName() string { return "VeloxTransactioner" }

// Validate implements graphql.HandlerExtension: it refuses a nil DB and
// options that ask for velox.TxRetryRolledBack.
func (t Transactioner) Validate(graphql.ExecutableSchema) error {
	if t.DB == nil {
		return errors.New("gqlgenx.Transactioner: nil DB")
	}
	if n := velox.TxRetries(t.Options...); n > 0 {
		return fmt.Errorf("gqlgenx.Transactioner: TxRetryRolledBack(%d) would run the mutation again, and gqlgen makes its response once", n)
	}
	return nil
}

// InterceptOperation implements graphql.OperationInterceptor: it marks a
// mutation's context, and runs its field resolvers one at a time
// (mutation.serial).
func (Transactioner) InterceptOperation(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
	oc := graphql.GetOperationContext(ctx)
	if op := oc.Operation; op != nil && op.Operation == ast.Mutation {
		m := new(mutation)
		ctx = context.WithValue(ctx, mutationKey{}, m)
		oc.ResolverMiddleware = m.serial(oc.ResolverMiddleware)
	}
	return next(ctx)
}

// InterceptResponse implements graphql.ResponseInterceptor: it makes a
// mutation's first response in a transaction. Every other response passes
// through.
func (t Transactioner) InterceptResponse(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
	m, _ := ctx.Value(mutationKey{}).(*mutation)
	if m == nil || m.started.Swap(true) {
		return next(ctx)
	}
	var resp *graphql.Response
	// TxRetryRolledBack(0), last, overrides a retry of the DB's
	// velox.WithTxDefaults.
	opts := append(slices.Clip(t.Options), velox.TxRetryRolledBack(0))
	err := t.DB.Tx(ctx, func(ctx context.Context, _ *velox.DB) error {
		// The transaction's ctx: its deadline, and the TxAround hooks' values.
		resp = next(ctx)
		if resp != nil && len(resp.Errors) > 0 || stopped(ctx) {
			return errRollback
		}
		return nil
	}, opts...)
	if err == nil {
		return resp
	}
	out := &graphql.Response{}
	if resp != nil {
		*out = *resp
		out.Data = nil
	}
	if !gqlcore.Is(err, errRollback) {
		// Begin or commit failed, or the transaction's deadline or the
		// request's context ended it.
		rctx := graphql.WithFreshResponseContext(ctx)
		graphql.AddError(rctx, err)
		out.Errors = append(out.Errors, graphql.GetErrors(rctx)...)
	}
	return out
}

// serial wraps a mutation's field middleware, next: its field resolvers run
// one at a time, so that its transaction is never used concurrently. The
// lock cannot nest: gqlgen calls the middleware around the field's resolver
// alone (graphql.ResolveField passes the resolver to
// OperationContext.ResolverMiddleware), and resolves the field's children
// after it returns. It is the mutation's middleware, not a
// graphql.FieldInterceptor, so that a query's fields pay nothing for it.
func (m *mutation) serial(next graphql.FieldMiddleware) graphql.FieldMiddleware {
	return func(ctx context.Context, resolve graphql.Resolver) (any, error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		return next(ctx, resolve)
	}
}

// stopped reports whether ResponseLimit stopped the response of ctx.
func stopped(ctx context.Context) bool {
	r, _ := ctx.Value(responseKey{}).(*response)
	return r != nil && r.why.Load() != 0
}
