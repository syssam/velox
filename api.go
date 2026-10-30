package velox

import (
	"context"
	"database/sql"
	"reflect"
	"time"

	"github.com/syssam/velox2/dbtype"
	"github.com/syssam/velox2/internal/engine"
)

// Aggregate is the function an aggregate read computes over its one column.
type Aggregate = dbtype.Aggregate

// The aggregate functions.
const (
	AggregateSum = dbtype.AggregateSum // SUM
	AggregateAvg = dbtype.AggregateAvg // AVG
	AggregateMin = dbtype.AggregateMin // MIN
	AggregateMax = dbtype.AggregateMax // MAX
)

// Caller is a statement's call site.
type Caller = dbtype.Caller

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
// Option is not valid, and Open, OpenConn and OpenShards panic on it.
type Option = engine.Option

// WithRewrite adds a rewrite hook. Rewrite hooks run in the order given. A
// nil hook panics.
func WithRewrite(r Rewrite) Option { return engine.WithRewrite(r) }

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
func WithAround(a Around) Option { return engine.WithAround(a) }

// Deadlines are the time limits velox gives statements and transactions.
type Deadlines = dbtype.Deadlines

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

// TxTimeout bounds the transaction to d, retries and commit included, in place
// of the DB's Tx default and within its MaxTx. An earlier deadline on the
// context still applies. TxTimeout(d) with d <= 0 is ignored. A nested Tx
// refuses it, as it refuses every option: a savepoint runs within the
// enclosing transaction's deadline.
func TxTimeout(d time.Duration) TxOption { return engine.TxTimeout(d) }

// ColumnDesc describes one column of a Desc. For generated code.
type ColumnDesc = dbtype.ColumnDesc

// Dialect is a SQL dialect.
type Dialect = dbtype.Dialect

// The dialects velox supports.
const (
	Postgres = dbtype.Postgres
	MySQL    = dbtype.MySQL
	SQLite   = dbtype.SQLite
)

// DriverCode returns the database's code for err, as text, when err or an
// error it wraps came from the database: PostgreSQL's SQLSTATE ("23505"),
// MySQL's error number ("1062"), or SQLite's extended result code
// ("2067"). It looks through wrapping and errors.Join, as errors.As does.
// When an errors.Join holds database errors of more than one driver, which
// is unusual, PostgreSQL's wins, then MySQL's, then SQLite's, whatever their
// order in the join; within one driver the first in errors.As's order wins.
//
// The code is for logs, traces and metrics, and for telling one database
// error from another, such as a serialization failure ("40001") from a
// deadlock ("40P01"). It names the database's internals, so it is not for a
// message an end user sees. velox reads the errors of pgx (natively or
// through database/sql), github.com/go-sql-driver/mysql and
// modernc.org/sqlite; another driver's error has no code here.
func DriverCode(err error) (string, bool) { return engine.DriverCode(err) }

