package gqlgenx_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
)

// budgetSDL is a schema of the leaves a response counts, and a field that fails.
const budgetSDL = `
scalar JSON
scalar Bytes
type Query {
	text: String
	must: String!
	bytes: Bytes
	json: JSON
	ints: [String!]
	fail: Int
	slow: Int
}
type Mutation { add(id: Int!): Int }
type Subscription { slow: Int }
`

func aliases(n int, field string) string {
	var b strings.Builder
	b.WriteString("{")
	for i := range n {
		fmt.Fprintf(&b, " a%d: %s", i, field)
	}
	b.WriteString(" }")
	return b.String()
}

// TestResponseBytesBounded: the values a response holds are counted as they resolve; past
// MaxResponseBytes the request fails with one clean error and data null, and at it, it
// answers. Each leaf type counts what it writes: a string as it is escaped.
func TestResponseBytesBounded(t *testing.T) {
	db, _ := handlerDB(t)
	type name string
	kb := strings.Repeat("x", 1000)
	for _, c := range []struct {
		field string
		value any
		n     int // aliases at the limit of 10000: each writes this much
	}{
		{"text", kb, 10},
		{"text", &kb, 10},
		{"text", name(kb), 10},
		{"bytes", veloxgql.Bytes(kb), 7},           // base64 is 4/3: 1336 a value
		{"bytes", []byte(kb), 7},                   //
		{"json", veloxgql.JSON(`"` + kb + `"`), 9}, // 1002 a value
		{"ints", []string{kb}, 10},
		{"ints", []*string{&kb}, 10},                 // a [String] list of the application's
		{"ints", &[]string{kb}, 10},                  //
		{"json", json.RawMessage(`"` + kb + `"`), 9}, // as it is, as JSON is
		{"text", strings.Repeat("\x01", 500), 3},     // \u0001 is six bytes: 3000 a value
		{"text", strings.Repeat(`"`, 500), 10},       // \" is two: 1000 a value
	} {
		value := c.value
		srv := serveSchema(testSchemaOf(budgetSDL, map[string]resolver{c.field: func(context.Context, map[string]any) (any, error) { return value, nil }}),
			db, gqlgenx.WithLimits(veloxgql.Limits{MaxResponseBytes: 10000}))
		if got := post(t, context.Background(), srv, aliases(c.n, c.field)); strings.Contains(got, "errors") {
			t.Errorf("%T at the limit: %.200s", value, got)
		}
		got := post(t, context.Background(), srv, aliases(c.n+1, c.field))
		if !strings.Contains(got, `"code":"COST_LIMIT_EXCEEDED"`) || !strings.Contains(got, veloxgql.ErrResponseTooLarge.Error()) || !strings.Contains(got, `"data":null`) || strings.Contains(got, "xxx") {
			t.Errorf("%T over the limit: %.300s", value, got)
		}
	}
}

// TestResponseBytesStop: past the limit no more resolvers run, and a mutation rolls back
// though no field failed. The fields of a mutation run one after another.
func TestResponseBytesStop(t *testing.T) {
	db, l := handlerDB(t)
	resolvers := handlerResolvers(db, nil)
	var calls atomic.Int64
	resolvers["add"] = func(ctx context.Context, args map[string]any) (any, error) {
		calls.Add(1)
		tx, err := veloxgql.DBFromContext(ctx)
		if err != nil {
			return nil, err
		}
		if err := tx.Insert(&person{ID: args["id"].(int64), Name: "p", Age: 1}).Exec(ctx); err != nil {
			return nil, err
		}
		return strings.Repeat("x", 1000), nil
	}
	srv := serve(db, resolvers, gqlgenx.WithLimits(veloxgql.Limits{MaxResponseBytes: 1500}))
	got := post(t, context.Background(), srv, `mutation { a: add(id: 1) b: add(id: 2) c: add(id: 3) d: add(id: 4) }`)
	if !strings.Contains(got, veloxgql.ErrResponseTooLarge.Error()) || !strings.Contains(got, `"data":null`) {
		t.Errorf("over: %.300s", got)
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("%d resolvers ran, want 2", n)
	}
	if ids := peopleIDs(t, db, l); len(ids) != 0 {
		t.Errorf("rows kept: %v", ids)
	}
}

