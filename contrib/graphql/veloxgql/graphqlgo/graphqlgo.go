package graphqlgo

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/syssam/graphql-go"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/request"
)

// mutationTx and cost are the values of WithMutationTx and WithCost in
// veloxgql.Config.Adapter.
type (
	mutationTx struct{}
	costCheck  struct{}
)

// WithMutationTx runs each mutation operation in one db.Tx, on the
// primary, unless veloxgql.WithTxFilter leaves it out. Its resolvers get
// the transaction's DB (veloxgql.DB) and context, so every statement of the
// operation, its nested fields' too, is in the transaction. The transaction
// commits when the response has no errors, and rolls back when it has any,
// even a field's error under data the other fields filled; a response whose
// transaction did not commit has data null, and an error of the commit is
// added, presented by ErrorPresenter(nil).
//
// A transaction's DB is not safe for concurrent use, and graphql-go resolves
// the fields below a mutation's root concurrently. So under WithMutationTx
// a mutation's fields run one at a time, through a field interceptor; a
// field waiting for its turn is parked in the operation's wave coordinator,
// so an application's loader still flushes. A field interceptor makes every
// field of every operation take graphql-go's type-erased path, which costs
// queries too. A resolver of a mutation must not itself use the DB from more
// than one goroutine.
//
// It is off by default: nested fields resolve concurrently, and an
// application's services often open transactions of their own. Without it a
// mutation runs on the DB with no transaction, in a velox session
// (velox.WithSession), so on a DB with replicas its reads after its own
// writes go to the primary.
func WithMutationTx() veloxgql.Option { return veloxgql.AdapterOption(mutationTx{}) }

// WithCost refuses, before any resolver runs, a request whose veloxgql.Cost
// is over the MaxCost of the limits, or whose introspection is over
// MaxIntrospectionCost, with veloxgql.ErrTooCostly, the code
// veloxgql.CodeTooCostly and no data. It costs the operation against the
// executor's schema (Executor.Schema), so the options With returns serve one
// executor: With panics when they are given to a second one.
//
// It is off by default: graphql-go has a cost model of its own
// (graphql.WithQueryCost) and depth and complexity limits, which an
// application configures.
func WithCost() veloxgql.Option { return veloxgql.AdapterOption(costCheck{}) }

// With returns the graphql-go executor options that serve velox's
// generated code on db, configured by opts: veloxgql's (veloxgql.WithLimits,
// veloxgql.WithTxOptions, veloxgql.WithTxFilter, veloxgql.WithBatchScope)
// and this package's (WithMutationTx, WithCost):
//
//	s, err := graph.NewSchema(post.Bindings(h), user.Bindings(h), graphqlgo.Scalars())
//	exec := graphql.NewExecutor(s, graphqlgo.With(db, veloxgql.WithLimits(l))...)
//
// The options are an operation interceptor, which puts the DB, the limits
// and graphql-go's selection source in each operation's context, where
// veloxgql.DB and veloxgql.LimitsFromContext read them and generated code
// finds the field it resolves, with a new table of rows, whose edges batch;
// a subscription interceptor, which puts the same state in the context a
// subscription's source opens under; and, with WithMutationTx, a field
// interceptor. graphql-go runs the operation interceptors for each event of
// a subscription, so each event has a table of its own and lets go of its
// rows when it is made.
//
// The limits are veloxgql's: gqlgraph reads MaxPage, MaxList,
// MaxOrderTerms and the filter limits from the state. The limits of a
// request as a whole are graphql-go's own options: Limits maps veloxgql's
// onto them, and WithCost adds velox's cost.
//
// It panics on a nil db, a negative limit, a zero veloxgql.Option, an
// option of another adapter, and transaction options that ask for
// velox.TxRetrySerializable under WithMutationTx: graphql-go makes the
// response once, so the operation cannot run again.
func With(db *velox.DB, opts ...veloxgql.Option) []graphql.ExecutorOption {
	c := veloxgql.NewConfig(opts...)
	a := &adapter{db: db, limits: c.Limits, txOpts: c.TxOptions, txFilter: c.TxFilter, batchScope: c.BatchScope}
	for _, v := range c.Adapter {
		switch v.(type) {
		case mutationTx:
			a.mutationTx = true
		case costCheck:
			a.cost = true
		default:
			panic(fmt.Sprintf("graphqlgo.With: an option of another adapter (%T), which graphql-go's does not take", v))
		}
	}
	if db == nil {
		panic("graphqlgo.With: nil DB")
	}
	if err := request.CheckLimits("graphqlgo.With", a.limits); err != nil {
		panic(err)
	}
	if n := velox.TxRetries(a.txOpts...); n > 0 && a.mutationTx {
		panic(fmt.Sprintf("veloxgql.WithTxOptions: TxRetrySerializable(%d) would run the mutation again, and graphql-go makes its response once", n))
	}
	out := []graphql.ExecutorOption{
		graphql.WithOperationInterceptor(a),
		graphql.WithSubscriptionInterceptor(a),
	}
	if a.cost {
		out = append(out, a.capture)
	}
	if a.mutationTx {
		out = append(out, graphql.WithFieldInterceptor(serial{}))
	}
	return out
}

