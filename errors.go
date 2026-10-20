package velox

import (
	"errors"
	"fmt"
	"strings"
)

// Errors velox returns. Each is returned wrapped, in an error whose text starts
// "velox: <Entity>: ", so a sentinel's own text is a bare phrase; test for them
// with errors.Is.
var (
	// ErrNotFound is returned by One when it finds no row, and by Save when no
	// row has the key.
	ErrNotFound = errors.New("not found")
	// ErrNotSingular is returned by One when it finds more than one row.
	ErrNotSingular = errors.New("more than one row")
	// ErrUnbounded is returned by a query that has no Limit and did not say
	// Unbounded, when it runs.
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
	// ErrTimeout is returned by a statement or transaction when a deadline
	// velox set (WithDeadlines, TxTimeout) passes. The error also matches
	// context.DeadlineExceeded; a deadline the caller set matches that alone.
	ErrTimeout = errors.New("deadline set by velox passed")
	// ErrNoTenant is returned by a statement on a tenant-scoped entity when its
	// context has neither WithTenant nor AllTenants.
	ErrNoTenant = errors.New("no tenant in the context")
	// ErrTenantMismatch is returned by a statement when the context's tenant is
	// not of the tenant column's type, or an inserted row names another tenant.
	ErrTenantMismatch = errors.New("tenant does not match")
	// ErrTenantImmutable is returned by an update when it assigns another
	// tenant to the tenant column.
	ErrTenantImmutable = errors.New("tenant column cannot change")
	// ErrTenantUpsert is returned by an upsert that updates, when the update
	// could change another tenant's row.
	ErrTenantUpsert = errors.New("upsert could cross tenants")
	// ErrDenied is returned by a statement when a policy refuses it. Every
	// *PolicyError matches it.
	ErrDenied = errors.New("denied by policy")
	// ErrPolicyUpsert is returned by an upsert that updates, on MySQL, of an
	// entity whose policy has a Read or Write predicate, because MySQL cannot
	// apply the predicate to the update.
	ErrPolicyUpsert = errors.New("upsert cannot apply the policy")
	// ErrImmutable is returned by an update, Save or upsert when its Update
	// list assigns an Immutable column, including one a hook adds. An insert
	// may set one.
	ErrImmutable = errors.New("column is immutable")
	// ErrNoShard is returned, on a DB opened with OpenShards, by a statement or
	// Tx when its context names no shard (neither OnShard nor a tenant;
	// AllTenants names none), names one the DB does not have, or when the
	// WithShardFor function failed. Nothing was sent.
	ErrNoShard = errors.New("no shard for the statement")
	// ErrOutsideTx is returned by a statement, or a Tx, under the context a
	// transaction's fn got (or one derived from it), when its DB is neither
	// that transaction nor one of its savepoints and the transaction has not
	// ended: it was most likely meant for the transaction's DB. Nothing was
	// sent. WithoutTx lifts the check.
	ErrOutsideTx = errors.New("statement outside its transaction")
	// ErrDryRun is returned by a write, or a Tx, under a context IsDryRun
	// reports true for, such as the one SQL(ctx) gives a hook: Insert,
	// InsertAll, Update and Delete's Exec, Save, an upsert and Tx. Nothing was
	// sent. Reads run. SQL(ctx) itself returns the statement it rendered, not
	// this error, when it is a hook's write that was refused.
	ErrDryRun = errors.New("write in a dry run")
)

// SchemaError reports a mistake in an entity's generated metadata (its Desc,
// or the Policy and Hooks methods it binds).
type SchemaError struct {
	Entity string // the Go type, e.g. "billing.Invoice"
	Field  string // the Go field name; empty when the error is about the entity
	Reason string
}

func (e *SchemaError) Error() string {
	if e.Field == "" {
		return "velox: " + e.Entity + ": " + e.Reason
	}
	return "velox: " + e.Entity + "." + e.Field + ": " + e.Reason
}

// ConstraintKind is the kind of constraint a statement violated.
type ConstraintKind uint8

// The kinds of constraint.
const (
	ConstraintUnique     ConstraintKind = iota + 1 // a unique index or primary key
	ConstraintForeignKey                           // a foreign key
	ConstraintNotNull                              // a NOT NULL column
	ConstraintCheck                                // a CHECK constraint
)

func (k ConstraintKind) String() string {
	switch k {
	case ConstraintUnique:
		return "unique"
	case ConstraintForeignKey:
		return "foreign key"
	case ConstraintNotNull:
		return "not null"
	case ConstraintCheck:
		return "check"
	}
	return "unknown"
}

// ConstraintError reports a violated constraint, in the same form on every
// dialect; find it with errors.As. Err is the driver's error.
type ConstraintError struct {
	Entity string
	Kind   ConstraintKind
	// Constraint is the constraint's name. SQLite reports the columns instead
	// ("invoices.org_id, invoices.number"), and a NOT NULL error names the
	// column.
	Constraint string
	Err        error
}

func (e *ConstraintError) Error() string {
	name := e.Constraint
	if name == "" {
		name = "(unnamed)"
	}
	return fmt.Sprintf("velox: %s: %s constraint %s violated: %v", e.Entity, e.Kind, name, e.Err)
}

func (e *ConstraintError) Unwrap() error { return e.Err }

// hookError is the error of an entity hook: "velox: <Entity>: hook: <err>".
// An err whose text already starts "velox: <Entity>: ", such as an invalid
// condition a Mutation recorded, does not name the entity twice.
type hookError struct {
	entity string
	err    error
}

func (e *hookError) Error() string {
	msg := e.err.Error()
	if p := "velox: " + e.entity + ": "; strings.HasPrefix(msg, p) {
		msg = msg[len(p):]
	}
	return "velox: " + e.entity + ": hook: " + msg
}

func (e *hookError) Unwrap() error { return e.err }
