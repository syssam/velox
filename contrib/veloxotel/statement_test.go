package veloxotel_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/veloxotel"
	"github.com/syssam/velox/internal/testmodel"
)

var (
	ctx = velox.WithAllTenants(context.Background(), "otel tests")
	I   = testmodel.Invoices
)

// traced returns a DB on f traced into an in-memory exporter, with opts
// given before veloxotel's options.
func traced(t *testing.T, f *fakeDB, cfg veloxotel.Config, opts ...velox.Option) (*velox.DB, *tracetest.InMemoryExporter, *sdktrace.TracerProvider) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	cfg.TracerProvider = tp
	return velox.Open(velox.Postgres, f.open(), append(opts, veloxotel.Options(cfg)...)...), exp, tp
}

func attrs(s tracetest.SpanStub) map[attribute.Key]attribute.Value {
	m := map[attribute.Key]attribute.Value{}
	for _, kv := range s.Attributes {
		m[kv.Key] = kv.Value
	}
	return m
}

func here() int { _, _, line, _ := runtime.Caller(1); return line }

// only returns the one span exp holds.
func only(t *testing.T, exp *tracetest.InMemoryExporter) tracetest.SpanStub {
	t.Helper()
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("%d spans, want 1", len(spans))
	}
	return spans[0]
}

func TestSelectSpan(t *testing.T) {
	f := &fakeDB{rows: [][]driver.Value{invoiceRow(1), invoiceRow(2)}}
	db, exp, _ := traced(t, f, veloxotel.Config{App: "billing", CommentTags: []string{"route"}})
	c := velox.WithTags(ctx, "route", "GET /invoices", "team", "billing")
	line := here() + 1
	if _, err := db.Query[testmodel.Invoice]().Where(I.Number.EQ("x")).Limit(5).All(c); err != nil {
		t.Fatal(err)
	}
	s := only(t, exp)
	if s.Name != "SELECT invoices" || s.SpanKind != trace.SpanKindClient || s.Status.Code != codes.Unset {
		t.Errorf("span %q kind %v status %v", s.Name, s.SpanKind, s.Status)
	}
	a := attrs(s)
	for k, want := range map[attribute.Key]attribute.Value{
		"db.system.name":      attribute.StringValue("postgresql"),
		"db.operation.name":   attribute.StringValue("SELECT"),
		"db.collection.name":  attribute.StringValue("invoices"),
		"db.query.summary":    attribute.StringValue("SELECT invoices"),
		"velox.entity":        attribute.StringValue("testmodel.Invoice"),
		"velox.tenant_bypass": attribute.StringValue("otel tests"),
		"velox.tag.route":     attribute.StringValue("GET /invoices"),
		"velox.tag.team":      attribute.StringValue("billing"),
		"code.file.path":      attribute.StringValue("statement_test.go"),
		"code.line.number":    attribute.Int64Value(int64(line)),
		"code.function.name":  attribute.StringValue("github.com/syssam/velox/contrib/veloxotel_test.TestSelectSpan"),
	} {
		if a[k] != want {
			t.Errorf("%s = %v, want %v", k, a[k].String(), want.String())
		}
	}
	if fp := a["velox.fingerprint"].AsString(); len(fp) != 16 {
		t.Errorf("velox.fingerprint %q", fp)
	}
	if q := a["db.query.text"].AsString(); q != f.log[0] {
		t.Errorf("db.query.text %q, sent %q", q, f.log[0])
	}
	// db.response.returned_rows is Opt-In on spans in v1.43.0.
	for _, k := range []attribute.Key{"db.response.returned_rows", "velox.affected_rows", "velox.count", "velox.tenant", "velox.policy_bypass", "error.type"} {
		if _, ok := a[k]; ok {
			t.Errorf("unexpected %s", k)
		}
	}
}

// TestRecordReturnedRows: with RecordReturnedRows, a read's span has
// db.response.returned_rows.
func TestRecordReturnedRows(t *testing.T) {
	f := &fakeDB{rows: [][]driver.Value{invoiceRow(1), invoiceRow(2)}}
	db, exp, _ := traced(t, f, veloxotel.Config{RecordReturnedRows: true})
	if _, err := db.Query[testmodel.Invoice]().Limit(5).All(ctx); err != nil {
		t.Fatal(err)
	}
	if got := attrs(only(t, exp))["db.response.returned_rows"]; got != attribute.Int64Value(2) {
		t.Errorf("db.response.returned_rows = %v, want 2", got.String())
	}
}

