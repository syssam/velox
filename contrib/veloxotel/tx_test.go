package veloxotel_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"maps"
	"reflect"
	"testing"

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

func byName(spans tracetest.SpanStubs) map[string][]tracetest.SpanStub {
	m := map[string][]tracetest.SpanStub{}
	for _, s := range spans {
		m[s.Name] = append(m[s.Name], s)
	}
	return m
}

// TestTxSpanTree: fn runs its statements on its
// ctx, the transaction's: they nest under velox.tx, and velox.tx under the
// request.
func TestTxSpanTree(t *testing.T) {
	// Attempt 1: BEGIN, the SELECT, then the UPDATE fails on a serialization
	// failure; attempt 2 runs all three cleanly.
	f := &fakeDB{rows: [][]driver.Value{invoiceRow(1)}, affected: 1, errs: []error{nil, nil, &pgconn.PgError{Code: "40001"}}}
	db, exp, tp := traced(t, f, veloxotel.Config{})
	req, root := tp.Tracer("t").Start(ctx, "POST /pay")
	err := db.Tx(req, func(ctx context.Context, tx *velox.DB) error {
		if _, err := tx.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx); err != nil {
			return err
		}
		_, err := tx.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.ID.EQ(1)).Exec(ctx)
		return err
	}, velox.TxRetrySerializable(1))
	root.End()
	if err != nil {
		t.Fatal(err)
	}
	spans := byName(exp.GetSpans())
	tx := spans["velox.tx"]
	if len(tx) != 1 || tx[0].Parent.SpanID() != root.SpanContext().SpanID() {
		t.Fatalf("velox.tx spans %v", tx)
	}
	if tx[0].SpanKind != trace.SpanKindInternal || tx[0].Status.Code != codes.Unset {
		t.Errorf("velox.tx kind %v status %v", tx[0].SpanKind, tx[0].Status)
	}
	a := attrs(tx[0])
	if a["velox.tx.attempts"] != attribute.IntValue(2) || a["velox.tx.outcome"] != attribute.StringValue("commit") ||
		a["velox.tx.isolation"] != attribute.StringValue("Default") || a["velox.tx.read_only"] != attribute.BoolValue(false) {
		t.Errorf("velox.tx attributes %v", tx[0].Attributes)
	}
	if ev := tx[0].Events; len(ev) != 1 || ev[0].Name != "retry" || !ev[0].Time.Before(tx[0].EndTime) {
		t.Errorf("events %v", ev)
	} else if len(ev[0].Attributes) != 1 || ev[0].Attributes[0] != attribute.String("error.type", "40001") {
		t.Errorf("retry event attributes %v", ev[0].Attributes)
	}
	if len(spans["SELECT invoices"]) != 2 || len(spans["UPDATE invoices"]) != 2 {
		t.Fatalf("statement spans: %d SELECT, %d UPDATE", len(spans["SELECT invoices"]), len(spans["UPDATE invoices"]))
	}
	for _, s := range append(spans["SELECT invoices"], spans["UPDATE invoices"]...) {
		if s.Parent.SpanID() != tx[0].SpanContext.SpanID() {
			t.Errorf("%s's parent is not velox.tx", s.Name)
		}
	}
	if failed := attrs(spans["UPDATE invoices"][0]); failed["error.type"] != attribute.StringValue("40001") ||
		failed["db.response.status_code"] != attribute.StringValue("40001") {
		t.Errorf("the failed UPDATE: %v", spans["UPDATE invoices"][0].Attributes)
	}
}

// TestTxOptions: the span carries the isolation level and read-only flag the
// transaction was given.
func TestTxOptions(t *testing.T) {
	db, exp, _ := traced(t, &fakeDB{}, veloxotel.Config{})
	err := db.Tx(ctx, func(context.Context, *velox.DB) error { return nil }, velox.TxIsolation(sql.LevelSerializable), velox.TxReadOnly())
	if err != nil {
		t.Fatal(err)
	}
	a := attrs(only(t, exp))
	if a["velox.tx.isolation"] != attribute.StringValue("Serializable") || a["velox.tx.read_only"] != attribute.BoolValue(true) ||
		a["velox.tx.attempts"] != attribute.IntValue(1) {
		t.Errorf("velox.tx attributes %v", a)
	}
}

