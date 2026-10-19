package veloxotel

import (
	"context"
	"errors"

	"github.com/syssam/velox2"
)

// The attribute names, from OpenTelemetry's semantic conventions where they
// have one (spec §2.4: written out, never imported from a semconv package).
// The code.* names are those of the release whose db.* names these are.
const (
	attrDBSystem     = "db.system.name"
	attrDBOperation  = "db.operation.name"
	attrDBCollection = "db.collection.name"
	attrDBQueryText  = "db.query.text"
	attrReturnedRows = "db.response.returned_rows"
	attrCodeFile     = "code.file.path"
	attrCodeLine     = "code.line.number"
	attrCodeFunction = "code.function.name"
	attrErrorType    = "error.type"

	attrAffectedRows = "velox.affected_rows"
	attrEntity       = "velox.entity"
	attrFingerprint  = "velox.fingerprint"
	attrCount        = "velox.count"
	attrTagPrefix    = "velox.tag." // + the tag's key
	attrTenant       = "velox.tenant"
	attrTenantBypass = "velox.tenant_bypass"
	attrPolicyBypass = "velox.policy_bypass"

	attrRouteShard    = "velox.route.shard"    // only on a sharded DB
	attrRouteReplica  = "velox.route.replica"  // whether a replica ran the statement
	attrRouteFallback = "velox.route.fallback" // only when a replica failed and the primary ran it

	attrTxIsolation = "velox.tx.isolation"
	attrTxReadOnly  = "velox.tx.read_only"
	attrTxAttempts  = "velox.tx.attempts"
	attrTxOutcome   = "velox.tx.outcome"
	attrTxRetried   = "velox.tx.retried"
)

// The span, event and instrument names (spec §2.2 and §2.3).
const (
	spanTx     = "velox.tx"
	eventRetry = "retry"

	metricDuration     = "db.client.operation.duration"
	metricReturnedRows = "db.client.response.returned_rows"
	metricTxDuration   = "velox.tx.duration"
	metricFallbacks    = "velox.route.fallbacks"
)

// dbSystem is db.system.name's value for d.
func dbSystem(d velox.Dialect) string {
	switch d {
	case velox.Postgres:
		return "postgresql"
	case velox.MySQL:
		return "mysql"
	case velox.SQLite:
		return "sqlite"
	}
	return "other_sql"
}

// operation is db.operation.name's value: the statement's SQL verb.
func operation(st *velox.Statement) string {
	switch st.Op {
	case velox.OpSelect:
		return "SELECT"
	case velox.OpInsert:
		return "INSERT"
	case velox.OpUpdate:
		return "UPDATE"
	case velox.OpDelete:
		return "DELETE"
	}
	return "UNKNOWN"
}

// collection is db.collection.name's value: the table, schema-qualified when
// the entity declares a schema.
func collection(st *velox.Statement) string {
	if st.Entity.Schema != "" {
		return st.Entity.Schema + "." + st.Entity.Table
	}
	return st.Entity.Table
}

// errorType is error.type's value for err, from a short fixed list (spec
// §2.1), so it can key metrics. ctx is the context the statement ran on: a
// deadline velox set is velox.timeout even when the driver reports it as
// context.DeadlineExceeded.
func errorType(ctx context.Context, err error) string {
	switch {
	case errors.Is(err, velox.ErrDenied):
		return "velox.denied"
	case errors.Is(err, velox.ErrNoTenant):
		return "velox.no_tenant"
	case errors.Is(err, velox.ErrUnbounded), errors.Is(err, velox.ErrNoWhere), errors.Is(err, velox.ErrLimitTooLarge):
		return "velox.unbounded"
	case errors.Is(err, velox.ErrTooManyRows):
		return "velox.too_many_rows"
	case errors.Is(err, velox.ErrTimeout),
		errors.Is(err, context.DeadlineExceeded) && errors.Is(context.Cause(ctx), velox.ErrTimeout):
		return "velox.timeout"
	}
	var ce *velox.ConstraintError
	if errors.As(err, &ce) {
		switch ce.Kind {
		case velox.Unique:
			return "constraint.unique"
		case velox.ForeignKey:
			return "constraint.foreign_key"
		case velox.NotNull:
			return "constraint.not_null"
		case velox.Check:
			return "constraint.check"
		}
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "context.canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "context.deadline_exceeded"
	}
	return "_OTHER"
}
