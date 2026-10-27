package veloxotel

import (
	"context"
	"strconv"

	"go.opentelemetry.io/otel/attribute"

	"github.com/syssam/velox2"
)

// schemaURL is the semantic-conventions release whose names this file holds.
const schemaURL = "https://opentelemetry.io/schemas/1.43.0"

// The attribute names, from OpenTelemetry's semantic conventions where they
// have one: those of semconv v1.43.0, written out so that the package imports
// no semconv package, which keeps the dependency to the stable API, and checked
// against it by TestNamesMatchSemconv.
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

	// errorTypeOther is error.type's value when no other fits, and the one a
	// span that ends on a panic gets: the convention's fallback, as a panic
	// value's Go type is unbounded and Goexit has none.
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

// The span, event and instrument names, and the statement instruments' units
// and descriptions, which are v1.43.0's too.
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
	if st.Op.Raw() {
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
// system, or its server (see rawName).
func summary(st *velox.Statement, coll string) string {
	if st.Op.Raw() {
		return ""
	}
	return operation(st) + " " + coll
}

// rawName is the name of a raw SQL span, which has no summary, operation or
// collection. semconv v1.43.0 names such a span {target}: db.namespace, else
// {server.address}:{server.port} (the address alone without a port), else
// db.system.name. t is the Target that names the server (zero: none).
func rawName(st *velox.Statement, t Target) string {
	switch {
	case t.Namespace != "":
		return t.Namespace
	case t.Address != "" && t.Port != 0:
		return t.Address + ":" + strconv.Itoa(t.Port)
	case t.Address != "":
		return t.Address
	}
	return dbSystem(st.Dialect)
}

// target appends to kv db.operation.name, db.collection.name and
// db.query.summary, coll and sum (see collection and summary), which raw SQL
// has none of.
func target(kv []attribute.KeyValue, st *velox.Statement, coll, sum string) []attribute.KeyValue {
	if st.Op.Raw() {
		return kv
	}
	return append(kv,
		attribute.String(attrDBOperation, operation(st)),
		attribute.String(attrDBCollection, coll),
		attribute.String(attrDBQuerySummary, sum),
	)
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
// none), from a short fixed list or, where none fits, the code, so it can key
// metrics. ctx is the context the statement ran on: a deadline velox set is
// velox.timeout even when the driver reports it as context.DeadlineExceeded.
// err is matched through errIs and errAs, so a driver's typed nil in its tree
// cannot panic.
func errorTypeOf(ctx context.Context, err error, code string) string {
	switch {
	case errIs(err, velox.ErrDenied):
		return "velox.denied"
	case errIs(err, velox.ErrNoTenant):
		return "velox.no_tenant"
	case errIs(err, velox.ErrUnbounded), errIs(err, velox.ErrNoWhere), errIs(err, velox.ErrLimitTooLarge):
		return "velox.unbounded"
	case errIs(err, velox.ErrTooManyRows):
		return "velox.too_many_rows"
	case errIs(err, velox.ErrTimeout),
		errIs(err, context.DeadlineExceeded) && errIs(context.Cause(ctx), velox.ErrTimeout):
		return "velox.timeout"
	}
	if ce, ok := errAs[*velox.ConstraintError](err); ok {
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
	case errIs(err, context.Canceled):
		return "context.canceled"
	case errIs(err, context.DeadlineExceeded):
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
