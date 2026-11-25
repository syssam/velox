package gqlgenx_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
)

// limitDB is handlerDB with the limits l.
func limitDB(t *testing.T, l veloxgql.Limits) (*velox.DB, *txLog) {
	t.Helper()
	return handlerDB(t, veloxgql.WithLimits(l))
}

// TestExtensionNames: the names gqlgen's server knows the extensions by.
func TestExtensionNames(t *testing.T) {
	for want, ext := range map[string]graphql.HandlerExtension{
		"VeloxCostLimit":     gqlgenx.CostLimit(nil),
		"VeloxRequestLimit":  gqlgenx.RequestLimit(nil),
		"VeloxResponseLimit": gqlgenx.ResponseLimit(nil),
	} {
		if got := ext.ExtensionName(); got != want {
			t.Errorf("ExtensionName() = %q, want %q", got, want)
		}
	}
}

// TestTwoDBsDiffer: the limits are each DB's, not the process's. Two DBs,
// MaxPage 2 and 3, serve requests in parallel: first 3 fails with
// ErrPageTooLarge on the first and passes on the second. A mutation's
// transaction keeps its DB's limits.
func TestTwoDBsDiffer(t *testing.T) {
	dbs := map[int]*velox.DB{}
	srvs := map[int]*handler.Server{}
	for _, most := range []int{2, 3} {
		db, _ := limitDB(t, veloxgql.Limits{MaxPage: most})
		for id := int64(1); id <= 5; id++ {
			if err := db.Insert(&person{ID: id, Name: "p", Age: 1}).Exec(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		dbs[most] = db
		srvs[most] = serve(handlerResolvers(db, nil), gqlgenx.Transactioner{DB: db})
	}
	const tooLarge = `"message":"veloxgql: Person connection: page is over the maximum: first is 3, and the most is 2","path":["page"]`
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for _, q := range []string{`{ page(first: 3) }`, `mutation { page(first: 3) }`} {
				if got := post(t, context.Background(), srvs[2], q); !strings.Contains(got, tooLarge) {
					t.Errorf("MaxPage 2: %s: %s, want the error %s", q, got, tooLarge)
				}
				if got, want := post(t, context.Background(), srvs[3], q), `{"data":{"page":3}}`; got != want {
					t.Errorf("MaxPage 3: %s: %s, want %s", q, got, want)
				}
			}
			// With no first, the page is the DB's MaxPage.
			if got, want := post(t, context.Background(), srvs[2], `{ page }`), `{"data":{"page":2}}`; got != want {
				t.Errorf("MaxPage 2, no first: %s, want %s", got, want)
			}
		})
	}
	wg.Wait()
	// The error is ErrPageTooLarge.
	if _, err := handlerResolvers(dbs[2], nil)["page"](context.Background(), map[string]any{"first": int64(3)}); !errors.Is(err, veloxgql.ErrPageTooLarge) {
		t.Errorf("first 3 under MaxPage 2: %v, want ErrPageTooLarge", err)
	}
}

