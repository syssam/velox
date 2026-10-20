package veloxgql_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
)

// handlerSDL is the schema of the handler's tests.
const handlerSDL = `
type Query {
	count: Int!
	hasDB: Boolean!
	pair: Pair!
}
type Mutation {
	add(id: Int!): Int!
	fail(id: Int!): Int
	boom(id: Int!): Int
	cancel(id: Int!): Int!
	pair: Pair!
}
type Subscription {
	count: Int!
}
type Pair {
	a: Int!
	b: Int!
}
`

// resolver resolves a field from its arguments.
type resolver func(ctx context.Context, args map[string]any) (any, error)

// testSchema is an executable schema of handlerSDL that runs resolvers as
// gqlgen's generated code does. An operation has one response. A mutation's
// root fields run one after another, in selection order; a query's root
// fields, and every object's fields below the root, run concurrently,
// through graphql.FieldSet, as gqlgen runs fields that take a context.
// Each field's resolver runs through the operation's field middleware, and
// its children resolve after it returns; a resolver's error or panic is
// added to the response's errors and its field is null. A subscription
// sends two events, each resolving its fields again, and ends.
//
// A root field's resolver is resolvers[name], another's resolvers["Type.name"].
func testSchema(resolvers map[string]resolver) graphql.ExecutableSchema {
	schema := gqlparser.MustLoadSchema(&ast.Source{Input: handlerSDL})
	return &graphql.ExecutableSchemaMock{
		SchemaFunc: func() *ast.Schema { return schema },
		ComplexityFunc: func(context.Context, string, string, int, map[string]any) (int, bool) {
			return 0, false
		},
		ExecFunc: func(ctx context.Context) graphql.ResponseHandler {
			opCtx := graphql.GetOperationContext(ctx)
			op := opCtx.Operation.Operation
			events := 1
			if op == ast.Subscription {
				events = 2
			}
			return func(ctx context.Context) *graphql.Response {
				if events == 0 {
					return nil
				}
				events--
				var buf bytes.Buffer
				resolveSet(ctx, opCtx, opCtx.Operation.SelectionSet, true, op == ast.Query, resolvers).MarshalGQL(&buf)
				return &graphql.Response{Data: buf.Bytes()}
			}
		},
	}
}

// resolveSet resolves the fields of sels, concurrently or one after another.
func resolveSet(ctx context.Context, opCtx *graphql.OperationContext, sels ast.SelectionSet, root, concurrent bool, resolvers map[string]resolver) graphql.Marshaler {
	fields := graphql.CollectFields(opCtx, sels, nil)
	fs := graphql.NewFieldSet(fields)
	for i, f := range fields {
		key := f.Name
		if !root {
			key = f.ObjectDefinition.Name + "." + f.Name
		}
		if concurrent {
			fs.Concurrently(i, func(ctx context.Context) graphql.Marshaler {
				return resolveField(ctx, opCtx, f, resolvers[key], resolvers)
			})
		} else {
			fs.Values[i] = resolveField(ctx, opCtx, f, resolvers[key], resolvers)
		}
	}
	fs.Dispatch(ctx)
	return fs
}

// resolveField runs one field, as a generated field function does: the
// resolver through the field middleware, then its children.
func resolveField(ctx context.Context, opCtx *graphql.OperationContext, f graphql.CollectedField, r resolver, resolvers map[string]resolver) (ret graphql.Marshaler) {
	fc := &graphql.FieldContext{
		Object:     f.ObjectDefinition.Name,
		Field:      f,
		Args:       f.ArgumentMap(opCtx.Variables),
		IsMethod:   true,
		IsResolver: true,
	}
	ctx = graphql.WithFieldContext(ctx, fc)
	defer func() {
		if p := recover(); p != nil {
			graphql.AddError(ctx, opCtx.Recover(ctx, p))
			ret = graphql.Null
		}
	}()
	res, err := opCtx.ResolverMiddleware(ctx, func(ctx context.Context) (any, error) {
		return r(ctx, fc.Args)
	})
	if err != nil {
		graphql.AddError(ctx, err)
		return graphql.Null
	}
	if len(f.Selections) > 0 {
		return resolveSet(ctx, opCtx, f.Selections, false, true, resolvers)
	}
	b, _ := json.Marshal(res)
	return rawJSON(b)
}