// TestEveryOperation names each operation's span and sets its results.
func TestEveryOperation(t *testing.T) {
	cases := []struct {
		name   string
		f      *fakeDB
		run    func(db *velox.DB) error
		span   string
		want   map[attribute.Key]attribute.Value
		absent []attribute.Key
	}{
		{
			name: "count by",
			f:    &fakeDB{cols: []string{"org_id", "count"}, rows: [][]driver.Value{{int64(1), int64(4)}}},
			run: func(db *velox.DB) error {
				_, err := velox.CountBy(ctx, db.Query[testmodel.Invoice]().Unbounded(), I.OrgID.Key())
				return err
			},
			span: "SELECT invoices",
			want: map[attribute.Key]attribute.Value{
				"db.operation.name": attribute.StringValue("SELECT"),
				"db.query.summary":  attribute.StringValue("SELECT invoices"),
			},
		},
		{
			name: "sum by",
			f:    &fakeDB{cols: []string{"org_id", "sum"}, rows: [][]driver.Value{{int64(1), 4.0}}},
			run: func(db *velox.DB) error {
				_, err := velox.SumBy(ctx, db.Query[testmodel.Invoice]().Unbounded(), I.OrgID.Key(), I.Total)
				return err
			},
			span: "SELECT invoices",
			want: map[attribute.Key]attribute.Value{
				"db.operation.name": attribute.StringValue("SELECT"),
				"db.query.summary":  attribute.StringValue("SELECT invoices"),
			},
		},
		{
			name: "count",
			f:    &fakeDB{cols: []string{"count"}, rows: [][]driver.Value{{int64(4)}}},
			run: func(db *velox.DB) error {
				_, err := db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).Count(ctx)
				return err
			},
			span: "SELECT invoices",
			want: map[attribute.Key]attribute.Value{
				"velox.count":               attribute.BoolValue(true),
				"db.operation.name":         attribute.StringValue("SELECT"),
				"db.response.returned_rows": attribute.Int64Value(1),
			},
			absent: []attribute.Key{"velox.affected_rows"},
		},
		{
			// On PostgreSQL the insert reads the key back with RETURNING.
			name: "insert",
			f:    &fakeDB{cols: []string{"id"}, rows: [][]driver.Value{{int64(9)}}},
			run: func(db *velox.DB) error {
				return db.Insert(&testmodel.Invoice{OrgID: 1, Number: "INV-9"}).Exec(ctx)
			},
			span: "INSERT invoices",
			want: map[attribute.Key]attribute.Value{
				"db.operation.name":   attribute.StringValue("INSERT"),
				"velox.affected_rows": attribute.Int64Value(1),
			},
			absent: []attribute.Key{"db.response.returned_rows", "velox.count"},
		},
		{
			name: "update",
			f:    &fakeDB{affected: 3},
			run: func(db *velox.DB) error {
				_, err := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.OrgID.EQ(1)).Exec(ctx)
				return err
			},
			span: "UPDATE invoices",
			want: map[attribute.Key]attribute.Value{
				"db.operation.name":   attribute.StringValue("UPDATE"),
				"velox.affected_rows": attribute.Int64Value(3),
			},
			absent: []attribute.Key{"db.response.returned_rows"},
		},
		{
			name: "delete",
			f:    &fakeDB{affected: 2},
			run: func(db *velox.DB) error {
				_, err := db.Delete[testmodel.Invoice]().Where(I.Paid.EQ(false)).Exec(ctx)
				return err
			},
			span: "DELETE invoices",
			want: map[attribute.Key]attribute.Value{
				"db.operation.name":   attribute.StringValue("DELETE"),
				"velox.affected_rows": attribute.Int64Value(2),
			},
			absent: []attribute.Key{"db.response.returned_rows"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db, exp, _ := traced(t, c.f, veloxotel.Config{RecordReturnedRows: true})
			if err := c.run(db); err != nil {
				t.Fatal(err)
			}
			s := only(t, exp)
			if s.Name != c.span || s.SpanKind != trace.SpanKindClient {
				t.Errorf("span %q kind %v, want %q", s.Name, s.SpanKind, c.span)
			}
			a := attrs(s)
			if got := a["db.query.summary"].AsString(); got != c.span {
				t.Errorf("db.query.summary %q, want %q", got, c.span)
			}
			for k, want := range c.want {
				if a[k] != want {
					t.Errorf("%s = %v, want %v", k, a[k].String(), want.String())
				}
			}
			for _, k := range c.absent {
				if _, ok := a[k]; ok {
					t.Errorf("unexpected %s = %v", k, a[k].String())
				}
			}
		})
	}
}

// TestSchemaQualifiedCollection: a declared schema qualifies the table in
// the span's name and db.collection.name.
func TestSchemaQualifiedCollection(t *testing.T) {
	f := &fakeDB{}
	db, exp, _ := traced(t, f, veloxotel.Config{}, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		info := *st.Entity
		info.Schema = "billing"
		st.Entity = &info
		return nil
	}))
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	s := only(t, exp)
	if s.Name != "SELECT billing.invoices" || attrs(s)["db.collection.name"].AsString() != "billing.invoices" {
		t.Errorf("span %q, db.collection.name %q", s.Name, attrs(s)["db.collection.name"].AsString())
	}
}

func TestOmitQueryText(t *testing.T) {
	db, exp, _ := traced(t, &fakeDB{}, veloxotel.Config{OmitQueryText: true})
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if q, ok := attrs(only(t, exp))["db.query.text"]; ok {
		t.Errorf("db.query.text = %q", q.AsString())
	}
}

func TestRecordTenant(t *testing.T) {
	db, exp, _ := traced(t, &fakeDB{}, veloxotel.Config{RecordTenant: true})
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(velox.WithTenant(context.Background(), int64(7))); err != nil {
		t.Fatal(err)
	}
	a := attrs(only(t, exp))
	if got := a["velox.tenant"]; got != attribute.StringValue("7") {
		t.Errorf("velox.tenant = %v", got.String())
	}
	if _, ok := a["velox.tenant_bypass"]; ok {
		t.Error("unexpected velox.tenant_bypass")
	}
}