// Errors velox returns. Each is returned wrapped, in an error whose text starts
// "velox: <Entity>: ", so a sentinel's own text is a bare phrase; test for them
// with errors.Is.
var (
	// ErrNotFound is returned by One and First when they find no row, and by
	// a Save's Exec when no row has the key and meets the Where conditions.
	ErrNotFound = engine.ErrNotFound
	// ErrNotSingular is returned by One when it finds more than one row.
	ErrNotSingular = engine.ErrNotSingular
	// ErrUnbounded is returned by a query that has no Limit and did not say
	// Unbounded, when it runs.
	ErrUnbounded = engine.ErrUnbounded
	// ErrNoWhere is returned by an update or delete that has no Where and did
	// not say All, when it runs.
	ErrNoWhere = engine.ErrNoWhere
	// ErrLimitTooLarge is returned by a query whose Limit is over the DB's
	// WithMaxLimit, when it runs.
	ErrLimitTooLarge = engine.ErrLimitTooLarge
	// ErrTooManyRows is returned by a query when it reads more rows than
	// WithMaxRows or the query's MaxRows allow.
	ErrTooManyRows = engine.ErrTooManyRows
	// ErrLocked is returned by a statement that could not take a lock without
	// waiting: a NoWait read that met a row another transaction holds, or a
	// PostgreSQL lock_timeout. It matches PostgreSQL's 55P03 (lock_not_available)
	// and MySQL's 3572; the driver's error is wrapped with it, and
	// TxRetrySerializable does not retry it. MySQL's lock wait timeout (1205,
	// innodb_lock_wait_timeout), the same situation on that dialect, is not
	// ErrLocked: TxRetrySerializable retries it, as a deadlock.
	ErrLocked = engine.ErrLocked
	// ErrTimeout is returned by a statement or transaction when a deadline
	// velox set (WithDeadlines, TxTimeout) passes. The error also matches
	// context.DeadlineExceeded; a deadline the caller set matches that alone.
	ErrTimeout = engine.ErrTimeout
	// ErrNoTenant is returned by a statement on a tenant-scoped entity when its
	// context has neither WithTenant nor WithAllTenants.
	ErrNoTenant = engine.ErrNoTenant
	// ErrTenantMismatch is returned by a statement when the context's tenant
	// cannot be converted to the tenant column's type, or an inserted row names
	// another tenant.
	ErrTenantMismatch = engine.ErrTenantMismatch
	// ErrTenantImmutable is returned by an update when it assigns another
	// tenant to the tenant column.
	ErrTenantImmutable = engine.ErrTenantImmutable
	// ErrTenantUpsert is returned by an upsert that updates, when the update
	// could change another tenant's row.
	ErrTenantUpsert = engine.ErrTenantUpsert
	// ErrDenied is returned by a statement when a policy refuses it. Every
	// *PolicyError matches it.
	ErrDenied = dbtype.ErrDenied
	// ErrPolicyUpsert is returned by an upsert that updates, on MySQL, of an
	// entity whose policy has a Read or Write predicate, because MySQL cannot
	// apply the predicate to the update.
	ErrPolicyUpsert = engine.ErrPolicyUpsert
	// ErrImmutable is returned by an update, Save or upsert when its Update
	// list assigns an Immutable column, including one a hook adds; the
	// error names the Go field. An insert may set one.
	ErrImmutable = engine.ErrImmutable
	// ErrNoShard is returned, on a DB opened with OpenShards, by a statement or
	// Tx when its context names no shard (neither WithShard nor a tenant;
	// WithAllTenants names none), names one the DB does not have, or when the
	// WithShardFor function failed. Nothing was sent.
	ErrNoShard = engine.ErrNoShard
	// ErrOutsideTx is returned by a statement, or a Tx, under the context a
	// transaction's fn got (or one derived from it), when its DB is neither
	// that transaction nor one of its savepoints and the transaction has not
	// ended: it was most likely meant for the transaction's DB. Nothing was
	// sent. WithoutTx lifts the check.
	ErrOutsideTx = engine.ErrOutsideTx
	// ErrDryRun is returned by a write, or a Tx, under a context IsDryRun
	// reports true for, such as the one SQL(ctx) gives a hook: Insert,
	// InsertAll, Update and Delete's Exec, Save, an upsert, DB.Exec (raw SQL)
	// and Tx. Nothing was sent. Reads run. SQL(ctx) itself returns the
	// statement it rendered, not this error, when it is a hook's write that
	// was refused.
	ErrDryRun = engine.ErrDryRun
)

// IsNotFound reports whether err is, or wraps, ErrNotFound. It walks err's
// tree as errors.Is does, but skips a nil error in it: a typed-nil error that a
// driver wraps does not panic. ent's IsNotFound is the precedent.
func IsNotFound(err error) bool { return engine.IsNotFound(err) }

// SchemaError reports a mistake in an entity's generated metadata.
type SchemaError = dbtype.SchemaError

// ConstraintKind is the kind of constraint a statement violated.
type ConstraintKind = dbtype.ConstraintKind

// The kinds of constraint.
const (
	ConstraintUnique     = dbtype.ConstraintUnique     // a unique index or primary key
	ConstraintForeignKey = dbtype.ConstraintForeignKey // a foreign key
	ConstraintNotNull    = dbtype.ConstraintNotNull    // a NOT NULL column
	ConstraintCheck      = dbtype.ConstraintCheck      // a CHECK constraint
)