// TestResolverErrorsBounded: a response holds MaxErrors of its fields' errors and one that
// says there are more, then the request stops: 97k of them took 516 MB. A field that is
// null for what stopped it adds none.
func TestResolverErrorsBounded(t *testing.T) {
	db, _ := handlerDB(t)
	boom := errors.New("secret")
	for name, presented := range map[string]bool{"ErrorPresenter": true, "another presenter": false} {
		var calls atomic.Int64
		srv := serveSchema(testSchemaOf(budgetSDL, map[string]resolver{
			"fail": func(context.Context, map[string]any) (any, error) { calls.Add(1); return nil, boom },
			// As generated code does for a non-null field that has no value.
			"must": func(ctx context.Context, _ map[string]any) (any, error) {
				graphql.AddError(ctx, boom)
				return nil, nil
			},
		}), db, gqlgenx.WithLimits(veloxgql.Limits{MaxErrors: 3, MaxCost: 1 << 30}))
		if presented {
			srv.SetErrorPresenter(gqlgenx.ErrorPresenter(func(context.Context, error) {}))
		}
		var resp struct {
			Data   any
			Errors []struct {
				Message    string
				Extensions map[string]any
			}
		}
		if err := json.Unmarshal([]byte(post(t, context.Background(), srv, aliases(3, "fail"))), &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Errors) != 3 || resp.Data == nil {
			t.Errorf("%s: at the limit: %+v", name, resp)
		}
		resp.Data, resp.Errors = nil, nil
		if err := json.Unmarshal([]byte(post(t, context.Background(), srv, aliases(2000, "fail"))), &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Errors) != 4 || resp.Data != nil {
			t.Fatalf("%s: over the limit: %+v", name, resp)
		}
		last := resp.Errors[3]
		if !strings.Contains(last.Message, veloxgql.ErrTooManyErrors.Error()) || last.Extensions["code"] != veloxgql.CodeTooCostly {
			t.Errorf("%s: the last: %+v", name, last)
		}
		// An error no resolver returned is held to it under ErrorPresenter, as it is made.
		resp.Data, resp.Errors = nil, nil
		if err := json.Unmarshal([]byte(post(t, context.Background(), srv, aliases(2000, "must"))), &resp); err != nil {
			t.Fatal(err)
		}
		if presented && (len(resp.Errors) != 4 || resp.Data != nil) {
			t.Errorf("%s: must not be null: %+v", name, resp)
		}
	}
}

// TestTimeout: a request past Limits.Timeout has its context cancelled, what it runs
// ends, and the client gets one clean error. A mutation rolls back; a subscription, which
// lives as long as it is listened to, has no such limit.
func TestTimeout(t *testing.T) {
	db, l := handlerDB(t)
	resolvers := handlerResolvers(db, nil)
	slow := func(ctx context.Context, _ map[string]any) (any, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(30 * time.Second):
			return 1, nil
		}
	}
	resolvers["slow"] = slow
	addSlow := func(ctx context.Context, args map[string]any) (any, error) {
		tx, err := veloxgql.DBFromContext(ctx)
		if err != nil {
			return nil, err
		}
		if err := tx.Insert(&person{ID: args["id"].(int64), Name: "p", Age: 1}).Exec(ctx); err != nil {
			return nil, err
		}
		return slow(ctx, nil)
	}
	resolvers["add"] = addSlow
	srv := handler.New(testSchemaOf(budgetSDL, resolvers))
	h := gqlgenx.Setup(srv, db, gqlgenx.WithLimits(veloxgql.Limits{Timeout: 200 * time.Millisecond}))
	srv.AddTransport(transport.POST{})
	const want = `{"errors":[{"message":"request timed out: it ran over 200ms","extensions":{"code":"TIMEOUT"}}],"data":null}`
	start := time.Now()
	if got := post(t, context.Background(), h, `{ slow }`); got != want {
		t.Errorf("a query: %s", got)
	}
	if got := post(t, context.Background(), h, `mutation { add(id: 1) }`); got != want {
		t.Errorf("a mutation: %s", got)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("took %v", d)
	}
	if ids := peopleIDs(t, db, l); len(ids) != 0 {
		t.Errorf("the mutation kept %v", ids)
	}
	// Quick: a request under it does not notice.
	resolvers["slow"] = func(context.Context, map[string]any) (any, error) { return 1, nil }
	if got := post(t, context.Background(), h, `{ slow }`); got != `{"data":{"slow":1}}` {
		t.Errorf("under it: %s", got)
	}
	// A client that went away, or a deadline of the application's, is no Timeout.
	short, cancelShort := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancelShort()
	if got := post(t, short, h, `{ slow }`); strings.Contains(got, "TIMEOUT") {
		t.Errorf("the application's deadline: %s", got)
	}
	// A transaction's own deadline, velox.TxTimeout, is the mutation's error,
	// and not the Timeout.
	tx := handler.New(testSchemaOf(budgetSDL, resolvers))
	txh := gqlgenx.Setup(tx, db, gqlgenx.WithLimits(veloxgql.Limits{Timeout: time.Hour}), gqlgenx.WithTxOptions(velox.TxTimeout(50*time.Millisecond)))
	tx.AddTransport(transport.POST{})
	resolvers["slow"] = slow
	if got := post(t, context.Background(), txh, `mutation { add(id: 2) }`); strings.Contains(got, "TIMEOUT") || !strings.Contains(got, `"data":null`) {
		t.Errorf("the transaction's deadline: %s", got)
	}
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	resolvers["slow"] = slow
	if got := post(t, gone, h, `{ slow }`); strings.Contains(got, "TIMEOUT") {
		t.Errorf("a client gone: %s", got)
	}
}

