package engine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/syssam/velox/internal/dberr"
)

// Errors velox returns. Each is returned wrapped, in an error whose text starts
// "velox: <Entity>: ", so a sentinel's own text is a bare phrase; test for them
// with errors.Is.
var (
	// ErrNotFound is returned by One and First when they find no row, and by
	// a Save's Exec when no row has the key and meets the Where conditions.
	ErrNotFound = errors.New("not found")
	// ErrNotSingular is returned by One when it finds more than one row.
	ErrNotSingular = errors.New("more than one row")
	// ErrLinked is returned, as the Err of an EdgeError, by an edge setter's
	// Add whose child, one the caller may read, has another owner, or whose
	// owner holds a one-to-one child the caller may not change.
	ErrLinked = errors.New("already linked to another owner")
	// ErrUnbounded is returned by a query that has no Limit and did not say
	// AllowUnbounded, when it runs.
	ErrUnbounded = errors.New("query has no Limit")
	// ErrNoWhere is returned by an update or delete that has no Where and did
	// not say All, when it runs.
	ErrNoWhere = errors.New("update or delete has no Where")
	// ErrLimitTooLarge is returned by a query whose Limit is over the DB's
	// WithMaxLimit, when it runs.
	ErrLimitTooLarge = errors.New("limit is over the maximum")
	// ErrTooManyRows is returned by a query when it reads more rows than
	// WithMaxRows or the query's MaxRows allow.
	ErrTooManyRows = errors.New("read more rows than the cap")
	// ErrOutOfRange is returned by Table.Convert for a number that its
	// column's Go type cannot hold.
	ErrOutOfRange = errors.New("value out of the column's range")
	// ErrLocked is returned by a statement that could not take a lock without
	// waiting: a NoWait read that met a row another transaction holds, or a
	// PostgreSQL lock_timeout. It matches PostgreSQL's 55P03 (lock_not_available)
	// and MySQL's 3572; the driver's error is wrapped with it, and
	// TxRetrySerializable does not retry it. MySQL's lock wait timeout (1205,
	// innodb_lock_wait_timeout), the same situation on that dialect, is not
	// ErrLocked: TxRetrySerializable retries it, as a deadlock.
	ErrLocked = errors.New("lock not available")
	// ErrTimeout is returned by a statement or transaction when a deadline
	// velox set (WithDeadlines, TxTimeout) passes. The error also matches
	// context.DeadlineExceeded; a deadline the caller set matches that alone.
	ErrTimeout = errors.New("deadline set by velox passed")
	// ErrPolicyUpsert is returned by an upsert that updates, on MySQL, of an
	// entity whose policy has a Read or Write predicate, because MySQL cannot
	// apply the predicate to the update.
	ErrPolicyUpsert = errors.New("upsert cannot apply the policy")
	// ErrImmutable is returned by an update, Save or upsert when its Update
	// list assigns an Immutable column, including one a hook adds; the
	// error names the Go field. An insert may set one.
	ErrImmutable = errors.New("column is immutable")
	// ErrStale is returned by a Save of an entity with a Version column when
	// no row has the key and the version *x holds: another write moved the
	// version, or deleted the row, since *x was read. The error also matches
	// ErrNotFound. A write that assigns the Version column fails with
	// ErrVersionSet.
	ErrStale = errors.New("stale: the row's version moved since it was read")
	// ErrVersionSet is returned by a write that assigns an entity's Version
	// column, a hook's included: velox adds 1 to it on every update.
	ErrVersionSet = errors.New("the version column is velox's to set")
	// ErrNoShard is returned, on a DB opened with OpenShards, by a statement or
	// Tx when its context names no shard (neither WithShard nor WithShardFor
	// does), names one the DB does not have, or when the
	// WithShardFor function failed. Nothing was sent.
	ErrNoShard = errors.New("no shard for the statement")
	// ErrOutsideTx is returned by a statement, or a Tx, under the context a
	// transaction's fn got (or one derived from it), when its DB is neither
	// that transaction nor one of its savepoints and the transaction has not
	// ended: it was most likely meant for the transaction's DB. Nothing was
	// sent. WithoutTx lifts the check.
	ErrOutsideTx = errors.New("statement outside its transaction")
	// ErrCommitUnknown is returned, matched by errors.Is beside the driver's
	// error, by a Tx whose COMMIT failed when the error does not say the
	// transaction rolled back: a lost connection,
	// a driver error, or a context that ended while COMMIT was sent. The
	// database may have committed. TxRetrySerializable does not run fn again
	// after it, and a caller that retries on its own should not either: it
	// reconciles, as for a lost acknowledgement. An error that says the
	// transaction rolled back does not match (dberr.RolledBack): a deferred
	// constraint (a *ConstraintError), a serialization failure or a
	// deadlock, PostgreSQL's ROLLBACK answer to the COMMIT of an aborted
	// transaction (pgx's ErrTxCommitRollback), MySQL's 3101, and SQLite's
	// SQLITE_BUSY or SQLITE_LOCKED. A MySQL lock wait timeout at COMMIT
	// matches. The error's message and the driver's error in it are
	// unchanged.
	ErrCommitUnknown = errors.New("the commit's outcome is unknown")
	// ErrTxControl is returned by raw SQL, in a transaction, that would end it:
	// COMMIT, END, ROLLBACK (ROLLBACK TO is allowed), ABORT, BEGIN, START, PREPARE
	// TRANSACTION, and on MySQL DDL, which commits. Nothing was sent. On SQLite it
	// is also the error, once the statement has run, of text that went on to end it.
	ErrTxControl = errors.New("raw SQL would end the transaction")
	// ErrDryRun is returned by a write, or a Tx, under a context IsDryRun
	// reports true for, such as the one SQL(ctx) gives a hook: Insert,
	// InsertAll, Update and Delete's Exec, Save, an upsert, DB.Exec (raw SQL)
	// and Tx. Nothing was sent. Reads run. SQL(ctx) itself returns the
	// statement it rendered, not this error, when it is a hook's write that
	// was refused.
	ErrDryRun = errors.New("write in a dry run")
)