// ConstraintError reports a violated constraint; find it with errors.As.
type ConstraintError = dbtype.ConstraintError

// BoundPolicy is an entity's Policy with E erased, made by BindPolicy for
// Desc.Policy. For generated code. Its fields are not exported: only BindPolicy
// makes one.
type BoundPolicy = engine.BoundPolicy

// BoundHooks is an entity's hooks with E erased, made by BindHooks for
// Desc.Hooks. For generated code. Its fields are not exported: only BindHooks
// makes one.
type BoundHooks = engine.BoundHooks

// EntityInfo is what velox knows about an entity, built once on its first use.
// It is the non-generic view that hooks see in a Statement, and the runtime
// view of the generated Desc it is built from.
//
// One EntityInfo is shared by every query on the entity, in every goroutine,
// so it must be treated as read-only, slices included. A hook that needs
// different metadata for a statement copies it and points Statement.Entity
// at the copy; it never changes the shared one.
//
// Its fields:
//
//	Name string // the Go type, e.g. "billing.Invoice"
//	Table string
//	Schema string // the schema holding Table; empty for the default
//	Columns []ColumnInfo
//	PrimaryKey []int // indexes into Columns
//	Tenant int // index into Columns; -1 when the entity has none
//	Indexes []IndexInfo
//
// Its methods:
//
//	Column (in *EntityInfo) Column(name string) (int, bool)
type EntityInfo = engine.EntityInfo

// ColumnInfo describes one column. It is the runtime view of the generated
// ColumnDesc it is built from.
//
// Its fields:
//
//	Field string // the Go field name
//	Name string // the column name
//	Type any // the field's Go type, a reflect.Type: read it with GoType
//	Nullable bool // the column holds NULL: the field is a pointer, or its ColumnDesc says Nullable
//	Default any
//	HasDefault bool
//	DefaultFunc func() any // nil when none
//	// UpdateDefault is the function whose result every update writes to the
//	// column; an insert does not (Default and DefaultFunc fill an insert's);
//	// nil when none.
//	UpdateDefault func() any
//	Immutable bool // an update may not assign the column
//	Sensitive bool // the column holds a secret (velox logs no values)
//	// RequiredEdge is ColumnDesc.RequiredEdge: the Required edge whose
//	// NULL-able key the column holds, which a write may not leave nil; "" when
//	// none.
//	RequiredEdge string
//	// Codec says the column's value is encoded: its ColumnDesc gave Encode
//	// and Decode, as velox-gen does for every json field. The package
//	// documentation's JSON fields section says how such a column binds and reads.
//	Codec bool
type ColumnInfo = engine.ColumnInfo

// GoType returns the Go type of column c's field. ColumnInfo.Type holds it as
// any, and this is a function rather than a method, so that no velox type
// reaches reflect.Type: every package that reaches it compiles about 134 KiB of
// wrappers for reflect's iterator types.
func GoType(c ColumnInfo) reflect.Type { return engine.GoType(c) }

// IndexInfo describes one index.
type IndexInfo = dbtype.IndexInfo

// Predicate is a condition in a Statement: the form hooks see and add. It is
// the untyped form of Pred, as OrderTerm is of Order.
type Predicate = engine.Predicate

// OrderTerm is an ordering by one column of a Statement's entity.
type OrderTerm = dbtype.OrderTerm

// ColumnEQ returns the condition column = v, for hooks that add conditions;
// column is an index into the statement's EntityInfo.Columns.
func ColumnEQ(column int, v any) Predicate { return engine.ColumnEQ(column, v) }

// EdgeDesc is one relation field of a generated Desc: its name and the columns
// that key it. For generated code. EdgeTo, EdgeBidi and EdgeThrough make it. It carries
// no target entity: the use site supplies that (see EdgeTo).
type EdgeDesc = engine.EdgeDesc

// Conn is a connection pool that velox can run statements on: a DB's primary
// or one of its replicas. Make one with SQLConn or a driver package's Conn, such
// as veloxpgx.Conn; the zero Conn is none, and a DB given one panics.
type Conn = engine.Conn

// SQLConn returns a Conn that runs statements through db, a *sql.DB the
// application opened with a driver for the DB's dialect. A nil db panics.
func SQLConn(db *sql.DB) Conn { return engine.SQLConn(db) }

