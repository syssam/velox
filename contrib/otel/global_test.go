package veloxotel_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/syssam/velox2"
	veloxotel "github.com/syssam/velox2/contrib/otel"
	"github.com/syssam/velox2/internal/testmodel"
)

// TestGlobalProviders: the zero Config traces into the global TracerProvider
// and measures into the global MeterProvider. No other test uses the global
// providers; this one leaves no-op ones behind.
func TestGlobalProviders(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	r := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(r))
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	t.Cleanup(func() {
		otel.SetTracerProvider(tracenoop.NewTracerProvider())
		otel.SetMeterProvider(metricnoop.NewMeterProvider())
		_ = tp.Shutdown(context.Background())
		_ = mp.Shutdown(context.Background())
	})
	db := velox.Open(velox.Postgres, (&fakeDB{}).open(), veloxotel.Options(veloxotel.Config{})...)
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_, err := tx.Query[testmodel.Invoice]().Limit(1).All(ctx)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	spans := byName(exp.GetSpans())
	if len(spans["velox.tx"]) != 1 || len(spans["SELECT invoices"]) != 1 {
		t.Errorf("spans %v", spans)
	}
	m := collect(t, r)
	for _, name := range []string{"db.client.operation.duration", "db.client.response.returned_rows", "velox.tx.duration"} {
		if _, ok := m[name]; !ok {
			t.Errorf("no %s", name)
		}
	}
}
