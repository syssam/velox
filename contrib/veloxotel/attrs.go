package veloxotel

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/attribute"

	"github.com/syssam/velox2"
)

// schemaURL is the semantic-conventions release whose names this file holds.
const schemaURL = "https://opentelemetry.io/schemas/1.43.0"

// The attribute names, from OpenTelemetry's semantic conventions where they
// have one: those of semconv v1.43.0, written out so that the package
// imports no semconv package (spec §2.4), and checked against it by
// TestNamesMatchSemconv.
const (
	attrDBSystem       = "db.system.name"
	attrDBOperation    = "db.operation.name"
	attrDBCollection   = "db.collection.name"
	attrDBQueryText    = "db.query.text"
	attrDBQuerySummary = "db.query.summary"
	attrReturnedRows   = "db.response.returned_rows"
	attrCodeFile       = "code.file.path"
	attrCodeLine       = "code.line.number"
	attrCodeFunction   = "code.function.name"
	attrErrorType      = "error.type"
	attrDBNamespace    = "db.namespace"
	attrServerAddress  = "server.address"
	attrServerPort     = "server.port"
	attrStatusCode     = "db.response.status_code"

	// errorTypeOther is error.type's value when no other fits.
	errorTypeOther = "_OTHER"

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

// The span, event and instrument names (spec §2.2 and §2.3), and the
// statement instruments' units and descriptions, which are v1.43.0's too.
const (
	spanTx     = "velox.tx"
	eventRetry = "retry"

	metricDuration     = "db.client.operation.duration"
	unitDuration       = "s"
	descDuration       = "Duration of database client operations."
	metricReturnedRows = "db.client.response.returned_rows"
	unitReturnedRows   = "{row}"
	descReturnedRows   = "The actual number of records returned by the database operation."
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

// operation is db.operation.name's value: the statement's SQL verb. Raw SQL
// (OpRawQuery and OpRawExec) has none: velox does not parse it out of the
// text, as the semantic conventions advise for a language whose one
// statement can hold several operations, so the attribute is left off.
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
// the entity declares a schema. Raw SQL has none either: the entity Raw scans
// into need not be the table it reads.
func collection(st *velox.Statement) string {
	if raw(st) {
		return ""
	}
	if st.Entity.Schema != "" {
		return st.Entity.Schema + "." + st.Entity.Table
	}
	return st.Entity.Table
}

// summary is a statement's db.query.summary and span name, as the semantic
// conventions build it: the operation and the table ("SELECT invoices"). Raw
// SQL, which has neither, has none: its span is named for the database
// system ("postgresql").
func summary(st *velox.Statement) string {
	if raw(st) {
		return ""
	}
	return operation(st) + " " + collection(st)
}

// target appends to kv db.operation.name, db.collection.name and
// db.query.summary, sum (see summary), which raw SQL has none of.
func target(kv []attribute.KeyValue, st *velox.Statement, sum string) []attribute.KeyValue {
	if raw(st) {
		return kv
	}
	return append(kv,
		attribute.String(attrDBOperation, operation(st)),
		attribute.String(attrDBCollection, collection(st)),
		attribute.String(attrDBQuerySummary, sum),
	)
}

// raw reports whether st is raw SQL, run by Raw or Exec.
func raw(st *velox.Statement) bool { return st.Op == velox.OpRawQuery || st.Op == velox.OpRawExec }

// returnsRows reports whether st's rows are reported as the rows it returned:
// a SELECT's and a Raw query's (OpRawQuery).
func returnsRows(st *velox.Statement) bool {
	return st.Op == velox.OpSelect || st.Op == velox.OpRawQuery
}

// failure is what a failed statement adds to its span and duration point:
// error.type, and db.response.status_code when the driver gave a code. A
// statement that succeeded has the zero failure.
type failure struct {
	errType string // "": the statement succeeded
	code    string // "": no driver code
}

// failureOf is err's failure, reading the driver's code once.
func failureOf(ctx context.Context, err error) failure {
	code, _ := velox.DriverCode(err)
	return failure{errType: errorTypeOf(ctx, err, code), code: code}
}

// errorType is error.type's value for err (see errorTypeOf).
func errorType(ctx context.Context, err error) string { return failureOf(ctx, err).errType }

// errorTypeOf is error.type's value for err, whose driver code is code ("":
// none), from a short fixed list (spec §2.1) or, where none fits, the code,
// so it can key metrics. ctx is the context the statement ran on: a deadline
// velox set is velox.timeout even when the driver reports it as
// context.DeadlineExceeded.
func errorTypeOf(ctx context.Context, err error, code string) string {
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
		case velox.ConstraintUnique:
			return "constraint.unique"
		case velox.ConstraintForeignKey:
			return "constraint.foreign_key"
		case velox.ConstraintNotNull:
			return "constraint.not_null"
		case velox.ConstraintCheck:
			return "constraint.check"
		}
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "context.canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "context.deadline_exceeded"
	}
	// The conventions prefer the database's code to _OTHER. SQLSTATE codes
	// and MySQL's and SQLite's error numbers are a bounded set too.
	if code != "" {
		return code
	}
	return errorTypeOther
}

// appendTo appends t's attributes to kv: those of its fields that are set,
// and server.port only with an address.
func (t Target) appendTo(kv []attribute.KeyValue) []attribute.KeyValue {
	if t.Namespace != "" {
		kv = append(kv, attribute.String(attrDBNamespace, t.Namespace))
	}
	if t.Address != "" {
		kv = append(kv, attribute.String(attrServerAddress, t.Address))
		if t.Port != 0 {
			kv = append(kv, attribute.Int(attrServerPort, t.Port))
		}
	}
	return kv
}