// WithReplicas adds read replicas to a DB's primary, the connection Open or
// OpenConn was given. A read outside a transaction goes to a replica, each in
// turn, unless its context is marked with WithPrimaryReads; a query's With
// loads go to the replica the query read. Writes, and every statement in a
// transaction, go to the primary. Statement.Route records the choice.
//
// Give each replica's pool a connect timeout shorter than your request
// deadlines: pgx's connect_timeout, a MySQL DSN's timeout=. Neither driver
// sets one, so a replica that drops packets stalls the dial until the
// operating system gives up. A dial that times out falls over to the primary
// and puts the replica in back-off; a dial still hanging when the request's
// context ends is the context's error, and neither retries nor backs off.
//
// A zero Conn panics, and a Conn valid only on another dialect
// panics in Open.
func WithReplicas(conns ...Conn) Option { return engine.WithReplicas(conns...) }

// Shard is one shard of a DB opened with OpenShards: a primary and its read
// replicas, among which the shard's statements are routed as WithReplicas
// routes a DB's. As there, each replica's pool needs a connect timeout
// shorter than the request deadlines (pgx's connect_timeout, a MySQL DSN's
// timeout=): a dial that times out falls over to the shard's primary, but a
// dial still hanging when the request's context ends is the context's error,
// and neither retries nor backs the replica off.
//
// Its fields:
//
//	Primary Conn
//	Replicas []Conn
type Shard = engine.Shard

// WithShardFor sets the function that gives the name of a statement's shard,
// on a DB opened with OpenShards, from its context's tenant: the id given to
// WithTenant. It is called, with the statement's context, for each statement
// and Tx whose context has a tenant and no WithShard, so it should be fast; a
// SQL(ctx) dry run calls it too. Its error refuses the statement, which then
// matches both ErrNoShard and the error.
//
// One write can call it more than once: an InsertAll split into two
// statements calls it three times, for its transaction and for each
// statement, and a Tx calls it when it begins and for each statement in it.
//
// f is called concurrently, from the goroutine of every statement, so it must
// be safe for that, and fast: a map lookup or a hash, not a query.
//
// It is required with OpenShards; given to Open or OpenConn, it panics there.
// A nil f panics. WithShard names a statement's shard from its context instead.
func WithShardFor(f func(ctx context.Context, tenant any) (string, error)) Option {
	return engine.WithShardFor(f)
}

// WithShard returns a context whose statements, on a DB opened with
// OpenShards, run on the shard named name, whatever the context's tenant:
// for admin jobs and for data that belongs to no tenant. A later WithShard
// replaces it. On a DB opened otherwise it is ignored. Without it, the shard
// is the one WithShardFor gives for the context's tenant.
func WithShard(ctx context.Context, name string) context.Context { return engine.WithShard(ctx, name) }

// WithPrimaryReads returns a context whose reads go to the primary, on a DB
// with replicas: for a read that must see a write the replicas may not have
// yet.
func WithPrimaryReads(ctx context.Context) context.Context { return engine.WithPrimaryReads(ctx) }

// WithSession returns a context carrying a new session, typically one
// request's: after a write made through the context (or a context derived
// from it), the session's reads go to the primary for the DB's pin window
// (WithPinWindow), so that the session reads its own writes from a replica
// that may not have them yet. A write pins it once it has reached the
// database, even when it then fails: its outcome is unknown. A transaction
// that wrote counts as a write once it sent its COMMIT, at the commit, even
// when the COMMIT then failed; one that rolled back does not. Without a
// session in the context, nothing is pinned. A session is safe for
// concurrent use and lives only in its context; a context derived from a
// session's keeps it, and WithSession on one starts a new one.
func WithSession(ctx context.Context) context.Context { return engine.WithSession(ctx) }

// PinUntil pins ctx's session to the primary until t, if that is later than
// its pin now: an application that stored the time of a user's last write
// restarts the pin on the user's next request with it (PinnedUntil gives the
// time to store). It never lowers a pin, and does nothing on a context
// without a session. A t after 2262, past what PinnedUntil can return, pins
// until then: until the end of the session, in effect.
func PinUntil(ctx context.Context, t time.Time) { engine.PinUntil(ctx, t) }

