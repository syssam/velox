package velox

import (
	"errors"
	"fmt"
	"strings"
)

// Errors velox returns. Each is returned wrapped, in an error whose text
// starts "velox: <Entity>: " (policy design §6), so a sentinel's own text is
// a bare phrase; test for them with errors.Is.
var (
	// ErrNotFound: One found no row, or Save no row with the key.
	ErrNotFound = errors.New("not found")
	// ErrNotSingular: One found more than one row.
	ErrNotSingular = errors.New("more than one row")
	// ErrUnbounded: a query with no Limit that did not say Unbounded.
	ErrUnbounded = errors.New("query has no Limit")
	// ErrNoWhere: an update or delete with no Where that did not say All.
	ErrNoWhere = errors.New("update or delete has no Where")
	// ErrLimitTooLarge: a query whose Limit is over the DB's WithMaxLimit.
	ErrLimitTooLarge = errors.New("limit is over the maximum")
	// ErrTooManyRows: a query returned more rows than WithMaxRows or the
	// query's MaxRows allow.
	ErrTooManyRows = errors.New("read more rows than the cap")
	// ErrTimeout: a deadline velox set (WithDeadlines, Timeout) passed. The
	// error also matches context.DeadlineExceeded; a deadline the caller set
	// matches that alone.
	ErrTimeout = errors.New("deadline set by velox passed")
	// ErrNoTenant: a statement on a tenant-scoped entity whose context has
	// neither WithTenant nor AllTenants.
	ErrNoTenant = errors.New("no tenant in the context")
	// ErrTenantMismatch: the context's tenant is not of the tenant column's
	// type, or an inserted row names another tenant.
	ErrTenantMismatch = errors.New("tenant does not match")
	// ErrTenantImmutable: a statement assigns another tenant to the tenant
	// column.
	ErrTenantImmutable = errors.New("tenant column cannot change")
	// ErrTenantUpsert: an upsert that updates could change another tenant's
	// row.
	ErrTenantUpsert = errors.New("upsert could cross tenants")
	// ErrDenied: a policy refused the statement. Every *PolicyError matches
	// it.
	ErrDenied = errors.New("denied by policy")
	// ErrPolicyUpsert: an upsert that updates, on MySQL, of an entity whose
	// policy has a Read or Write predicate, which MySQL cannot apply to the
	// update.
	ErrPolicyUpsert = errors.New("upsert cannot apply the policy")
	// ErrImmutable: an update, Save or upsert's Update list assigned an
	// Immutable column, including one a hook adds. An insert may set one.
	ErrImmutable = errors.New("column is immutable")
	// ErrNoShard: on a DB opened with OpenShards, a statement or Tx whose
	// context names no shard (neither OnShard nor a tenant; AllTenants names
	// none), or names one the DB does not have, or whose WithShardFor
	// function failed. Nothing was sent.
	ErrNoShard = errors.New("no shard for the statement")
	// ErrOutsideTx: a statement, or a Tx, on a DB in no transaction, under
	// the context a transaction's fn got (or one derived from it): it was
	// most likely meant for the transaction's DB. Nothing was sent.
	ErrOutsideTx = errors.New("statement outside its transaction")
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
	Unique ConstraintKind = iota + 1 // a unique index or primary key
	ForeignKey
	NotNull
	Check
)

func (k ConstraintKind) String() string {
	switch k {
	case Unique:
		return "unique"
	case ForeignKey:
		return "foreign key"
	case NotNull:
		return "not null"
	case Check:
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
