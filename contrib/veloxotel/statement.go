package veloxotel

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/syssam/velox"
)

// around is the Around hook: one client span per execution of a statement,
// from the call to next to its return, and its metric points.
func (a *adapter) around(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
	// The shape's collection and summary are built once per shape, for the
	// span's name, its attributes and the metric points.
	sh := a.shapeOf(st)
	coll, sum := sh.coll, sh.sum
	name := sum
	if name == "" { // raw SQL
		name = rawName(st, a.cfg.Target) // zero with TargetFor: renamed below
	}
	// Config.Target cannot change, so it is a start attribute, for
	// samplers. TargetFor's depends on the route, which is final only once
	// next has returned: it is set then (see below).
	sctx, span := a.tracer.Start(ctx, name,
		a.clientKind, trace.WithAttributes(a.attributes(ctx, st, coll, sum)...))
	// A panic in next, such as Iter's loop body's, ends the span and goes on:
	// the adapter never recovers one. runtime.Goexit in an Iter loop body is
	// reported the same way.
	ended := false
	defer func() {
		if !ended {
			endPanicked(span)
		}
	}()

	// Fallback is sticky on the Statement: an outer hook that retries calls
	// next again with it set. Only a failover in this run is marked on the
	// span and counted.
	wasFallback := st.Route.Fallback
	start := time.Now()
	err := next(sctx)
	// The span covers Iter's loop body, as the connection is held for it;
	// the duration point is the database's time, without it.
	seconds := max(0, time.Since(start)-st.Result.LoopBody).Seconds()
	// The route is read after next returns: a fallback happens during it.
	fellOver := st.Route.Fallback && !wasFallback
	tkv := a.target
	if a.cfg.TargetFor != nil {
		// Called once: the span's attributes, its raw name and the duration
		// point all share this server.
		t := a.cfg.TargetFor(st.Route)
		tkv = t.appendTo(make([]attribute.KeyValue, 0, 3))
		if sum == "" { // raw SQL: the span is named for the final route's server
			span.SetName(rawName(st, t))
		}
	}
	var f failure
	if err != nil {
		f = failureOf(ctx, err)
	}
	// A span that is not sampled records nothing: what only it would carry
	// is not built. Its start attributes were, for the sampler.
	if span.IsRecording() {
		a.endAttributes(span, st, fellOver, tkv, f)
		if err != nil {
			setFailed(span, err, f)
		}
	}
	ended = true
	span.End()
	a.record(sctx, st, sh, tkv, seconds, f, fellOver)
	return err
}

// setFailed marks span as failed with err, whose failure is f: the status
// Error described by f's error.type, and an exception event of err's Go
// type. Neither holds err's text: a driver's message can quote a row's
// values, as MySQL's "Duplicate entry" does. error.type is the caller's to
// set, as a statement and a transaction build it differently.
func setFailed(span trace.Span, err error, f failure) {
	span.SetStatus(codes.Error, f.errType)
	span.AddEvent(eventException, trace.WithAttributes(attribute.String(attrExceptionType, fmt.Sprintf("%T", err))))
}

// endPanicked ends a span whose hook's next panicked or called
// runtime.Goexit: the status Error, the description "panic", and error.type
// _OTHER, the conventions' fallback, as a panic value's Go type is not a
// bounded set and Goexit has no value.
func endPanicked(span trace.Span) {
	span.SetAttributes(attribute.String(attrErrorType, errorTypeOther))
	span.SetStatus(codes.Error, "panic")
	span.End()
}

// endAttributes sets on span the attributes known once st ran: where it ran
// (fellOver, and its server tkv when TargetFor names it), its rows, and its
// failure f.
func (a *adapter) endAttributes(span trace.Span, st *velox.Statement, fellOver bool, tkv []attribute.KeyValue, f failure) {
	var buf [8]attribute.KeyValue
	kv := routeAttributes(buf[:0], st, fellOver)
	if a.cfg.TargetFor != nil {
		kv = append(kv, tkv...)
	}
	switch {
	case st.Op.Reads():
		// db.response.returned_rows is Opt-In on spans in v1.43.0.
		if a.cfg.RecordReturnedRows {
			kv = append(kv, attribute.Int64(attrReturnedRows, st.Result.Rows))
		}
	case st.Result.Affected >= 0:
		kv = append(kv, attribute.Int64(attrAffectedRows, st.Result.Affected))
	}
	if f.errType != "" {
		kv = append(kv, attribute.String(attrErrorType, f.errType))
		if f.code != "" {
			kv = append(kv, attribute.String(attrStatusCode, f.code))
		}
	}
	span.SetAttributes(kv...)
}

// routeAttributes appends to kv the span attributes of where st ran that
// can change while it runs: whether a replica ran it, and a fallback when
// this execution fell over (fellOver), not an earlier one an outer hook
// retried. The shard is final before the hooks run, so it is a start
// attribute (see attributes).
func routeAttributes(kv []attribute.KeyValue, st *velox.Statement, fellOver bool) []attribute.KeyValue {
	kv = append(kv, attribute.Bool(attrRouteReplica, st.Route.Replica >= 0))
	if fellOver {
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
	return !a.cfg.OmitQueryText && (!st.Op.Raw() || len(st.Args) > 0)
}

// attributes are a statement's span attributes known before it runs; coll
// and sum are its collection and summary (see collection and summary). The
// server is Config.Target's, none with TargetFor, whose is set at the end.
func (a *adapter) attributes(ctx context.Context, st *velox.Statement, coll, sum string) []attribute.KeyValue {
	kv := make([]attribute.KeyValue, 0, 15+len(st.Tags)+len(a.target))
	kv = append(kv, attribute.String(attrDBSystem, dbSystem(st.Dialect)))
	kv = target(kv, st, coll, sum)
	kv = append(kv, a.target...)
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
		kv = append(kv, a.tagKey(t.Key).String(t.Value))
	}
	if st.Route.Shard != "" {
		kv = append(kv, attribute.String(attrRouteShard, st.Route.Shard))
	}
	if st.PolicyBypass != "" {
		kv = append(kv, attribute.String(attrPolicyBypass, st.PolicyBypass))
	}
	if a.cfg.RecordScope {
		if v := velox.RowScopeFromContext(ctx).Values; v != "" {
			kv = append(kv, attribute.String(attrScope, v))
		}
	}
	return kv
}
