package veloxotel_test

import (
	"context"
	"database/sql/driver"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/veloxotel"
	"github.com/syssam/velox2/internal/testmodel"
)

// The adapter's own cost (reported, not budgeted) is BenchmarkAdapter's time
// less BenchmarkBareSpan's: the same one-row read, with one span on the same
// provider made by a minimal hook.

// providers returns an SDK tracer provider exporting synchronously to a
// discarding exporter, and a meter provider read by a manual reader.
func providers(b *testing.B) (*sdktrace.TracerProvider, *sdkmetric.MeterProvider) {
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(tracetest.NewNoopExporter())))
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewManualReader()))
	b.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		_ = mp.Shutdown(context.Background())
	})
	return tp, mp
}

func benchOneRow(b *testing.B, db *velox.DB) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAdapter(b *testing.B) {
	tp, mp := providers(b)
	f := &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}
	benchOneRow(b, velox.Open(velox.Postgres, f.open(), veloxotel.Options(veloxotel.Config{TracerProvider: tp, MeterProvider: mp})...))
}

func BenchmarkBareSpan(b *testing.B) {
	tp, _ := providers(b)
	tr := tp.Tracer("bench")
	f := &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}
	benchOneRow(b, velox.Open(velox.Postgres, f.open(), velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
		ctx, s := tr.Start(ctx, "SELECT invoices")
		defer s.End()
		return next(ctx)
	})))
}