// rawJSON is a resolved leaf value.
type rawJSON []byte

func (j rawJSON) MarshalGQL(w io.Writer) { _, _ = w.Write(j) }

// txLog records what a DB runs: each statement, "insert" or "select" with
// " in tx" when it ran in a transaction, and how each transaction ended.
type txLog struct {
	mu    sync.Mutex
	stmts []string
	txs   []string
}

func (l *txLog) around(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
	s := st.Op.String()
	if st.InTx {
		s += " in tx"
	}
	l.mu.Lock()
	l.stmts = append(l.stmts, s)
	l.mu.Unlock()
	return next(ctx)
}

func (l *txLog) txAround(ctx context.Context, tx *velox.TxInfo, next func(context.Context) error) error {
	defer func() {
		l.mu.Lock()
		l.txs = append(l.txs, tx.Outcome.String())
		l.mu.Unlock()
	}()
	return next(ctx)
}

// take returns what was recorded, and forgets it.
func (l *txLog) take() (stmts, txs []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	stmts, txs = l.stmts, l.txs
	l.stmts, l.txs = nil, nil
	return stmts, txs
}

// handlerDB opens an empty in-memory people table through velox, with
// hooks that record into the returned log, and opts.
func handlerDB(t *testing.T, opts ...velox.Option) (*velox.DB, *txLog) {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1) // an in-memory database lives as long as its one connection
	if _, err := sqlDB.Exec(`CREATE TABLE people (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	l := &txLog{}
	return velox.Open(velox.SQLite, sqlDB, append([]velox.Option{velox.WithAround(l.around), velox.WithTxAround(l.txAround)}, opts...)...), l
}

// errFail is the error of the fail field.
var errFail = errors.New("fail failed")

// handlerServer serves testSchema through veloxgql.Handler(db). Its
// resolvers read the DB with DBFrom: add inserts a person and returns its
// id; fail and boom insert one and then fail with errFail or panic; cancel
// inserts one, calls the cancel function set in *cancel, and returns its
// id; count counts the people; hasDB reports whether the context holds db
// itself.
func handlerServer(t *testing.T, db *velox.DB, cancel *context.CancelFunc) *handler.Server {
	t.Helper()
	return serve(db, handlerResolvers(db, cancel))
}

// handlerResolvers are handlerServer's resolvers.
func handlerResolvers(db *velox.DB, cancel *context.CancelFunc) map[string]resolver {
	insert := func(ctx context.Context, args map[string]any) (int64, error) {
		tx, err := veloxgql.DBFrom(ctx)
		if err != nil {
			return 0, err
		}
		id := args["id"].(int64)
		return id, tx.Insert(&person{ID: id, Name: "p", Age: 1}).Exec(ctx)
	}
	count := func(ctx context.Context, _ map[string]any) (any, error) {
		db, err := veloxgql.DBFrom(ctx)
		if err != nil {
			return nil, err
		}
		return db.Query[person]().Count(ctx)
	}
	return map[string]resolver{
		"count": count,
		"hasDB": func(ctx context.Context, _ map[string]any) (any, error) {
			got, err := veloxgql.DBFrom(ctx)
			return got == db, err
		},
		"add": func(ctx context.Context, args map[string]any) (any, error) {
			return insert(ctx, args)
		},
		"fail": func(ctx context.Context, args map[string]any) (any, error) {
			if _, err := insert(ctx, args); err != nil {
				return nil, err
			}
			return nil, errFail
		},
		"boom": func(ctx context.Context, args map[string]any) (any, error) {
			if _, err := insert(ctx, args); err != nil {
				return nil, err
			}
			panic("boom")
		},
		"cancel": func(ctx context.Context, args map[string]any) (any, error) {
			id, err := insert(ctx, args)
			(*cancel)()
			return id, err
		},
	}
}

// serve serves testSchema with resolvers through veloxgql.Handler(db).
func serve(db *velox.DB, resolvers map[string]resolver) *handler.Server {
	srv := handler.New(testSchema(resolvers))
	srv.AddTransport(transport.SSE{})
	srv.AddTransport(transport.POST{})
	srv.SetRecoverFunc(func(context.Context, any) error { return gqlerror.Errorf("internal system error") })
	srv.Use(veloxgql.Handler(db))
	return srv
}

// post sends query to srv as a JSON POST on ctx and returns the body.
func post(t *testing.T, ctx context.Context, srv http.Handler, query string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"query": query})
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	return w.Body.String()
}

// peopleIDs returns the ids in the people table, read on db outside any
// transaction.
func peopleIDs(t *testing.T, db *velox.DB, l *txLog) []int64 {
	t.Helper()
	rows, err := db.Query[person]().OrderBy(people.ID.Asc()).Unbounded().All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	l.take()
	out := []int64{}
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

func TestHandlerPutsTheDB(t *testing.T) {
	db, l := handlerDB(t)
	srv := handlerServer(t, db, nil)
	if got, want := post(t, context.Background(), srv, `{ hasDB }`), `{"data":{"hasDB":true}}`; got != want {
		t.Errorf("query: %s, want %s", got, want)
	}
	// A mutation's resolvers get the transaction's DB, not db.
	if got, want := post(t, context.Background(), srv, `mutation { add(id: 1) }`), `{"data":{"add":1}}`; got != want {
		t.Errorf("mutation: %s, want %s", got, want)
	}
	if stmts, _ := l.take(); !slices.Equal(stmts, []string{"insert in tx"}) {
		t.Errorf("mutation statements %q, want one insert in a transaction", stmts)
	}

	// Without the handler, a resolver fails with ErrNoDB.
	bare := handler.New(testSchema(map[string]resolver{"hasDB": func(ctx context.Context, _ map[string]any) (any, error) {
		_, err := veloxgql.DBFrom(ctx)
		if !errors.Is(err, veloxgql.ErrNoDB) {
			t.Errorf("DBFrom without the handler: %v, want ErrNoDB", err)
		}
		return nil, err
	}}))
	bare.AddTransport(transport.POST{})
	if got := post(t, context.Background(), bare, `{ hasDB }`); !strings.Contains(got, "veloxgql.Handler") {
		t.Errorf("without the handler: %s, want an error naming veloxgql.Handler", got)
	}
}

func TestHandlerNilDB(t *testing.T) {
	defer func() {
		if p := recover(); p == nil || !strings.Contains(fmt.Sprint(p), "nil DB") {
			t.Errorf("Use(Handler(nil)) panicked with %v, want an error naming the nil DB", p)
		}
	}()
	handler.New(testSchema(nil)).Use(veloxgql.Handler(nil))
}

// TestHandlerRefusesNonPositiveLimits: a limit of 0 or less would fail every
// request it bounds, so the extension refuses it when the server takes it.
func TestHandlerRefusesNonPositiveLimits(t *testing.T) {
	db, _ := handlerDB(t)
	if err := veloxgql.Handler(db).Validate(nil); err != nil {
		t.Fatalf("the defaults: %v", err)
	}
	for _, l := range []struct {
		name string
		p    *int
	}{
		{"MaxPage", &veloxgql.MaxPage},
		{"MaxList", &veloxgql.MaxList},
		{"MaxFilterDepth", &veloxgql.MaxFilterDepth},
		{"MaxFilterWidth", &veloxgql.MaxFilterWidth},
		{"MaxFilterNodes", &veloxgql.MaxFilterNodes},
		{"MaxOrderTerms", &veloxgql.MaxOrderTerms},
	} {
		for _, bad := range []int{0, -1} {
			func() {
				old := *l.p
				defer func() { *l.p = old }()
				*l.p = bad
				err := veloxgql.Handler(db).Validate(nil)
				if err == nil || !strings.Contains(err.Error(), "veloxgql."+l.name+" is "+fmt.Sprint(bad)) {
					t.Errorf("%s = %d: %v, want an error naming the limit", l.name, bad, err)
				}
				defer func() {
					if p := recover(); p == nil || !strings.Contains(fmt.Sprint(p), l.name) {
						t.Errorf("%s = %d: Use panicked with %v, want an error naming the limit", l.name, bad, p)
					}
				}()
				handler.New(testSchema(nil)).Use(veloxgql.Handler(db))
			}()
		}
	}
}

func TestMutationCommits(t *testing.T) {
	db, l := handlerDB(t)
	srv := handlerServer(t, db, nil)
	got := post(t, context.Background(), srv, `mutation { a: add(id: 1) b: add(id: 2) }`)
	if want := `{"data":{"a":1,"b":2}}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	// Both root fields ran in the one transaction, which committed.
	stmts, txs := l.take()
	if !slices.Equal(stmts, []string{"insert in tx", "insert in tx"}) || !slices.Equal(txs, []string{"commit"}) {
		t.Errorf("statements %q, transactions %q; want two inserts in one committed transaction", stmts, txs)
	}
	if ids := peopleIDs(t, db, l); !slices.Equal(ids, []int64{1, 2}) {
		t.Errorf("people %v, want [1 2]", ids)
	}
}

