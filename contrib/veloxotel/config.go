package veloxotel

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/syssam/velox2"
)

// Config configures the adapter. The zero Config traces into the global
// TracerProvider and measures into the global MeterProvider, with call sites
// recorded and no SQL comment.
type Config struct {
	TracerProvider trace.TracerProvider // nil: otel.GetTracerProvider()
	MeterProvider  metric.MeterProvider // nil: otel.GetMeterProvider()
	// App names the application in the SQL comment; empty: no comment. With
	// call sites recorded (the default), the comment carries the call site, so
	// pgx prepares a statement per call site per connection: see the package
	// doc on sizing its cache.
	App string
	// CommentTags are the velox.WithTags keys the SQL comment carries, in
	// this order. List only tags whose values are stable per code path (a
	// route template, never an ID): see velox.WithSQLComment.
	CommentTags []string

	OmitCallers             bool // leave call sites out: no code.* attributes, one SQL comment per shape
	OmitQueryText           bool // leave db.query.text off spans
	RecordTenant            bool // add velox.tenant to spans
	RecordMetricFingerprint bool // add velox.fingerprint to the statement metrics
	RecordReturnedRows      bool // add db.response.returned_rows to spans
}

// Options returns the velox options that trace and measure every statement
// and transaction: velox.WithCallers unless OmitCallers is set,
// velox.WithAround, velox.WithTxAround, and velox.WithSQLComment when App is
// set. Place them after an Around hook of your own that retries, to get one
// span per attempt.
func Options(cfg Config) []velox.Option {
	a := newAdapter(cfg)
	opts := make([]velox.Option, 0, 4)
	if !cfg.OmitCallers {
		opts = append(opts, velox.WithCallers())
	}
	opts = append(opts, velox.WithAround(a.around), velox.WithTxAround(a.txAround))
	if cfg.App != "" {
		opts = append(opts, velox.WithSQLComment(cfg.App, cfg.CommentTags...))
	}
	return opts
}

// adapter turns velox's statements and transactions into spans and metric
// points, for one Config.
type adapter struct {
	cfg    Config
	tracer trace.Tracer

	duration   metric.Float64Histogram // db.client.operation.duration, s
	returned   metric.Int64Histogram   // db.client.response.returned_rows, {row}
	txDuration metric.Float64Histogram // velox.tx.duration, s
	fallbacks  metric.Int64Counter     // velox.route.fallbacks, {fallback}
}

func newAdapter(cfg Config) *adapter {
	tp := cfg.TracerProvider
	if tp == nil {
		tp = otel.GetTracerProvider()
	}
	mp := cfg.MeterProvider
	if mp == nil {
		mp = otel.GetMeterProvider()
	}
	a := &adapter{cfg: cfg, tracer: tp.Tracer(modulePath,
		trace.WithInstrumentationVersion(version()), trace.WithSchemaURL(schemaURL))}
	a.instruments(mp.Meter(modulePath,
		metric.WithInstrumentationVersion(version()), metric.WithSchemaURL(schemaURL)))
	return a
}
