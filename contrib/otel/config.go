package veloxotel

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/syssam/velox2"
)

// Config configures the adapter. The zero Config traces into the global
// TracerProvider, with no SQL comment.
type Config struct {
	TracerProvider trace.TracerProvider // nil: otel.GetTracerProvider()
	MeterProvider  metric.MeterProvider // nil: otel.GetMeterProvider()
	// App names the application in the SQL comment; empty: no comment.
	App string
	// CommentTags are the velox.WithTags keys the SQL comment carries, in
	// this order. List only tags whose values are stable per code path (a
	// route template, never an ID): see velox.WithSQLComment.
	CommentTags       []string
	OmitQueryText     bool // leave db.query.text off spans
	RecordTenant      bool // add velox.tenant to spans
	MetricFingerprint bool // add velox.fingerprint to the statement metrics
}

// Options returns the velox options that trace every statement: WithCallers,
// WithAround, and WithSQLComment when App is set. Place them after an Around
// hook of your own that retries, to get one span per attempt.
func Options(cfg Config) []velox.Option {
	a := newAdapter(cfg)
	opts := []velox.Option{velox.WithCallers(), velox.WithAround(a.around)}
	if cfg.App != "" {
		opts = append(opts, velox.WithSQLComment(cfg.App, cfg.CommentTags...))
	}
	return opts
}

// Around returns the hook that makes a span per statement, alone. Without
// velox.WithCallers the spans carry no call site.
func Around(cfg Config) velox.Around { return newAdapter(cfg).around }

// scope is the instrumentation scope: the tracer's and meter's name.
const scope = "github.com/syssam/velox2/contrib/otel"

// adapter turns velox's statements into spans, for one Config.
type adapter struct {
	cfg    Config
	tracer trace.Tracer
}

func newAdapter(cfg Config) *adapter {
	tp := cfg.TracerProvider
	if tp == nil {
		tp = otel.GetTracerProvider()
	}
	return &adapter{cfg: cfg, tracer: tp.Tracer(scope)}
}
