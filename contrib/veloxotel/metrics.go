package veloxotel

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/syssam/velox2"
)

// The histograms' boundaries: those semconv v1.43.0 advises, for durations in
// seconds and for rows.
var (
	durationBounds = []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10}
	rowBounds      = []float64{1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000}
)

// instruments creates a's histograms on m. An error goes to otel.Handle, and
// the instrument the API returned with it, which still works, is kept.
func (a *adapter) instruments(m metric.Meter) {
	var err error
	a.duration, err = m.Float64Histogram(metricDuration,
		metric.WithDescription(descDuration),
		metric.WithUnit(unitDuration), metric.WithExplicitBucketBoundaries(durationBounds...))
	handle(err)
	a.returned, err = m.Int64Histogram(metricReturnedRows,
		metric.WithDescription(descReturnedRows),
		metric.WithUnit(unitReturnedRows), metric.WithExplicitBucketBoundaries(rowBounds...))
	handle(err)
	a.txDuration, err = m.Float64Histogram(metricTxDuration,
		metric.WithDescription("Duration of velox transactions, every attempt and the commit included."),
		metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(durationBounds...))
	handle(err)
	a.fallbacks, err = m.Int64Counter(metricFallbacks,
		metric.WithDescription("Reads that fell over from a failed replica to the primary."),
		metric.WithUnit("{fallback}"))
	handle(err)
}

func handle(err error) {
	if err != nil {
		otel.Handle(err)
	}
}

// record records a statement's duration, and a successful read's rows,
// after it ran (not on a panic); sum is its summary (see summary), tkv its
// server's (Config.Target's or TargetFor's) and f its failure. Its attributes are bounded:
// never the tags, tenant, SQL or call site, and the fingerprint only with
// Config.RecordMetricFingerprint.
func (a *adapter) record(ctx context.Context, st *velox.Statement, sum string, tkv []attribute.KeyValue, seconds float64, f failure, fellOver bool) {
	kv := make([]attribute.KeyValue, 0, 9+len(tkv))
	kv = append(kv, attribute.String(attrDBSystem, dbSystem(st.Dialect)))
	kv = target(kv, st, sum)
	kv = append(kv, attribute.String(attrEntity, st.Entity.Name))
	kv = append(kv, tkv...)
	if st.Route.Shard != "" {
		kv = append(kv, attribute.String(attrRouteShard, st.Route.Shard))
	}
	if f.errType != "" {
		kv = append(kv, attribute.String(attrErrorType, f.errType))
	}
	if f.code != "" {
		kv = append(kv, attribute.String(attrStatusCode, f.code))
	}
	if a.cfg.RecordMetricFingerprint {
		kv = append(kv, attribute.String(attrFingerprint, st.Fingerprint))
	}
	set := metric.WithAttributeSet(attribute.NewSet(kv...))
	a.duration.Record(ctx, seconds, set)
	if fellOver {
		a.fallbacks.Add(ctx, 1, shardSet(st.Route.Shard))
	}
	if returnsRows(st) && f.errType == "" {
		a.returned.Record(ctx, st.Result.Rows, set)
	}
}

// shardSet is the attributes of a fallback count: the shard, when there is one.
func shardSet(shard string) metric.AddOption {
	if shard == "" {
		return metric.WithAttributes()
	}
	return metric.WithAttributes(attribute.String(attrRouteShard, shard))
}