// TestTooCostlyRunsNothing: a request over MaxCost is refused before any
// resolver runs, with COST_LIMIT_EXCEEDED and data null (the server's
// presenter is gqlgen's here, which shows the message as it is; see
// TestTooCostlyPresented for ErrorPresenter); one at the limit runs.
func TestTooCostlyRunsNothing(t *testing.T) {
	db, l := limitDB(t, veloxgql.Limits{MaxCost: 202})
	srv := serve(handlerResolvers(db, nil), gqlgenx.Transactioner{DB: db}, gqlgenx.CostLimit(db))

	// people(first: 30) { totalCount } costs 2 fields and 2 statements, 202; a
	// __typename more is over.
	got := post(t, context.Background(), srv, `{ people(first: 30) { totalCount __typename } }`)
	const want = `{"errors":[{"message":"veloxgql: request is too costly: it costs 203, and the most is 202","extensions":{"code":"COST_LIMIT_EXCEEDED"}}],"data":null}`
	if got != want {
		t.Errorf("cost 203:\n got %s\nwant %s", got, want)
	}
	if stmts, _ := l.take(); len(stmts) != 0 {
		t.Errorf("cost 203 ran statements: %v", stmts)
	}

	got = post(t, context.Background(), srv, `{ people(first: 30) { totalCount } }`)
	if want := `{"data":{"people":{"totalCount":0}}}`; got != want {
		t.Errorf("cost 202:\n got %s\nwant %s", got, want)
	}
	if stmts, _ := l.take(); len(stmts) != 1 {
		t.Errorf("cost 202 ran %v, want one select", stmts)
	}

	// A mutation is checked as well, before its transaction.
	got = post(t, context.Background(), srv, `mutation { a: page(first: 1) b: page(first: 1) c: page(first: 1) d: page(first: 1) e: page(first: 1) f: page(first: 1) g: page(first: 1) h: page(first: 1) i: page(first: 1) j: page(first: 1) k: page(first: 1) l: page(first: 1) m: page(first: 1) n: page(first: 1) o: page(first: 1) p: page(first: 1) q: page(first: 1) r: page(first: 1) s: page(first: 1) t: page(first: 1) u: page(first: 1) v: page(first: 1) w: page(first: 1) x: page(first: 1) y: page(first: 1) z: page(first: 1) a1: page(first: 1) b1: page(first: 1) c1: page(first: 1) d1: page(first: 1) e1: page(first: 1) }`)
	if !strings.Contains(got, `COST_LIMIT_EXCEEDED`) {
		t.Errorf("a mutation over the limit: %s", got)
	}
	if stmts, txs := l.take(); len(stmts) != 0 || len(txs) != 0 {
		t.Errorf("a refused mutation ran %v, %v", stmts, txs)
	}
}

