package velox

import (
	"context"

	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/veloxtype"
)

// Caller is a statement's call site.
type Caller = veloxtype.Caller

// WithCallers records each statement's call site in Statement.Caller. It
// costs one runtime.Callers per batch of up to 8 frames between velox and the
// call site, usually one per statement, and nothing more once a call site's
// program counters are cached: each is resolved once per DB, for up to 4,096
// of them.
func WithCallers() Option { return engine.WithCallers() }

// WithCallerSkip names packages, by import path, whose frames are not the
// caller either, such as the application's own repository layer: the call
// site is then the frame that called into them. It implies WithCallers.
func WithCallerSkip(pkgPath ...string) Option { return engine.WithCallerSkip(pkgPath...) }

// WithSQLComment appends a sqlcommenter comment to every statement's text:
// app, the call site when WithCallers is on, then each tag in tags that the
// context sets (WithTags), in that order. An empty app panics, as does a tag
// that is empty, repeated, or named app or caller.
//
//	SELECT … /*app='billing',caller='billing%2Finvoice.go%3A42',route='GET%20%2Finvoices'*/
//
// Every byte of a key or value outside A–Z a–z 0–9 - . _ ~ is percent-encoded,
// so no value can end the comment. Statement.SQL is the text sent, comment
// included; the fingerprint is of the text without it. A Rewrite hook sees no
// comment, since it is rendered after them, from the Caller and Tags they
// leave; an Around hook sees the text sent. SQL(ctx) includes the comment,
// with the call site of the SQL call.
//
// List only tags whose values are stable per code path, such as a route
// template, never an ID: pgx caches prepared statements by their text, so a
// per-request value would prepare a statement per request, and it would
// make pg_stat_activity hard to read. (pg_stat_statements and MySQL's
// digest normalise comments away, so their statistics are not split.)
//
// With WithCallers, each call site of a shape is a distinct text, so pgx
// prepares one statement per call site per connection. A shape called from
// many sites multiplies statements against pgx's per-connection cache,
// which holds 512 by default: size pgxpool.Config.ConnConfig's
// StatementCacheCapacity for it, or use WithSQLComment without WithCallers.
//
// Comments are cached per call site and tag values, up to 4,096 of them and
// 4 MiB, evicting the oldest. Appending one to a statement's text costs one
// allocation, and a cache key over 256 bytes (a long file path with long
// tag values) one more.
func WithSQLComment(app string, tags ...string) Option { return engine.WithSQLComment(app, tags...) }

// Option configures a DB's pipeline. The With functions make them; the zero
// Option is not valid, and Connect, Open, OpenConn and OpenShards panic on it.
type Option = engine.Option

// WithRewrite adds a rewrite hook. Rewrite hooks run in the order given. A
// nil hook panics.
func WithRewrite(r Rewrite) Option { return engine.WithRewrite(engine.Rewrite(r)) }

// Filter is a row filter for every entity, which WithFilter adds. Given
// the statement's context, an entity's name (as Mutation.Entity gives it,
// "Invoice") and the op the entity is read for, it returns that entity's
// row condition, by column name as an Interceptor's Filter does: FieldEQ
// and the like. It returns Allow() for an entity it does not restrict.
// Its zero Predicate fails the statement, and so does an error, which is
// not a denial.
//
// op is the statement's own for its own entity: OpSelect for a read,
// OpUpdate for an update, a Save or a Writable read, OpDelete for a delete,
// and OpInsert for the update branch of an upsert. A soft delete (package
// softdelete) is OpDelete, though it sends an UPDATE, so a filter that
// restricts deletes restricts it too; a restore is OpUpdate. The entity's
// policies still see the soft delete as an update. It is OpSelect for every
// entity the statement reaches: by a With load, a Join, Has, HasWith or a
// subquery. A filter that should narrow reads only, as ent's interceptors
// do, returns Allow() when op is not OpSelect.
//
// Return Allow() for the join entities of many-to-many edges, edge
// schemas included: Has, Of, a With load, LoadEach and CountEach through one
// fail when a filter restricts it, as they read its rows through the
// relation's two sides.
type Filter func(ctx context.Context, entity string, op Op) (Predicate, error)

// WithFilter adds f to the row filters of every entity, for the DB and every
// Tx and shard it opens, as ent's client.Intercept adds an interceptor for
// every entity. Its reach is an Interceptor's: every read, update and delete
// of an entity, and every With load, Join, Has and HasWith subquery, Count,
// aggregate and page that reads one, gets the condition f returns for it.
// WithoutPolicy does not lift it: f offers its own way to be skipped, such
// as a context value it reads.
//
//	db, err := velox.Connect("postgres", dsn, velox.WithFilter(func(ctx context.Context, entity string, op velox.Op) (velox.Predicate, error) {
//		if op != velox.OpSelect || !softDeleted[entity] || includeDeleted(ctx) {
//			return velox.Allow(), nil
//		}
//		return velox.FieldIsNull("deleted_at"), nil
//	}))
//
// The scope calls f with the statement's context, at most once per
// statement for each entity and op, except where a relation loops back
// (ADR 0091): for the statement's own entity and for each one a Has,
// HasWith or Join of it reaches. velox keeps nothing between statements.
// f's conditions come before the entity's interceptors' and its Read
// policy's in the WHERE, and several WithFilter filters AND in the order
// given. A column f names that the entity lacks fails the statement. A
// Filter that reads a context value records it with WithScopeValue, so
// that the GraphQL batcher keeps callers apart.
//
// A join entity of a many-to-many edge is an entity too: f is called for it
// where its rows are read or written as its own, as Rel.Add and the like
// do. A Has, an Of, a With load, LoadEach or CountEach through it calls f
// for it with OpSelect, once per statement, and fails unless f returns
// Allow(). On MySQL, an upsert that updates an entity f returns a condition for (given OpInsert) is refused
// with ErrPolicyUpsert. A nil f panics.
func WithFilter(f Filter) Option {
	if f == nil {
		panic("velox: WithFilter(nil)")
	}
	return engine.WithFilter(engine.EntityFilterFn(f))
}