// TestTenantNotRecordedByDefault: without RecordTenant, a tenant-scoped
// statement's span has no velox.tenant.
func TestTenantNotRecordedByDefault(t *testing.T) {
	db, exp, _ := traced(t, &fakeDB{}, veloxotel.Config{})
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(velox.WithTenant(context.Background(), int64(7))); err != nil {
		t.Fatal(err)
	}
	if v, ok := attrs(only(t, exp))["velox.tenant"]; ok {
		t.Errorf("velox.tenant = %v", v.String())
	}
}

func TestPolicyBypass(t *testing.T) {
	f := &fakeDB{cols: []string{}}
	db, exp, _ := traced(t, f, veloxotel.Config{})
	if _, err := db.Query[testmodel.Ticket]().Limit(1).All(velox.WithoutPolicy(ctx, "support")); err != nil {
		t.Fatal(err)
	}
	s := only(t, exp)
	if got := attrs(s)["velox.policy_bypass"]; s.Name != "SELECT tickets" || got != attribute.StringValue("support") {
		t.Errorf("span %q, velox.policy_bypass = %v", s.Name, got.String())
	}
}

// TestNoSpanFromSQL: SQL(ctx) sends nothing, so it makes no span.
func TestNoSpanFromSQL(t *testing.T) {
	db, exp, _ := traced(t, &fakeDB{}, veloxotel.Config{App: "billing"})
	if _, _, err := db.Query[testmodel.Invoice]().Limit(1).SQL(ctx); err != nil {
		t.Fatal(err)
	}
	if spans := exp.GetSpans(); len(spans) != 0 {
		t.Errorf("%d spans from SQL(ctx)", len(spans))
	}
}

// TestErrorTypes runs failing statements through the hook: each span has
// the status Error, its error.type and an exception event with the error's
// text.
func TestErrorTypes(t *testing.T) {
	boom := errors.New("boom")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	past, cancelPast := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer cancelPast()
	three := [][]driver.Value{invoiceRow(1), invoiceRow(2), invoiceRow(3)}
	cases := []struct {
		name string
		f    *fakeDB
		opts []velox.Option
		ctx  context.Context
		want string
	}{
		{"unique", &fakeDB{errs: []error{&pgconn.PgError{Code: "23505", ConstraintName: "invoices_org_number_uq"}}}, nil, ctx, "constraint.unique"},
		{"too many rows", &fakeDB{rows: three}, []velox.Option{velox.WithMaxRows(2)}, ctx, "velox.too_many_rows"},
		{"canceled", &fakeDB{}, nil, canceled, "context.canceled"},
		{"deadline", &fakeDB{}, nil, past, "context.deadline_exceeded"},
		{"timeout", &fakeDB{block: true}, []velox.Option{velox.WithDeadlines(velox.Deadlines{Statement: time.Millisecond})}, ctx, "velox.timeout"},
		{"other", &fakeDB{errs: []error{boom}}, nil, ctx, "_OTHER"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// The error the span sees is what next returns to the hooks;
			// velox may add to it after them (a timeout's ErrTimeout).
			var err error
			seen := velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
				err = next(ctx)
				return err
			})
			db, exp, _ := traced(t, c.f, veloxotel.Config{}, append(c.opts, seen)...)
			if _, err := db.Query[testmodel.Invoice]().Limit(5).All(c.ctx); err == nil {
				t.Fatal("no error")
			}
			if err == nil {
				t.Fatal("the hooks saw no error")
			}
			s := only(t, exp)
			if s.Status.Code != codes.Error || s.Status.Description != err.Error() {
				t.Errorf("status %v, want Error %q", s.Status, err)
			}
			if got := attrs(s)["error.type"].AsString(); got != c.want {
				t.Errorf("error.type %q, want %q (%v)", got, c.want, err)
			}
			if msg := exception(s); msg != err.Error() {
				t.Errorf("exception.message %q, want %q", msg, err)
			}
		})
	}
}

// exception returns the exception.message of s's exception event; "" when
// it has none.
func exception(s tracetest.SpanStub) string {
	for _, e := range s.Events {
		if e.Name != "exception" {
			continue
		}
		for _, kv := range e.Attributes {
			if kv.Key == "exception.message" {
				return kv.Value.AsString()
			}
		}
	}
	return ""
}

// TestOneSpanPerAttempt: an Around hook of the application's own, given
// before veloxotel's options, that retries makes one span per attempt.
func TestOneSpanPerAttempt(t *testing.T) {
	f := &fakeDB{errs: []error{errors.New("boom")}}
	retry := velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
		if err := next(ctx); err == nil {
			return nil
		}
		return next(ctx)
	})
	db, exp, _ := traced(t, f, veloxotel.Config{}, retry)
	if _, err := db.Query[testmodel.Invoice]().Limit(5).All(ctx); err != nil {
		t.Fatal(err)
	}
	spans := exp.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("%d spans, want 2", len(spans))
	}
	if got := attrs(spans[0])["error.type"].AsString(); got != "_OTHER" || spans[0].Status.Code != codes.Error {
		t.Errorf("first attempt: error.type %q, status %v", got, spans[0].Status)
	}
	if _, ok := attrs(spans[1])["error.type"]; ok || spans[1].Status.Code != codes.Unset {
		t.Errorf("second attempt: status %v", spans[1].Status)
	}
}