// PinnedUntil returns the time until which ctx's session reads the primary:
// the latest of its writes' pin windows and PinUntil's times. It is zero on a
// context without a session, and on a session that has not been pinned.
func PinnedUntil(ctx context.Context) time.Time { return engine.PinnedUntil(ctx) }

// WithPinWindow sets how long after a write its session's reads go to the
// primary; the default is 2 seconds, sized to typical replica lag, so a DB
// whose replicas lag more needs a larger window. A negative d panics.
func WithPinWindow(d time.Duration) Option { return engine.WithPinWindow(d) }

// WithReplicaBackoff sets how long a replica is skipped after a read sent to
// it failed with a connection error; the default is 5 seconds. The read
// itself is retried once on the primary, with Statement.Route's Fallback set.
// A read whose context has ended is neither retried nor backs the replica
// off; see WithReplicas for the connect timeout that keeps a hung dial from
// ending that way.
// A negative d panics.
func WithReplicaBackoff(d time.Duration) Option { return engine.WithReplicaBackoff(d) }

// Route is where a statement runs.
type Route = dbtype.Route

// WithTenant returns ctx scoped to tenant id: statements on a tenant-scoped
// entity read and change only that tenant's rows, and inserts fill or check
// the tenant column. id is converted to the tenant column's Go type when it
// is of a predeclared integer type (an untyped constant is an int) and the
// column's type, predeclared or named such as an OrgID int64, is an integer
// type whose range holds it; or when it is a string and the column's kind is
// string. An id of a named type is never converted: it must be the column's
// type, so a UserID never serves an OrgID column. Any other id fails the
// statement with ErrTenantMismatch. A nil id, or the type's zero value (what
// parsing a missing claim gives), scopes nothing: such statements fail with
// ErrNoTenant. It replaces an earlier WithTenant or WithAllTenants on ctx.
func WithTenant(ctx context.Context, id any) context.Context { return engine.WithTenant(ctx, id) }

// WithAllTenants returns ctx unscoped: statements on a tenant-scoped entity
// reach every tenant's rows. The reason is required and is shown to hooks as
// Statement.TenantBypass; with a reason that is empty or only white space
// WithAllTenants returns ctx unchanged, so a statement without a tenant still
// fails. It replaces an earlier WithTenant or WithAllTenants on ctx.
func WithAllTenants(ctx context.Context, reason string) context.Context {
	return engine.WithAllTenants(ctx, reason)
}

// WithoutPolicy returns ctx with every entity's policy switched off. The reason
// is required and is shown to hooks as Statement.PolicyBypass; with a reason
// that is empty or only white space WithoutPolicy returns ctx unchanged. It
// does not lift tenant scoping: see WithAllTenants.
func WithoutPolicy(ctx context.Context, reason string) context.Context {
	return engine.WithoutPolicy(ctx, reason)
}

// RowScope is what a context scopes statements by.
type RowScope = dbtype.RowScope

// RowScopeFromContext returns ctx's row scope.
func RowScopeFromContext(ctx context.Context) RowScope { return engine.RowScopeFromContext(ctx) }

// IsDryRun reports whether ctx is that of a SQL(ctx) method: the statement
// being built will not be sent. The SQL(ctx) methods mark the context before
// anything runs, so everything handed that context sees it: the entity's
// hooks (Hooks), the Policy functions and the Rewrite hooks. A Rewrite hook
// can also read it from Statement.DryRun.
//
// A write made under such a context is refused: Exec of an Insert, InsertAll,
// Update or Delete, Save, an upsert, DB.Exec (raw SQL) and Tx fail with
// ErrDryRun before anything is sent, on any *DB, so a hook's audit write through
// Mutation.DB does not happen and needs no check. Reads run as normal.
func IsDryRun(ctx context.Context) bool { return engine.IsDryRun(ctx) }

// PolicyError is a statement a policy denied.
type PolicyError = dbtype.PolicyError

// Deny returns an error for a policy function to deny a statement with: its
// text is reason, and it matches ErrDenied.
func Deny(reason string) error { return engine.Deny(reason) }

// Op is the kind of a Statement.
type Op = dbtype.Op

