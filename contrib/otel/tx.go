package veloxotel

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/syssam/velox2"
)

// txAround is the TxAround hook: one internal span per db.Tx call, around
// every attempt and the commit, and its velox.tx.duration point. The
// statements in the transaction nest under the span, whatever context fn
// gives them, because next passes its context on as TxInfo.Context.
func (a *adapter) txAround(ctx context.Context, tx *velox.TxInfo, next func(context.Context) error) error {
	start := time.Now()
	sctx, span := a.tracer.Start(ctx, spanTx, trace.WithSpanKind(trace.SpanKindInternal), trace.WithAttributes(
		attribute.String(attrTxIsolation, tx.Isolation.String()),
		attribute.Bool(attrTxReadOnly, tx.ReadOnly),
	))
	// A panic in fn ends the span and goes on: the adapter never recovers
	// one. velox rolled the transaction back.
	ended := false
	defer func() {
		if !ended {
			txEnd(ctx, span, tx, velox.TxRollback)
			span.SetStatus(codes.Error, "panic")
			span.End()
		}
	}()

	err := next(sctx)
	txEnd(ctx, span, tx, tx.Outcome)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.SetAttributes(attribute.String(attrErrorType, errorType(ctx, err)))
		span.RecordError(err)
	}
	ended = true
	span.End()
	a.txDuration.Record(sctx, time.Since(start).Seconds(), metric.WithAttributeSet(attribute.NewSet(
		attribute.String(attrTxOutcome, tx.Outcome.String()),
		attribute.Bool(attrTxReadOnly, tx.ReadOnly),
		attribute.Bool(attrTxRetried, tx.Attempt > 1),
	)))
	return err
}

// txEnd sets what is known once next has returned, or panicked: a retry
// event per retried attempt, stamped when velox decided to retry, the final
// attempt count and the outcome.
func txEnd(ctx context.Context, span trace.Span, tx *velox.TxInfo, outcome velox.TxOutcome) {
	for _, r := range tx.Retries {
		span.AddEvent(eventRetry, trace.WithTimestamp(r.At),
			trace.WithAttributes(attribute.String(attrErrorType, errorType(ctx, r.Err))))
	}
	span.SetAttributes(
		attribute.Int(attrTxAttempts, tx.Attempt),
		attribute.String(attrTxOutcome, outcome.String()),
	)
}