// TestMutationGetsTheTxContext: a mutation's resolvers get the transaction's
// context. It has the transaction's deadline, and under it a statement on the
// DB given to Handler, not the transaction's, fails with ErrOutsideTx, before
// it waits for the connection the transaction holds.
func TestMutationGetsTheTxContext(t *testing.T) {
	db, _ := handlerDB(t, velox.WithDeadlines(velox.Deadlines{Tx: 30 * time.Second}))
	var deadline bool
	var outside error
	srv := serve(db, map[string]resolver{
		"add": func(ctx context.Context, args map[string]any) (any, error) {
			_, deadline = ctx.Deadline()
			_, outside = db.Query[person]().Count(ctx)
			return args["id"], nil
		},
	})
	if got, want := post(t, context.Background(), srv, `mutation { add(id: 1) }`), `{"data":{"add":1}}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if !deadline {
		t.Error("the resolver's context has no deadline")
	}
	if !errors.Is(outside, velox.ErrOutsideTx) {
		t.Errorf("a statement on the handler's DB: %v, want ErrOutsideTx", outside)
	}
}

// TestMutationWithoutTx: a mutation's resolver holds only the transaction's
// context. Under velox.WithoutTx it, its statement on the DB given to Handler
// runs outside the transaction: the audit row survives the mutation's
// rollback. Once the mutation has ended, the context the resolver kept marks
// nothing, and a statement on the DB runs under it.
func TestMutationWithoutTx(t *testing.T) {
	// A file, so that a second connection sees the same database.
	sqlDB, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "people.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(2) // the transaction's, and the audit row's
	if _, err := sqlDB.Exec(`CREATE TABLE people (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	db := velox.Open(velox.SQLite, sqlDB)
	var kept context.Context
	srv := serve(db, map[string]resolver{
		"fail": func(ctx context.Context, args map[string]any) (any, error) {
			kept = ctx
			id := args["id"].(int64)
			// The audit row first: SQLite's transaction takes its write
			// lock at its first write, and would make this one wait.
			if err := db.Insert(&person{ID: 100 + id, Name: "audit", Age: 1}).Exec(velox.WithoutTx(ctx)); err != nil {
				return nil, err
			}
			tx, err := veloxgql.DBFrom(ctx)
			if err != nil {
				return nil, err
			}
			if err := tx.Insert(&person{ID: id, Name: "p", Age: 1}).Exec(ctx); err != nil {
				return nil, err
			}
			return nil, errFail
		},
	})
	if got := post(t, context.Background(), srv, `mutation { fail(id: 1) }`); !strings.Contains(got, errFail.Error()) {
		t.Fatalf("got %s, want the resolver's error", got)
	}
	if ids := peopleIDs(t, db, &txLog{}); !slices.Equal(ids, []int64{101}) {
		t.Errorf("people %v, want [101]: the audit row alone", ids)
	}
	if n, err := db.Query[person]().Count(context.WithoutCancel(kept)); err != nil || n != 1 {
		t.Errorf("after the mutation, on the resolver's context: %d, %v; want 1 and no error", n, err)
	}
}

func TestMutationRollsBack(t *testing.T) {
	for _, c := range []struct {
		name, query, want string
	}{
		{
			"error",
			`mutation { fail(id: 1) }`,
			`{"errors":[{"message":"fail failed","path":["fail"]}],"data":null}`,
		},
		{
			"panic",
			`mutation { boom(id: 1) }`,
			`{"errors":[{"message":"internal system error","path":["boom"]}],"data":null}`,
		},
		{
			// A field that succeeds is rolled back with the one that fails,
			// and its data is not reported.
			"partial",
			`mutation { a: add(id: 1) b: fail(id: 2) c: add(id: 3) }`,
			`{"errors":[{"message":"fail failed","path":["b"]}],"data":null}`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			db, l := handlerDB(t)
			srv := handlerServer(t, db, nil)
			if got := post(t, context.Background(), srv, c.query); got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
			if _, txs := l.take(); !slices.Equal(txs, []string{"rollback"}) {
				t.Errorf("transactions %q, want one rolled back", txs)
			}
			if ids := peopleIDs(t, db, l); len(ids) != 0 {
				t.Errorf("people %v, want none", ids)
			}
		})
	}
}