// The kinds of statement.
const (
	OpSelect = dbtype.OpSelect
	OpInsert = dbtype.OpInsert
	OpUpdate = dbtype.OpUpdate
	OpDelete = dbtype.OpDelete
	// OpRawQuery is raw SQL run by DB.Raw, which velox treats as a read: its
	// SQL and Args are the caller's.
	OpRawQuery = dbtype.OpRawQuery
	// OpRawExec is raw SQL run by DB.Exec, which velox treats as a write: its
	// SQL and Args are the caller's.
	OpRawExec = dbtype.OpRawExec
)

// Lock is the lock a locking read takes on the rows it reads.
type Lock = dbtype.Lock

// The locks.
const (
	LockForUpdate = dbtype.LockForUpdate // FOR UPDATE
	LockForShare  = dbtype.LockForShare  // FOR SHARE
)

// LockWait is what a locking read does about rows another transaction has
// locked.
type LockWait = dbtype.LockWait

// The ways of not waiting.
const (
	LockSkipLocked = dbtype.LockSkipLocked // SKIP LOCKED: leave them out
	LockNoWait     = dbtype.LockNoWait     // NOWAIT: fail at once
)

// AssignOp is how an UPDATE assigns a column.
type AssignOp = dbtype.AssignOp

// The assignment operators.
const (
	AssignSet = dbtype.AssignSet // column = value
	AssignAdd = dbtype.AssignAdd // column = column + value
	AssignSub = dbtype.AssignSub // column = column - value
)

// Assignment is one column an UPDATE sets.
type Assignment = dbtype.Assignment

// ConflictClause is an INSERT's upsert clause.
//
// Its fields:
//
//	Columns []int // the unique key whose conflict the insert resolves
//	Update []int // columns overwritten with the inserted row's values
//	DoNothing bool // keep the existing row; also what an empty Update means
//	// Where is the condition of the update branch, on the existing row: a
//	// conflicting row it rejects is neither updated nor replaced. PostgreSQL
//	// and SQLite render it; velox refuses it on MySQL.
//	Where []Predicate
type ConflictClause = engine.ConflictClause

// EachClause makes a SELECT read, or count, the rows of each owner apart: the
// rows whose owner column holds one of Keys. velox sets it for a With load
// with Rel.LimitEach, for LoadEach and for CountEach; a Rewrite hook may not
// change it. With Count, the statement reads each owner's key and its count,
// one row per owner that has rows.
//
// Its fields:
//
//	Keys []any // the owners' keys, distinct
//	Column int // without Join: the column of Entity that holds an owner's key
//	// Join is the join entity of a many-to-many relation; nil otherwise. The
//	// statement joins it on Join.Columns[JoinTarget] = Entity.Columns[Target]
//	// and partitions by Join.Columns[JoinOwner].
//	Join *EntityInfo
//	JoinOwner, JoinTarget int
//	Target int
//	// Limit is the most rows per owner, in the statement's OrderBy; 0 is no
//	// limit, and is always 0 on a Count.
//	Limit int
type EachClause = engine.EachClause

