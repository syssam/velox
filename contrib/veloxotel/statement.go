package veloxotel

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/syssam/velox2"
)

// around is the Around hook: one client span per execution of a statement,
// from the call to next to its return, and its metric points.
func (a *adapter) around(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
	// The summary is built once, for the span's name, its attributes and
	// the metric points.
	sum := summary(st)
	name := sum
	if name == "" {
		name = dbSystem(st.Dialect) // raw SQL
	}
	// The route stage ran before the hooks, so the server is known at the
	// start, for samplers.
	route := st.Route
	tkv := a.targetOf(route)
	sctx, span := a.tracer.Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(a.attributes(st, sum, tkv)...))
	// A panic in next, such as Iter's loop body's, ends the span and goes on:
	// the adapter never recovers one. runtime.Goexit in an Iter loop body is
	// reported the same way, with the description "panic".
	ended := false
	defer func() {
		if !ended {
			span.SetStatus(codes.Error, "panic")
			span.End()
		}
	}()

	// Fallback is sticky on the Statement: an outer hook that retries calls
	// next again with it set. Only a failover in this run is counted.
	wasFallback := st.Route.Fallback
	start := time.Now()
	err := next(sctx)
	// The span covers Iter's loop body, as the connection is held for it;
	// the duration point is the database's time, without it.
	seconds := max(0, time.Since(start)-st.Result.LoopBody).Seconds()
	// The route is read after next returns: a fallback happens during it.
	var buf [8]attribute.KeyValue
	kv := routeAttributes(buf[:0], st)
	if a.cfg.TargetFor != nil && st.Route != route {
		tkv = a.targetOf(st.Route)
		kv = append(kv, tkv...)
	}
	switch {
	case returnsRows(st):
		// db.response.returned_rows is Opt-In on spans in v1.43.0.
		if a.cfg.RecordReturnedRows {
			kv = append(kv, attribute.Int64(attrReturnedRows, st.Result.Rows))
		}
	case st.Result.Affected >= 0:
		kv = append(kv, attribute.Int64(attrAffectedRows, st.Result.Affected))
	}
	var f failure
	if err != nil {
		f.errType = errorType(ctx, err)
		kv = append(kv, attribute.String(attrErrorType, f.errType))
		if code, ok := velox.DriverCode(err); ok {
			f.code = code
			kv = append(kv, attribute.String(attrStatusCode, code))
		}
	}
	span.SetAttributes(kv...)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
	}
	ended = true
	span.End()
	a.record(sctx, st, sum, tkv, seconds, f, st.Route.Fallback && !wasFallback)
	return err
}

// failure is what a failed statement adds to its span and duration point:
// error.type, and db.response.status_code when the driver gave a code. A
// statement that succeeded has the zero failure.
type failure struct {
	errType string // "": the statement succeeded
	code    string // "": no driver code
}

// targetOf is the server attributes of a statement on route r: those of
// Config.TargetFor's Target when it is set, Config.Target's otherwise.
func (a *adapter) targetOf(r velox.Route) []attribute.KeyValue {
	if a.cfg.TargetFor == nil {
		return a.target
	}
	return a.cfg.TargetFor(r).appendTo(make([]attribute.KeyValue, 0, 3))
}

// routeAttributes appends to kv the span attributes of where st ran that
// can change while it runs: whether a replica ran it, and a fallback when
// there was one. The shard is final before the hooks run, so it is a start
// attribute (see attributes).
func routeAttributes(kv []attribute.KeyValue, st *velox.Statement) []attribute.KeyValue {
	kv = append(kv, attribute.Bool(attrRouteReplica, st.Route.Replica >= 0))
	if st.Route.Fallback {
		kv = append(kv, attribute.Bool(attrRouteFallback, true))
	}
	return kv
}

// queryText reports whether st's span carries db.query.text. v1.43.0 says
// non-parameterized text SHOULD NOT be collected by default, since it may
// hold literals. A builder statement is always parameterized; raw SQL is
// taken as parameterized only when it has arguments, as velox cannot see
// literals in its text. The text never carries the arguments' values.
func (a *adapter) queryText(st *velox.Statement) bool {
	return !a.cfg.OmitQueryText && (!raw(st) || len(st.Args) > 0)
}

// attributes are a statement's span attributes known before it runs; sum is
// its summary (see summary), and tkv its server's (see targetOf).
func (a *adapter) attributes(st *velox.Statement, sum string, tkv []attribute.KeyValue) []attribute.KeyValue {
	kv := make([]attribute.KeyValue, 0, 15+len(st.Tags)+len(tkv))
	kv = append(kv, attribute.String(attrDBSystem, dbSystem(st.Dialect)))
	kv = target(kv, st, sum)
	kv = append(kv, tkv...)
	kv = append(kv,
		attribute.String(attrEntity, st.Entity.Name),
		attribute.String(attrFingerprint, st.Fingerprint),
	)
	if a.queryText(st) {
		kv = append(kv, attribute.String(attrDBQueryText, st.SQL))
	}
	if st.Count {
		kv = append(kv, attribute.Bool(attrCount, true))
	}
	if c := st.Caller; c.File != "" {
		kv = append(kv,
			attribute.String(attrCodeFile, c.File),
			attribute.Int(attrCodeLine, c.Line),
			attribute.String(attrCodeFunction, c.Func),
		)
	}
	for _, t := range st.Tags {
		kv = append(kv, attribute.String(attrTagPrefix+t.Key, t.Value))
	}
	if st.Route.Shard != "" {
		kv = append(kv, attribute.String(attrRouteShard, st.Route.Shard))
	}
	if st.TenantBypass != "" {
		kv = append(kv, attribute.String(attrTenantBypass, st.TenantBypass))
	}
	if st.PolicyBypass != "" {
		kv = append(kv, attribute.String(attrPolicyBypass, st.PolicyBypass))
	}
	if a.cfg.RecordTenant && st.Tenant != nil {
		kv = append(kv, attribute.String(attrTenant, fmt.Sprint(st.Tenant)))
	}
	return kv
}