// TestParent: a statement's span is a child of the span on its context: in a
// transaction, on fn's ctx, the transaction's span, or a span the
// application started inside fn; outside one, the request's. The
// transaction's span here is the application's own: Around alone, without
// veloxotel's TxAround.
func TestParent(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	tr := tp.Tracer("t")
	txSpan := velox.WithTxAround(func(ctx context.Context, _ *velox.TxInfo, next func(context.Context) error) error {
		ctx, s := tr.Start(ctx, "tx")
		defer s.End()
		return next(ctx)
	})
	db := velox.Open(velox.Postgres, (&fakeDB{}).open(), txSpan, velox.WithAround(veloxotel.AroundOf(veloxotel.Config{TracerProvider: tp})))

	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		if _, err := tx.Query[testmodel.Invoice]().Limit(1).All(ctx); err != nil { // fn's ctx: the tx span
			return err
		}
		actx, app := tr.Start(ctx, "app")
		defer app.End()
		_, err := tx.Query[testmodel.Invoice]().Limit(1).All(actx)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	rctx, req := tr.Start(ctx, "req")
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(rctx); err != nil {
		t.Fatal(err)
	}
	req.End()

	byName := map[string]tracetest.SpanStub{}
	var stmts []tracetest.SpanStub
	for _, s := range exp.GetSpans() {
		if s.Name == "SELECT invoices" {
			stmts = append(stmts, s)
		} else {
			byName[s.Name] = s
		}
	}
	if len(stmts) != 3 {
		t.Fatalf("%d statement spans, want 3", len(stmts))
	}
	if got, want := stmts[0].Parent.SpanID(), byName["tx"].SpanContext.SpanID(); !want.IsValid() || got != want {
		t.Errorf("in the transaction: parent %v, want tx %v", got, want)
	}
	if got, want := stmts[1].Parent.SpanID(), byName["app"].SpanContext.SpanID(); !want.IsValid() || got != want {
		t.Errorf("under the application's span in the transaction: parent %v, want app %v", got, want)
	}
	if got, want := byName["app"].Parent.SpanID(), byName["tx"].SpanContext.SpanID(); got != want {
		t.Errorf("the application's span: parent %v, want tx %v", got, want)
	}
	if got, want := stmts[2].Parent.SpanID(), byName["req"].SpanContext.SpanID(); !want.IsValid() || got != want {
		t.Errorf("outside: parent %v, want req %v", got, want)
	}
}

// TestIterPanicEndsTheSpan pins that a loop body's panic ends the span with
// status Error and the description "panic", and reaches the caller unchanged.
func TestIterPanicEndsTheSpan(t *testing.T) {
	db, exp, _ := traced(t, &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}, veloxotel.Config{})
	got := func() (v any) {
		defer func() { v = recover() }()
		for _, err := range db.Query[testmodel.Invoice]().Limit(5).Iter(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			panic("in the loop")
		}
		return nil
	}()
	if got != "in the loop" {
		t.Errorf("recovered %v", got)
	}
	s := only(t, exp)
	if s.Status.Code != codes.Error || s.Status.Description != "panic" {
		t.Errorf("status %v", s.Status)
	}
	if got := attrs(s)["error.type"].AsString(); got != "_OTHER" {
		t.Errorf("error.type %q, want _OTHER", got)
	}
}

// shardOf maps the tenants 1 and 2 to the shards "a" and "b".
func shardOf(_ context.Context, tenant any) (string, error) {
	if tenant == int64(1) {
		return "a", nil
	}
	return "b", nil
}

// tenant returns a context for the org with id n, which shardOf maps to a shard.
func tenant(n int64) context.Context { return velox.WithTenant(context.Background(), n) }

// shardedTraced returns a DB over shards "a" and "b" traced and measured.
func shardedTraced(t *testing.T, shards map[string]*fakeDB) (*velox.DB, *tracetest.InMemoryExporter, *sdkmetric.ManualReader) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	r := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(r))
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		_ = mp.Shutdown(context.Background())
	})
	m := map[string]velox.Shard{}
	for name, f := range shards {
		m[name] = velox.Shard{Primary: velox.SQLConn(f.open())}
	}
	opts := append(veloxotel.Options(veloxotel.Config{TracerProvider: tp, MeterProvider: mp}),
		velox.WithShardFor(shardOf))
	return velox.OpenShards(velox.Postgres, m, opts...), exp, r
}

// badConn is the errors a replica returns to fail every attempt
// database/sql makes (it retries driver.ErrBadConn), so that velox falls over.
func badConn() []error {
	return []error{driver.ErrBadConn, driver.ErrBadConn, driver.ErrBadConn}
}

