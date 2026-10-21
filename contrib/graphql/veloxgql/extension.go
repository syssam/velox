package veloxgql

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox2"
)

// Extension is the gqlgen handler extension that serves a schema on a DB.
// Make one with NewExtension, and use it first, so that it is the outermost
// of the server's extensions:
//
//	srv := handler.New(graph.NewExecutableSchema(cfg))
//	srv.Use(veloxgql.NewExtension(db))
//	srv.SetErrorPresenter(veloxgql.ErrorPresenter(nil))
//
// Every operation's context gets the DB, which DBFrom reads, and the
// extension's limits, which LimitsFrom reads, so queries and subscriptions
// run on the DB, and a DB with replicas routes their reads to them.
//
// A mutation operation runs in one db.Tx, on the primary, unless WithTxFilter
// leaves it out, and its resolvers get the transaction's DB and context, so every statement of the
// operation, its nested fields' too, is in the transaction. A statement a
// mutation's resolver runs on db itself, not on the DB DBFrom reads, fails
// with velox.ErrOutsideTx; under velox.WithoutTx(ctx) it runs outside the
// transaction, as an audit row that must survive a rollback does.
//
// A transaction's DB is not safe for concurrent use, and gqlgen runs only
// the mutation's root fields one after another: below the root it resolves
// an object's fields that take a context, edges among them, concurrently. So
// the extension runs a mutation's field resolvers one at a time; queries'
// and subscriptions' still run concurrently. A resolver of a mutation must
// not itself use the DB from more than one goroutine.
//
// A mutation WithTxFilter leaves out runs on db with no transaction: its
// statements are not atomic, and its field resolvers run concurrently as a
// query's do. It runs in a velox session (velox.WithSession), so on a DB
// with replicas its reads after its own writes go to the primary. Use the
// filter for mutations that only call other services, which would hold a
// pooled connection for no statement.
//
// The transaction commits when the response has no errors. It rolls back
// when the response has any,
// even a field's error under data the other fields filled, and when the
// operation panics, whether gqlgen recovered the panic into an error or the
// panic carries on. A response whose transaction did not commit, rolled
// back or failed to commit, has data null, since nothing it reports was
// kept, and the commit's error, if any, is added to its errors through the
// server's error presenter, which ErrorPresenter makes "internal error".
// WithDeadlines' Tx and MaxTx bound the whole operation: its resolvers'
// context carries the transaction's deadline.
//
// A subscription runs in no transaction: each of its events' statements
// run on db.
//
// A mutation's transaction does not cover @defer: gqlgen resolves a deferred
// fragment after the response with the main data has been made, which for a
// mutation is after the commit, so its resolvers find the transaction done
// and fail. @defer inside a mutation is unsupported; use it in queries.
//
// Every limit (Limits) bounds one argument or one edge, not a request: a
// query that nests connections reads up to MaxPage to the power of its depth
// rows, and aliases repeat a field as many times as the client writes it. A
// server needs a limit on the cost of the whole request as well, such as
// gqlgen's extension.FixedComplexityLimit
// (srv.Use(extension.FixedComplexityLimit(n))).
//
// The server's Use panics when the DB is nil, or when a limit is negative:
// Validate refuses it, naming the limit.
//
// The extension intercepts the operation, to put the DB and the limits in
// its context, which the executable schema runs in; the response, because a
// mutation's resolvers run while gqlgen makes its response (inside the
// response interceptors), and the response is where their errors collect,
// to decide between commit and rollback; and a mutation's fields, to run
// their resolvers one at a time.
type Extension struct {
	db     *velox.DB
	limits Limits
	// txOpts are the options of a mutation's transaction.
	txOpts []velox.TxOption
	// txFilter decides whether a mutation runs in a transaction; nil means
	// every one does.
	txFilter func(ctx context.Context, op *ast.OperationDefinition) bool
}

// Option configures an Extension.
type Option func(*Extension)

// NewExtension returns the extension that serves a schema on db, with
// DefaultLimits() unless WithLimits says otherwise.
//
// Set ErrorPresenter on the server too
// (srv.SetErrorPresenter(veloxgql.ErrorPresenter(nil))): without it, gqlgen's
// default presenter shows a client every error's text, velox's and the
// driver's, which name entities, tables, constraints and tenants.
func NewExtension(db *velox.DB, opts ...Option) *Extension {
	e := &Extension{db: db, limits: DefaultLimits()}
	for _, o := range opts {
		o(e)
	}
	return e
}

// WithLimits sets the extension's limits to l, each zero field of l taken
// from DefaultLimits(). Given twice, the last wins.
func WithLimits(l Limits) Option {
	return func(e *Extension) { e.limits = l.orDefault() }
}