// adapter is what With configures, and its interceptors.
type adapter struct {
	db     *velox.DB
	limits veloxgql.Limits
	// txOpts are the options of a mutation's transaction.
	txOpts []velox.TxOption
	// txFilter decides whether a mutation runs in a transaction; nil means
	// every one does, under WithMutationTx.
	txFilter func(ctx context.Context, op *ast.OperationDefinition) bool
	// batchScope is veloxgql.WithBatchScope's function, or nil.
	batchScope func(context.Context) any
	mutationTx bool
	cost       bool

	// exec is the executor the options serve, for WithCost's schema.
	mu   sync.Mutex
	exec *graphql.Executor
}

var (
	_ graphql.OperationInterceptor    = (*adapter)(nil)
	_ graphql.SubscriptionInterceptor = (*adapter)(nil)
	_ graphql.FieldInterceptor        = serial{}
)

// capture is the executor option that records the executor, whose schema
// WithCost reads. It panics on a second executor.
func (a *adapter) capture(e *graphql.Executor) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.exec != nil && a.exec != e {
		panic("graphqlgo.With: the options of WithCost serve one executor, and were given to a second")
	}
	a.exec = e
}

// executor is the executor capture recorded, or nil.
func (a *adapter) executor() *graphql.Executor {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.exec
}

// install is ctx with the operation's state.
func (a *adapter) install(ctx context.Context) context.Context {
	return gqlcore.Install(ctx, a.db, &a.limits, source{})
}

// InterceptSubscription implements graphql.SubscriptionInterceptor: the
// subscription's source opens with the operation's state in its context,
// and under WithCost one over the cost does not open. Each event's
// resolvers get the state, and a table of their own, from
// InterceptOperation.
func (a *adapter) InterceptSubscription(ctx context.Context, oc *graphql.OperationContext, next graphql.SubscriptionHandler) (<-chan *graphql.Response, error) {
	if err := a.costError(oc); err != nil {
		return nil, err
	}
	return next(a.install(ctx), oc)
}

// costError is the error of oc under WithCost when it costs too much; nil
// without WithCost.
func (a *adapter) costError(oc *graphql.OperationContext) *graphql.Error {
	if !a.cost {
		return nil
	}
	e := a.executor()
	if e == nil {
		return nil
	}
	err := request.CostError(e.Schema().AST(), oc.Operation, oc.Variables, a.limits)
	if err == nil {
		return nil
	}
	return &graphql.Error{Message: err.Error(), Extensions: err.Extensions(), Err: err}
}

// InterceptOperation implements graphql.OperationInterceptor: it puts the
// operation's state and a new table of rows in its context, refuses a
// request over the cost under WithCost, and runs a mutation in a
// transaction under WithMutationTx, or in a velox session.
func (a *adapter) InterceptOperation(ctx context.Context, oc *graphql.OperationContext, next graphql.OperationHandler) *graphql.Response {
	ctx = gqlcore.WithTable(a.install(ctx), a.batchScope)
	op := oc.Operation
	if op != nil && op.Operation != ast.Subscription { // a subscription's cost is InterceptSubscription's
		if err := a.costError(oc); err != nil {
			return &graphql.Response{Errors: []*graphql.Error{err}}
		}
	}
	if op != nil && op.Operation == ast.Mutation {
		if a.mutationTx && (a.txFilter == nil || a.txFilter(ctx, op)) {
			return a.inTx(ctx, oc, next)
		}
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
	}, a.txOpts...)
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
		resp.Errors = append(resp.Errors, ErrorPresenter(nil)(ctx, err))
	}
	return resp
}

// serial is the field interceptor of WithMutationTx: the fields of a
// mutation in a transaction run one at a time. The lock cannot nest:
// graphql-go calls the field interceptors around the field's resolver alone,
// and resolves its children after it returns. A field that waits for the
// lock is parked in the operation's wave coordinator.
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
