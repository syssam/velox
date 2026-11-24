package graphqlgo_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/syssam/graphql-go"

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

// TestNoWith (decision 0173): under graphql-go without graphqlgo.With, a
// connection of a query reads on its query's DB; an edge of a row built by
// hand, which has no DB of its own, fails with velox.ErrNoSource and
// ErrNoDB, naming graphqlgo.With, and sends no statement. With it, both
// reads succeed.
func TestNoWith(t *testing.T) {
	db, log := modelDB(t)
	ctx := context.Background()
	resp := bareExecutor(t, db).Execute(ctx, &graphql.Request{Query: `{ page { totalCount } one { name } }`})
	defer resp.Release()
	if len(resp.Errors) != 1 {
		t.Fatalf("errors %v, want one, the edge of a row built by hand", resp.Errors)
	}
	if e := resp.Errors[0]; !errors.Is(e.Err, velox.ErrNoSource) || !errors.Is(e.Err, veloxgql.ErrNoDB) || !strings.Contains(e.Message, "graphqlgo.With") {
		t.Errorf("%v at %v, want ErrNoSource and ErrNoDB, naming graphqlgo.With", e.Err, e.Path)
	}
	if b, _ := resp.MarshalJSON(); !strings.Contains(string(b), `"page":{"totalCount":3}`) {
		t.Errorf("the connection of a query: %s", b)
	}
	if st, _ := log.take(); len(st) != 1 {
		t.Errorf("sent %q, want the connection's count alone", st)
	}
	out := executorServer(bareExecutor(t, db, graphqlgo.With(db)))(t, ctx, `{ page { totalCount } one { name } }`, nil)
	if want := `{"data":{"page":{"totalCount":3},"one":{"name":"ann"}}}`; out != want {
		t.Errorf("with With: %s, want %s", out, want)
	}
}

// TestWith: With refuses a nil DB; given twice, it serves with the later
// one's state, and a mutation runs in a velox session, in no transaction:
// a resolver that fails after its insert keeps the row.
func TestWith(t *testing.T) {
	func() {
		defer func() {
			if p := recover(); p == nil || !strings.Contains(panicText(p), "graphqlgo.With: nil DB") {
				t.Errorf("nil DB: panic %v", p)
			}
		}()
		graphqlgo.With(nil)
	}()
	other, _ := modelDB(t)
	db, log := modelDB(t)
	srv := executorServer(newExecutor(graphqlgoSchema(t), graphqlgo.With(other), graphqlgo.With(db), graphql.WithErrorPresenter(graphqlgo.ErrorPresenter(nil))))
	ctx := context.Background()
	if out := srv(t, ctx, `{ authors(first: 1) { totalCount } }`, nil); out != `{"data":{"authors":{"totalCount":3}}}` {
		t.Errorf("query: %s", out)
	}
	if st, _ := log.take(); len(st) != 1 {
		t.Errorf("the later With's DB sent %q, want the count", st)
	}
	out := srv(t, ctx, `mutation { failPost(author: 1, title: "kept") { title } }`, nil)
	st, txs := log.take()
	if !strings.Contains(out, `"data":{"failPost":null}`) || len(txs) != 0 || len(st) != 1 || strings.HasSuffix(st[0], " in tx") {
		t.Errorf("mutation: %s, statements %q, transactions %q; want the insert outside a transaction", out, st, txs)
	}
	if n := postCount(t, db, log); n != 7 {
		t.Errorf("%d posts, want the insert kept", n)
	}
}

// TestLimitsFromContext: under graphqlgo the limits are
// veloxgql.DefaultLimits(): generated code holds a page to MaxPage 100.
func TestLimitsFromContext(t *testing.T) {
	db, _ := modelDB(t)
	var got veloxgql.Limits
	s, err := graphql.NewSchema(graphql.SDL(`type Query { limits: Boolean! }`),
		graphql.Query(graphql.Resolve("limits", func(ctx context.Context, _ graphql.Root) (bool, error) {
			got = veloxgql.LimitsFromContext(ctx)
			return true, nil
		})))
	if err != nil {
		t.Fatal(err)
	}
	executorServer(newExecutor(s, graphqlgo.With(db)))(t, context.Background(), `{ limits }`, nil)
	if got != veloxgql.DefaultLimits() {
		t.Errorf("LimitsFromContext = %+v, want DefaultLimits()", got)
	}
	out := graphqlgoServer(t, db)(t, context.Background(), `{ authors(first: 101) { totalCount } }`, nil)
	if !strings.Contains(out, "BAD_USER_INPUT") || !strings.Contains(out, "100") {
		t.Errorf("first: 101: %s", out)
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
			src = gqlcore.SourceOf(ctx)
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
			f, _ := gqlcore.FieldOf(ctx)
			tables = append(tables, f.Table)
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

// panicText is a recovered value's text.
func panicText(p any) string {
	if err, ok := p.(error); ok {
		return err.Error()
	}
	s, _ := p.(string)
	return s
}
