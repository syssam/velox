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
	sctx, span := a.tracer.Start(ctx, spanName(st),
		trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(a.attributes(st)...))
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
	seconds := time.Since(start).Seconds()
	// The route is read after next returns: a fallback happens during it.
	var buf [4]attribute.KeyValue
	kv := routeAttributes(buf[:0], st)
	switch {
	case returnsRows(st):
		// db.response.returned_rows is Opt-In on spans in v1.43.0.
		if a.cfg.RecordReturnedRows {
			kv = append(kv, attribute.Int64(attrReturnedRows, st.Result.Rows))
		}
	case st.Result.Affected >= 0:
		kv = append(kv, attribute.Int64(attrAffectedRows, st.Result.Affected))
	}
	span.SetAttributes(kv...)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.SetAttributes(attribute.String(attrErrorType, errorType(ctx, err)))
		span.RecordError(err)
	}
	ended = true
	span.End()
	a.record(sctx, st, seconds, err, st.Route.Fallback && !wasFallback)
	return err
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

// attributes are a statement's span attributes known before it runs.
func (a *adapter) attributes(st *velox.Statement) []attribute.KeyValue {
	kv := make([]attribute.KeyValue, 0, 15+len(st.Tags))
	kv = append(kv, attribute.String(attrDBSystem, dbSystem(st.Dialect)))
	kv = target(kv, st)
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
