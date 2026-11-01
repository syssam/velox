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
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// Extension is the gqlgen handler extension that serves a schema on a DB.
// Make one with NewExtension, and use it first, so that it is the outermost
// of the server's extensions:
//
//	srv := handler.New(graph.NewExecutableSchema(cfg))
//	srv.Use(veloxgql.NewExtension(db))
//	srv.SetErrorPresenter(veloxgql.ErrorPresenter(nil))
//
// or let Setup do that, with a limit on the body too, which no extension can
// set.
//
// Every operation's context gets the DB, which DBFromContext reads, and the
// extension's limits, which LimitsFromContext reads, so queries and
// subscriptions run on the DB, and a DB with replicas routes their reads to
// them.
//
// A mutation operation runs in one db.Tx, on the primary, unless WithTxFilter
// leaves it out, and its resolvers get the transaction's DB and context, so
// every statement of the operation, its nested fields' too, is in the
// transaction. A statement a mutation's resolver runs on db itself, not on
// the DB DBFromContext reads, fails with velox.ErrOutsideTx; under
// velox.WithoutTx(ctx) it runs outside the transaction, as an audit row that
// must survive a rollback does.
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
// The transaction commits when the response has no errors. It rolls back when
// the response has any, even a field's error under data the other fields
// filled, and when the operation panics, whether gqlgen recovered the panic
// into an error or the panic carries on. A response whose transaction did not
// commit, rolled back or failed to commit, has data null, since nothing it
// reports was kept, and the commit's error, if any, is added to its errors
// through the server's error presenter, which ErrorPresenter makes "internal
// error". WithDeadlines' Tx and MaxTx bound the whole operation: its
// resolvers' context carries the transaction's deadline.
//
// A subscription runs in no transaction: each of its events' statements
// run on db.
//
// A mutation's transaction does not cover @defer: gqlgen resolves a deferred
// fragment after the response with the main data has been made, which for a
// mutation is after the commit, so its resolvers find the transaction done
// and fail. @defer inside a mutation is unsupported; use it in queries.
//
// Every limit but MaxCost bounds one argument or one edge, not a request: a
// query that nests connections reads up to MaxPage to the power of its depth
// rows, and aliases repeat a field as many times as the client writes it.
// Limits.MaxCost bounds the whole request. Before any resolver runs, the
// extension computes the operation's Cost, which multiplies by first and
// last, counts every alias, every statement and every branch of a fragment; a
// request over MaxCost, or whose introspection, counted by the fields of its
// response, is over MaxIntrospectionCost, fails with ErrTooCostly, the code
// CodeTooCostly and data null, and sends no statement. It reads the schema the
// server's Use gives Validate; gqlgen's extension.FixedComplexityLimit counts
// fields, not rows, and is not needed with it.
//
// Before gqlgen parses a request, whose validation is quadratic in the fields
// that share a response name, the extension parses it and refuses one over
// MaxTokens or MaxFields, on every transport. That is a second parse, which a
// query cache of gqlgen's does not spare. Use it before
// extension.AutomaticPersistedQuery, so that the cache never stores a refused
// query.
//
// The server's Use panics when the DB is nil, when a limit is negative, or
// when WithTxOptions asks for velox.TxRetrySerializable: Validate refuses
// it, naming the limit or the option.
//
// The extension mutates the operation's parameters, to refuse a document over
// MaxTokens or MaxFields; the operation's context, to refuse a request over
// MaxCost; intercepts the operation, to put the DB and the limits in
// its context, which the executable schema runs in; the response, because a
// mutation's resolvers run while gqlgen makes its response (inside the
// response interceptors), and the response is where their errors collect,
// to decide between commit and rollback; and a mutation's fields, to run
// their resolvers one at a time.
type Extension struct {
	db     *velox.DB
	limits Limits
	// schema is the server's, which Validate stores; nil for Validate(nil).
	schema *ast.Schema
	// txOpts are the options of a mutation's transaction.
	txOpts []velox.TxOption
	// txFilter decides whether a mutation runs in a transaction; nil means
	// every one does.
	txFilter func(ctx context.Context, op *ast.OperationDefinition) bool
	// batchScope is WithBatchScope's function, or nil.
	batchScope func(context.Context) any
}

// Option configures an Extension. The With functions make them; the zero
// Option is not valid, and NewExtension panics on it, as velox.Open does.
type Option struct{ apply func(*Extension) }

// NewExtension returns the extension that serves a schema on db, with
// DefaultLimits() unless WithLimits says otherwise.
//
// Make one Extension per server: the server's Use gives it the server's
// schema (Validate stores it), which costs every request, so one Extension
// used by two servers would cost both against the last one's schema.
//
// Set ErrorPresenter on the server too
// (srv.SetErrorPresenter(veloxgql.ErrorPresenter(nil))): without it, gqlgen's
// default presenter shows a client every error's text, velox's and the
// driver's, which name entities, tables, constraints and tenants.
func NewExtension(db *velox.DB, opts ...Option) *Extension {
	e := &Extension{db: db, limits: DefaultLimits()}
	for _, o := range opts {
		o.apply(e)
	}
	return e
}

// WithLimits sets the extension's limits to l, each zero field of l taken
// from DefaultLimits(). Given twice, the last wins.
func WithLimits(l Limits) Option {
	return Option{func(e *Extension) { e.limits = l.orDefault() }}
}

// WithTxOptions sets the options of a mutation's transaction. Validate
// refuses TxRetrySerializable: gqlgen makes the response once, so the
// operation cannot run again. Given twice, the last wins.
func WithTxOptions(opts ...velox.TxOption) Option {
	opts = slices.Clone(opts)
	return Option{func(e *Extension) { e.txOpts = opts }}
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
	return Option{func(e *Extension) { e.txFilter = f }}
}