// TestRouteAttributes: every span says whether a replica ran it; a shard and
// a fallback are said only when there are any.
func TestRouteAttributes(t *testing.T) {
	read := func(c context.Context, db *velox.DB) {
		t.Helper()
		if _, err := db.Query[testmodel.Invoice]().Limit(1).All(c); err != nil {
			t.Fatal(err)
		}
	}
	absent := func(t *testing.T, a map[attribute.Key]attribute.Value, ks ...attribute.Key) {
		t.Helper()
		for _, k := range ks {
			if _, ok := a[k]; ok {
				t.Errorf("unexpected %s", k)
			}
		}
	}
	t.Run("unrouted", func(t *testing.T) {
		db, exp, _ := traced(t, &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}, veloxotel.Config{})
		read(ctx, db)
		a := attrs(only(t, exp))
		if v, ok := a["velox.route.replica"]; !ok || v != attribute.BoolValue(false) {
			t.Errorf("velox.route.replica = %v", v.String())
		}
		absent(t, a, "velox.route.shard", "velox.route.fallback")
	})
	t.Run("replica", func(t *testing.T) {
		p, r := &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}, &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}
		db, exp, _ := traced(t, p, veloxotel.Config{}, velox.WithReplicas(velox.SQLConn(r.open())))
		read(ctx, db)
		a := attrs(only(t, exp))
		if a["velox.route.replica"] != attribute.BoolValue(true) {
			t.Errorf("velox.route.replica = %v", a["velox.route.replica"].String())
		}
		absent(t, a, "velox.route.shard", "velox.route.fallback")
		if len(p.log) != 0 || len(r.log) != 1 {
			t.Errorf("primary %d calls, replica %d", len(p.log), len(r.log))
		}
	})
	t.Run("primary read", func(t *testing.T) {
		p, r := &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}, &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}
		db, exp, _ := traced(t, p, veloxotel.Config{}, velox.WithReplicas(velox.SQLConn(r.open())))
		read(velox.WithPrimaryReads(ctx), db)
		a := attrs(only(t, exp))
		if a["velox.route.replica"] != attribute.BoolValue(false) {
			t.Errorf("velox.route.replica = %v", a["velox.route.replica"].String())
		}
		absent(t, a, "velox.route.shard", "velox.route.fallback")
	})
	t.Run("fallback", func(t *testing.T) {
		p, r := &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}, &fakeDB{errs: badConn()}
		db, exp, _ := traced(t, p, veloxotel.Config{}, velox.WithReplicas(velox.SQLConn(r.open())))
		read(ctx, db)
		s := only(t, exp)
		a := attrs(s)
		if a["velox.route.replica"] != attribute.BoolValue(false) || a["velox.route.fallback"] != attribute.BoolValue(true) {
			t.Errorf("replica = %v, fallback = %v", a["velox.route.replica"].String(), a["velox.route.fallback"].String())
		}
		absent(t, a, "velox.route.shard")
		if s.Status.Code != codes.Unset || len(p.log) != 1 {
			t.Errorf("status %v, primary %d calls", s.Status, len(p.log))
		}
	})
	t.Run("shard", func(t *testing.T) {
		a1, b1 := &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}, &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}
		db, exp, _ := shardedTraced(t, map[string]*fakeDB{"a": a1, "b": b1})
		read(tenant(2), db)
		read(velox.WithShard(velox.WithAllTenants(context.Background(), "otel tests"), "a"), db)
		var got []string
		for _, s := range exp.GetSpans() {
			a := attrs(s)
			got = append(got, a["velox.route.shard"].AsString())
			if a["velox.route.replica"] != attribute.BoolValue(false) {
				t.Errorf("velox.route.replica = %v", a["velox.route.replica"].String())
			}
			absent(t, a, "velox.route.fallback")
		}
		if !slices.Equal(got, []string{"b", "a"}) {
			t.Errorf("shards %q", got)
		}
	})
}

// startShards records velox.route.shard as a span processor sees it at start.
type startShards struct {
	sdktrace.SpanProcessor
	got []string
}

func (s *startShards) OnStart(_ context.Context, sp sdktrace.ReadWriteSpan) {
	for _, kv := range sp.Attributes() {
		if kv.Key == "velox.route.shard" {
			s.got = append(s.got, kv.Value.AsString())
			return
		}
	}
	s.got = append(s.got, "")
}
func (*startShards) OnEnd(sdktrace.ReadOnlySpan)      {}
func (*startShards) Shutdown(context.Context) error   { return nil }
func (*startShards) ForceFlush(context.Context) error { return nil }

// TestRouteShardAtStart: a sharded statement's span has velox.route.shard
// when it starts, for samplers and span processors.
func TestRouteShardAtStart(t *testing.T) {
	sp := &startShards{}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sp))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	a1, b1 := &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}, &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}
	db := velox.OpenShards(velox.Postgres, map[string]velox.Shard{
		"a": {Primary: velox.SQLConn(a1.open())},
		"b": {Primary: velox.SQLConn(b1.open())},
	}, append(veloxotel.Options(veloxotel.Config{TracerProvider: tp}), velox.WithShardFor(shardOf))...)
	for _, n := range []int64{2, 1} {
		if _, err := db.Query[testmodel.Invoice]().Limit(1).All(tenant(n)); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(sp.got, []string{"b", "a"}) {
		t.Errorf("shards at start %q", sp.got)
	}
}