// TestTxOutcomes: how the transaction ended sets velox.tx.outcome, and a
// failed one has the status Error, an error.type and an exception event.
func TestTxOutcomes(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name    string
		f       *fakeDB
		fn      func(ctx context.Context, tx *velox.DB) error
		outcome string
		errType string
	}{
		{"rollback", &fakeDB{}, func(context.Context, *velox.DB) error { return boom }, "rollback", "_OTHER"},
		{"commit fails", &fakeDB{commitErr: boom}, func(context.Context, *velox.DB) error { return nil }, "error", "_OTHER"},
		{"begin fails", &fakeDB{errs: []error{boom}}, func(context.Context, *velox.DB) error { return nil }, "error", "_OTHER"},
		{
			// Ticket's policy denies a context that names no user (this one
			// has an org, and no policy bypass): the refusal comes before
			// execution, so it makes no statement span and is seen only on
			// the transaction it ends.
			"denied", &fakeDB{},
			func(_ context.Context, tx *velox.DB) error {
				_, err := tx.Query[testmodel.Ticket]().Limit(1).All(testmodel.Org.With(context.Background(), 1))
				return err
			},
			"rollback", "velox.denied",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db, exp, _ := traced(t, c.f, veloxotel.Config{})
			err := db.Tx(ctx, c.fn)
			if err == nil {
				t.Fatal("no error")
			}
			s := only(t, exp)
			if s.Name != "velox.tx" {
				t.Fatalf("span %q", s.Name)
			}
			a := attrs(s)
			if a["velox.tx.outcome"] != attribute.StringValue(c.outcome) || a["error.type"] != attribute.StringValue(c.errType) {
				t.Errorf("outcome %v, error.type %v; want %s, %s", a["velox.tx.outcome"].String(), a["error.type"].String(), c.outcome, c.errType)
			}
			if s.Status.Code != codes.Error || s.Status.Description != err.Error() {
				t.Errorf("status %v, want Error %q", s.Status, err)
			}
			if msg := exception(s); msg != err.Error() {
				t.Errorf("exception.message %q, want %q", msg, err)
			}
		})
	}
}

// TestPanicsEndTheSpans pins that a panic in fn, or in an Iter loop body inside
// fn, ends every open span with status Error and the description "panic", and
// reaches the caller unchanged.
func TestPanicsEndTheSpans(t *testing.T) {
	t.Run("fn", func(t *testing.T) {
		db, exp, _ := traced(t, &fakeDB{}, veloxotel.Config{})
		got := func() (v any) {
			defer func() { v = recover() }()
			_ = db.Tx(ctx, func(context.Context, *velox.DB) error { panic("in fn") })
			return nil
		}()
		if got != "in fn" {
			t.Errorf("recovered %v", got)
		}
		s := only(t, exp)
		if s.Name != "velox.tx" || s.Status.Code != codes.Error || s.Status.Description != "panic" {
			t.Errorf("span %q status %v", s.Name, s.Status)
		}
		if got := attrs(s)["error.type"].AsString(); got != "_OTHER" {
			t.Errorf("error.type %q, want _OTHER", got)
		}
		if a := attrs(s); a["velox.tx.outcome"] != attribute.StringValue("rollback") || a["velox.tx.attempts"] != attribute.IntValue(1) {
			t.Errorf("velox.tx attributes %v", s.Attributes)
		}
	})
	t.Run("Iter", func(t *testing.T) {
		db, exp, _ := traced(t, &fakeDB{rows: [][]driver.Value{invoiceRow(1)}}, veloxotel.Config{})
		got := func() (v any) {
			defer func() { v = recover() }()
			_ = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
				for _, err := range tx.Query[testmodel.Invoice]().Limit(5).Iter(ctx) {
					if err != nil {
						return err
					}
					panic("in the loop")
				}
				return nil
			})
			return nil
		}()
		if got != "in the loop" {
			t.Errorf("recovered %v", got)
		}
		spans := byName(exp.GetSpans())
		stmt, tx := spans["SELECT invoices"], spans["velox.tx"]
		if len(stmt) != 1 || len(tx) != 1 {
			t.Fatalf("%d statement spans, %d velox.tx spans", len(stmt), len(tx))
		}
		for _, s := range []tracetest.SpanStub{stmt[0], tx[0]} {
			if s.Status.Code != codes.Error || s.Status.Description != "panic" {
				t.Errorf("%s: status %v", s.Name, s.Status)
			}
			if got := attrs(s)["error.type"].AsString(); got != "_OTHER" {
				t.Errorf("%s: error.type %q, want _OTHER", s.Name, got)
			}
		}
		if stmt[0].Parent.SpanID() != tx[0].SpanContext.SpanID() {
			t.Error("the statement's parent is not velox.tx")
		}
		if a := attrs(tx[0]); a["velox.tx.outcome"] != attribute.StringValue("rollback") {
			t.Errorf("velox.tx.outcome %v", a["velox.tx.outcome"].String())
		}
	})
}