// TestTimeoutIsNotTooManyErrors: at the deadline every field running fails at once, which
// is over MaxErrors; the client is told of the Timeout. Under -race on a real query it
// answered 100 internal errors and ErrTooManyErrors.
func TestTimeoutIsNotTooManyErrors(t *testing.T) {
	db, _ := handlerDB(t)
	logged := atomic.Int64{}
	srv := handler.New(testSchemaOf(budgetSDL, map[string]resolver{"fail": func(ctx context.Context, _ map[string]any) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}))
	h := gqlgenx.Setup(srv, db, gqlgenx.WithLimits(veloxgql.Limits{Timeout: 50 * time.Millisecond, MaxErrors: 3}))
	srv.SetErrorPresenter(gqlgenx.ErrorPresenter(func(context.Context, error) { logged.Add(1) }))
	srv.AddTransport(transport.POST{})
	const want = `{"errors":[{"message":"request timed out: it ran over 50ms","extensions":{"code":"TIMEOUT"}}],"data":null}`
	for range 20 {
		if got := post(t, context.Background(), h, aliases(200, "fail")); got != want {
			t.Fatalf("%.300s", got)
		}
	}
	if n := logged.Load(); n != 0 {
		t.Errorf("%d cancelled fields logged as internal errors", n)
	}
}

// TestSubscriptionHasNoTimeout: Limits.Timeout leaves a subscription's context alone.
func TestSubscriptionHasNoTimeout(t *testing.T) {
	db, _ := handlerDB(t)
	var deadline atomic.Bool
	srv := serveSchema(testSchemaOf(budgetSDL, map[string]resolver{"slow": func(ctx context.Context, _ map[string]any) (any, error) {
		_, has := ctx.Deadline()
		deadline.Store(has)
		return 1, nil
	}}), db, gqlgenx.WithLimits(veloxgql.Limits{Timeout: time.Hour}))
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"query":"subscription { slow }"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "text/event-stream")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if deadline.Load() {
		t.Error("a subscription's event has a deadline")
	}
	if got := w.Body.String(); !strings.Contains(got, `"slow":1`) {
		t.Errorf("no event: %s", got)
	}
}

// TestLimitsNegativeNewOnes: Validate refuses a negative Timeout or MaxResponseBytes.
func TestLimitsNegativeNewOnes(t *testing.T) {
	db, _ := handlerDB(t)
	for name, l := range map[string]veloxgql.Limits{"Timeout": {Timeout: -1}, "MaxResponseBytes": {MaxResponseBytes: -1}} {
		e := gqlgenx.NewExtension(db, gqlgenx.WithLimits(l))
		if err := e.Validate(nil); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestStoppedStreamEnds: over a streaming transport gqlgen asks for responses until it
// gets none. A query or mutation stopped by a limit sends its one error and ends: the
// replacement is not made again for the end of the stream, which over SSE and websocket
// sent error frames without end and held the handler.
func TestStoppedStreamEnds(t *testing.T) {
	db, _ := handlerDB(t)
	resolvers := handlerResolvers(db, nil)
	wait := func(ctx context.Context, _ map[string]any) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	resolvers["slow"], resolvers["add"] = wait, wait
	srv := handler.New(testSchemaOf(budgetSDL, resolvers))
	h := gqlgenx.Setup(srv, db, gqlgenx.WithLimits(veloxgql.Limits{Timeout: 50 * time.Millisecond}))
	srv.AddTransport(transport.SSE{})
	for _, q := range []string{`{ slow }`, `mutation { add(id: 1) }`} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		body := strings.NewReader(`{"query":` + strconvQuote(q) + `}`)
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/", body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		w := httptest.NewRecorder()
		done := make(chan struct{})
		go func() { defer close(done); h.ServeHTTP(w, req) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			cancel()
			t.Fatalf("%s: the stream did not end", q) // its handler never returns, so no wait
		}
		cancel()
		if n := strings.Count(w.Body.String(), "event: next"); n != 1 {
			t.Errorf("%s: %d events, want 1:\n%.500s", q, n, w.Body.String())
		}
	}
}

func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }
