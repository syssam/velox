package graphqlgo

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"weak"

	"github.com/syssam/graphql-go"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/request"
)

// With is the executor option that serves velox's generated code on db.
// Give it to every executor whose schema binds generated code:
//
//	exec := graphql.NewExecutor(s, graphqlgo.With(db), graphql.WithMaxDepth(12))
//
// For each operation it puts db, the page limits and the selection source
// in the context, where veloxgql.DB reads the DB and generated code finds
// the field it resolves, with a new table of rows, so that edges batch. A
// subscription's source opens with the same state, and each of its events
// gets a table of its own. A mutation runs in a velox session
// (velox.WithSession), so on a DB with replicas it reads its own writes
// from the primary; MutationTx runs it in a transaction instead.
//
// Without With, generated code fails with veloxgql.ErrNoSelectionSource and
// veloxgql.ErrNoDB before any statement. With panics on a nil db, and when
// an executor is given it twice.
func With(db *velox.DB) graphql.ExecutorOption {
	if db == nil {
		panic("graphqlgo.With: nil DB")
	}
	return func(e *graphql.Executor) {
		a := adapterOf(e)
		if a.db != nil {
			panic("graphqlgo.With: given twice to one executor")
		}
		a.db = db
	}
}

// PageLimits is the executor option that sets the limits generated code
// holds a request to: the largest page of a connection (MaxPage), the rows
// of a list edge (MaxList), the terms of an orderBy (MaxOrderTerms) and the
// size of a where filter (MaxFilterDepth, MaxFilterWidth, MaxFilterNodes),
// and the cost Cost refuses (MaxCost, MaxIntrospectionCost). Use it when
// velox's defaults do not suit the schema. A zero field is
// veloxgql.DefaultLimits()'s, and so are all of them without PageLimits.
// The other fields of l limit a whole request, which graphql-go's own
// options do (graphql.WithMaxDepth, graphql.WithMaxTokens and the rest):
// PageLimits ignores them. It panics on a negative field, and when an
// executor is given it twice.
func PageLimits(l veloxgql.Limits) graphql.ExecutorOption {
	if err := request.CheckLimits("graphqlgo.PageLimits", l); err != nil {
		panic(err)
	}
	l = veloxgql.NewConfig(veloxgql.WithLimits(l)).Limits
	return func(e *graphql.Executor) {
		a := adapterOf(e)
		if a.limitsSet {
			panic("graphqlgo.PageLimits: given twice to one executor")
		}
		a.limits, a.limitsSet = l, true
	}
}

// Cost is the executor option that refuses, before any resolver runs, a
// request whose veloxgql.Cost is over MaxCost, or whose introspection is
// over MaxIntrospectionCost (see PageLimits), with veloxgql.ErrTooCostly,
// the code veloxgql.CodeTooCostly and no data. A subscription over it does
// not open. Use it when the schema's connections and list edges are what
// you need to bound: velox's cost charges each connection its page and
// each list edge its MaxList, per parent row. graphql.WithQueryCost is
// graphql-go's own cost model; the two can be used together.
func Cost() graphql.ExecutorOption {
	return func(e *graphql.Executor) { adapterOf(e).cost = true }
}

// MutationTxOption configures MutationTx.
type MutationTxOption func(*txConfig)

// txConfig is what MutationTx's options set.
type txConfig struct {
	opts   []velox.TxOption
	filter func(ctx context.Context, op *ast.OperationDefinition) bool
	log    *slog.Logger
}

// TxOptions gives each mutation's transaction opts, such as
// velox.TxIsolation. velox.TxRetrySerializable is refused: graphql-go
// writes a response once, so a mutation cannot run again.
func TxOptions(opts ...velox.TxOption) MutationTxOption {
	return func(c *txConfig) { c.opts = append(c.opts, opts...) }
}

// TxFilter runs in a transaction only the mutations f returns true for;
// the others run as they do without MutationTx. f gets the operation's
// context and its definition, and must be safe for concurrent use.
func TxFilter(f func(ctx context.Context, op *ast.OperationDefinition) bool) MutationTxOption {
	return func(c *txConfig) { c.filter = f }
}

// ErrorLog sends a transaction's begin or commit error, which MutationTx
// adds to the response itself, to l: an internal error at level error, a
// canceled request at level debug, as ErrorPresenter logs. Without it, or
// with a nil l, it goes to slog's default logger.
func ErrorLog(l *slog.Logger) MutationTxOption {
	return func(c *txConfig) { c.log = l }
}

// MutationTx is the executor option that runs each mutation in one db.Tx
// on the primary, the db of With, in any order with With. Use it when a
// mutation's writes must commit together, and the application's resolvers
// do not open transactions of their own. The resolvers get the
// transaction's DB (veloxgql.DB) and context, so every statement of the
// operation, its nested fields' too, is in the transaction.
//
// The transaction commits when the response has no errors, and rolls back
// when it has any, even a field's error under data the other fields
// filled; a response whose transaction did not commit has data null, and a
// begin or commit error is added, presented as ErrorPresenter presents it
// and logged to ErrorLog's logger. Without With, a mutation fails, naming
// graphqlgo.With, and no resolver runs.
//
// A transaction's DB is not safe for concurrent use, and graphql-go resolves
// the fields below a mutation's root concurrently. So a mutation's fields
// run one at a time, through a field interceptor; a field waiting for its
// turn is parked in the operation's wave coordinator, so an application's
// loader still flushes. A field interceptor makes every field of every
// operation, a query's too, take graphql-go's type-erased path. A
// mutation's resolver must not itself use the DB from more than one
// goroutine.
//
// It panics on transaction options that ask for velox.TxRetrySerializable,
// on a nil option, and when an executor is given it twice.
func MutationTx(opts ...MutationTxOption) graphql.ExecutorOption {
	c := &txConfig{}
	for _, o := range opts {
		if o == nil {
			panic("graphqlgo.MutationTx: a nil option")
		}
		o(c)
	}
	if n := velox.TxRetries(c.opts...); n > 0 {
		panic(fmt.Sprintf("graphqlgo.TxOptions: TxRetrySerializable(%d) would run the mutation again, and graphql-go writes its response once", n))
	}
	present := loggerPresenter(c.log)
	return func(e *graphql.Executor) {
		a := adapterOf(e)
		if a.tx != nil {
			panic("graphqlgo.MutationTx: given twice to one executor")
		}
		a.tx, a.present = c, present
		graphql.WithFieldInterceptor(serial{})(e)
	}
}

// adapters holds the adapter of each executor that one of this package's
// options was given to, while the executor lives, so that the options
// configure one adapter in any order.
var adapters sync.Map // weak.Pointer[graphql.Executor] -> *adapter

// adapterOf is e's adapter. The first call for e makes it and registers its
// interceptors on e; it is dropped from adapters when e is collected.
func adapterOf(e *graphql.Executor) *adapter {
	k := weak.Make(e)
	if a, ok := adapters.Load(k); ok {
		return a.(*adapter)
	}
	a := &adapter{schema: e.Schema(), limits: veloxgql.DefaultLimits()}
	adapters.Store(k, a)
	runtime.AddCleanup(e, func(k weak.Pointer[graphql.Executor]) { adapters.Delete(k) }, k)
	graphql.WithOperationInterceptor(a)(e)
	graphql.WithSubscriptionInterceptor(a)(e)
	return a
}

// adapter is what the options configure for one executor, and its
// interceptors. It holds no reference to the executor.
type adapter struct {
	db        *velox.DB
	schema    *graphql.Schema
	limits    veloxgql.Limits
	limitsSet bool
	cost      bool
	// tx is MutationTx's configuration, nil without it; present presents
	// its begin and commit errors.
	tx      *txConfig
	present graphql.ErrorPresenter
}

var (
	_ graphql.OperationInterceptor    = (*adapter)(nil)
	_ graphql.SubscriptionInterceptor = (*adapter)(nil)
	_ graphql.FieldInterceptor        = serial{}
)

// errNoWith is MutationTx's error without With.
var errNoWith = fmt.Errorf("graphqlgo.MutationTx: give the executor graphqlgo.With(db): %w", veloxgql.ErrNoDB)

// install is ctx with the operation's state, or ctx without With.
func (a *adapter) install(ctx context.Context) context.Context {
	if a.db == nil {
		return ctx
	}
	return gqlcore.Install(ctx, a.db, &a.limits, source{})
}