// Statement is one operation on its way to the database. Every query becomes
// one, and the pipeline's hooks see and may change it: rewrite hooks before it
// is rendered, around hooks while it runs.
//
// Raw SQL (OpRawQuery and OpRawExec) is not rendered: Rewrite hooks do not see
// it, and its SQL, Args and Fingerprint are set when it is made. Its Entity is
// the entity Raw scans into, or for Exec an EntityInfo named "Exec" with no
// table or columns. Its TenantBypass and PolicyBypass both hold the caller's
// reason, as given, untrimmed (so does a WithAllTenants or WithoutPolicy
// reason).
//
// The EntityInfo that Entity points to is shared by every query on the
// entity, in every goroutine: treat it as read-only. A rewrite hook may point
// Entity at an EntityInfo of its own (a copy with another Table, say), but
// must never change the shared one. Pointing it at another table does not
// change the tables inside an EXISTS (see Rewrite).
//
// Its fields:
//
//	Op Op
//	Entity *EntityInfo // shared and read-only: replace it, never edit it
//	Count bool // SELECT COUNT(*): Count clears Columns, OrderBy, Limit and Offset; a Rewrite hook may not change it, or set Columns, and one that sets the others gets them rendered
//	Exists bool // SELECT 1 … LIMIT 1: Exists clears Columns, OrderBy and Offset and sets Limit to 1; a Rewrite hook may not change it, or set Columns
//	Aggregate Aggregate // SELECT SUM(c) …: Columns holds c alone; like Count, it clears OrderBy, Limit and Offset; a Rewrite hook may not change it, or Columns
//	Each *EachClause // SELECT: a relation read or counted per owner; a Rewrite hook may not change it
//	Lock Lock // SELECT: ForUpdate or ForShare; 0 for a read that locks nothing; a Rewrite hook may not change it
//	LockWait LockWait // SELECT: SkipLocked or NoWait; a Rewrite hook may not change it
//	Columns []int // SELECT: the selected columns, nil selects all; INSERT: the columns written
//	Where []Predicate // all must hold
//	OrderBy []OrderTerm
//	Limit int // 0: no limit; after the Rewrite hooks, velox lowers it to the row cap + 1
//	Offset int
//	Unbounded bool // the query said Unbounded
//	Writable bool // SELECT: the query said Writable; the scope adds the Write policy, and a Rewrite hook may not change it
//	MaxRows int // SELECT and OpRawQuery: the most rows it may return; 0: the DB's WithMaxRows, or, with Each, its bound by construction (Rel.MaxRows sets it)
//	Set []Assignment // UPDATE: the assignments
//	Rows [][]any // INSERT: the rows, one value per Columns entry
//	Conflict *ConflictClause // INSERT: the upsert clause, if any
//	Returning []int // INSERT: columns read back (PostgreSQL and SQLite)
//	AllRows bool // UPDATE and DELETE: the statement said All
//	Tenant any // the tenant scope applied, as the type of the entity's tenant column; nil when unscoped; when the entity is unscoped but the statement reads a scoped entity, the id as WithTenant was given it
//	TenantBypass string // the WithAllTenants reason, when it applied; raw SQL's reason
//	PolicyBypass string // the WithoutPolicy reason, when it applied; raw SQL's reason
//	DryRun bool // built for SQL(ctx), not sent
//	InTx bool // set by velox: the statement runs in a transaction, inside db.Tx or one velox opens for it (a split InsertAll, a policy-conditioned upsert, an EdgeBidi write); a Rewrite hook may not change it
//	Tx *TxInfo // the transaction the statement runs in, when the DB has hooks; nil otherwise
//	Dialect Dialect // the DB's: an Around hook has only the statement
//	// Tags are the context's WithTags tags, set before the hooks run. A
//	// Rewrite hook gets its own copy; it may change them, and the SQL
//	// comment is rendered from what it leaves.
//	Tags []Tag
//	// Caller is the statement's call site, with WithCallers; zero
//	// otherwise. A Rewrite hook may change it, as Tags.
//	Caller Caller
//	SQL string // set by rendering, after the rewrite hooks: the text sent, WithSQLComment's comment included
//	Fingerprint string // set by rendering: the shape's ID, the hash of SQL without the comment
//	Args []any // set by rendering
//	// Route is where the statement runs: set by the route stage, after
//	// rendering. Rewrite hooks run before it and see it unset (zero, not
//	// Replica -1); Around hooks, and everything after them, see it set. An
//	// Around hook reads it, with one exception: setting Route.Fallback to true
//	// sends the hook's retry to the primary. Changing WithPrimaryReads,
//	// WithShard or WithSession in the context passed to next does not re-route
//	// the statement, as routing happens before the Around hooks. A fallback
//	// keeps Route.Fallback set for the hook's later calls to next; a hook
//	// that counts failovers compares it with its value before next.
//	Route Route
//	// Result is reset before the Around hooks run, and set by each
//	// execution before next returns to them; a hook that retries sees the
//	// last attempt's.
//	Result Result
type Statement = engine.Statement

// Result is what executing a statement did, which an Around hook reads.
type Result = dbtype.Result