// TestMutationCommitFails: a mutation whose resolvers succeed but whose
// transaction does not commit, here because the request was canceled, keeps
// nothing and says so.
func TestMutationCommitFails(t *testing.T) {
	db, l := handlerDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := handlerServer(t, db, &cancel)
	got := post(t, ctx, srv, `mutation { cancel(id: 1) }`)
	if want := `{"errors":[{"message":"velox: transaction: context canceled"}],"data":null}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if _, txs := l.take(); !slices.Equal(txs, []string{"error"}) {
		t.Errorf("transactions %q, want one that failed", txs)
	}
	if ids := peopleIDs(t, db, l); len(ids) != 0 {
		t.Errorf("people %v, want none", ids)
	}
}

// TestMutationPanicRollsBack: a panic gqlgen does not recover, here in a
// response interceptor inside the handler's, rolls the transaction back and
// carries on to the server, which reports it.
func TestMutationPanicRollsBack(t *testing.T) {
	db, l := handlerDB(t)
	srv := handlerServer(t, db, nil)
	srv.AroundResponses(func(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
		resp := next(ctx)
		if resp != nil {
			panic("after the response")
		}
		return resp
	})
	got := post(t, context.Background(), srv, `mutation { add(id: 1) }`)
	if want := `{"errors":[{"message":"internal system error"}],"data":null}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if _, txs := l.take(); !slices.Equal(txs, []string{"rollback"}) {
		t.Errorf("transactions %q, want one rolled back", txs)
	}
	if ids := peopleIDs(t, db, l); len(ids) != 0 {
		t.Errorf("people %v, want none", ids)
	}
}

