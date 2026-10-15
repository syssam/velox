package veloxotel_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/syssam/velox2"
	veloxotel "github.com/syssam/velox2/contrib/otel"
	"github.com/syssam/velox2/internal/testmodel"
)

var (
	ctx = velox.AllTenants(context.Background(), "otel tests")
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
		"db.system.name":            attribute.StringValue("postgresql"),
		"db.operation.name":         attribute.StringValue("SELECT"),
		"db.collection.name":        attribute.StringValue("invoices"),
		"db.response.returned_rows": attribute.Int64Value(2),
		"velox.entity":              attribute.StringValue("testmodel.Invoice"),
		"velox.tenant_bypass":       attribute.StringValue("otel tests"),
		"velox.tag.route":           attribute.StringValue("GET /invoices"),
		"velox.tag.team":            attribute.StringValue("billing"),
		"code.file.path":            attribute.StringValue("statement_test.go"),
		"code.line.number":          attribute.Int64Value(int64(line)),
		"code.function.name":        attribute.StringValue("github.com/syssam/velox2/contrib/otel_test.TestSelectSpan"),
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
	for _, k := range []attribute.Key{"velox.affected_rows", "velox.count", "velox.tenant", "velox.policy_bypass", "error.type"} {
		if _, ok := a[k]; ok {
			t.Errorf("unexpected %s", k)
		}
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
			db, exp, _ := traced(t, c.f, veloxotel.Config{})
			if err := c.run(db); err != nil {
				t.Fatal(err)
			}
			s := only(t, exp)
			if s.Name != c.span || s.SpanKind != trace.SpanKindClient {
				t.Errorf("span %q kind %v, want %q", s.Name, s.SpanKind, c.span)
			}
			a := attrs(s)
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

func TestPolicyBypass(t *testing.T) {
	f := &fakeDB{cols: []string{}}
	db, exp, _ := traced(t, f, veloxotel.Config{})
	if _, err := db.Query[testmodel.Ticket]().Limit(1).All(velox.SkipPolicy(ctx, "support")); err != nil {
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

// TestParent: in a transaction a statement's span is a child of the span on
// the transaction's context, even when the statement runs on another
// context; outside one, of the span on the statement's context.
func TestParent(t *testing.T) {
	var tr trace.Tracer
	txSpan := velox.WithTxAround(func(ctx context.Context, _ *velox.TxInfo, next func(context.Context) error) error {
		ctx, s := tr.Start(ctx, "tx")
		defer s.End()
		return next(ctx)
	})
	db, exp, tp := traced(t, &fakeDB{}, veloxotel.Config{}, txSpan)
	tr = tp.Tracer("t")

	err := db.Tx(ctx, func(tx *velox.DB) error {
		_, err := tx.Query[testmodel.Invoice]().Limit(1).All(ctx) // the outer ctx: no span
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
	if len(stmts) != 2 {
		t.Fatalf("%d statement spans, want 2", len(stmts))
	}
	if got, want := stmts[0].Parent.SpanID(), byName["tx"].SpanContext.SpanID(); !want.IsValid() || got != want {
		t.Errorf("in the transaction: parent %v, want tx %v", got, want)
	}
	if got, want := stmts[1].Parent.SpanID(), byName["req"].SpanContext.SpanID(); !want.IsValid() || got != want {
		t.Errorf("outside: parent %v, want req %v", got, want)
	}
}

// TestIterPanicEndsTheSpan is Review Focus 3: a loop body's panic ends the
// span with status Error and the description "panic", and reaches the
// caller unchanged.
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
}
