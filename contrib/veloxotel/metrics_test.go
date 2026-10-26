package veloxotel_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/veloxotel"
	"github.com/syssam/velox2/internal/testmodel"
)

// The boundaries semconv v1.43.0 advises, for durations and for rows.
var (
	wantDurationBounds = []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10}
	wantRowBounds      = []float64{1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000}
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
		if sm.Scope.Name != "github.com/syssam/velox2/contrib/veloxotel" {
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

var statementKeys = []string{"db.collection.name", "db.operation.name", "db.query.summary", "db.system.name", "velox.entity"}

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
			if et.AsString() != "_OTHER" || !slices.Equal(k, slices.Insert(slices.Clone(statementKeys), 4, "error.type")) {
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

// TestMetricsAttributesAreBounded pins that whatever the spans carry, a metric
// point carries only the bounded keys. Tenants, tags, the SQL comment and call
// sites make no new points.
func TestMetricsAttributesAreBounded(t *testing.T) {
	db, r := metered(t, &fakeDB{}, veloxotel.Config{RecordTenant: true, App: "billing", CommentTags: []string{"route"}})
	for _, tenant := range []int64{1, 2} {
		for _, route := range []string{"GET /a", "GET /b"} {
			c := velox.WithTags(velox.WithTenant(context.Background(), tenant), "route", route, "team", "billing")
			if _, err := db.Query[testmodel.Invoice]().Limit(1).All(c); err != nil {
				t.Fatal(err)
			}
			err := db.Tx(c, func(ctx context.Context, tx *velox.DB) error {
				_, err := tx.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).Limit(1).All(c)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	allowed := []string{
		"db.system.name", "db.operation.name", "db.collection.name", "db.query.summary", "velox.entity", "error.type",
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

// TestStatementMetricsSummary: a builder statement's points carry
// db.query.summary, the span's name.
func TestStatementMetricsSummary(t *testing.T) {
	f := &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}
	db, r := metered(t, f, veloxotel.Config{})
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	for _, dp := range points(t, r, "db.client.operation.duration", "s", wantDurationBounds) {
		if v, _ := dp.Attributes.Value("db.query.summary"); v.AsString() != "SELECT invoices" {
			t.Errorf("duration: db.query.summary %q", v.AsString())
		}
	}
	for _, dp := range intPoints(t, r, "db.client.response.returned_rows", "{row}", wantRowBounds) {
		if v, _ := dp.Attributes.Value("db.query.summary"); v.AsString() != "SELECT invoices" {
			t.Errorf("returned rows: db.query.summary %q", v.AsString())
		}
	}
}

// TestScope: every span and metric is under the module's instrumentation
// scope, with the pinned release's schema URL. The test binary's main module
// is this one at "(devel)", so the version is "".
func TestScope(t *testing.T) {
	r := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(r))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	db, exp, _ := traced(t, &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}, veloxotel.Config{MeterProvider: mp})
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_, err := tx.Query[testmodel.Invoice]().Limit(1).All(ctx)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	const name, schema = "github.com/syssam/velox2/contrib/veloxotel", "https://opentelemetry.io/schemas/1.43.0"
	spans := exp.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("%d spans, want 2", len(spans))
	}
	for _, s := range spans {
		if sc := s.InstrumentationScope; sc.Name != name || sc.SchemaURL != schema || sc.Version != "" {
			t.Errorf("span %q: scope %+v", s.Name, sc)
		}
	}
	var rm metricdata.ResourceMetrics
	if err := r.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	if len(rm.ScopeMetrics) == 0 {
		t.Fatal("no metrics")
	}
	for _, sm := range rm.ScopeMetrics {
		if sc := sm.Scope; sc.Name != name || sc.SchemaURL != schema || sc.Version != "" {
			t.Errorf("metrics: scope %+v", sc)
		}
	}
}

// TestMetricFingerprint: with RecordMetricFingerprint, both statement
// instruments key their points by shape too.
func TestMetricFingerprint(t *testing.T) {
	db, r := metered(t, &fakeDB{}, veloxotel.Config{RecordMetricFingerprint: true})
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
	fn := func(ctx context.Context, tx *velox.DB) error {
		_, err := tx.Query[testmodel.Invoice]().Limit(1).All(ctx)
		return err
	}
	if err := db.Tx(ctx, fn); err != nil {
		t.Fatal(err)
	}
	f.errs = []error{nil, &pgconn.PgError{Code: "40001"}}
	if err := db.Tx(ctx, fn, velox.TxRetrySerializable(1)); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	if err := db.Tx(ctx, func(context.Context, *velox.DB) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("rollback: %v", err)
	}
	f.commitErr = boom
	if err := db.Tx(ctx, fn); !errors.Is(err, boom) {
		t.Fatalf("commit fails: %v", err)
	}
	f.commitErr = nil
	if err := db.Tx(ctx, fn, velox.TxReadOnly()); err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() { _ = recover() }()
		_ = db.Tx(ctx, func(context.Context, *velox.DB) error { panic("in fn") })
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

// TestShardedMetrics: a sharded DB's statement metrics carry the shard, and
// an unsharded DB's do not.
func TestShardedMetrics(t *testing.T) {
	a1, b1 := &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}, &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}
	db, _, r := shardedTraced(t, map[string]*fakeDB{"a": a1, "b": b1})
	for _, n := range []int64{1, 2, 2} {
		if _, err := db.Query[testmodel.Invoice]().Limit(1).All(tenant(n)); err != nil {
			t.Fatal(err)
		}
	}
	got := map[string]uint64{}
	for _, dp := range points(t, r, "db.client.operation.duration", "s", wantDurationBounds) {
		sh, ok := dp.Attributes.Value("velox.route.shard")
		if !ok || !slices.Equal(keys(dp.Attributes), slices.Insert(slices.Clone(statementKeys), 5, "velox.route.shard")) {
			t.Errorf("attributes %v", dp.Attributes.ToSlice())
		}
		got[sh.AsString()] += dp.Count
	}
	if got["a"] != 1 || got["b"] != 2 || len(got) != 2 {
		t.Errorf("durations by shard %v", got)
	}
	for _, dp := range intPoints(t, r, "db.client.response.returned_rows", "{row}", wantRowBounds) {
		if _, ok := dp.Attributes.Value("velox.route.shard"); !ok {
			t.Errorf("returned_rows attributes %v", dp.Attributes.ToSlice())
		}
	}
}

// fallbackPoints returns the points of the counter velox.route.fallbacks.
func fallbackPoints(t *testing.T, r *sdkmetric.ManualReader) []metricdata.DataPoint[int64] {
	t.Helper()
	m, ok := collect(t, r)["velox.route.fallbacks"]
	if !ok {
		return nil
	}
	if m.Unit != "{fallback}" {
		t.Errorf("unit %q", m.Unit)
	}
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok || !sum.IsMonotonic {
		t.Fatalf("velox.route.fallbacks is a %T, monotonic %v", m.Data, ok && sum.IsMonotonic)
	}
	return sum.DataPoints
}

func TestFallbackCounter(t *testing.T) {
	p, rep := &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}, &fakeDB{errs: badConn(), rows: [][]driver.Value{invoiceRow(1)}}
	db, r := metered(t, p, veloxotel.Config{}, velox.WithReplicas(velox.SQLConn(rep.open())))
	if pts := fallbackPoints(t, r); len(pts) != 0 {
		t.Fatalf("points before any fallback: %v", pts)
	}
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	pts := fallbackPoints(t, r)
	if len(pts) != 1 || pts[0].Value != 1 || pts[0].Attributes.Len() != 0 {
		t.Fatalf("points %v, want one of 1 with no attributes (unsharded)", pts)
	}
	// A read the replica serves does not count.
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if pts := fallbackPoints(t, r); len(pts) != 1 || pts[0].Value != 1 {
		t.Errorf("points after a replica read %v", pts)
	}
}

// TestFallbackCounterShard: a sharded fallback counts under its shard.
func TestFallbackCounterShard(t *testing.T) {
	rep := &fakeDB{errs: badConn()}
	pa, pb := &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}, &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}
	r := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(r))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	db := velox.OpenShards(velox.Postgres, map[string]velox.Shard{
		"a": {Primary: velox.SQLConn(pa.open()), Replicas: []velox.Conn{velox.SQLConn(rep.open())}},
		"b": {Primary: velox.SQLConn(pb.open())},
	}, append(veloxotel.Options(veloxotel.Config{MeterProvider: mp}),
		velox.WithShardFor(shardOf))...)
	for _, n := range []int64{1, 2} {
		if _, err := db.Query[testmodel.Invoice]().Limit(1).All(tenant(n)); err != nil {
			t.Fatal(err)
		}
	}
	pts := fallbackPoints(t, r)
	if len(pts) != 1 || pts[0].Value != 1 {
		t.Fatalf("points %v", pts)
	}
	if sh, ok := pts[0].Attributes.Value("velox.route.shard"); !ok || sh.AsString() != "a" || pts[0].Attributes.Len() != 1 {
		t.Errorf("attributes %v", pts[0].Attributes.ToSlice())
	}
}

