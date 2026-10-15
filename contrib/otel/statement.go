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
	// In a transaction, the parent is the span on the transaction's context,
	// whatever context the statement was given.
	parent := ctx
	if st.Tx != nil && st.Tx.Context != nil {
		if s := trace.SpanFromContext(st.Tx.Context); s.SpanContext().IsValid() {
			parent = trace.ContextWithSpan(ctx, s)
		}
	}
	sctx, span := a.tracer.Start(parent, operation(st)+" "+collection(st),
		trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(a.attributes(st)...))
	// A panic in next, such as Iter's loop body's, ends the span and goes on:
	// the adapter never recovers one.
	ended := false
	defer func() {
		if !ended {
			span.SetStatus(codes.Error, "panic")
			span.End()
		}
	}()

	start := time.Now()
	err := next(sctx)
	seconds := time.Since(start).Seconds()
	switch st.Op {
	case velox.OpSelect:
		span.SetAttributes(attribute.Int64(attrReturnedRows, st.Result.Rows))
	default:
		if st.Result.Affected >= 0 {
			span.SetAttributes(attribute.Int64(attrAffectedRows, st.Result.Affected))
		}
	}
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.SetAttributes(attribute.String(attrErrorType, errorType(ctx, err)))
		span.RecordError(err)
	}
	ended = true
	span.End()
	a.record(sctx, st, seconds, err)
	return err
}

// attributes are a statement's span attributes known before it runs.
func (a *adapter) attributes(st *velox.Statement) []attribute.KeyValue {
	kv := make([]attribute.KeyValue, 0, 12+len(st.Tags))
	kv = append(kv,
		attribute.String(attrDBSystem, dbSystem(st.Dialect)),
		attribute.String(attrDBOperation, operation(st)),
		attribute.String(attrDBCollection, collection(st)),
		attribute.String(attrEntity, st.Entity.Name),
		attribute.String(attrFingerprint, st.Fingerprint),
	)
	if !a.cfg.OmitQueryText {
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
