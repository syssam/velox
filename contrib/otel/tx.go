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
// every attempt and the commit, and its velox.tx.duration point. next passes
// on the span's context, which velox gives fn as its ctx: a statement run on
// that ctx, or on one derived from it, nests under the span. One run on
// another context nests under that context's span.
func (a *adapter) txAround(ctx context.Context, tx *velox.TxInfo, next func(context.Context) error) error {
	start := time.Now()
	sctx, span := a.tracer.Start(ctx, spanTx, trace.WithSpanKind(trace.SpanKindInternal), trace.WithAttributes(
		attribute.String(attrTxIsolation, tx.Isolation.String()),
		attribute.Bool(attrTxReadOnly, tx.ReadOnly),
	))
	// A panic in fn ends the span, records the duration and goes on: the
	// adapter never recovers one. velox rolled the transaction back.
	// runtime.Goexit in fn is reported the same way, as "panic".
	ended := false
	defer func() {
		if !ended {
			txEnd(ctx, span, tx, velox.TxRollback)
			span.SetStatus(codes.Error, "panic")
			span.End()
			a.txRecord(sctx, start, tx, velox.TxRollback)
		}
	}()

	err := next(sctx)
	// An inner hook that returned without calling next left no outcome:
	// the transaction did not happen, which is an error.
	outcome := tx.Outcome
	if outcome == 0 {
		outcome = velox.TxError
	}
	txEnd(ctx, span, tx, outcome)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.SetAttributes(attribute.String(attrErrorType, errorType(ctx, err)))
		span.RecordError(err)
	}
	ended = true
	span.End()
	a.txRecord(sctx, start, tx, outcome)
	return err
}

// txRecord records the velox.tx.duration point of a transaction that
// started at start and ended with outcome.
func (a *adapter) txRecord(ctx context.Context, start time.Time, tx *velox.TxInfo, outcome velox.TxOutcome) {
	a.txDuration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributeSet(attribute.NewSet(
		attribute.String(attrTxOutcome, outcome.String()),
		attribute.Bool(attrTxReadOnly, tx.ReadOnly),
		attribute.Bool(attrTxRetried, tx.Attempt > 1),
	)))
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