// TestFallbackCountedOncePerFailover: an outer Around hook that calls next
// twice sees Route.Fallback still set on the second call, which did not fail
// over; the failover counts once.
func TestFallbackCountedOncePerFailover(t *testing.T) {
	p, rep := &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}, &fakeDB{errs: badConn()}
	twice := velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
		if err := next(ctx); err != nil {
			return err
		}
		return next(ctx)
	})
	db, r := metered(t, p, veloxotel.Config{}, twice, velox.WithReplicas(velox.SQLConn(rep.open())))
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if pts := fallbackPoints(t, r); len(pts) != 1 || pts[0].Value != 1 {
		t.Errorf("points %v, want one of 1", pts)
	}
}

// TestFallbackSpanOncePerFailover: of the two executions an outer Around
// hook makes, only the span of the one that fell over says so.
func TestFallbackSpanOncePerFailover(t *testing.T) {
	p, rep := &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}, &fakeDB{errs: badConn()}
	twice := velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
		if err := next(ctx); err != nil {
			return err
		}
		return next(ctx)
	})
	db, exp, _ := traced(t, p, veloxotel.Config{}, twice, velox.WithReplicas(velox.SQLConn(rep.open())))
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	spans := byName(exp.GetSpans())["SELECT invoices"]
	if len(spans) != 2 {
		t.Fatalf("%d statement spans, want 2", len(spans))
	}
	if v, ok := attrs(spans[0])["velox.route.fallback"]; !ok || v != attribute.BoolValue(true) {
		t.Errorf("the first span's velox.route.fallback = %v", v.String())
	}
	if v, ok := attrs(spans[1])["velox.route.fallback"]; ok {
		t.Errorf("the second span's velox.route.fallback = %v, want none", v.String())
	}
}

