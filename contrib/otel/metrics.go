package veloxotel

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/syssam/velox2"
)

// The histograms' boundaries (spec §2.3): the semantic conventions'
// recommended ones for durations, in seconds, and powers of 4 for rows.
var (
	durationBounds = []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10}
	rowBounds      = []float64{1, 4, 16, 64, 256, 1024, 4096, 16384, 65536}
)

// instruments creates a's histograms on m. An error goes to otel.Handle, and
// the instrument the API returned with it, which still works, is kept.
func (a *adapter) instruments(m metric.Meter) {
	var err error
	a.duration, err = m.Float64Histogram(metricDuration,
		metric.WithDescription("Duration of database client operations."),
		metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(durationBounds...))
	handle(err)
	a.returned, err = m.Int64Histogram(metricReturnedRows,
		metric.WithDescription("Number of rows returned by the operation."),
		metric.WithUnit("{row}"), metric.WithExplicitBucketBoundaries(rowBounds...))
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
// after it ran (not on a panic). Its attributes are bounded: never the tags,
// tenant, SQL or call site, and the fingerprint only with
// Config.MetricFingerprint.
func (a *adapter) record(ctx context.Context, st *velox.Statement, seconds float64, err error, fellOver bool) {
	kv := make([]attribute.KeyValue, 0, 7)
	kv = append(kv,
		attribute.String(attrDBSystem, dbSystem(st.Dialect)),
		attribute.String(attrDBOperation, operation(st)),
		attribute.String(attrDBCollection, collection(st)),
		attribute.String(attrEntity, st.Entity.Name),
	)
	if st.Route.Shard != "" {
		kv = append(kv, attribute.String(attrRouteShard, st.Route.Shard))
	}
	if err != nil {
		kv = append(kv, attribute.String(attrErrorType, errorType(ctx, err)))
	}
	if a.cfg.MetricFingerprint {
		kv = append(kv, attribute.String(attrFingerprint, st.Fingerprint))
	}
	set := metric.WithAttributeSet(attribute.NewSet(kv...))
	a.duration.Record(ctx, seconds, set)
	if fellOver {
		a.fallbacks.Add(ctx, 1, shardSet(st.Route.Shard))
	}
	if st.Op == velox.OpSelect && err == nil {
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