// TestRawSpans: raw SQL's span is named for the database system, as the
// semantic conventions name a span with neither an operation nor a table, and
// carries neither db.operation.name, db.collection.name nor db.query.summary:
// velox does not parse them out of the SQL. It carries the reason as
// velox.tenant_bypass and velox.policy_bypass, and Raw's returned rows or
// Exec's affected rows. It carries the SQL as sent only when the statement
// has arguments: v1.43.0 says non-parameterized text SHOULD NOT be collected
// by default, and velox cannot tell whether raw text holds literals.
func TestRawSpans(t *testing.T) {
	f := &fakeDB{cols: []string{"id", "number"}, rows: [][]driver.Value{{int64(1), "A"}, {int64(2), "B"}}, affected: 3}
	db, exp, _ := traced(t, f, veloxotel.Config{RecordReturnedRows: true})
	bare := context.Background()
	const q = "SELECT id, number FROM invoices WHERE org_id = $1 FOR UPDATE"
	if _, err := db.Raw[testmodel.Invoice]("locks the batch", q, int64(1)).All(bare); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(bare, "nightly cleanup", "DELETE FROM invoices WHERE paid"); err != nil {
		t.Fatal(err)
	}
	spans := exp.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("%d spans, want 2", len(spans))
	}
	for i, c := range []struct {
		entity, reason string
		want, absent   []attribute.Key
		n              int64
	}{
		{"testmodel.Invoice", "locks the batch", []attribute.Key{"db.response.returned_rows", "db.query.text"}, []attribute.Key{"velox.affected_rows"}, 2},
		{"Exec", "nightly cleanup", []attribute.Key{"velox.affected_rows"}, []attribute.Key{"db.response.returned_rows", "db.query.text"}, 3},
	} {
		s := spans[i]
		if s.Name != "postgresql" || s.SpanKind != trace.SpanKindClient || s.Status.Code != codes.Unset {
			t.Errorf("span %d: %q kind %v status %v", i, s.Name, s.SpanKind, s.Status)
		}
		a := attrs(s)
		want := map[attribute.Key]attribute.Value{
			"db.system.name":      attribute.StringValue("postgresql"),
			"velox.entity":        attribute.StringValue(c.entity),
			"velox.tenant_bypass": attribute.StringValue(c.reason),
			"velox.policy_bypass": attribute.StringValue(c.reason),
			c.want[0]:             attribute.Int64Value(c.n),
		}
		if slices.Contains(c.want, "db.query.text") {
			want["db.query.text"] = attribute.StringValue(q)
		}
		for k, want := range want {
			if a[k] != want {
				t.Errorf("span %d: %s = %v, want %v", i, k, a[k].String(), want.String())
			}
		}
		for _, k := range append([]attribute.Key{"db.operation.name", "db.collection.name", "db.query.summary"}, c.absent...) {
			if _, ok := a[k]; ok {
				t.Errorf("span %d: unexpected %s = %v", i, k, a[k].String())
			}
		}
	}

	// A Raw read that fails: its span has the error, and still no operation
	// or collection.
	failing := &fakeDB{errs: []error{errors.New("boom")}}
	db, exp, _ = traced(t, failing, veloxotel.Config{})
	if _, err := db.Raw[testmodel.Invoice]("locks the batch", q, int64(1)).All(bare); err == nil {
		t.Fatal("no error")
	}
	s := only(t, exp)
	a := attrs(s)
	if s.Name != "postgresql" || s.Status.Code != codes.Error || a["error.type"].AsString() != "_OTHER" {
		t.Errorf("failed Raw: %q status %v error.type %q", s.Name, s.Status, a["error.type"].AsString())
	}
	for _, k := range []attribute.Key{"db.operation.name", "db.collection.name"} {
		if _, ok := a[k]; ok {
			t.Errorf("failed Raw: unexpected %s = %v", k, a[k].String())
		}
	}
}

// startAttrs records each span's attributes as a span processor sees them
// when it starts.
type startAttrs struct {
	sdktrace.SpanProcessor
	got []map[attribute.Key]attribute.Value
}

func (s *startAttrs) OnStart(_ context.Context, sp sdktrace.ReadWriteSpan) {
	m := map[attribute.Key]attribute.Value{}
	for _, kv := range sp.Attributes() {
		m[kv.Key] = kv.Value
	}
	s.got = append(s.got, m)
}
func (*startAttrs) OnEnd(sdktrace.ReadOnlySpan)      {}
func (*startAttrs) Shutdown(context.Context) error   { return nil }
func (*startAttrs) ForceFlush(context.Context) error { return nil }

// observed returns a DB on p, with opts, traced into exp and seen at start
// by sp, and measured into r.
func observed(t *testing.T, p *fakeDB, cfg veloxotel.Config, opts ...velox.Option) (db *velox.DB, sp *startAttrs, exp *tracetest.InMemoryExporter, r *sdkmetric.ManualReader) {
	t.Helper()
	sp, exp, r = &startAttrs{}, tracetest.NewInMemoryExporter(), sdkmetric.NewManualReader()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sp), sdktrace.WithSyncer(exp))
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(r))
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		_ = mp.Shutdown(context.Background())
	})
	cfg.TracerProvider, cfg.MeterProvider = tp, mp
	return velox.Open(velox.Postgres, p.open(), append(opts, veloxotel.Options(cfg)...)...), sp, exp, r
}

