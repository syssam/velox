package gqlgenx

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/errcode"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx/internal/request"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// Extension is the gqlgen handler extension that serves a schema on a DB.
// Make one with NewExtension, and use it first, so that it is the outermost
// of the server's extensions:
//
//	srv := handler.New(graph.NewExecutableSchema(cfg))
//	srv.Use(gqlgenx.NewExtension(db))
//	srv.SetErrorPresenter(gqlgenx.ErrorPresenter(nil))
//
// or let Setup do that, with a limit on the body too, which no extension can
// set.
//
// Every operation's context gets the DB, which veloxgql.DBFromContext reads, and the
// extension's limits, which veloxgql.LimitsFromContext reads, so queries and
// subscriptions run on the DB, and a DB with replicas routes their reads to
// them.
//
// A mutation operation runs in one db.Tx, on the primary, unless WithTxFilter
// leaves it out, and its resolvers get the transaction's DB and context, so
// every statement of the operation, its nested fields' too, is in the
// transaction. A statement a mutation's resolver runs on db itself, not on
// the DB veloxgql.DBFromContext reads, fails with velox.ErrOutsideTx; under
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
// veloxgql.Limits.MaxCost bounds the whole request. Before any resolver runs, the
// extension computes the operation's Cost, which multiplies by first and
// last, counts every alias, every statement and every branch of a fragment; a
// request over MaxCost, or whose introspection, counted by the fields of its
// response, is over MaxIntrospectionCost, fails with veloxgql.ErrTooCostly, the code
// veloxgql.CodeTooCostly and data null, and sends no statement. It reads the schema the
// server's Use gives Validate; gqlgen's extension.FixedComplexityLimit counts
// fields, not rows, and is not needed with it.
//
// Before gqlgen parses a request, whose validation is quadratic in the fields
// that share a response name, the extension parses it and refuses one over
// MaxTokens, MaxFields, MaxFragments, MaxDepth, MaxNameBytes or MaxLiteralBytes, or
// whose variables are over MaxDepth or MaxVariableNodes, on every transport. That is a
// second parse, which a query cache of gqlgen's does not spare. Use it before
// extension.AutomaticPersistedQuery, so that the cache never stores a refused
// query.
//
// Cost cannot see a value's size or a statement's time, so the Extension also holds a
// response to MaxResponseBytes and MaxErrors and a query or a mutation to Timeout, and
// stops the request there: what is not yet resolved does not run, and a
// statement running is cancelled. A subscription has no Timeout, and each of its events
// has its own counts. Only values of a string, bytes or JSON kind are counted, with a
// pointer to one and a list of them, such as the []*string of a [String] field: a custom
// scalar of a struct's is not. A deadline of the application's, the request's or a
// transaction's, is not the Timeout, and its errors are the fields' own.
//
// The server's Use panics when the DB is nil, when a limit is negative, or
// when WithTxOptions asks for velox.TxRetrySerializable: Validate refuses
// it, naming the limit or the option.
//
// The extension mutates the operation's parameters, to refuse a document or variables
// over those; the operation's context, to refuse a request over
// MaxCost; intercepts the operation, to put the DB and the limits in
// its context, which the executable schema runs in; the response, because a
// mutation's resolvers run while gqlgen makes its response (inside the
// response interceptors), and the response is where their errors collect,
// to decide between commit and rollback; and a mutation's fields, to run
// their resolvers one at a time.
type Extension struct {
	db     *velox.DB
	limits veloxgql.Limits
	// schema is the server's, which Validate stores; nil for Validate(nil).
	schema *ast.Schema
	// bounds is what the check of a request reads, of the limits and the schema.
	bounds request.Bounds
	// txOpts are the options of a mutation's transaction.
	txOpts []velox.TxOption
	// txFilter decides whether a mutation runs in a transaction; nil means
	// every one does.
	txFilter func(ctx context.Context, op *ast.OperationDefinition) bool
	// batchScope is WithBatchScope's function, or nil.
	batchScope func(context.Context) any
}

// NewExtension returns the extension that serves a schema on db, configured
// by opts: veloxgql.DefaultLimits() unless WithLimits says otherwise. It
// panics on a zero Option.
//
// Make one Extension per server: the server's Use gives it the server's
// schema (Validate stores it), which costs every request, so one Extension
// used by two servers would cost both against the last one's schema.
//
// Set ErrorPresenter on the server too
// (srv.SetErrorPresenter(gqlgenx.ErrorPresenter(nil))): without it, gqlgen's
// default presenter shows a client every error's text, velox's and the
// driver's, which name entities, tables, constraints and scope values.
func NewExtension(db *velox.DB, opts ...Option) *Extension {
	c := newConfig(opts...)
	return &Extension{db: db, limits: c.limits, txOpts: c.txOptions, txFilter: c.txFilter, batchScope: c.batchScope}
}

// NewContext returns ctx with the extension's DB and limits, as a query
// operation gets them: for generated code called outside a gqlgen server.
// veloxgql.DBFromContext and veloxgql.LimitsFromContext read them back. Its
// selection source is gqlgen's, which outside a gqlgen field finds none, so
// generated code reads alone and every column. Outside a server nothing
// batches, so the context holds no table of rows. It panics with the error
// Validate would return.
func (e *Extension) NewContext(ctx context.Context) context.Context {
	if err := e.Validate(nil); err != nil {
		panic(err)
	}
	return gqlcore.Install(ctx, e.db, &e.limits, &source{schema: e.schema})
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
	e.bounds = request.NewBounds(e.limits, e.schema)
	if e.db == nil {
		return errors.New("gqlgenx.NewExtension: nil DB")
	}
	if err := request.CheckLimits("gqlgenx.NewExtension", e.limits); err != nil {
		return err
	}
	if n := velox.TxRetries(e.txOpts...); n > 0 {
		return fmt.Errorf("gqlgenx.WithTxOptions: TxRetrySerializable(%d) would run the mutation again, and gqlgen makes its response once", n)
	}
	return nil
}