// TestSavepointNoSpan: a nested Tx is a savepoint, not a transaction; it
// adds no velox.tx span, and its statements nest under the transaction's.
func TestSavepointNoSpan(t *testing.T) {
	db, exp, _ := traced(t, &fakeDB{}, veloxotel.Config{})
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		return tx.Tx(ctx, func(ctx context.Context, sp *velox.DB) error {
			_, err := sp.Query[testmodel.Invoice]().Limit(1).All(ctx)
			return err
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	spans := byName(exp.GetSpans())
	tx, stmt := spans["velox.tx"], spans["SELECT invoices"]
	if len(tx) != 1 || len(stmt) != 1 {
		t.Fatalf("%d velox.tx spans, %d statement spans", len(tx), len(stmt))
	}
	if stmt[0].Parent.SpanID() != tx[0].SpanContext.SpanID() {
		t.Error("the savepoint's statement's parent is not velox.tx")
	}
}

// TestTxAroundAlone: TxAroundOf gives the transaction span without the
// statement hook.
func TestTxAroundAlone(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	db := velox.Open(velox.Postgres, (&fakeDB{}).open(), velox.WithTxAround(veloxotel.TxAroundOf(veloxotel.Config{TracerProvider: tp})))
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_, err := tx.Query[testmodel.Invoice]().Limit(1).All(ctx)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if s := only(t, exp); s.Name != "velox.tx" {
		t.Errorf("span %q", s.Name)
	}
}

// TestTxAroundInnerHookWithoutNext: an inner hook that returns without
// calling next leaves velox no outcome to report; the span and the metric
// say error, one of velox.tx.outcome's three values, not "unknown".
func TestTxAroundInnerHookWithoutNext(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	r := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(r))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()); _ = mp.Shutdown(context.Background()) })
	refuse := velox.WithTxAround(func(context.Context, *velox.TxInfo, func(context.Context) error) error { return nil })
	opts := append(veloxotel.Options(veloxotel.Config{TracerProvider: tp, MeterProvider: mp}), refuse)
	db := velox.Open(velox.Postgres, (&fakeDB{}).open(), opts...)
	if err := db.Tx(ctx, func(context.Context, *velox.DB) error { return nil }); err == nil {
		t.Fatal("no error")
	}
	if a := attrs(only(t, exp)); a["velox.tx.outcome"] != attribute.StringValue("error") {
		t.Errorf("velox.tx.outcome %q", a["velox.tx.outcome"].String())
	}
	for _, dp := range points(t, r, "velox.tx.duration", "s", wantDurationBounds) {
		if o, _ := dp.Attributes.Value("velox.tx.outcome"); o.AsString() != "error" {
			t.Errorf("velox.tx.duration's velox.tx.outcome %q", o.AsString())
		}
	}
}