// durationPoint returns the one db.client.operation.duration point r holds.
func durationPoint(t *testing.T, r *sdkmetric.ManualReader) attribute.Set {
	t.Helper()
	dps := points(t, r, "db.client.operation.duration", "s", wantDurationBounds)
	if len(dps) != 1 {
		t.Fatalf("%d duration points, want 1", len(dps))
	}
	return dps[0].Attributes
}

var targetKeys = []attribute.Key{"db.namespace", "server.address", "server.port"}

// TestTargetAttributes: Config.Target's db.namespace, server.address and
// server.port are on a statement's span from its start, and on its duration
// point; a zero field leaves its attribute off. TargetFor's are set once the
// route is final, so not at the start, and are on the point too. The
// transaction's span has them too, from its start: its route is known then.
func TestTargetAttributes(t *testing.T) {
	for _, c := range []struct {
		name   string
		target veloxotel.Target
		want   map[attribute.Key]attribute.Value
	}{
		{"all three", veloxotel.Target{Namespace: "billing|public", Address: "db.internal", Port: 6432}, map[attribute.Key]attribute.Value{
			"db.namespace":   attribute.StringValue("billing|public"),
			"server.address": attribute.StringValue("db.internal"),
			"server.port":    attribute.Int64Value(6432),
		}},
		{"no port", veloxotel.Target{Address: "db.internal"}, map[attribute.Key]attribute.Value{
			"server.address": attribute.StringValue("db.internal"),
		}},
		{"a port without an address", veloxotel.Target{Port: 6432}, map[attribute.Key]attribute.Value{}},
		{"zero", veloxotel.Target{}, map[attribute.Key]attribute.Value{}},
	} {
		for _, perRoute := range []bool{false, true} {
			name, cfg := c.name+", Target", veloxotel.Config{Target: c.target}
			if perRoute {
				name, cfg = c.name+", TargetFor", veloxotel.Config{TargetFor: func(velox.Route) veloxotel.Target { return c.target }}
			}
			t.Run(name, func(t *testing.T) {
				db, sp, exp, r := observed(t, &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}, cfg)
				if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); err != nil {
					t.Fatal(err)
				}
				point := durationPoint(t, r)
				if err := db.Tx(ctx, func(context.Context, *velox.DB) error { return nil }); err != nil {
					t.Fatal(err)
				}
				spans := byName(exp.GetSpans())
				if len(sp.got) != 2 || len(spans["SELECT invoices"]) != 1 || len(spans["velox.tx"]) != 1 {
					t.Fatalf("%d spans started, ended %v", len(sp.got), spans)
				}
				end, tx := attrs(spans["SELECT invoices"][0]), attrs(spans["velox.tx"][0])
				for _, k := range targetKeys {
					want, ok := c.want[k]
					if got, has := sp.got[0][k]; perRoute && has {
						t.Errorf("TargetFor: at start, %s = %v, want none until the route is final", k, got.String())
					} else if !perRoute && (has != ok || got != want) {
						t.Errorf("at start, %s = %v (%v), want %v (%v)", k, got.String(), has, want.String(), ok)
					}
					if got, has := end[k]; has != ok || got != want {
						t.Errorf("at end, %s = %v (%v), want %v (%v)", k, got.String(), has, want.String(), ok)
					}
					if got, has := point.Value(k); has != ok || got != want {
						t.Errorf("duration point: %s = %v (%v), want %v (%v)", k, got.String(), has, want.String(), ok)
					}
					if got, has := sp.got[1][k]; has != ok || got != want {
						t.Errorf("velox.tx at start: %s = %v (%v), want %v (%v)", k, got.String(), has, want.String(), ok)
					}
					if got, has := tx[k]; has != ok || got != want {
						t.Errorf("velox.tx: %s = %v (%v), want %v (%v)", k, got.String(), has, want.String(), ok)
					}
				}
			})
		}
	}
}

