package veloxotel

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/syssam/velox"
)

// txAround is the TxAround hook: one internal span per db.Tx call, around
// every attempt and the commit, and its velox.tx.duration point. next passes
// on the span's context, which velox gives fn as its ctx: a statement run on
// that ctx, or on one derived from it, nests under the span. One run on
// another context nests under that context's span.
func (a *adapter) txAround(ctx context.Context, tx *velox.TxInfo, next func(context.Context) error) error {
	start := time.Now()
	kv := make([]attribute.KeyValue, 0, 2+txPlaceAttrs)
	kv = append(kv,
		attribute.String(attrTxIsolation, tx.Isolation.String()),
		attribute.Bool(attrTxReadOnly, tx.ReadOnly),
	)
	// Where tx runs is known now and cannot change: the span and the
	// duration point share it, so TargetFor is called once.
	kv = a.txPlace(kv, tx)
	place := kv[2:]
	sctx, span := a.tracer.Start(ctx, spanTx, a.internalKind, trace.WithAttributes(kv...))
	// A panic in fn ends the span, records the duration and goes on: the
	// adapter never recovers one. velox rolled the transaction back.
	// runtime.Goexit in fn is reported the same way.
	ended := false
	defer func() {
		if !ended {
			txEnd(ctx, span, tx, velox.TxRollback)
			endPanicked(span)
			a.txRecord(sctx, start, tx, velox.TxRollback, place)
		}
	}()

	err := next(sctx)
	// An inner hook that returned without calling next left no outcome:
	// the transaction did not happen, which is an error.
	outcome := tx.Outcome
	if outcome == 0 {
		outcome = velox.TxError
	}
	if !span.IsRecording() { // not sampled: the span carries nothing more
		ended = true
		span.End()
		a.txRecord(sctx, start, tx, outcome, place)
		return err
	}
	txEnd(ctx, span, tx, outcome)
	if err != nil {
		f := failureOf(ctx, err)
		span.SetAttributes(attribute.String(attrErrorType, f.errType))
		if f.code != "" {
			span.SetAttributes(attribute.String(attrStatusCode, f.code))
		}
		setFailed(span, err)
	}
	ended = true
	span.End()
	a.txRecord(sctx, start, tx, outcome, place)
	return err
}

// txRecord records the velox.tx.duration point of a transaction that
// started at start and ended with outcome, with place, where it ran (see
// txPlace); it does nothing when the instrument is not enabled.
func (a *adapter) txRecord(ctx context.Context, start time.Time, tx *velox.TxInfo, outcome velox.TxOutcome, place []attribute.KeyValue) {
	if !a.txDuration.Enabled(ctx) {
		return
	}
	kv := make([]attribute.KeyValue, 0, 3+len(place))
	kv = append(kv,
		attribute.String(attrTxOutcome, outcome.String()),
		attribute.Bool(attrTxReadOnly, tx.ReadOnly),
		attribute.Bool(attrTxRetried, tx.Attempt > 1),
	)
	kv = append(kv, place...)
	a.txDuration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributeSet(attribute.NewSet(kv...)))
}

// txPlaceAttrs is the most attributes txPlace appends: the shard, and a
// Target's three.
const txPlaceAttrs = 4

// txPlace appends to kv the attributes of where tx runs: its shard, when it
// has one, and its server, TargetFor's for its route when it is set,
// Config.Target's otherwise.
func (a *adapter) txPlace(kv []attribute.KeyValue, tx *velox.TxInfo) []attribute.KeyValue {
	if tx.Route.Shard != "" {
		kv = append(kv, attribute.String(attrRouteShard, tx.Route.Shard))
	}
	if a.cfg.TargetFor != nil {
		return a.cfg.TargetFor(tx.Route).appendTo(kv)
	}
	return append(kv, a.target...)
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
