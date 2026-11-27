package velox

import (
	"github.com/syssam/velox/internal/dberr"
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
// through database/sql), github.com/go-sql-driver/mysql, modernc.org/sqlite
// and github.com/mattn/go-sqlite3; another driver's error has no code here.
func DriverCode(err error) (string, bool) { return dberr.DriverCode(err) }

// Errors velox returns. Each is returned wrapped, in an error whose text starts
// "velox: <Entity>: ", where <Entity> is the Go type with its package name
// (velox: veloxgen.Invoice: ), so a sentinel's own text is a bare phrase; test for them
// with errors.Is.
var (
	// ErrNotFound is returned by One and First when they find no row, and by
	// a Save's Exec when no row has the key and meets the Where conditions.
	ErrNotFound = engine.ErrNotFound
	// ErrNotSingular is returned by One when it finds more than one row.
	ErrNotSingular = engine.ErrNotSingular
	// ErrLinked is returned, as the Err of an *EdgeError, by an edge setter's
	// Add whose child, one the caller may read, has another owner, or whose
	// owner holds a one-to-one child the caller may not change.
	ErrLinked = engine.ErrLinked
	// ErrUnbounded is returned by a query that has no Limit and did not say
	// AllowUnbounded, when it runs.
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
	// TxRetryRolledBack does not retry it. MySQL's lock wait timeout (1205,
	// innodb_lock_wait_timeout), the same situation on that dialect, is not
	// ErrLocked: TxRetryRolledBack retries it, as a deadlock.
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
	// ErrStale is returned by a Save of an entity with a Version column when
	// no row has the key and the version *x holds: another write moved the
	// version, or deleted the row, since *x was read. The error also matches
	// ErrNotFound.
	ErrStale = engine.ErrStale
	// ErrVersionSet is returned by an update, Save or upsert that assigns an
	// entity's Version column, a hook's included: velox adds 1 to it on every
	// update.
	ErrVersionSet = engine.ErrVersionSet
	// ErrNoShard is returned, on a DB opened with OpenShards, by a statement or
	// Tx when its context names no shard (no WithShard, and
	// WithShardFor names none), names one the DB does not have, or when the
	// WithShardFor function failed. Nothing was sent.
	ErrNoShard = engine.ErrNoShard
	// ErrOutsideTx is returned by a statement, or a Tx, under the context a
	// transaction's fn got (or one derived from it), when its DB is neither
	// that transaction, one of its savepoints, nor the DB that began it, which
	// joins it, and the transaction has not ended: it was most likely meant
	// for the transaction's DB. Nothing was sent. WithoutTx lifts the check.
	ErrOutsideTx = engine.ErrOutsideTx
	// ErrNoTx is returned by AfterCommit, BeforeCommit and AfterRollback, and
	// by a locking read, when the DB it was given is not a running
	// transaction's: the pool DB, even under a transaction's context, whose
	// statements join it (TxFrom finds the handle that does); a transaction's
	// DB once it ended, committed or rolled back; a savepoint's DB once it
	// returned. It does not say the work is committed. A UseTx DB is
	// ErrAdoptedTx, not this.
	ErrNoTx = engine.ErrNoTx
	// ErrAdoptedTx is returned by AfterCommit, BeforeCommit and
	// AfterRollback on a UseTx DB: its commit is the caller's, and velox
	// never sees it end, so nothing could be run after it.
	ErrAdoptedTx = engine.ErrAdoptedTx
	// ErrTxEnded is returned by context.Cause for the context TxCancelOnEnd
	// cancels once its transaction or savepoint has ended; the context's own
	// error is context.Canceled.
	ErrTxEnded = engine.ErrTxEnded
	// ErrCommitUnknown is returned, matched by errors.Is beside the driver's
	// error, by a Tx whose COMMIT failed when the error does not say the
	// transaction rolled back: a lost connection,
	// a driver error, or a context that ended while COMMIT was sent. The
	// database may have committed. TxRetryRolledBack does not run fn again
	// after it, and a caller that retries on its own should not either: it
	// reconciles, as for a lost acknowledgement. An error that says the
	// transaction rolled back does not match: a deferred constraint (a
	// *ConstraintError), a serialization failure or a deadlock,
	// PostgreSQL's ROLLBACK answer to the COMMIT of an aborted transaction
	// (pgx's ErrTxCommitRollback), MySQL's 3101, and SQLite's SQLITE_BUSY
	// or SQLITE_LOCKED. A MySQL lock wait timeout at COMMIT matches. The
	// error's message and the driver's error in it are unchanged.
	ErrCommitUnknown = engine.ErrCommitUnknown
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

// IsNotSingular reports whether err is, or wraps, ErrNotSingular: a One that
// found more than one row. It walks err's tree as IsNotFound does, a typed
// nil skipped.
func IsNotSingular(err error) bool { return dberr.Is(err, ErrNotSingular) }

// IsConstraintError reports whether err is, or wraps, a violated unique,
// foreign key, CHECK or NOT NULL constraint: a *ConstraintError, which velox
// makes of the driver's error, or a driver's error velox reads as one on
// PostgreSQL, MySQL or SQLite. It walks err's tree as errors.As does, a
// typed nil skipped. ent's IsConstraintError is the precedent.
func IsConstraintError(err error) bool {
	_, ok := AsConstraintError(err)
	return ok
}

// AsConstraintError returns the *ConstraintError in err's tree, or the one
// velox reads from a driver's error on PostgreSQL, MySQL or SQLite, as
// IsConstraintError does: it reports true exactly when that does. It is for a
// raw SQL or Pool path, where errors.As alone finds nothing, and the Kind and
// Constraint tell which constraint fired. Constraint is the index or
// constraint name on PostgreSQL and MySQL; on SQLite it holds what the
// driver's text names, as that names no index: "table.col, table.col" for a
// unique or primary key, the column for NOT NULL, the expression of a CHECK,
// and nothing for a foreign key. The Entity is empty for an error velox did
// not make.
func AsConstraintError(err error) (*ConstraintError, bool) {
	if ce, ok := dberr.As[*ConstraintError](err); ok {
		return ce, true
	}
	ce := dberr.Constraint("", err)
	return ce, ce != nil
}

// IsValidationError reports whether err is, or wraps, a *ValidationError: a
// value a column's validators refused, or a missing required edge, before
// anything was sent. It walks err's tree as errors.As does, a typed nil
// skipped. ent's IsValidationError is the precedent.
func IsValidationError(err error) bool {
	_, ok := dberr.As[*ValidationError](err)
	return ok
}

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

// EdgeError is an edge setter's write that failed on its keys; find it with
// errors.As. Its Err wraps ErrNotFound or ErrLinked.
type EdgeError = veloxtype.EdgeError