// TestTargetForFallback pins that TargetFor names the server per route, once
// the route is final. A read that fell over from a failed replica to the
// primary names only the primary on its span and its duration point: none of
// the replica's fields.
func TestTargetForFallback(t *testing.T) {
	var routes []velox.Route
	cfg := veloxotel.Config{
		Target: veloxotel.Target{Address: "unused"}, // TargetFor is used instead
		TargetFor: func(r velox.Route) veloxotel.Target {
			routes = append(routes, r)
			if r.Replica >= 0 {
				return veloxotel.Target{Namespace: "replica-only", Address: "replica", Port: 5433}
			}
			return veloxotel.Target{Address: "primary"}
		},
	}
	read := func(t *testing.T, r *fakeDB) (map[attribute.Key]attribute.Value, map[attribute.Key]attribute.Value, attribute.Set) {
		t.Helper()
		routes = nil
		p := &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}
		db, sp, exp, mr := observed(t, p, cfg, velox.WithReplicas(velox.SQLConn(r.open())))
		if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); err != nil {
			t.Fatal(err)
		}
		if len(sp.got) != 1 {
			t.Fatalf("%d spans started", len(sp.got))
		}
		return sp.got[0], attrs(only(t, exp)), durationPoint(t, mr)
	}
	t.Run("fallback", func(t *testing.T) {
		start, end, point := read(t, &fakeDB{errs: badConn()})
		for _, k := range targetKeys {
			if v, has := start[k]; has {
				t.Errorf("at start, %s = %v, want none", k, v.String())
			}
		}
		if end["server.address"] != attribute.StringValue("primary") || end["velox.route.fallback"] != attribute.BoolValue(true) {
			t.Errorf("at end, server.address %v, fallback %v; want primary, true",
				end["server.address"].String(), end["velox.route.fallback"].String())
		}
		for _, k := range []attribute.Key{"db.namespace", "server.port"} {
			if v, has := end[k]; has {
				t.Errorf("at end, the replica's %s = %v", k, v.String())
			}
			if v, has := point.Value(k); has {
				t.Errorf("duration point: the replica's %s = %v", k, v.String())
			}
		}
		if v, _ := point.Value("server.address"); v != attribute.StringValue("primary") {
			t.Errorf("duration point: server.address = %v, want primary", v.String())
		}
		if len(routes) != 1 || routes[0].Replica != -1 || !routes[0].Fallback {
			t.Errorf("TargetFor was called with %+v, want once, with the primary's fallback", routes)
		}
	})
	t.Run("replica", func(t *testing.T) {
		_, end, point := read(t, &fakeDB{rows: [][]driver.Value{invoiceRow(1)}})
		if end["server.address"] != attribute.StringValue("replica") || end["server.port"] != attribute.Int64Value(5433) {
			t.Errorf("at end, server %v:%v, want replica:5433", end["server.address"].String(), end["server.port"].String())
		}
		if v, _ := point.Value("server.address"); v != attribute.StringValue("replica") {
			t.Errorf("duration point: server.address = %v, want replica", v.String())
		}
		if len(routes) != 1 {
			t.Errorf("TargetFor was called %d times, want once: the route did not change", len(routes))
		}
	})
}

// TestStatusCode: a failed statement's span and duration point carry the
// driver's code as db.response.status_code, and error.type is velox's value,
// or the code where velox's would be _OTHER. A success carries no code.
func TestStatusCode(t *testing.T) {
	for _, c := range []struct {
		name    string
		err     error
		code    string // "": none
		errType string
	}{
		{"serialization failure", &pgconn.PgError{Code: "40001"}, "40001", "40001"},
		{"unique violation", &pgconn.PgError{Code: "23505", ConstraintName: "invoices_uq"}, "23505", "constraint.unique"},
		{"no driver code", errors.New("boom"), "", "_OTHER"},
		{"success", nil, "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			db, _, exp, r := observed(t, &fakeDB{rows: [][]driver.Value{invoiceRow(1)}, errs: []error{c.err}}, veloxotel.Config{})
			if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); (err != nil) != (c.err != nil) {
				t.Fatalf("err %v, want %v", err, c.err)
			}
			span, point := attrs(only(t, exp)), durationPoint(t, r)
			for k, want := range map[attribute.Key]string{"db.response.status_code": c.code, "error.type": c.errType} {
				got, has := span[k]
				if has != (want != "") || got.AsString() != want {
					t.Errorf("span: %s = %q (%v), want %q", k, got.AsString(), has, want)
				}
				got, has = point.Value(k)
				if has != (want != "") || got.AsString() != want {
					t.Errorf("duration point: %s = %q (%v), want %q", k, got.AsString(), has, want)
				}
			}
		})
	}
}

// TestRawSpanNames pins the name of a raw SQL span, which has no summary:
// semconv v1.43.0's {target}, that is db.namespace, else
// server.address:server.port, else the database system.
func TestRawSpanNames(t *testing.T) {
	for _, c := range []struct {
		name   string
		target veloxotel.Target
		want   string
	}{
		{"none", veloxotel.Target{}, "postgresql"},
		{"namespace", veloxotel.Target{Namespace: "billing", Address: "db", Port: 5432}, "billing"},
		{"address and port", veloxotel.Target{Address: "db.internal", Port: 5432}, "db.internal:5432"},
		{"address", veloxotel.Target{Address: "db.internal"}, "db.internal"},
		{"port only", veloxotel.Target{Port: 5432}, "postgresql"},
	} {
		t.Run(c.name, func(t *testing.T) {
			db, exp, _ := traced(t, &fakeDB{affected: 1}, veloxotel.Config{Target: c.target})
			if _, err := db.Exec(context.Background(), "cleanup", "DELETE FROM invoices"); err != nil {
				t.Fatal(err)
			}
			if got := only(t, exp).Name; got != c.want {
				t.Errorf("span %q, want %q", got, c.want)
			}
		})
	}

	t.Run("TargetFor", func(t *testing.T) {
		db, exp, _ := traced(t, &fakeDB{affected: 1}, veloxotel.Config{
			TargetFor: func(velox.Route) veloxotel.Target { return veloxotel.Target{Namespace: "primary-db"} },
		})
		if _, err := db.Exec(context.Background(), "cleanup", "DELETE FROM invoices"); err != nil {
			t.Fatal(err)
		}
		if got := only(t, exp).Name; got != "primary-db" {
			t.Errorf("span %q, want primary-db", got)
		}
	})
}
