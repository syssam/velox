package gqlgenx

import (
	"context"
	"slices"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx/internal/request"
)

// config is what the Options configure, which NewExtension reads.
type config struct {
	// limits are the operation's limits, which the extension puts in each
	// operation's context; each zero field is veloxgql.DefaultLimits()'s.
	limits veloxgql.Limits
	// txOptions are the options of a mutation's transaction.
	txOptions []velox.TxOption
	// txFilter decides whether a mutation runs in a transaction; nil means
	// every one does.
	txFilter func(ctx context.Context, op *ast.OperationDefinition) bool
	// batchScope is WithBatchScope's function, or nil.
	batchScope func(context.Context) any
}

// Option configures NewExtension and Setup. The With functions make them;
// the zero Option is not valid, and NewExtension panics on it, as
// velox.Open does.
type Option struct{ apply func(*config) }

// newConfig returns the config of opts, applied in order, from
// veloxgql.DefaultLimits() and nothing else. It panics on a zero Option.
func newConfig(opts ...Option) config {
	c := config{limits: veloxgql.DefaultLimits()}
	for _, o := range opts {
		if o.apply == nil {
			panic("gqlgenx: a zero Option; make one with a With function")
		}
		o.apply(&c)
	}
	return c
}

// WithLimits sets the limits the extension puts in each operation's
// context to l, each zero field of l taken from veloxgql.DefaultLimits().
// Use it when the defaults do not suit the schema. Given twice, the last
// wins.
func WithLimits(l veloxgql.Limits) Option {
	return Option{func(c *config) { c.limits = request.OrDefault(l) }}
}

// WithTxOptions sets the options of a mutation's transaction, such as
// velox.TxIsolation. Validate refuses TxRetrySerializable: gqlgen makes the response once, so the
// operation cannot run again. Given twice, the last wins.
func WithTxOptions(opts ...velox.TxOption) Option {
	opts = slices.Clone(opts)
	return Option{func(c *config) { c.txOptions = opts }}
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
	return Option{func(c *config) { c.txFilter = f }}
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
// scope (velox.RowScopeFromContext: the scope values, the
// WithoutPolicy bypass, the shard) is always in the key, and so are the DB,
// a list edge's MaxList and the SQL each caller's context reads the target
// with, after the Read policy, the interceptors and the Rewrite hooks. So
// callers that a policy shows different rows never share a read. An Around
// hook runs only when the batch is read: a context value that only an Around
// hook reads must be the same for every row of a field, or be in f's value.
func WithBatchScope(f func(context.Context) any) Option {
	return Option{func(c *config) { c.batchScope = f }}
}