// Rewrite is a hook that may change a statement before it is rendered, or
// refuse it by returning an error. Rewrite hooks run in the order given to
// Open. They run for the SQL(ctx) methods too, with the statement's DryRun
// set, so a hook must change nothing but the statement. They do not run for
// raw SQL (OpRawQuery and OpRawExec), which has nothing to rewrite; Around
// hooks see it.
//
// A hook that points st.Entity at another table changes the statement's own
// table only: the tables in an EXISTS that Has or HasWhere adds keep their
// names. The target's own tenant column and Read policy apply inside the
// EXISTS, but a hook that moves tables cannot reach it. With a table per
// tenant, avoid Has and HasWhere, or load the relation with With, which goes
// through the hooks.
type Rewrite = engine.Rewrite

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
type Around = engine.Around

// Tag is a string a context carries to every statement run with it.
type Tag = dbtype.Tag

// WithTags returns ctx with the tags kv (key, value, key, value, …) added: a
// key already on ctx keeps its place and takes the new value; a new key is
// appended. A last key with no value takes the value "", as WithTags runs on a
// request's path, where a mistake should cost a tag and not the request; no
// strings return ctx unchanged.
func WithTags(ctx context.Context, kv ...string) context.Context { return engine.WithTags(ctx, kv...) }

// TxOption configures one call of DB.Tx: TxIsolation, TxReadOnly,
// TxRetrySerializable and TxTimeout. The zero TxOption is not valid: TxRetries
// panics on it, and so does DB.Tx when it begins the transaction.
type TxOption = engine.TxOption

// TxIsolation sets the transaction's isolation level; the default is the
// database's.
func TxIsolation(level sql.IsolationLevel) TxOption { return engine.TxIsolation(level) }

// TxReadOnly starts a read-only transaction.
func TxReadOnly() TxOption { return engine.TxReadOnly() }

// TxRetrySerializable runs the transaction again, up to n more times, when it
// fails on a serialization failure or a deadlock: PostgreSQL 40001 and 40P01,
// MySQL 1213 and 1205, SQLite BUSY and LOCKED. fn must be safe to run again.
func TxRetrySerializable(n int) TxOption { return engine.TxRetrySerializable(n) }

// TxRetries returns the n of the last TxRetrySerializable in opts, or 0 when
// there is none. It reads options and is not one. It is for code that runs a
// transaction's fn on an application's behalf and cannot run it again.
func TxRetries(opts ...TxOption) int { return engine.TxRetries(opts...) }

// WithoutTx returns ctx without the mark that Tx puts on fn's ctx, keeping
// everything else: its values (the tenant, tags, spans), its deadline and its
// cancellation. It is for work inside fn that must not join the transaction,
// such as an audit row that must survive a rollback: under it, a statement on
// db runs outside the transaction, on its own connection, and a Tx on db
// begins a transaction of its own. A statement on tx under it still runs in
// the transaction. On a context without the mark it returns ctx.
//
// A statement on db under it needs a second connection while the
// transaction holds one: on a pool of one connection, as SQLite often has,
// it waits until the transaction ends or its deadline passes.
func WithoutTx(ctx context.Context) context.Context { return engine.WithoutTx(ctx) }

// TxAround is a hook that wraps a transaction.
type TxAround = dbtype.TxAround

// TxInfo describes a transaction to the hooks.
type TxInfo = dbtype.TxInfo

// TxRetry is an attempt that TxRetrySerializable retried.
type TxRetry = dbtype.TxRetry

// TxOutcome is how a transaction's attempt ended.
type TxOutcome = dbtype.TxOutcome

// The outcomes of an attempt.
const (
	TxCommit   = dbtype.TxCommit   // fn returned nil and the commit succeeded
	TxRollback = dbtype.TxRollback // fn returned an error, or panicked
	TxError    = dbtype.TxError    // begin or commit failed, or the transaction's deadline passed
)

// WithTxAround adds a transaction hook. The first one given is the
// outermost. A nil hook panics.
func WithTxAround(h TxAround) Option { return engine.WithTxAround(h) }

// ValidationError is a value a column's validators refused; nothing was sent.
type ValidationError = dbtype.ValidationError

// Pred is a condition on entity E, made by a column handle or by And, Or and
// Not. It is the typed form of Predicate, as Order is of OrderTerm.
type Pred[E any] = engine.Pred[E]
