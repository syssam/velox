package veloxotel_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/syssam/velox2"
	veloxotel "github.com/syssam/velox2/contrib/otel"
	"github.com/syssam/velox2/internal/testmodel"
)

// The boundaries spec §2.3 sets: the conventions' durations, and powers of 4
// for rows.
var (
	wantDurationBounds = []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10}
	wantRowBounds      = []float64{1, 4, 16, 64, 256, 1024, 4096, 16384, 65536}
)

// metered returns a DB on f traced and measured, its metrics read by the
// returned reader.
func metered(t *testing.T, f *fakeDB, cfg veloxotel.Config, opts ...velox.Option) (*velox.DB, *sdkmetric.ManualReader) {
	t.Helper()
	r := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(r))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	cfg.MeterProvider = mp
	db, _, _ := traced(t, f, cfg, opts...)
	return db, r
}

// collect returns r's metrics by name.
func collect(t *testing.T, r *sdkmetric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := r.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	m := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		if sm.Scope.Name != "github.com/syssam/velox2/contrib/otel" {
			t.Errorf("scope %q", sm.Scope.Name)
		}
		for _, x := range sm.Metrics {
			m[x.Name] = x
		}
	}
	return m
}

// points returns the data points of the float64 histogram name, checking
// its unit and boundaries.
func points(t *testing.T, r *sdkmetric.ManualReader, name, unit string, bounds []float64) []metricdata.HistogramDataPoint[float64] {
	t.Helper()
	m, ok := collect(t, r)[name]
	if !ok {
		t.Fatalf("no %s", name)
	}
	h, ok := m.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("%s is a %T", name, m.Data)
	}
	checkHistogram(t, m, unit, bounds, h.DataPoints)
	return h.DataPoints
}

// intPoints is points for an int64 histogram.
func intPoints(t *testing.T, r *sdkmetric.ManualReader, name, unit string, bounds []float64) []metricdata.HistogramDataPoint[int64] {
	t.Helper()
	m, ok := collect(t, r)[name]
	if !ok {
		t.Fatalf("no %s", name)
	}
	h, ok := m.Data.(metricdata.Histogram[int64])
	if !ok {
		t.Fatalf("%s is a %T", name, m.Data)
	}
	checkHistogram(t, m, unit, bounds, h.DataPoints)
	return h.DataPoints
}

func checkHistogram[N int64 | float64](t *testing.T, m metricdata.Metrics, unit string, bounds []float64, dps []metricdata.HistogramDataPoint[N]) {
	t.Helper()
	if m.Unit != unit {
		t.Errorf("%s: unit %q, want %q", m.Name, m.Unit, unit)
	}
	for _, dp := range dps {
		if !slices.Equal(dp.Bounds, bounds) {
			t.Errorf("%s: bounds %v, want %v", m.Name, dp.Bounds, bounds)
		}
	}
}

// keys returns the keys of s, sorted.
func keys(s attribute.Set) []string {
	var k []string
	for _, kv := range s.ToSlice() {
		k = append(k, string(kv.Key))
	}
	return k
}

var statementKeys = []string{"db.collection.name", "db.operation.name", "db.system.name", "velox.entity"}