// WithTxOptions sets the options of a mutation's transaction. Validate
// refuses TxRetrySerializable: gqlgen makes the response once, so the
// operation cannot run again. Given twice, the last wins.
func WithTxOptions(opts ...velox.TxOption) Option {
	opts = slices.Clone(opts)
	return func(e *Extension) { e.txOpts = opts }
}

// WithTxFilter decides, for each mutation operation, whether it runs in a
// transaction. f gets the operation's context and its definition, and runs
// once per mutation operation. A nil f means every mutation does.
//
// Use it for mutations that only call other services: in a transaction they
// would hold a pooled connection while they wait, for no statement. A
// mutation f leaves out runs on the extension's DB with no transaction, so
// its statements are not atomic, and its field resolvers are not serialized,
// since no transaction's DB is shared. It keeps read-your-writes: it runs in
// a velox session (velox.WithSession), so on a DB with replicas a read after
// its own write goes to the primary. A session of the request's context is
// replaced, with its pin kept.
func WithTxFilter(f func(ctx context.Context, op *ast.OperationDefinition) bool) Option {
	return func(e *Extension) { e.txFilter = f }
}

// Context returns ctx with the extension's DB and limits, as a query
// operation gets them: for generated code called outside a gqlgen server.
// It panics with the error Validate would return.
func (e *Extension) Context(ctx context.Context) context.Context {
	if err := e.Validate(nil); err != nil {
		panic(err)
	}
	return withState(ctx, e.db, e.limits)
}

var (
	_ graphql.HandlerExtension     = (*Extension)(nil)
	_ graphql.OperationInterceptor = (*Extension)(nil)
	_ graphql.ResponseInterceptor  = (*Extension)(nil)
	_ graphql.FieldInterceptor     = (*Extension)(nil)
)

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
	// plain is set when the filter left the mutation out of a transaction.
	plain bool
}

// errRollback is what the transaction's function returns to roll back a
// response with errors.
var errRollback = errors.New("veloxgql: the response has errors")

// ExtensionName implements graphql.HandlerExtension.
func (*Extension) ExtensionName() string { return "VeloxDB" }

// Validate implements graphql.HandlerExtension: it refuses a nil DB, and a
// negative limit, when the server takes the extension.
func (e *Extension) Validate(graphql.ExecutableSchema) error {
	if e.db == nil {
		return errors.New("veloxgql.NewExtension: nil DB")
	}
	for _, l := range e.limits.fields() {
		if l.n < 0 {
			return fmt.Errorf("veloxgql.NewExtension: Limits.%s is %d, and a limit must be positive", l.name, l.n)
		}
	}
	if n := velox.TxRetries(e.txOpts...); n > 0 {
		return fmt.Errorf("veloxgql.WithTxOptions: TxRetrySerializable(%d) would run the mutation again, and gqlgen makes its response once", n)
	}
	return nil
}

// InterceptOperation implements graphql.OperationInterceptor: it puts the
// DB and the limits in the operation's context, and marks a mutation's.
func (e *Extension) InterceptOperation(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
	ctx = withState(ctx, e.db, e.limits)
	if op := graphql.GetOperationContext(ctx).Operation; op != nil && op.Operation == ast.Mutation {
		m := new(mutation)
		m.plain = e.txFilter != nil && !e.txFilter(ctx, op)
		if m.plain {
			pin := velox.PinnedUntil(ctx)
			ctx = velox.WithSession(ctx)
			velox.PinUntil(ctx, pin)
		}
		ctx = context.WithValue(ctx, mutationKey{}, m)
	}
	return next(ctx)
}

// InterceptResponse implements graphql.ResponseInterceptor: it makes a
// mutation's first response in a transaction. Every other response, and an
// error response gqlgen makes without running the operation, passes
// through.
func (e *Extension) InterceptResponse(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
	m, _ := ctx.Value(mutationKey{}).(*mutation)
	if m == nil || m.plain || m.started.Swap(true) {
		return next(ctx)
	}
	var resp *graphql.Response
	err := e.db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		resp = next(withState(ctx, tx, e.limits)) // the transaction's ctx: its deadline, and the TxAround hooks' values
		if resp != nil && len(resp.Errors) > 0 {
			return errRollback
		}
		return nil
	}, e.txOpts...)
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
func (*Extension) InterceptField(ctx context.Context, next graphql.Resolver) (any, error) {
	m, _ := ctx.Value(mutationKey{}).(*mutation)
	if m == nil || m.plain {
		return next(ctx)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return next(ctx)
}