// TestRawMetrics: raw SQL's points carry the database system and the entity,
// and no operation or table; a Raw read's rows are returned rows, Exec's are
// not.
func TestRawMetrics(t *testing.T) {
	f := &fakeDB{cols: []string{"id"}, rows: [][]driver.Value{{int64(1)}, {int64(2)}}, affected: 3}
	db, r := metered(t, f, veloxotel.Config{})
	if _, err := db.Raw[testmodel.Invoice]("report", "SELECT id FROM invoices").All(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "cleanup", "DELETE FROM invoices"); err != nil {
		t.Fatal(err)
	}
	var count uint64
	for _, dp := range points(t, r, "db.client.operation.duration", "s", wantDurationBounds) {
		count += dp.Count
		if k := keys(dp.Attributes); !slices.Equal(k, []string{"db.system.name", "velox.entity"}) {
			t.Errorf("attributes %v", k)
		}
	}
	if count != 2 {
		t.Errorf("%d durations, want 2", count)
	}
	dps := intPoints(t, r, "db.client.response.returned_rows", "{row}", wantRowBounds)
	if len(dps) != 1 || dps[0].Sum != 2 || dps[0].Count != 1 {
		t.Fatalf("returned rows %+v, want Raw's 2 alone", dps)
	}
	if e, _ := dps[0].Attributes.Value("velox.entity"); e.AsString() != "testmodel.Invoice" {
		t.Errorf("returned rows of %s", e.AsString())
	}
}

// TestIterDurationExcludesLoopBody pins that an Iter's duration point is the
// database's time, without the loop body's, also when the loop breaks after its
// first row, while its span covers the loop. A loop that panics, or an Iter
// never ranged over, records no point.
func TestIterDurationExcludesLoopBody(t *testing.T) {
	const body = 100 * time.Millisecond
	run := func(t *testing.T, n int, panics bool) (*sdkmetric.ManualReader, []time.Duration) {
		t.Helper()
		r := sdkmetric.NewManualReader()
		mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(r))
		t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
		f := &fakeDB{rows: [][]driver.Value{invoiceRow(1), invoiceRow(2), invoiceRow(3)}}
		db, exp, _ := traced(t, f, veloxotel.Config{MeterProvider: mp})
		_ = db.Query[testmodel.Invoice]().Limit(5).Iter(ctx) // never ranged
		func() {
			defer func() {
				if r := recover(); (r != nil) != panics {
					t.Errorf("recovered %v", r)
				}
			}()
			i := 0
			for _, err := range db.Query[testmodel.Invoice]().Limit(5).Iter(ctx) {
				if err != nil {
					t.Fatal(err)
				}
				time.Sleep(body)
				if panics {
					panic("boom")
				}
				if i++; i == n {
					break
				}
			}
		}()
		var spans []time.Duration
		for _, s := range exp.GetSpans() {
			spans = append(spans, s.EndTime.Sub(s.StartTime))
		}
		return r, spans
	}
	check := func(t *testing.T, r *sdkmetric.ManualReader, most float64) {
		t.Helper()
		dps := points(t, r, "db.client.operation.duration", "s", wantDurationBounds)
		if len(dps) != 1 || dps[0].Count != 1 {
			t.Fatalf("%d points, want 1 of one statement", len(dps))
		}
		if sum := dps[0].Sum; sum < 0 || sum >= most {
			t.Errorf("duration %v s, want 0 <= it < %v", sum, most)
		}
	}
	t.Run("three rows", func(t *testing.T) {
		r, spans := run(t, -1, false)
		check(t, r, 0.15)
		if len(spans) != 1 || spans[0] < 3*body {
			t.Errorf("spans %v, want one of >= %v", spans, 3*body)
		}
	})
	t.Run("broken after the first row", func(t *testing.T) {
		r, spans := run(t, 1, false)
		check(t, r, 0.05)
		if len(spans) != 1 || spans[0] < body {
			t.Errorf("spans %v, want one of >= %v", spans, body)
		}
	})
	t.Run("a panicking loop body", func(t *testing.T) {
		r, spans := run(t, -1, true)
		if _, ok := collect(t, r)["db.client.operation.duration"]; ok {
			t.Error("a panicking loop recorded a duration")
		}
		if len(spans) != 1 || spans[0] < body {
			t.Errorf("spans %v, want one of >= %v", spans, body)
		}
	})
}