// WithMaxLimit refuses, with ErrLimitTooLarge, a query whose Limit is over n.
// n must be at least 2, the limit One uses; a smaller n panics.
func WithMaxLimit(n int) Option { return engine.WithMaxLimit(n) }

// WithMaxRows fails, with ErrTooManyRows, a query that returns more than n
// rows: a relation load and Iter included, Count and an insert's RETURNING
// not. velox sends such a query with LIMIT n+1 (or the query's own Limit,
// when that is lower), so the database stops early: the extra row is the one
// that trips the cap. The cap counts per statement, so a With load that is
// split into chunks to stay under the parameter limit is counted chunk by
// chunk. A query's MaxRows overrides it, for the query and its With loads; a
// load with Rel.LimitEach is bounded by its limit instead, and Rel.MaxRows
// gives a load a cap of its own.
// n <= 0 sets no cap; n must otherwise be at least 2, the rows One reads, and
// n = 1 panics.
func WithMaxRows(n int) Option { return engine.WithMaxRows(n) }

// WithAround adds an around hook. The first one given is the outermost. A nil
// hook panics.
func WithAround(a Around) Option { return engine.WithAround(engine.Around(a)) }

// Deadlines are the time limits velox gives statements and transactions.
type Deadlines = veloxtype.Deadlines

// WithDeadlines sets the DB's time limits. A statement's limit covers its
// Around hooks and, with Iter, the caller's loop body. A transaction's covers
// fn, every TxRetrySerializable attempt and the commit.
//
// A transaction's effective deadline is velox's or the one on the context
// given to Tx, whichever is earlier. It bounds every statement inside the
// transaction, nested Tx included, whatever context that statement is given,
// and a transaction whose context has ended by commit time is rolled back.
// The error matches ErrTimeout only when velox's deadline was the one that
// passed; the caller's matches context.DeadlineExceeded alone.
func WithDeadlines(d Deadlines) Option { return engine.WithDeadlines(d) }

// Rewrite is a hook that may change a statement before it is rendered, or
// refuse it by returning an error. Rewrite hooks run in the order given to
// Open. They run for the SQL(ctx) methods too, with the statement's DryRun
// set, so a hook must change nothing but the statement. They do not run for
// raw SQL (OpRawQuery and OpRawExec), which has nothing to rewrite; Around
// hooks see it.
//
// A hook that points st.Entity at another table changes the statement's own
// table only: the tables in an EXISTS that Has or HasWith adds keep their
// names. The target's own Read policy, a scope included, applies inside the
// EXISTS, but a hook that moves tables cannot reach it. With a table per
// scope value, avoid Has and HasWith, or load the relation with With, which goes
// through the hooks.
//
// For generated code and extensions; outside the 1.0 compatibility promise:
// the Statement a hook sees changes as velox does.
type Rewrite func(ctx context.Context, st *Statement) error

// Around is a hook that wraps a statement's execution. next runs the
// statement and reads all its rows, so the hook wraps everything from sending
// it to its last row; with Iter, that includes the body of the caller's loop,
// and Result.LoopBody says how much of next was the loop body.
// The context passed to next must stay valid until next returns: a hook may
// set a deadline and cancel it once next has returned. A write pins the
// session (WithSession) found in the context the hook passes to next.
//
// A hook must call next, and return its error, unless it returns an error of
// its own instead. A hook must not assign to *st after next returns: velox
// records in st where the statement ran, and the query's With loads read from
// there. It may call next again to retry after an error; running
// the statement again after Iter has yielded rows is an error. A retry is
// sent where the route stage now points: a read that fell over to the
// primary (Route.Fallback), on a connection error from its query or its rows,
// is retried there, not on the replica in back-off. The first Around given
// to Open is the outermost.
//
// With Iter the caller's loop body runs inside the hooks, so a hook that
// recovers a panic and returns normally makes Go report that a range function
// recovered a loop body panic: let panics through.
//
// For generated code and extensions; outside the 1.0 compatibility promise:
// the Statement a hook sees changes as velox does.
type Around func(ctx context.Context, st *Statement, next func(context.Context) error) error

// Tag is a string a context carries to every statement run with it.
type Tag = veloxtype.Tag

// WithTags returns ctx with the tags kv (key, value, key, value, …) added: a
// key already on ctx keeps its place and takes the new value; a new key is
// appended. A last key with no value takes the value "", as WithTags runs on a
// request's path, where a mistake should cost a tag and not the request; no
// strings return ctx unchanged.
func WithTags(ctx context.Context, kv ...string) context.Context { return engine.WithTags(ctx, kv...) }