// TestCostLimitNilDB: with no DB the limits are the defaults.
func TestCostLimitNilDB(t *testing.T) {
	db, _ := handlerDB(t)
	srv := serve(handlerResolvers(db, nil), gqlgenx.CostLimit(nil))
	if got, want := post(t, context.Background(), srv, `{ count }`), `{"data":{"count":0}}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// TestIntrospectionLimit: introspection answers to MaxIntrospectionCost
// alone, and data to MaxCost alone, so that neither loosens the other. Both
// run no statement when refused.
func TestIntrospectionLimit(t *testing.T) {
	const meta = `{ __type(name: "Query") { name } }` // 2
	run := func(l veloxgql.Limits, query string) (string, []string) {
		db, log := limitDB(t, l)
		srv := serve(handlerResolvers(db, nil), gqlgenx.CostLimit(db))
		got := post(t, context.Background(), srv, query)
		stmts, _ := log.take()
		return got, stmts
	}
	got, stmts := run(veloxgql.Limits{MaxIntrospectionCost: 1}, meta)
	const want = `{"errors":[{"message":"veloxgql: request is too costly: its introspection costs 2, and the most is 1","extensions":{"code":"COST_LIMIT_EXCEEDED"}}],"data":null}`
	if got != want {
		t.Errorf("over MaxIntrospectionCost:\n got %s\nwant %s", got, want)
	}
	if len(stmts) != 0 {
		t.Errorf("over MaxIntrospectionCost ran %v", stmts)
	}
	if got, _ := run(veloxgql.Limits{MaxCost: 1}, meta); strings.Contains(got, "COST_LIMIT_EXCEEDED") {
		t.Errorf("a MaxCost of 1 limits introspection: %s", got)
	}
	got, stmts = run(veloxgql.Limits{MaxCost: 202, MaxIntrospectionCost: 1 << 40}, `{ people(first: 30) { totalCount __typename } }`)
	if !strings.Contains(got, "COST_LIMIT_EXCEEDED") {
		t.Errorf("a large MaxIntrospectionCost loosened MaxCost: %s", got)
	}
	if len(stmts) != 0 {
		t.Errorf("over MaxCost ran %v", stmts)
	}
}

// TestListCostNamesTheFix: a client cannot lower what a list costs, as it
// can a page, so the refusal says what can: the schema's annotation, or a
// connection.
func TestListCostNamesTheFix(t *testing.T) {
	db, _ := limitDB(t, veloxgql.Limits{MaxCost: 500})
	srv := serveSchema(testSchemaOf(`type Query { items: [Item!]! one: Int } type Item { id: ID! }`, nil), gqlgenx.CostLimit(db))
	got := post(t, context.Background(), srv, `{ items { id } }`)
	for _, want := range []string{"COST_LIMIT_EXCEEDED", "Query.items", "graphql.MaxList", "a Connection"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in %s", want, got)
		}
	}
	db, _ = limitDB(t, veloxgql.Limits{MaxCost: 2})
	srv = serveSchema(testSchemaOf(`type Query { one: Int }`, nil), gqlgenx.CostLimit(db))
	if got := post(t, context.Background(), srv, `{ a: one b: one c: one }`); strings.Contains(got, "MaxList") {
		t.Errorf("no list in the request: %s", got)
	}
}

// TestDocumentRefused: RequestLimit alone refuses a document over MaxTokens
// or MaxFields before gqlgen parses or validates it, and says of a syntax
// error what gqlgen says.
func TestDocumentRefused(t *testing.T) {
	const tooMany = `{"errors":[{"message":"veloxgql: request is too large: it has 3 fields, and the most is 2","extensions":{"code":"BAD_USER_INPUT"}}],"data":null}`
	for _, c := range []struct {
		name  string
		lim   veloxgql.Limits
		query string
		want  string
	}{
		{"over MaxFields", veloxgql.Limits{MaxFields: 2}, `{ a: count b: count c: count }`, tooMany},
		{"in a fragment", veloxgql.Limits{MaxFields: 2}, `{ ...F } fragment F on Query { a: count b: count c: count }`, tooMany},
		{"at MaxFields", veloxgql.Limits{MaxFields: 2}, `{ a: count b: count }`, `{"data":`},
		{"over MaxTokens", veloxgql.Limits{MaxTokens: 3}, `{ a b c d }`, `"code":"GRAPHQL_PARSE_FAILED"`},
		{"a syntax error", veloxgql.Limits{}, `{`, `"code":"GRAPHQL_PARSE_FAILED"`},
	} {
		db, l := limitDB(t, c.lim)
		srv := serve(handlerResolvers(db, nil), gqlgenx.RequestLimit(db))
		if got := post(t, context.Background(), srv, c.query); !strings.Contains(got, c.want) {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
		if stmts, _ := l.take(); len(stmts) != 0 && c.want != `{"data":` {
			t.Errorf("%s: refused, it ran %v", c.name, stmts)
		}
	}
}

// TestRequestLimitVariables: variables over MaxVariableNodes are refused
// before gqlgen reads them, and run no statement.
func TestRequestLimitVariables(t *testing.T) {
	db, l := limitDB(t, veloxgql.Limits{MaxVariableNodes: 3})
	srv := serve(handlerResolvers(db, nil), gqlgenx.RequestLimit(db))
	send := func(vars string) string {
		body := `{"query":"query($a: Int, $b: Int, $c: Int, $d: Int, $e: Int) { a: page(first: $a) b: page(first: $b) c: page(first: $c) d: page(first: $d) e: page(first: $e) }","variables":` + vars + `}`
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w.Body.String()
	}
	if got := send(`{"a":1}`); strings.Contains(got, "errors") {
		t.Errorf("under the limit: %s", got)
	}
	l.take()
	if got := send(`{"a":1,"b":2,"c":3,"d":4,"e":5}`); !strings.Contains(got, "BAD_USER_INPUT") || !strings.Contains(got, "over 3 nodes") {
		t.Errorf("over the limit: %s", got)
	}
	if stmts, _ := l.take(); len(stmts) != 0 {
		t.Errorf("a refused request ran %v", stmts)
	}
}

// TestDocumentMergePairs: fields that differ under one response name count
// in pairs, however they merge: at MaxFields 30, 60 pairs.
func TestDocumentMergePairs(t *testing.T) {
	db, _ := limitDB(t, veloxgql.Limits{MaxFields: 30})
	srv := serve(handlerResolvers(db, nil), gqlgenx.RequestLimit(db))
	next := 0
	a := func(n int) string {
		return spell(n, func(int) string {
			next++
			return fmt.Sprintf("a: count(n: %d)", next)
		})
	}
	for _, c := range []struct {
		name, query string
		refused     bool
	}{
		{"11 siblings, 55 pairs", `{ ` + a(11) + ` }`, false},
		{"12 siblings, 66 pairs", `{ ` + a(12) + ` }`, true},
		{"in an inline fragment", `{ ... { ` + a(12) + ` } }`, true},
		{"through two fragments", `{ ...A ...B } fragment A on Query { ` + a(6) + ` } fragment B on Query { ` + a(6) + ` }`, true},
		{"a fragment spread twice counts once", `{ ...A ...A } fragment A on Query { ` + a(8) + ` }`, false},
		{"other response names", `{ ` + a(6) + strings.Repeat("b: count ", 6) + ` }`, false},
	} {
		got := post(t, context.Background(), srv, c.query)
		if refused := strings.Contains(got, "over 60 pairs"); refused != c.refused {
			t.Errorf("%s: %s", c.name, got)
		}
	}
	// Fragments that spread two of the next reach 2^n fields, in a document of few.
	var dag strings.Builder
	for i := range 40 {
		fmt.Fprintf(&dag, "fragment F%d on Query { x: __schema { ...F%d } y: __schema { ...F%d } } ", i, i+1, i+1)
	}
	db, _ = limitDB(t, veloxgql.Limits{MaxTokens: 10000})
	srv = serve(handlerResolvers(db, nil), gqlgenx.RequestLimit(db))
	if got := post(t, context.Background(), srv, `{ ...F0 } `+dag.String()+`fragment F40 on Query { a: count }`); !strings.Contains(got, "its fragments spread to over 200000 fields") {
		t.Errorf("a DAG of fragments: %.300s", got)
	}
}

// TestDocumentAndAPQ: a document refused is not what APQ stores, the
// RequestLimit being first; one APQ fills from its cache was checked when it
// was stored.
func TestDocumentAndAPQ(t *testing.T) {
	db, _ := limitDB(t, veloxgql.Limits{MaxFields: 2})
	srv := handler.New(testSchema(handlerResolvers(db, nil)))
	srv.Use(gqlgenx.RequestLimit(db))
	srv.Use(extension.AutomaticPersistedQuery{Cache: lru.New[string](10)})
	srv.AddTransport(transport.POST{})
	const query = `{ a: count b: count c: count }`
	sum := sha256.Sum256([]byte(query))
	apq := func(q string) string {
		ext := `"extensions":{"persistedQuery":{"version":1,"sha256Hash":"` + hex.EncodeToString(sum[:]) + `"}}`
		body := `{` + ext + `}`
		if q != "" {
			body = `{"query":"` + q + `",` + ext + `}`
		}
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w.Body.String()
	}
	if got := apq(query); !strings.Contains(got, "BAD_USER_INPUT") {
		t.Errorf("sent whole: %s", got)
	}
	if got := apq(""); !strings.Contains(got, "PersistedQueryNotFound") {
		t.Errorf("sent by its hash: %s", got)
	}
}

// TestRequestLimitPresented: under ErrorPresenter a refused request's error
// has no "veloxgql: " prefix, which gqlgen's own presenter would show.
func TestRequestLimitPresented(t *testing.T) {
	db, _ := limitDB(t, veloxgql.Limits{MaxFields: 2})
	srv := serve(handlerResolvers(db, nil), gqlgenx.RequestLimit(db))
	srv.SetErrorPresenter(gqlgenx.ErrorPresenter(func(context.Context, error) {}))
	const want = `{"errors":[{"message":"request is too large: it has 3 fields, and the most is 2","extensions":{"code":"BAD_USER_INPUT"}}],"data":null}`
	if got := post(t, context.Background(), srv, `{ a: count b: count c: count }`); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// BenchmarkCheckDocument is what RequestLimit adds to each request: a parse
// and a walk of GraphiQL's query, which gqlgen's cache would have spared.
func BenchmarkCheckDocument(b *testing.B) {
	e, ok := gqlgenx.RequestLimit(&velox.DB{}).(graphql.OperationParameterMutator)
	if !ok {
		b.Fatal("RequestLimit is no OperationParameterMutator")
	}
	p := &graphql.RawParams{Query: graphiQL}
	b.ReportAllocs()
	for b.Loop() {
		if err := e.MutateOperationParameters(context.Background(), p); err != nil {
			b.Fatal(err)
		}
	}
}