// InterceptSubscription implements graphql.SubscriptionInterceptor: the
// subscription's source opens with the state in its context, and under
// Cost one over the cost does not open. Each event's resolvers get the
// state, and a table of their own, from InterceptOperation.
func (a *adapter) InterceptSubscription(ctx context.Context, oc *graphql.OperationContext, next graphql.SubscriptionHandler) (<-chan *graphql.Response, error) {
	if err := a.costError(oc); err != nil {
		return nil, err
	}
	return next(a.install(ctx), oc)
}

// costError is the error of oc under Cost when it costs too much; nil
// without Cost.
func (a *adapter) costError(oc *graphql.OperationContext) *graphql.Error {
	if !a.cost {
		return nil
	}
	err := request.CostError(a.schema.AST(), oc.Operation, oc.Variables, a.limits)
	if err == nil {
		return nil
	}
	return &graphql.Error{Message: err.Error(), Extensions: err.Extensions(), Err: err}
}

// InterceptOperation implements graphql.OperationInterceptor: it puts the
// operation's state and a new table of rows in its context, refuses a
// request over the cost under Cost, and runs a mutation in a transaction
// under MutationTx, or in a velox session.
func (a *adapter) InterceptOperation(ctx context.Context, oc *graphql.OperationContext, next graphql.OperationHandler) *graphql.Response {
	if a.db != nil {
		ctx = gqlcore.WithTable(a.install(ctx), nil)
	}
	op := oc.Operation
	if op != nil && op.Operation != ast.Subscription { // a subscription's cost is InterceptSubscription's
		if err := a.costError(oc); err != nil {
			return &graphql.Response{Errors: []*graphql.Error{err}}
		}
	}
	if op == nil || op.Operation != ast.Mutation {
		return next(ctx, oc)
	}
	if a.tx != nil && (a.tx.filter == nil || a.tx.filter(ctx, op)) {
		if a.db == nil {
			return &graphql.Response{Data: []byte("null"), Errors: []*graphql.Error{a.present(ctx, errNoWith)}}
		}
		return a.inTx(ctx, oc, next)
	}
	if a.db != nil {
		pin := velox.PinnedUntil(ctx)
		ctx = velox.WithSession(ctx)
		velox.PinUntil(ctx, pin)
	}
	return next(ctx, oc)
}

// errRollback is what the transaction's function returns to roll back a
// response with errors.
var errRollback = errors.New("veloxgql: the response has errors")

// serialKey is the context key of a mutation's *sync.Mutex, which its
// field resolvers hold one at a time.
type serialKey struct{}

// inTx runs the mutation oc in a transaction: it commits when the response
// has no errors, and otherwise rolls back and nulls the response's data.
func (a *adapter) inTx(ctx context.Context, oc *graphql.OperationContext, next graphql.OperationHandler) *graphql.Response {
	ctx = context.WithValue(ctx, serialKey{}, new(sync.Mutex))
	var resp *graphql.Response
	err := a.db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		// The transaction's ctx: its deadline, and the TxAround hooks' values.
		resp = next(gqlcore.WithDB(ctx, tx), oc)
		if resp != nil && len(resp.Errors) > 0 {
			return errRollback
		}
		return nil
	}, a.tx.opts...)
	if err == nil {
		return resp
	}
	if resp == nil {
		resp = &graphql.Response{}
	}
	resp.Release()
	resp.Data = []byte("null")
	if !gqlcore.Is(err, errRollback) {
		// Begin or commit failed, or the transaction's deadline or the
		// request's context ended it.
		resp.Errors = append(resp.Errors, a.present(ctx, err))
	}
	return resp
}

// serial is MutationTx's field interceptor: the fields of a mutation in a
// transaction run one at a time. The lock cannot nest: graphql-go calls the
// field interceptors around the field's resolver alone, and resolves its
// children after it returns. A field that waits for the lock is parked in
// the operation's wave coordinator.
type serial struct{}

// InterceptField implements graphql.FieldInterceptor.
func (serial) InterceptField(ctx context.Context, _ *graphql.FieldContext, next graphql.FieldHandler) (any, error) {
	m, _ := ctx.Value(serialKey{}).(*sync.Mutex)
	if m == nil {
		return next(ctx)
	}
	if !m.TryLock() {
		w := graphql.OperationFrom(ctx).Waves()
		w.Park()
		m.Lock()
		w.Unpark()
	}
	defer m.Unlock()
	return next(ctx)
}
