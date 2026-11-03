package velox

import (
	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/veloxtype"
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
	// ErrOutOfRange is returned by Table.Convert for a number that its
	// column's Go type cannot hold, such as 300 for an int8.
	ErrOutOfRange = engine.ErrOutOfRange
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
	// ErrDenied is returned by a statement when a policy refuses it. Every
	// *PolicyError matches it.
	ErrDenied = veloxtype.ErrDenied
	// ErrPolicyUpsert is returned by an upsert that updates, on MySQL, of an
	// entity whose policy has a Read or Write predicate, because MySQL cannot
	// apply the predicate to the update.
	ErrPolicyUpsert = engine.ErrPolicyUpsert
	// ErrImmutable is returned by an update, Save or upsert when its Update
	// list assigns an Immutable column, including one a hook adds; the
	// error names the Go field. An insert may set one.
	ErrImmutable = engine.ErrImmutable
	// ErrNoShard is returned, on a DB opened with OpenShards, by a statement or
	// Tx when its context names no shard (no WithShard, and
	// WithShardFor names none), names one the DB does not have, or when the
	// WithShardFor function failed. Nothing was sent.
	ErrNoShard = engine.ErrNoShard
	// ErrOutsideTx is returned by a statement, or a Tx, under the context a
	// transaction's fn got (or one derived from it), when its DB is neither
	// that transaction nor one of its savepoints and the transaction has not
	// ended: it was most likely meant for the transaction's DB. Nothing was
	// sent. WithoutTx lifts the check.
	ErrOutsideTx = engine.ErrOutsideTx
	// ErrTxControl is returned by raw SQL, in a transaction, that would end it:
	// COMMIT, END, ROLLBACK (ROLLBACK TO is allowed), ABORT, BEGIN, START, PREPARE
	// TRANSACTION, and on MySQL DDL, which commits. Nothing was sent. On SQLite it
	// is also the error, once the statement has run, of text that went on to end it.
	ErrTxControl = engine.ErrTxControl
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
type SchemaError = veloxtype.SchemaError

// ConstraintKind is the kind of constraint a statement violated.
type ConstraintKind = veloxtype.ConstraintKind

// The kinds of constraint.
const (
	ConstraintUnique     = veloxtype.ConstraintUnique     // a unique index or primary key
	ConstraintForeignKey = veloxtype.ConstraintForeignKey // a foreign key
	ConstraintNotNull    = veloxtype.ConstraintNotNull    // a NOT NULL column
	ConstraintCheck      = veloxtype.ConstraintCheck      // a CHECK constraint
)

// ConstraintError reports a violated constraint; find it with errors.As.
type ConstraintError = veloxtype.ConstraintError

// PolicyError is a statement a policy denied.
type PolicyError = veloxtype.PolicyError

// Deny returns an error for a policy function to deny a statement with: its
// text is reason, and it matches ErrDenied.
func Deny(reason string) error { return engine.Deny(reason) }

// ValidationError is a value a column's validators refused; nothing was sent.
type ValidationError = veloxtype.ValidationError