// WithBatchScope adds f's value to what the edges' batches are keyed by:
// edge calls whose contexts give different values never share a read. f
// gets the context of each call of a generated edge method (its field's
// resolver's), and returns a comparable value; a caller whose value cannot
// be compared (reflect.Value.Comparable) reads alone. f is called from
// concurrent resolvers, so it must be safe for concurrent use. Given twice,
// the last wins.
//
// A batch reads every row's edge under its first caller's context. velox's row
// scope (velox.RowScopeFromContext: the tenant, the WithAllTenants and
// WithoutPolicy bypasses, the shard) is always in the key, and so are the DB
// and a list edge's MaxList. Any other context value that a Read policy or a
// Rewrite or Around hook reads, such as the viewer or a role, must be the same
// for every row of a field, or be in f's value: a directive or a field
// middleware that gives an edge field's resolver another context for some
// parent rows (acting as the row's owner, raising a role) needs WithBatchScope,
// or the rows of the field are read under whichever context comes first.
func WithBatchScope(f func(context.Context) any) Option {
	return Option{func(e *Extension) { e.batchScope = f }}
}

// NewContext returns ctx with the extension's DB and limits, as a query
// operation gets them: for generated code called outside a gqlgen server.
// DBFromContext and LimitsFromContext read them back. Outside a server
// nothing batches, so the context holds no table of rows. It panics with the
// error Validate would return.
func (e *Extension) NewContext(ctx context.Context) context.Context {
	if err := e.Validate(nil); err != nil {
		panic(err)
	}
	return withState(ctx, e.db, e.limits)
}

var (
	_ graphql.HandlerExtension          = (*Extension)(nil)
	_ graphql.OperationParameterMutator = (*Extension)(nil)
	_ graphql.OperationContextMutator   = (*Extension)(nil)
	_ graphql.OperationInterceptor      = (*Extension)(nil)
	_ graphql.ResponseInterceptor       = (*Extension)(nil)
	_ graphql.FieldInterceptor          = (*Extension)(nil)
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

// Validate implements graphql.HandlerExtension: it refuses a nil DB, a
// negative limit, and transaction options that ask for
// velox.TxRetrySerializable, when the server takes the extension.
func (e *Extension) Validate(es graphql.ExecutableSchema) error {
	if es != nil {
		e.schema = es.Schema()
	}
	if e.db == nil {
		return errors.New("veloxgql.NewExtension: nil DB")
	}
	for _, l := range e.limits.fields() {
		if l.n < 0 {
			return fmt.Errorf("veloxgql.NewExtension: Limits.%s is %d, and a limit must not be negative (zero is the default)", l.name, l.n)
		}
	}
	if n := velox.TxRetries(e.txOpts...); n > 0 {
		return fmt.Errorf("veloxgql.WithTxOptions: TxRetrySerializable(%d) would run the mutation again, and gqlgen makes its response once", n)
	}
	return nil
}

// MutateOperationParameters implements graphql.OperationParameterMutator: it
// refuses a document over MaxTokens or MaxFields. gqlgen runs it before it parses
// or validates, whichever the transport. An empty query, that an APQ extension
// after this one fills, passed here when it was stored.
func (e *Extension) MutateOperationParameters(_ context.Context, p *graphql.RawParams) *gqlerror.Error {
	if p.Query == "" {
		return nil
	}
	return checkDocument(p.Query, e.limits)
}

// MutateOperationContext implements graphql.OperationContextMutator: it
// refuses a request whose Cost is over MaxCost with ErrTooCostly. gqlgen runs
// it after validation and before any resolver, so no statement is sent.
func (e *Extension) MutateOperationContext(_ context.Context, rc *graphql.OperationContext) *gqlerror.Error {
	if e.schema == nil || rc.Operation == nil {
		return nil
	}
	c := costOf(e.schema, rc.Operation, rc.Variables, e.limits)
	var msg string
	if meta := c.meta; meta > int64(e.limits.MaxIntrospectionCost) {
		msg = fmt.Sprintf("%v: its introspection costs %d, and the most is %d", ErrTooCostly, meta, e.limits.MaxIntrospectionCost)
	} else if data := add(c.rows, c.statements); data > int64(e.limits.MaxCost) {
		msg = fmt.Sprintf("%v: it costs %d, and the most is %d", ErrTooCostly, data, e.limits.MaxCost)
	} else {
		return nil
	}
	err := &gqlcore.Error{Code: CodeTooCostly, Msg: msg, Err: ErrTooCostly}
	return &gqlerror.Error{Err: err, Message: err.Error(), Extensions: err.Extensions()}
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

// InterceptResponse implements graphql.ResponseInterceptor: it gives each
// response a table of the rows it reads, whose edges batch (Batch), and
// makes a mutation's first response in a transaction. Every other
// response, and an error response gqlgen makes without running the
// operation, passes through. A subscription's event is a response: its
// rows are let go of when it is made.
func (e *Extension) InterceptResponse(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
	ctx = gqlcore.WithTable(ctx, e.schema, e.batchScope)
	m, _ := ctx.Value(mutationKey{}).(*mutation)
	if m == nil || m.plain || m.started.Swap(true) {
		return next(ctx)
	}
	var resp *graphql.Response
	err := e.db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		// The transaction's ctx: its deadline, and the TxAround hooks' values.
		resp = next(withState(ctx, tx, e.limits))
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
	if !gqlcore.Is(err, errRollback) {
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