// TestTxSpanRoute: the velox.tx span and its velox.tx.duration point name
// the transaction's shard and its server, TargetFor's for its route or
// Config.Target's.
func TestTxSpanRoute(t *testing.T) {
	none := func(context.Context, *velox.DB) error { return nil }
	t.Run("TargetFor", func(t *testing.T) {
		exp := tracetest.NewInMemoryExporter()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
		r := sdkmetric.NewManualReader()
		mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(r))
		t.Cleanup(func() { _ = tp.Shutdown(context.Background()); _ = mp.Shutdown(context.Background()) })
		cfg := veloxotel.Config{TracerProvider: tp, MeterProvider: mp, TargetFor: func(r velox.Route) veloxotel.Target {
			return veloxotel.Target{Namespace: "db_" + r.Shard, Address: "h"}
		}}
		db := velox.OpenShards(velox.Postgres, map[string]velox.Shard{
			"a": {Primary: velox.SQLConn((&fakeDB{}).open())},
			"b": {Primary: velox.SQLConn((&fakeDB{}).open())},
		}, append(veloxotel.Options(cfg), velox.WithShardFor(shardOf))...)
		if err := db.Tx(velox.WithShard(ctx, "a"), none); err != nil {
			t.Fatal(err)
		}
		want := map[attribute.Key]attribute.Value{
			"velox.route.shard": attribute.StringValue("a"),
			"db.namespace":      attribute.StringValue("db_a"),
			"server.address":    attribute.StringValue("h"),
		}
		a := attrs(only(t, exp))
		for k, v := range want {
			if a[k] != v {
				t.Errorf("velox.tx's %s = %q, want %q", k, a[k].String(), v.String())
			}
		}
		dps := points(t, r, "velox.tx.duration", "s", wantDurationBounds)
		if len(dps) != 1 {
			t.Fatalf("%d points, want 1", len(dps))
		}
		// The point's whole set: its outcome and flags, then where it ran,
		// as the span names it.
		full := map[attribute.Key]attribute.Value{
			"velox.tx.outcome":   attribute.StringValue("commit"),
			"velox.tx.read_only": attribute.BoolValue(false),
			"velox.tx.retried":   attribute.BoolValue(false),
		}
		maps.Copy(full, want)
		got := map[attribute.Key]attribute.Value{}
		for _, kv := range dps[0].Attributes.ToSlice() {
			got[kv.Key] = kv.Value
		}
		if !reflect.DeepEqual(got, full) {
			t.Errorf("velox.tx.duration's attributes %v, want %v", got, full)
		}
	})
	t.Run("Target", func(t *testing.T) {
		db, exp, _ := traced(t, &fakeDB{}, veloxotel.Config{Target: veloxotel.Target{Namespace: "n"}})
		if err := db.Tx(ctx, none); err != nil {
			t.Fatal(err)
		}
		a := attrs(only(t, exp))
		if a["db.namespace"] != attribute.StringValue("n") {
			t.Errorf("velox.tx's db.namespace = %q, want n", a["db.namespace"].String())
		}
		if v, ok := a["velox.route.shard"]; ok {
			t.Errorf("velox.tx's velox.route.shard = %q, want none", v.String())
		}
	})
}

// TestSavepointStatementsSeeTheTxRoute: a statement in a savepoint sees the
// transaction's TxInfo, its Route included, as the transaction's own
// statements do: the savepoint makes no TxInfo of its own.
func TestSavepointStatementsSeeTheTxRoute(t *testing.T) {
	var routes []velox.Route
	seen := velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		if st.Tx != nil {
			routes = append(routes, st.Tx.Route)
		}
		return next(ctx)
	})
	db := velox.OpenShards(velox.Postgres, map[string]velox.Shard{
		"a": {Primary: velox.SQLConn((&fakeDB{}).open())},
		"b": {Primary: velox.SQLConn((&fakeDB{}).open())},
	}, append(veloxotel.Options(veloxotel.Config{}), velox.WithShardFor(shardOf), seen)...)
	read := func(ctx context.Context, db *velox.DB) error {
		_, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx)
		return err
	}
	err := db.Tx(velox.WithShard(ctx, "b"), func(ctx context.Context, tx *velox.DB) error {
		if err := read(ctx, tx); err != nil {
			return err
		}
		return tx.Tx(ctx, read)
	})
	if err != nil {
		t.Fatal(err)
	}
	want := velox.Route{Shard: "b", Replica: -1}
	if len(routes) != 2 || routes[0] != want || routes[1] != want {
		t.Errorf("the statements' Tx.Route %+v, want %+v twice", routes, want)
	}
}