func TestStatementMetrics(t *testing.T) {
	f := &fakeDB{rows: [][]driver.Value{invoiceRow(1), invoiceRow(2)}, affected: 3}
	db, r := metered(t, f, veloxotel.Config{})
	if _, err := db.Query[testmodel.Invoice]().Limit(5).All(ctx); err != nil {
		t.Fatal(err)
	}
	f.cols, f.rows = []string{"count"}, [][]driver.Value{{int64(2)}}
	if _, err := db.Query[testmodel.Invoice]().Count(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.OrgID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	f.cols, f.errs = nil, []error{errors.New("boom")}
	if _, err := db.Query[testmodel.Invoice]().Limit(5).All(ctx); err == nil {
		t.Fatal("no error")
	}

	var count uint64
	failed := 0
	for _, dp := range points(t, r, "db.client.operation.duration", "s", wantDurationBounds) {
		count += dp.Count
		k := keys(dp.Attributes)
		if et, ok := dp.Attributes.Value("error.type"); ok {
			failed++
			if et.AsString() != "_OTHER" || !slices.Equal(k, slices.Insert(slices.Clone(statementKeys), 3, "error.type")) {
				t.Errorf("failed statement's attributes %v", dp.Attributes.ToSlice())
			}
		} else if !slices.Equal(k, statementKeys) {
			t.Errorf("attributes %v, want the keys %v", k, statementKeys)
		}
		if dp.Sum <= 0 {
			t.Errorf("duration sum %v", dp.Sum)
		}
	}
	if count != 4 || failed != 1 {
		t.Errorf("%d durations, %d of them failed; want 4, 1", count, failed)
	}

	// The two reads that succeeded; the failed one returned no rows to
	// count.
	var rows int64
	count = 0
	for _, dp := range intPoints(t, r, "db.client.response.returned_rows", "{row}", wantRowBounds) {
		if op, _ := dp.Attributes.Value("db.operation.name"); op.AsString() != "SELECT" {
			t.Errorf("returned rows for %s", op.AsString())
		}
		if _, ok := dp.Attributes.Value("error.type"); ok {
			t.Errorf("returned rows for a failed read: %v", dp.Attributes.ToSlice())
		}
		rows += dp.Sum
		count += dp.Count
	}
	if rows != 3 || count != 2 {
		t.Errorf("returned rows: %d in %d points, want 3 in 2", rows, count)
	}
}

// TestMetricsAttributesAreBounded is Review Focus 5: whatever the spans
// carry, a metric point carries only the bounded keys. Tenants, tags, the
// SQL comment and call sites make no new points.
func TestMetricsAttributesAreBounded(t *testing.T) {
	db, r := metered(t, &fakeDB{}, veloxotel.Config{RecordTenant: true, App: "billing", CommentTags: []string{"route"}})
	for _, tenant := range []int64{1, 2} {
		for _, route := range []string{"GET /a", "GET /b"} {
			c := velox.WithTags(velox.WithTenant(context.Background(), tenant), "route", route, "team", "billing")
			if _, err := db.Query[testmodel.Invoice]().Limit(1).All(c); err != nil {
				t.Fatal(err)
			}
			err := db.Tx(c, func(tx *velox.DB) error {
				_, err := tx.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).Limit(1).All(c)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	allowed := []string{
		"db.system.name", "db.operation.name", "db.collection.name", "velox.entity", "error.type",
		"velox.tx.outcome", "velox.tx.read_only", "velox.tx.retried",
	}
	check := func(name string, s attribute.Set) {
		for _, k := range keys(s) {
			if !slices.Contains(allowed, k) {
				t.Errorf("%s carries %s", name, k)
			}
		}
	}
	m := collect(t, r)
	for _, name := range []string{"db.client.operation.duration", "db.client.response.returned_rows", "velox.tx.duration"} {
		var n int
		switch d := m[name].Data.(type) {
		case metricdata.Histogram[float64]:
			n = len(d.DataPoints)
			for _, dp := range d.DataPoints {
				check(name, dp.Attributes)
			}
		case metricdata.Histogram[int64]:
			n = len(d.DataPoints)
			for _, dp := range d.DataPoints {
				check(name, dp.Attributes)
			}
		default:
			t.Errorf("%s: %T", name, m[name].Data)
		}
		if n != 1 {
			t.Errorf("%s: %d points, want 1", name, n)
		}
	}
}

// TestMetricFingerprint: with MetricFingerprint, both statement instruments
// key their points by shape too.
func TestMetricFingerprint(t *testing.T) {
	db, r := metered(t, &fakeDB{}, veloxotel.Config{MetricFingerprint: true})
	for range 2 {
		if _, err := db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).Limit(1).All(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Query[testmodel.Invoice]().Where(I.Number.EQ("x")).Limit(1).All(ctx); err != nil {
			t.Fatal(err)
		}
	}
	fingerprints := func(name string, sets []attribute.Set) {
		t.Helper()
		var fps []string
		for _, s := range sets {
			fp, _ := s.Value("velox.fingerprint")
			if len(fp.AsString()) != 16 {
				t.Errorf("%s: velox.fingerprint %q", name, fp.AsString())
			}
			fps = append(fps, fp.AsString())
		}
		if len(fps) != 2 || fps[0] == fps[1] {
			t.Errorf("%s: fingerprints %v, want two", name, fps)
		}
	}
	var sets []attribute.Set
	for _, dp := range points(t, r, "db.client.operation.duration", "s", wantDurationBounds) {
		sets = append(sets, dp.Attributes)
	}
	fingerprints("db.client.operation.duration", sets)
	sets = nil
	for _, dp := range intPoints(t, r, "db.client.response.returned_rows", "{row}", wantRowBounds) {
		sets = append(sets, dp.Attributes)
	}
	fingerprints("db.client.response.returned_rows", sets)
}

func TestTxDurationMetric(t *testing.T) {
	// The first transaction commits; the second's first attempt fails its
	// SELECT on a serialization failure, and its second commits. Then one
	// rolls back, one fails its commit, one read-only one commits, and one
	// panics in fn, which is a rollback.
	f := &fakeDB{}
	db, r := metered(t, f, veloxotel.Config{})
	fn := func(tx *velox.DB) error {
		_, err := tx.Query[testmodel.Invoice]().Limit(1).All(ctx)
		return err
	}
	if err := db.Tx(ctx, fn); err != nil {
		t.Fatal(err)
	}
	f.errs = []error{nil, &pgconn.PgError{Code: "40001"}}
	if err := db.Tx(ctx, fn, velox.RetrySerializable(1)); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	if err := db.Tx(ctx, func(*velox.DB) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("rollback: %v", err)
	}
	f.commitErr = boom
	if err := db.Tx(ctx, fn); !errors.Is(err, boom) {
		t.Fatalf("commit fails: %v", err)
	}
	f.commitErr = nil
	if err := db.Tx(ctx, fn, velox.ReadOnly()); err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() { _ = recover() }()
		_ = db.Tx(ctx, func(*velox.DB) error { panic("in fn") })
		t.Error("Tx did not re-raise the panic")
	}()
	got := map[string]uint64{}
	for _, dp := range points(t, r, "velox.tx.duration", "s", wantDurationBounds) {
		var kv []string
		for _, a := range dp.Attributes.ToSlice() {
			kv = append(kv, string(a.Key)+"="+a.Value.String())
		}
		got[strings.Join(kv, ",")] += dp.Count
	}
	want := map[string]uint64{
		"velox.tx.outcome=commit,velox.tx.read_only=false,velox.tx.retried=false":   1,
		"velox.tx.outcome=commit,velox.tx.read_only=false,velox.tx.retried=true":    1,
		"velox.tx.outcome=rollback,velox.tx.read_only=false,velox.tx.retried=false": 2,
		"velox.tx.outcome=error,velox.tx.read_only=false,velox.tx.retried=false":    1,
		"velox.tx.outcome=commit,velox.tx.read_only=true,velox.tx.retried=false":    1,
	}
	if len(got) != len(want) {
		t.Fatalf("points %v, want %v", got, want)
	}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("points %v, want %v", got, want)
		}
	}
}