func TestQueryHasNoTx(t *testing.T) {
	db, l := handlerDB(t)
	srv := handlerServer(t, db, nil)
	if got, want := post(t, context.Background(), srv, `{ count }`), `{"data":{"count":0}}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if stmts, txs := l.take(); !slices.Equal(stmts, []string{"select"}) || len(txs) != 0 {
		t.Errorf("statements %q, transactions %q; want one select outside a transaction", stmts, txs)
	}
}

// TestStreamedOperations: over SSE, which asks for responses until there are
// none, a mutation still runs in one transaction, and a subscription gets
// the DB with no transaction: each event's statements run on db.
func TestStreamedOperations(t *testing.T) {
	db, l := handlerDB(t)
	srv := handlerServer(t, db, nil)
	sse := func(query string) string {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"query": query})
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "text/event-stream")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w.Body.String()
	}
	got := sse(`mutation { add(id: 1) }`)
	if want := "event: next\ndata: {\"data\":{\"add\":1}}\n\n"; !strings.Contains(got, want) {
		t.Errorf("mutation: %q, want it to hold %q", got, want)
	}
	if stmts, txs := l.take(); !slices.Equal(stmts, []string{"insert in tx"}) || !slices.Equal(txs, []string{"commit"}) {
		t.Errorf("mutation: statements %q, transactions %q; want one insert in one committed transaction", stmts, txs)
	}
	got = sse(`subscription { count }`)
	if want := "event: next\ndata: {\"data\":{\"count\":1}}\n\n"; strings.Count(got, want) != 2 {
		t.Errorf("subscription: %q, want two events %q", got, want)
	}
	if stmts, txs := l.take(); !slices.Equal(stmts, []string{"select", "select"}) || len(txs) != 0 {
		t.Errorf("subscription: statements %q, transactions %q; want two selects outside a transaction", stmts, txs)
	}
}

// TestMutationResolversRunOneAtATime: below a mutation's root, gqlgen
// resolves an object's fields concurrently, but the transaction's DB is not
// safe for concurrent use, so the handler runs a mutation's resolvers one at
// a time. Pair.a and Pair.b each wait for the other to be in flight, and then
// count the people on the DB in their context. In a query they overlap; in a
// mutation they never do, and both run in its transaction. A query's wait
// has a bound only to fail a test that would hang, and a long one, so that
// load cannot make the two miss each other; a mutation's is short, since
// there the wait is expected to run out.
func TestMutationResolversRunOneAtATime(t *testing.T) {
	db, l := handlerDB(t)
	var inFlight, most atomic.Int64
	// arrived counts the resolvers that have started, and never goes down: a
	// resolver waits for the other to start, not to be in flight still, which
	// the other may no longer be by the time this one looks.
	var arrived atomic.Int64
	var wait atomic.Int64 // how long a resolver waits for the other, in ns
	wait.Store(int64(30 * time.Second))
	seen := func() {
		for n := inFlight.Load(); ; {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				return
			}
		}
	}
	side := func(ctx context.Context, args map[string]any) (any, error) {
		inFlight.Add(1)
		defer inFlight.Add(-1)
		seen()
		arrived.Add(1)
		for end := time.Now().Add(time.Duration(wait.Load())); arrived.Load() < 2 && time.Now().Before(end); {
			time.Sleep(time.Millisecond)
		}
		seen()
		db, err := veloxgql.DBFrom(ctx)
		if err != nil {
			return nil, err
		}
		return db.Query[person]().Count(ctx)
	}
	resolvers := handlerResolvers(db, nil)
	resolvers["pair"] = func(context.Context, map[string]any) (any, error) { return struct{}{}, nil }
	resolvers["Pair.a"] = side
	resolvers["Pair.b"] = side
	srv := serve(db, resolvers)

	if got, want := post(t, context.Background(), srv, `{ pair { a b } }`), `{"data":{"pair":{"a":0,"b":0}}}`; got != want {
		t.Errorf("query: %s, want %s", got, want)
	}
	if n := most.Swap(0); n != 2 {
		t.Errorf("query: at most %d resolvers in flight, want 2: the test schema does not run them concurrently", n)
	}
	l.take()

	wait.Store(int64(100 * time.Millisecond))
	arrived.Store(0)
	if got, want := post(t, context.Background(), srv, `mutation { pair { a b } }`), `{"data":{"pair":{"a":0,"b":0}}}`; got != want {
		t.Errorf("mutation: %s, want %s", got, want)
	}
	if n := most.Load(); n != 1 {
		t.Errorf("mutation: at most %d resolvers in flight, want 1", n)
	}
	if stmts, txs := l.take(); !slices.Equal(stmts, []string{"select in tx", "select in tx"}) || !slices.Equal(txs, []string{"commit"}) {
		t.Errorf("mutation: statements %q, transactions %q; want two selects in one committed transaction", stmts, txs)
	}
}
