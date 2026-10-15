package veloxotel

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/syssam/velox2"
)

// Config configures the adapter. The zero Config traces into the global
// TracerProvider and measures into the global MeterProvider, with no SQL
// comment.
type Config struct {
	TracerProvider trace.TracerProvider // nil: otel.GetTracerProvider()
	MeterProvider  metric.MeterProvider // nil: otel.GetMeterProvider()
	// App names the application in the SQL comment; empty: no comment. The
	// comment carries the call site, so pgx prepares a statement per call
	// site per connection: see the package doc on sizing its cache.
	App string
	// CommentTags are the velox.WithTags keys the SQL comment carries, in
	// this order. List only tags whose values are stable per code path (a
	// route template, never an ID): see velox.WithSQLComment.
	CommentTags       []string
	OmitQueryText     bool // leave db.query.text off spans
	RecordTenant      bool // add velox.tenant to spans
	MetricFingerprint bool // add velox.fingerprint to the statement metrics
}

// Options returns the velox options that trace and measure every statement
// and transaction: WithCallers, WithAround, WithTxAround, and WithSQLComment
// when App is set. Place them after an Around hook of your own that retries,
// to get one span per attempt.
func Options(cfg Config) []velox.Option {
	a := newAdapter(cfg)
	opts := []velox.Option{velox.WithCallers(), velox.WithAround(a.around), velox.WithTxAround(a.txAround)}
	if cfg.App != "" {
		opts = append(opts, velox.WithSQLComment(cfg.App, cfg.CommentTags...))
	}
	return opts
}

// Around returns the hook that makes a span per statement and records the
// statement metrics, alone. Without velox.WithCallers the spans carry no call
// site.
func Around(cfg Config) velox.Around { return newAdapter(cfg).around }

// TxAround returns the hook that makes the velox.tx span per transaction and
// records velox.tx.duration, alone.
func TxAround(cfg Config) velox.TxAround { return newAdapter(cfg).txAround }

// scope is the instrumentation scope: the tracer's and meter's name.
const scope = "github.com/syssam/velox2/contrib/otel"

// adapter turns velox's statements and transactions into spans and metric
// points, for one Config.
type adapter struct {
	cfg    Config
	tracer trace.Tracer

	duration   metric.Float64Histogram // db.client.operation.duration, s
	returned   metric.Int64Histogram   // db.client.response.returned_rows, {row}
	txDuration metric.Float64Histogram // velox.tx.duration, s
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
	a := &adapter{cfg: cfg, tracer: tp.Tracer(scope)}
	a.instruments(mp.Meter(scope))
	return a
}