// IsNotFound reports whether err is, or wraps, ErrNotFound. It walks err's
// tree as errors.Is does, but skips a nil error in it: a typed-nil error that a
// driver wraps does not panic. ent's IsNotFound is the precedent.
func IsNotFound(err error) bool { return dberr.Is(err, ErrNotFound) }

// stageError is the error of a hook stage: "velox: <Entity>: <stage>: <err>",
// stage "hook" or "rewrite"; an err whose text starts "velox: <Entity>: " does
// not name the entity twice.
type stageError struct {
	entity, stage string
	err           error
}

func (e *stageError) Error() string {
	msg := e.err.Error()
	if p := "velox: " + e.entity + ": "; strings.HasPrefix(msg, p) {
		msg = msg[len(p):]
	}
	return "velox: " + e.entity + ": " + e.stage + ": " + msg
}

func (e *stageError) Unwrap() error { return e.err }

// newStageError returns the error of a stage of entity's statement.
func newStageError(entity, stage string, err error) *stageError {
	return &stageError{entity, stage, err}
}

// dbErr names the entity in an error from the database or the driver, turns
// a violated constraint into a *ConstraintError, and wraps a lock that could
// not be taken with ErrLocked.
func dbErr(info *EntityInfo, err error) error {
	if _, ok := dberr.As[*ConstraintError](err); ok {
		return err // already classified
	}
	if ce := dberr.Constraint(info.Name, err); ce != nil {
		return ce
	}
	if dberr.Locked(err) {
		return fmt.Errorf("velox: %s: %w: %w", info.Name, ErrLocked, err)
	}
	return fmt.Errorf("velox: %s: %w", info.Name, err)
}