// MutateOperationParameters implements graphql.OperationParameterMutator: it
// refuses a request whose document or variables are over a limit. gqlgen runs it
// before it parses or validates, whichever the transport.
func (e *Extension) MutateOperationParameters(_ context.Context, p *graphql.RawParams) *gqlerror.Error {
	b := e.bounds
	if e.schema == nil {
		b = request.NewBounds(e.limits, nil) // before Validate, for a test
	}
	return request.Check(p.OperationName, p.Query, p.Variables, b)
}

// MutateOperationContext implements graphql.OperationContextMutator: it
// refuses a request whose Cost is over MaxCost with veloxgql.ErrTooCostly. gqlgen runs
// it after validation and before any resolver, so no statement is sent.
func (e *Extension) MutateOperationContext(_ context.Context, rc *graphql.OperationContext) *gqlerror.Error {
	err := request.CostError(e.schema, rc.Operation, rc.Variables, e.limits)
	if err == nil {
		return nil
	}
	return &gqlerror.Error{Err: err, Message: err.Error(), Extensions: err.Extensions()}
}

// InterceptOperation implements graphql.OperationInterceptor: it puts the
// DB and the limits in the operation's context, and marks a mutation's.
func (e *Extension) InterceptOperation(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
	rc := graphql.GetOperationContext(ctx)
	// After every extension's MutateOperationContext, extension.Introspection's too.
	if rc.DisableIntrospection && rc.Operation != nil && asksIntrospection(rc.Operation.SelectionSet, nil) {
		err := gqlerror.Errorf("introspection is disabled")
		errcode.Set(err, errcode.ValidationFailed)
		return graphql.OneShot(&graphql.Response{Errors: gqlerror.List{err}})
	}
	ctx = gqlcore.Install(ctx, e.db, &e.limits, &source{schema: e.schema})
	if rc.Operation == nil || rc.Operation.Operation != ast.Subscription {
		ctx = context.WithValue(ctx, operationKey{}, new(operation))
	}
	if op := rc.Operation; op != nil && op.Operation == ast.Mutation {
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

// asksIntrospection reports whether set, a root selection, holds __schema or __type,
// each fragment read once. gqlgen's own answer to them, with introspection off, is an
// error ErrorPresenter could only call internal.
func asksIntrospection(set ast.SelectionSet, seen map[string]bool) bool {
	for _, s := range set {
		switch s := s.(type) {
		case *ast.Field:
			if s.Name == "__schema" || s.Name == "__type" {
				return true
			}
		case *ast.InlineFragment:
			if asksIntrospection(s.SelectionSet, seen) {
				return true
			}
		case *ast.FragmentSpread:
			if s.Definition == nil || seen[s.Name] {
				continue
			}
			if seen == nil {
				seen = map[string]bool{}
			}
			seen[s.Name] = true
			if asksIntrospection(s.Definition.SelectionSet, seen) {
				return true
			}
		}
	}
	return false
}

// InterceptResponse implements graphql.ResponseInterceptor: it gives each
// response a table of the rows it reads, whose edges batch (Batch), and
// makes a mutation's first response in a transaction. A query's or a mutation's first
// response starts its Timeout, and every response holds to MaxResponseBytes and MaxErrors:
// one over them is replaced, with one error for what stopped it and data null, and a
// mutation rolls back. Every other response, and an error response gqlgen makes without
// running the operation, passes through. A subscription's event is a response: its
// rows are let go of when it is made.
func (e *Extension) InterceptResponse(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
	given := ctx
	ctx = gqlcore.WithTable(ctx, e.batchScope)
	ctx, r, done := e.operationResponse(ctx)
	m, _ := ctx.Value(mutationKey{}).(*mutation)
	if m == nil || m.plain || m.started.Swap(true) {
		resp := next(ctx)
		r.expired()
		done(resp)
		return e.finish(given, r, resp)
	}
	var resp *graphql.Response
	err := e.db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		// The transaction's ctx: its deadline, and the TxAround hooks' values.
		resp = next(gqlcore.WithDB(ctx, tx))
		if resp != nil && len(resp.Errors) > 0 || r.why.Load() != 0 {
			return errRollback
		}
		return nil
	}, e.txOpts...)
	r.expired()
	done(resp)
	if err == nil {
		return resp
	}
	if r.why.Load() != 0 {
		return e.finish(given, r, resp)
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
// children after they return. It counts what a field gives its response, and a field of
// a response that has stopped does nothing.
func (*Extension) InterceptField(ctx context.Context, next graphql.Resolver) (any, error) {
	r, _ := ctx.Value(responseKey{}).(*response)
	if r == nil {
		return next(ctx)
	}
	if r.why.Load() != 0 {
		return nil, nil
	}
	if m := r.mutation; m != nil {
		m.mu.Lock()
		defer m.mu.Unlock()
	}
	v, err := next(ctx)
	if err != nil {
		r.failed(ctx)
		return v, err
	}
	r.count(v)
	return v, nil
}
