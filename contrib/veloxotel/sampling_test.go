package veloxotel_test

import (
	"context"
	"database/sql/driver"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/veloxotel"
	"github.com/syssam/velox/internal/testmodel"
)

// TestUnsampledKeepsMetrics: a statement or a transaction whose span is not
// sampled exports no span, and its metric points are those of a sampled one:
// the failure that a span would carry is still on its duration point.
func TestUnsampledKeepsMetrics(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.NeverSample()), sdktrace.WithSyncer(exp))
	r := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(r))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()); _ = mp.Shutdown(context.Background()) })
	f := &fakeDB{errs: []error{&pgconn.PgError{Code: "40P01"}}}
	db := velox.Open(velox.Postgres, f.open(), veloxotel.Options(veloxotel.Config{TracerProvider: tp, MeterProvider: mp})...)
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); err == nil {
		t.Fatal("no error")
	}
	if err := db.Tx(ctx, func(context.Context, *velox.DB) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if n := len(exp.GetSpans()); n != 0 {
		t.Errorf("%d spans exported, want none", n)
	}
	point := durationPoint(t, r)
	for k, want := range map[attribute.Key]string{"error.type": "40P01", "db.response.status_code": "40P01", "db.query.summary": "SELECT invoices"} {
		if v, _ := point.Value(k); v.AsString() != want {
			t.Errorf("duration point: %s = %q, want %q", k, v.AsString(), want)
		}
	}
	if dps := points(t, r, "velox.tx.duration", "s", wantDurationBounds); len(dps) != 1 {
		t.Errorf("%d velox.tx.duration points, want 1", len(dps))
	}
}

// TestAdapterAllocs bounds what the adapter allocates per statement on top of
// velox running an Around hook, with providers that record nothing: its
// start attributes (built for the sampler, a slice and its option), the
// start options, and the context the tracer returns. A shape's description
// and its metric set are cached; an unsampled span builds no end attributes.
// The SDK's own allocations, which a recording provider adds, are not the
// adapter's.
func TestAdapterAllocs(t *testing.T) {
	const budget = 4
	read := func(opts ...velox.Option) float64 {
		db := velox.Open(velox.Postgres, (&fakeDB{rows: [][]driver.Value{invoiceRow(1)}}).open(), opts...)
		return testing.AllocsPerRun(200, func() {
			if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
	hooked := read(velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
		return next(ctx)
	}))
	observed := read(veloxotel.Options(veloxotel.Config{
		TracerProvider: tracenoop.NewTracerProvider(), MeterProvider: metricnoop.NewMeterProvider(), OmitCallers: true,
	})...)
	if got := observed - hooked; got > budget {
		t.Errorf("the adapter allocates %v times per statement, over its budget of %d", got, budget)
	}
}
