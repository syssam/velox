package graphqlgo_test

import (
	"context"
	"database/sql"
	"errors"
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

// TestNoSetup (decision 0173): under graphql-go with no option, a
// connection of a query reads on its query's DB; an edge of a row built by
// hand, which has no DB of its own, fails with velox.ErrNoSource and sends
// no statement.
func TestNoSetup(t *testing.T) {
	db, log := modelDB(t)
	ctx := context.Background()
	resp := bareExecutor(t, db).Execute(ctx, &graphql.Request{Query: `{ page { totalCount } one { name } }`})
	defer resp.Release()
	if len(resp.Errors) != 1 {
		t.Fatalf("errors %v, want one, the edge of a row built by hand", resp.Errors)
	}
	if e := resp.Errors[0]; !errors.Is(e.Err, velox.ErrNoSource) {
		t.Errorf("%v at %v, want ErrNoSource", e.Err, e.Path)
	}
	if b, _ := resp.MarshalJSON(); !strings.Contains(string(b), `"page":{"totalCount":3}`) {
		t.Errorf("the connection of a query: %s", b)
	}
	if st, _ := log.take(); len(st) != 1 {
		t.Errorf("sent %q, want the connection's count alone", st)
	}
}

// sessionInterceptor is the mutation session velox's docs give an
// application on a routed DB: each mutation runs in a velox session.
func sessionInterceptor(seen *[]time.Time) graphql.ExecutorOption {
	return graphql.WithOperationInterceptor(graphql.OperationInterceptorFunc(
		func(ctx context.Context, oc *graphql.OperationContext, next graphql.OperationHandler) *graphql.Response {
			if oc.Operation.Operation == ast.Mutation {
				ctx = velox.WithSession(ctx)
				defer func() { *seen = append(*seen, velox.PinnedUntil(ctx)) }()
			}
			return next(ctx, oc)
		}))
}

// emptyReplica is a read replica whose authors table is empty: a read that
// reaches it finds no author, whatever the primary holds.
func emptyReplica(t *testing.T) velox.Conn {
	t.Helper()
	raw, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	raw.SetMaxOpenConns(1)
	if _, err := raw.Exec(`CREATE TABLE authors (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	return velox.SQLConn(raw)
}

// TestMutationSession: a mutation runs in no transaction: a resolver that
// fails after its insert keeps the row. On a DB with replicas, a mutation
// reads its own write from the primary only when the application's
// operation interceptor puts it in a velox session, which its insert pins;
// a query's session is none.
func TestMutationSession(t *testing.T) {
	db, log := modelDB(t, velox.WithReplicas(emptyReplica(t)))
	var seen []time.Time
	opts := []graphql.ExecutorOption{graphql.WithErrorPresenter(graphqlgo.ErrorPresenter(nil))}
	plain := bound(db, executorServer(newExecutor(graphqlgoSchema(t), opts...)))
	piped := bound(db, executorServer(newExecutor(graphqlgoSchema(t), append(opts, sessionInterceptor(&seen))...)))
	ctx := context.Background()
	const add = `mutation { addPost(author: 2, title: "new") { title author { name } } }`
	if out := plain(t, ctx, add, nil); !strings.Contains(out, `"data":null`) {
		t.Errorf("with no session: %s, want the author read on the empty replica to fail", out)
	}
	log.take()
	if out := piped(t, ctx, add, nil); out != `{"data":{"addPost":{"title":"new","author":{"name":"bob"}}}}` {
		t.Errorf("with a session: %s, want the author read on the primary", out)
	}
	if len(seen) != 1 || seen[0].IsZero() {
		t.Errorf("the mutation's session is pinned until %v, want a time: its insert pins it", seen)
	}
	seen = nil
	if out := piped(t, ctx, `{ authors(first: 1) { totalCount } }`, nil); out != `{"data":{"authors":{"totalCount":0}}}` {
		t.Errorf("query: %s, want the empty replica's count", out)
	}
	if len(seen) != 0 {
		t.Errorf("a query ran in a session: %v", seen)
	}
	log.take()
	out := piped(t, ctx, `mutation { failPost(author: 1, title: "kept") { title } }`, nil)
	st, txs := log.take()
	if !strings.Contains(out, `"data":{"failPost":null}`) || len(txs) != 0 || len(st) != 1 || strings.HasSuffix(st[0], " in tx") {
		t.Errorf("mutation: %s, statements %q, transactions %q; want the insert outside a transaction", out, st, txs)
	}
	if n, err := db.Query[post]().Count(velox.WithPrimaryReads(ctx)); err != nil || n != 9 {
		t.Errorf("%d posts, %v; want the three inserts kept", n, err)
	}
}

// TestLimitsOfDB: under graphqlgo the limits are the DB's, which the
// resolver's own DB holds: generated code holds a page to the DB's MaxPage,
// 100 by default.
func TestLimitsOfDB(t *testing.T) {
	db, _ := modelDB(t)
	out := graphqlgoServer(t, db)(t, context.Background(), `{ authors(first: 101) { totalCount } }`, nil)
	if !strings.Contains(out, "BAD_USER_INPUT") || !strings.Contains(out, "100") {
		t.Errorf("first: 101: %s", out)
	}
	small, _ := modelDB(t, veloxgql.WithLimits(veloxgql.Limits{MaxPage: 2}))
	srv := graphqlgoServer(t, small)
	if out := srv(t, context.Background(), `{ authors(first: 3) { totalCount } }`, nil); !strings.Contains(out, "BAD_USER_INPUT") {
		t.Errorf("first: 3 under MaxPage 2: %s", out)
	}
	if out := srv(t, context.Background(), `{ authors(first: 2) { totalCount } }`, nil); out != `{"data":{"authors":{"totalCount":3}}}` {
		t.Errorf("first: 2 under MaxPage 2: %s", out)
	}
}

// TestWaitParks: the source's Wait parks the caller in the operation's wave
// coordinator, so a wave whose only running goroutine waits on velox's
// batch is ready, and an application's loader flushes.
func TestWaitParks(t *testing.T) {
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
	out := executorServer(newExecutor(s))(t, context.Background(), `{ wait }`, nil)
	if out != `{"data":{"wait":true}}` {
		t.Errorf("%s: the wave was not ready while the resolver waited", out)
	}
	// Outside an operation, Wait does nothing.
	src.Wait(context.Background())()
}

// tick is an event of the subscription of TestSubscription.
type tick struct{}

// TestSubscription: each event's resolvers read in a table of the event's
// own:
// the event's edges batch, and two events never share a table.
func TestSubscription(t *testing.T) {
	db, log := modelDB(t)
	var mu sync.Mutex
	var tables []*gqlcore.Table
	s, err := graphql.NewSchema(graphql.SDL(paritySDL+`
type Subscription { tick: Tick }
type Tick { authors(first: Int): AuthorConnection }
`),
		graphqlgoSchemaOptions(),
		graphql.Subscription(graphql.Subscribe("tick", func(ctx context.Context) (<-chan *tick, error) {
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
			return authors(withResolverDB(ctx, db), a.First, nil, nil)
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	e := newExecutor(s)
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
	if n != 2 {
		t.Errorf("%d events, want 2", n)
	}
	if len(tables) != 2 || tables[0] == nil || tables[1] == nil || tables[0] == tables[1] {
		t.Errorf("tables %v, want one of each event's own", tables)
	}
	// The page and the posts' batch, for each event.
	if st, _ := log.take(); len(st) != 4 {
		t.Errorf("%d statements, want two per event: %q", len(st), st)
	}
}
