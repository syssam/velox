package graphqlgo_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/syssam/graphql-go"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/contrib/graphql/veloxgql/graphqlgo"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
	"github.com/syssam/velox/untyped"
)

// bareSDL is the schema of the fail-closed test: reads of generated code
// whose DB is not the context's.
const bareSDL = `
type Query { page: AuthorConnection one: Author }
type Author { id: ID! name: String! }
type AuthorConnection { totalCount: Int! }
`

// bareExecutor serves bareSDL on db, whose page and one read through
// gqlgraph on db itself, with opts.
func bareExecutor(t *testing.T, db *velox.DB, opts ...graphql.ExecutorOption) *graphql.Executor {
	t.Helper()
	s, err := graphql.NewSchema(graphql.SDL(bareSDL),
		graphql.Query(
			graphql.Resolve("page", func(ctx context.Context, _ graphql.Root) (*conn[author], error) {
				c, err := gqlgraph.Paginate(ctx, gqlgraph.Query(untyped.FromQuery(db.Query[author]())), &authorType, gqlgraph.Args{})
				if err != nil {
					return nil, err
				}
				return &conn[author]{c}, nil
			}),
			graphql.Resolve("one", func(ctx context.Context, _ graphql.Root) (*author, error) {
				return postAuthor(ctx, &post{ID: 11, AuthorID: 1})
			}),
		),
		graphql.Object[author]("Author",
			graphql.Field("id", func(a *author) int64 { return a.ID }),
			graphql.Field("name", func(a *author) string { return a.Name }),
		),
		graphql.Object[conn[author]]("AuthorConnection",
			graphql.Resolve("totalCount", func(ctx context.Context, c *conn[author]) (int, error) { return c.totalCount(ctx) }),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	return newExecutor(s, opts...)
}

// TestNoWith: under graphql-go without graphqlgo.With, every gqlgraph read
// fails with veloxgql.ErrNoSelectionSource, and ErrNoDB, and sends no
// statement; with it, the same reads succeed.
func TestNoWith(t *testing.T) {
	db, log := modelDB(t)
	ctx := context.Background()
	resp := bareExecutor(t, db).Execute(ctx, &graphql.Request{Query: `{ page { totalCount } one { name } }`})
	defer resp.Release()
	if len(resp.Errors) != 2 {
		t.Fatalf("errors %v, want one for each read", resp.Errors)
	}
	for _, e := range resp.Errors {
		if !errors.Is(e.Err, veloxgql.ErrNoSelectionSource) || !errors.Is(e.Err, veloxgql.ErrNoDB) || !strings.Contains(e.Message, "graphqlgo.With") {
			t.Errorf("%v at %v, want ErrNoSelectionSource and ErrNoDB, naming graphqlgo.With", e.Err, e.Path)
		}
	}
	if st, _ := log.take(); len(st) != 0 {
		t.Errorf("sent %q", st)
	}
	out := executorServer(bareExecutor(t, db, graphqlgo.With(db)))(t, ctx, `{ page { totalCount } one { name } }`, nil)
	if want := `{"data":{"page":{"totalCount":3},"one":{"name":"ann"}}}`; out != want {
		t.Errorf("with With: %s, want %s", out, want)
	}
}

// TestOptionPanics: With refuses a nil DB, PageLimits a negative limit,
// MutationTx a retrying transaction and a nil option, and each refuses
// being given twice to one executor.
func TestOptionPanics(t *testing.T) {
	db, _ := modelDB(t)
	s := graphqlgoSchema(t)
	for name, c := range map[string]struct {
		f    func()
		want string
	}{
		"nil DB":         {func() { graphqlgo.With(nil) }, "graphqlgo.With: nil DB"},
		"negative limit": {func() { graphqlgo.PageLimits(veloxgql.Limits{MaxPage: -1}) }, "Limits.MaxPage is -1"},
		"retry":          {func() { graphqlgo.MutationTx(graphqlgo.TxOptions(velox.TxRetrySerializable(2))) }, "TxRetrySerializable(2)"},
		"nil option":     {func() { graphqlgo.MutationTx(nil) }, "graphqlgo.MutationTx: a nil option"},
		"With twice":     {func() { newExecutor(s, graphqlgo.With(db), graphqlgo.With(db)) }, "graphqlgo.With: given twice"},
		"limits twice": {func() {
			newExecutor(s, graphqlgo.PageLimits(veloxgql.Limits{}), graphqlgo.PageLimits(veloxgql.Limits{}))
		}, "graphqlgo.PageLimits: given twice"},
		"tx twice": {func() { newExecutor(s, graphqlgo.MutationTx(), graphqlgo.MutationTx()) }, "graphqlgo.MutationTx: given twice"},
	} {
		func() {
			defer func() {
				if p := recover(); p == nil || !strings.Contains(panicText(p), c.want) {
					t.Errorf("%s: panic %v, want %q", name, p, c.want)
				}
			}()
			c.f()
		}()
	}
}

// TestOptionsAnyOrder: the options configure one executor in any order,
// mixed with graphql-go's own, and one option value serves several
// executors, each with its own state.
func TestOptionsAnyOrder(t *testing.T) {
	db, log := modelDB(t)
	ctx := context.Background()
	limits := graphqlgo.PageLimits(veloxgql.Limits{MaxPage: 2})
	for name, opts := range map[string][]graphql.ExecutorOption{
		"With first": {graphqlgo.With(db), graphql.WithMaxErrors(5), graphqlgo.MutationTx(), limits, graphqlgo.Cost()},
		"With last":  {graphqlgo.Cost(), limits, graphqlgo.MutationTx(graphqlgo.TxOptions(), graphqlgo.TxFilter(nil), graphqlgo.ErrorLog(nil)), graphql.WithMaxErrors(5), graphqlgo.With(db)},
	} {
		srv := executorServer(newExecutor(graphqlgoSchema(t), opts...))
		if out := srv(t, ctx, `{ authors(first: 3) { totalCount } }`, nil); !strings.Contains(out, "BAD_USER_INPUT") {
			t.Errorf("%s: over PageLimits' MaxPage: %s", name, out)
		}
		out := srv(t, ctx, `mutation { addPost(author: 1, title: "x") { title } }`, nil)
		if _, txs := log.take(); out != `{"data":{"addPost":{"title":"x"}}}` || !slices.Equal(txs, []string{"commit"}) {
			t.Errorf("%s: %s, transactions %q; want a commit", name, out, txs)
		}
	}
}

// TestMutationTxNoWith: MutationTx without With fails a mutation before
// any resolver runs, with veloxgql.ErrNoDB, logged naming graphqlgo.With.
func TestMutationTxNoWith(t *testing.T) {
	db, log := modelDB(t)
	var buf bytes.Buffer
	tx := graphqlgo.MutationTx(graphqlgo.ErrorLog(slog.New(slog.NewTextHandler(&buf, nil))))
	srv := executorServer(newExecutor(graphqlgoSchema(t), tx, graphql.WithErrorPresenter(graphqlgo.ErrorPresenter(nil))))
	out := srv(t, context.Background(), `mutation { addPost(author: 1, title: "x") { title } }`, nil)
	if !strings.Contains(out, `"data":null`) || !strings.Contains(out, "INTERNAL_SERVER_ERROR") || !strings.Contains(buf.String(), "give the executor graphqlgo.With(db)") {
		t.Errorf("mutation: %s, logged %q", out, buf.String())
	}
	if st, txs := log.take(); len(st) != 0 || len(txs) != 0 {
		t.Errorf("sent %q %q", st, txs)
	}
	if n := postCount(t, db, log); n != 6 {
		t.Errorf("%d posts, want none added", n)
	}
}

// panicText is a recovered value's text.
func panicText(p any) string {
	if err, ok := p.(error); ok {
		return err.Error()
	}
	s, _ := p.(string)
	return s
}

// TestMutationPlain: without MutationTx a mutation runs with no
// transaction: a resolver that fails after its insert keeps the row. Under
// MutationTx, a mutation TxFilter leaves out does the same.
func TestMutationPlain(t *testing.T) {
	ctx := context.Background()
	for name, opts := range map[string][]graphql.ExecutorOption{
		"default":   nil,
		"filtered":  {graphqlgo.MutationTx(graphqlgo.TxFilter(func(context.Context, *ast.OperationDefinition) bool { return false }))},
		"filter on": {graphqlgo.MutationTx(graphqlgo.TxFilter(func(_ context.Context, op *ast.OperationDefinition) bool { return op.Name == "tx" }))},
	} {
		db, log := modelDB(t)
		out := graphqlgoServer(t, db, opts...)(t, ctx, `mutation plain { failPost(author: 1, title: "kept") { title } }`, nil)
		st, txs := log.take()
		if !strings.Contains(out, `"data":{"failPost":null}`) || len(txs) != 0 || len(st) != 1 || strings.HasSuffix(st[0], " in tx") {
			t.Errorf("%s: %s, statements %q, transactions %q; want the insert outside a transaction", name, out, st, txs)
		}
		if n := postCount(t, db, log); n != 7 {
			t.Errorf("%s: %d posts, want the insert kept", name, n)
		}
	}
}

// TestMutationSerial: under MutationTx a mutation's fields run one at a
// time, as a field interceptor after MutationTx's sees them; a query's do
// not wait on each other.
func TestMutationSerial(t *testing.T) {
	db, _ := modelDB(t)
	var cur, most atomic.Int32
	count := graphql.FieldInterceptorFunc(func(ctx context.Context, _ *graphql.FieldContext, next graphql.FieldHandler) (any, error) {
		n := cur.Add(1)
		defer cur.Add(-1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(time.Millisecond)
		return next(ctx)
	})
	srv := executorServer(newExecutor(graphqlgoSchema(t), graphqlgo.With(db), graphqlgo.MutationTx(), graphql.WithFieldInterceptor(count)))
	const sel = `author { name posts(first: 9) { edges { node { title author { name } tags { name } } } } }`
	out := srv(t, context.Background(), `mutation { addPost(author: 2, title: "t") { title `+sel+` } }`, nil)
	if !strings.Contains(out, `"title":"t"`) || strings.Contains(out, "errors") {
		t.Fatal(out)
	}
	if n := most.Load(); n != 1 {
		t.Errorf("mutation: at most %d fields in flight, want 1", n)
	}
	most.Store(0)
	out = srv(t, context.Background(), `{ node(id: "11") { ... on Post { a: `+sel+` b: `+sel+` } } }`, nil)
	if strings.Contains(out, "errors") {
		t.Fatal(out)
	}
	if n := most.Load(); n < 2 {
		t.Errorf("query: at most %d fields in flight, want more than 1", n)
	}
}

// TestWaitParks: the source's Wait parks the caller in the operation's wave
// coordinator, so a wave whose only running goroutine waits on velox's
// batch is ready, and an application's loader flushes.
func TestWaitParks(t *testing.T) {
	db, _ := modelDB(t)
	var fired atomic.Bool
	var src gqlcore.SelectionSource
	s, err := graphql.NewSchema(graphql.SDL(`type Query { wait: Boolean! }`),
		graphql.Query(graphql.Resolve("wait", func(ctx context.Context, _ graphql.Root) (bool, error) {
			graphql.OperationFrom(ctx).Waves().OnReady(func() { fired.Store(true) })
			src = gqlcore.Source(ctx)
			done := src.Wait(ctx)
			parked := fired.Load()
			done()
			return parked, nil
		})))
	if err != nil {
		t.Fatal(err)
	}
	out := executorServer(newExecutor(s, graphqlgo.With(db)))(t, context.Background(), `{ wait }`, nil)
	if out != `{"data":{"wait":true}}` {
		t.Errorf("%s: the wave was not ready while the resolver waited", out)
	}
	// Outside an operation, Wait does nothing.
	src.Wait(context.Background())()
}

// TestCost: under Cost a request over MaxCost is refused before any
// statement, with COST_LIMIT_EXCEEDED, and one within it runs; without
// Cost the same request runs.
func TestCost(t *testing.T) {
	db, log := modelDB(t)
	ctx := context.Background()
	l := graphqlgo.PageLimits(veloxgql.Limits{MaxCost: 300})
	const query = `{ authors(first: 10) { edges { node { posts(first: 10) { totalCount } } } } }`
	out := graphqlgoServer(t, db, l, graphqlgo.Cost())(t, ctx, query, nil)
	if !strings.Contains(out, `"code":"COST_LIMIT_EXCEEDED"`) || !strings.Contains(out, "request is too costly: it costs") || strings.Contains(out, `"data"`) {
		t.Errorf("over MaxCost: %s", out)
	}
	if st, _ := log.take(); len(st) != 0 {
		t.Errorf("over MaxCost sent %q", st)
	}
	if out := graphqlgoServer(t, db, l, graphqlgo.Cost())(t, ctx, `{ authors(first: 1) { totalCount } }`, nil); out != `{"data":{"authors":{"totalCount":3}}}` {
		t.Errorf("within MaxCost: %s", out)
	}
	if out := graphqlgoServer(t, db, l)(t, ctx, query, nil); strings.Contains(out, "errors") {
		t.Errorf("without Cost: %s", out)
	}
}

// TestMutationTxErrorLog: a transaction that fails after the resolvers
// succeed nulls the data and adds an internal error, logged to ErrorLog's
// logger.
func TestMutationTxErrorLog(t *testing.T) {
	refuse := velox.WithTxAround(func(ctx context.Context, _ *velox.TxInfo, next func(context.Context) error) error {
		if err := next(ctx); err != nil {
			return err
		}
		return errors.New("the transaction was refused")
	})
	db, _ := modelDB(t, refuse)
	var buf bytes.Buffer
	srv := graphqlgoServer(t, db, graphqlgo.MutationTx(graphqlgo.ErrorLog(slog.New(slog.NewTextHandler(&buf, nil)))))
	out := srv(t, context.Background(), `mutation { addPost(author: 1, title: "x") { title } }`, nil)
	if out != `{"errors":[{"message":"internal error","extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":null}` {
		t.Errorf("a failed transaction: %s", out)
	}
	if !strings.Contains(buf.String(), "veloxgql: internal error") || !strings.Contains(buf.String(), "the transaction was refused") {
		t.Errorf("logged %q, want the transaction's error", buf.String())
	}
}

// tick is an event of the subscription of TestSubscription.
type tick struct{}

// TestSubscription: a subscription's source opens with the state in its
// context, and each event's resolvers read in a table of the event's own:
// the event's edges batch, and two events never share a table.
func TestSubscription(t *testing.T) {
	db, log := modelDB(t)
	var mu sync.Mutex
	var tables []*gqlcore.Table
	var sourceErr error
	s, err := graphql.NewSchema(graphql.SDL(paritySDL+`
type Subscription { tick: Tick }
type Tick { authors(first: Int): AuthorConnection }
`),
		graphqlgoSchemaOptions(),
		graphql.Subscription(graphql.Subscribe("tick", func(ctx context.Context) (<-chan *tick, error) {
			_, sourceErr = veloxgql.DBFromContext(ctx)
			ch := make(chan *tick, 2)
			ch <- &tick{}
			ch <- &tick{}
			close(ch)
			return ch, nil
		})),
		graphql.Object[tick]("Tick", graphql.ResolveArgs("authors", func(ctx context.Context, _ *tick, a struct{ First *int }) (*conn[author], error) {
			mu.Lock()
			tables = append(tables, gqlcore.TableFromContext(ctx))
			mu.Unlock()
			return authors(ctx, a.First, nil, nil)
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	e := newExecutor(s, graphqlgo.With(db))
	ch, err := e.Subscribe(context.Background(), &graphql.Request{Query: `subscription { tick { authors(first: 5) { edges { node { name posts(first: 1) { edges { node { title } } } } } } } }`})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"data":{"tick":{"authors":{"edges":[{"node":{"name":"ann","posts":{"edges":[{"node":{"title":"a1"}}]}}},{"node":{"name":"bob","posts":{"edges":[{"node":{"title":"b1"}}]}}},{"node":{"name":"cy","posts":{"edges":[{"node":{"title":"c1"}}]}}}]}}}}`
	n := 0
	for resp := range ch {
		b, _ := resp.MarshalJSON()
		resp.Release()
		if string(b) != want {
			t.Errorf("event %d: %s", n, b)
		}
		n++
	}
	if n != 2 || sourceErr != nil {
		t.Errorf("%d events, the source's DB error %v; want 2 and none", n, sourceErr)
	}
	if len(tables) != 2 || tables[0] == nil || tables[1] == nil || tables[0] == tables[1] {
		t.Errorf("tables %v, want one of each event's own", tables)
	}
	// The page and the posts' batch, for each event.
	if st, _ := log.take(); len(st) != 4 {
		t.Errorf("%d statements, want two per event: %q", len(st), st)
	}
}
