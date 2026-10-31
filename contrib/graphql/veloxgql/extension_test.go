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

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

// handlerSDL is the schema of the extension's tests.
const handlerSDL = `
type Query {
	count: Int!
	hasDB: Boolean!
	pair: Pair!
	page(first: Int): Int!
	people(first: Int, last: Int): PeopleConnection!
}
type PeopleConnection {
	totalCount: Int!
	edges: [PeopleEdge!]!
	pageInfo: PeoplePageInfo!
}
type PeopleEdge {
	cursor: String!
}
type PeoplePageInfo {
	hasNextPage: Boolean!
}
type Mutation {
	page(first: Int): Int!
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
// A resolver finds its object in its field context's Parent.Result, as
// gqlgen's field functions give it obj; a []any result with a selection is
// a list of objects.
func testSchema(resolvers map[string]resolver) graphql.ExecutableSchema {
	return testSchemaOf(handlerSDL, resolvers)
}

// testSchemaOf is testSchema of the schema sdl.
func testSchemaOf(sdl string, resolvers map[string]resolver) graphql.ExecutableSchema {
	schema := gqlparser.MustLoadSchema(&ast.Source{Input: sdl})
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
	fc.Result = res
	if len(f.Selections) > 0 {
		if list, ok := res.([]any); ok {
			out := make(graphql.Array, len(list))
			for i, v := range list {
				ctx := graphql.WithFieldContext(ctx, &graphql.FieldContext{Index: &i, Result: v})
				out[i] = resolveSet(ctx, opCtx, f.Selections, false, true, resolvers)
			}
			return out
		}
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

// TestZeroOptionPanics: a zero Option is not valid; NewExtension panics on
// it, as velox.Open does on a zero velox.Option.
func TestZeroOptionPanics(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	panics := func(f func()) (p bool) {
		defer func() { p = recover() != nil }()
		f()
		return false
	}
	db := velox.Open(velox.SQLite, sqlDB)
	if !panics(func() { veloxgql.NewExtension(db, veloxgql.Option{}) }) {
		t.Error("NewExtension(db, Option{}) did not panic")
	}
	if !panics(func() { velox.Open(velox.SQLite, sqlDB, velox.Option{}) }) {
		t.Error("velox.Open(SQLite, sqlDB, Option{}) did not panic")
	}
}

// errFail is the error of the fail field.
var errFail = errors.New("fail failed")

// handlerServer serves testSchema through veloxgql.NewExtension(db). Its
// resolvers read the DB with DBFromContext: add inserts a person and returns
// its id; fail and boom insert one and then fail with errFail or panic; cancel
// inserts one, calls the cancel function set in *cancel, and returns its id;
// count counts the people; hasDB reports whether the context holds db itself;
// page returns the number of rows of the people connection's page paged by
// first.
func handlerServer(t *testing.T, db *velox.DB, cancel *context.CancelFunc) *handler.Server {
	t.Helper()
	return serve(db, handlerResolvers(db, cancel))
}

// handlerResolvers are handlerServer's resolvers.
func handlerResolvers(db *velox.DB, cancel *context.CancelFunc) map[string]resolver {
	insert := func(ctx context.Context, args map[string]any) (int64, error) {
		tx, err := veloxgql.DBFromContext(ctx)
		if err != nil {
			return 0, err
		}
		id := args["id"].(int64)
		return id, tx.Insert(&person{ID: id, Name: "p", Age: 1}).Exec(ctx)
	}
	count := func(ctx context.Context, _ map[string]any) (any, error) {
		db, err := veloxgql.DBFromContext(ctx)
		if err != nil {
			return nil, err
		}
		return db.Query[person]().Count(ctx)
	}
	page := func(ctx context.Context, args map[string]any) (any, error) {
		db, err := veloxgql.DBFromContext(ctx)
		if err != nil {
			return nil, err
		}
		var first *int
		if n, ok := args["first"].(int64); ok {
			first = new(int(n))
		}
		key := []gqlimpl.OrderTerm[person]{gqlimpl.Term("id", veloxgql.OrderDirectionAsc, people.ID.Asc(), people.ID.Desc(),
			func(x *person) int64 { return x.ID }, func(x *person, v int64) { x.ID = v })}
		c, err := gqlimpl.NewConnection(ctx, db.Query[person](), "Person", gqlimpl.Args{First: first}, nil, key)
		if err != nil {
			return nil, err
		}
		p, err := c.Page(ctx)
		if err != nil {
			return nil, err
		}
		return len(p.Rows), nil
	}
	return map[string]resolver{
		"people": func(context.Context, map[string]any) (any, error) { return struct{}{}, nil },
		"PeopleConnection.totalCount": func(ctx context.Context, args map[string]any) (any, error) {
			return count(ctx, args)
		},
		"count": count,
		"page":  page,
		"hasDB": func(ctx context.Context, _ map[string]any) (any, error) {
			got, err := veloxgql.DBFromContext(ctx)
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

// serve serves testSchema with resolvers through
// veloxgql.NewExtension(db, opts...).
func serve(db *velox.DB, resolvers map[string]resolver, opts ...veloxgql.Option) *handler.Server {
	return serveSchema(testSchema(resolvers), db, opts...)
}

// serveSchema serves es through veloxgql.NewExtension(db, opts...).
func serveSchema(es graphql.ExecutableSchema, db *velox.DB, opts ...veloxgql.Option) *handler.Server {
	srv := handler.New(es)
	srv.AddTransport(transport.SSE{})
	srv.AddTransport(transport.POST{})
	srv.SetRecoverFunc(func(context.Context, any) error { return gqlerror.Errorf("internal system error") })
	srv.Use(veloxgql.NewExtension(db, opts...))
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

func TestExtensionPutsTheDB(t *testing.T) {
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

	// Without the extension, a resolver fails with ErrNoDB.
	bare := handler.New(testSchema(map[string]resolver{"hasDB": func(ctx context.Context, _ map[string]any) (any, error) {
		_, err := veloxgql.DBFromContext(ctx)
		if !errors.Is(err, veloxgql.ErrNoDB) {
			t.Errorf("DBFromContext without the extension: %v, want ErrNoDB", err)
		}
		return nil, err
	}}))
	bare.AddTransport(transport.POST{})
	if got := post(t, context.Background(), bare, `{ hasDB }`); !strings.Contains(got, "veloxgql.NewExtension") {
		t.Errorf("without the extension: %s, want an error naming veloxgql.NewExtension", got)
	}
	if name := veloxgql.NewExtension(db).ExtensionName(); name != "VeloxDB" {
		t.Errorf("ExtensionName() = %q, want VeloxDB", name)
	}
}

// TestExtensionGivesMutationsItsTx: DBFromContext gives a mutation the
// extension runs in a transaction that transaction's DB, where
// gqlimpl.Atomic makes its savepoints; a query, a mutation WithTxFilter
// leaves out and NewContext get db itself.
func TestExtensionGivesMutationsItsTx(t *testing.T) {
	db, _ := handlerDB(t)
	var got *velox.DB
	read := func(ctx context.Context, args map[string]any) (any, error) {
		var err error
		got, err = veloxgql.DBFromContext(ctx)
		return args["id"], err
	}
	srv := serve(db, map[string]resolver{"add": read, "count": func(ctx context.Context, _ map[string]any) (any, error) {
		return read(ctx, map[string]any{"id": 0})
	}}, veloxgql.WithTxFilter(func(_ context.Context, op *ast.OperationDefinition) bool { return op.Name != "Plain" }))

	_ = post(t, context.Background(), srv, `mutation { add(id: 1) }`)
	if got == nil || got == db {
		t.Errorf("mutation: DBFromContext %p, db %p; want the transaction's DB", got, db)
	}
	for name, query := range map[string]string{
		"query":                   `{ count }`,
		"mutation left out of tx": `mutation Plain { add(id: 2) }`,
	} {
		got = nil
		_ = post(t, context.Background(), srv, query)
		if got != db {
			t.Errorf("%s: DBFromContext %p, want db %p", name, got, db)
		}
	}
	got, err := veloxgql.DBFromContext(veloxgql.NewExtension(db).NewContext(context.Background()))
	if err != nil || got != db {
		t.Errorf("NewContext: DBFromContext %p, %v; want db %p", got, err, db)
	}
}

func TestExtensionNilDB(t *testing.T) {
	const want = "veloxgql.NewExtension: nil DB"
	if err := veloxgql.NewExtension(nil).Validate(nil); err == nil || err.Error() != want {
		t.Errorf("Validate: %v, want %q", err, want)
	}
	for name, use := range map[string]func(){
		"Use":        func() { handler.New(testSchema(nil)).Use(veloxgql.NewExtension(nil)) },
		"NewContext": func() { veloxgql.NewExtension(nil).NewContext(context.Background()) },
	} {
		func() {
			defer func() {
				if p := recover(); p == nil || fmt.Sprint(p) != want {
					t.Errorf("%s with a nil DB panicked with %v, want %q", name, p, want)
				}
			}()
			use()
		}()
	}
}

// TestExtensionRefusesNegativeLimits: a negative limit would fail every
// request it bounds, so Validate refuses it, naming the field, and so do the
// server's Use and Context, with the same text. A zero field is the default.
func TestExtensionRefusesNegativeLimits(t *testing.T) {
	db, _ := handlerDB(t)
	if err := veloxgql.NewExtension(db).Validate(nil); err != nil {
		t.Fatalf("the defaults: %v", err)
	}
	if err := veloxgql.NewExtension(db, veloxgql.WithLimits(veloxgql.Limits{})).Validate(nil); err != nil {
		t.Fatalf("zero limits: %v", err)
	}
	for name, set := range map[string]func(*veloxgql.Limits){
		"MaxPage":        func(l *veloxgql.Limits) { l.MaxPage = -1 },
		"MaxList":        func(l *veloxgql.Limits) { l.MaxList = -1 },
		"MaxFilterDepth": func(l *veloxgql.Limits) { l.MaxFilterDepth = -1 },
		"MaxFilterWidth": func(l *veloxgql.Limits) { l.MaxFilterWidth = -1 },
		"MaxFilterNodes": func(l *veloxgql.Limits) { l.MaxFilterNodes = -1 },
		"MaxOrderTerms":  func(l *veloxgql.Limits) { l.MaxOrderTerms = -1 },
		"MaxCost":        func(l *veloxgql.Limits) { l.MaxCost = -1 },
	} {
		var l veloxgql.Limits
		set(&l)
		ext := veloxgql.NewExtension(db, veloxgql.WithLimits(l))
		want := "veloxgql.NewExtension: Limits." + name + " is -1, and a limit must not be negative (zero is the default)"
		if err := ext.Validate(nil); err == nil || err.Error() != want {
			t.Errorf("%s -1: Validate: %v, want %q", name, err, want)
		}
		for how, use := range map[string]func(){
			"Use":        func() { handler.New(testSchema(nil)).Use(ext) },
			"NewContext": func() { ext.NewContext(context.Background()) },
		} {
			func() {
				defer func() {
					if p := recover(); p == nil || !strings.Contains(fmt.Sprint(p), want) {
						t.Errorf("%s -1: %s panicked with %v, want %q", name, how, p, want)
					}
				}()
				use()
			}()
		}
	}
}

// TestTwoServersDiffer: the limits are each server's, in its operations'
// contexts, not the process's. Two servers, MaxPage 2 and 3, serve
// requests in parallel: first 3 fails with ErrPageTooLarge on the first and
// passes on the second. A mutation's transaction keeps its server's limits.
func TestTwoServersDiffer(t *testing.T) {
	db, _ := handlerDB(t)
	for id := int64(1); id <= 5; id++ {
		if err := db.Insert(&person{ID: id, Name: "p", Age: 1}).Exec(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	two := serve(db, handlerResolvers(db, nil), veloxgql.WithLimits(veloxgql.Limits{MaxPage: 2}))
	three := serve(db, handlerResolvers(db, nil), veloxgql.WithLimits(veloxgql.Limits{MaxPage: 3}))
	const tooLarge = `"message":"veloxgql: Person connection: page is over the maximum: first is 3, and the most is 2","path":["page"]`
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for _, q := range []string{`{ page(first: 3) }`, `mutation { page(first: 3) }`} {
				if got := post(t, context.Background(), two, q); !strings.Contains(got, tooLarge) {
					t.Errorf("MaxPage 2: %s: %s, want the error %s", q, got, tooLarge)
				}
				if got, want := post(t, context.Background(), three, q), `{"data":{"page":3}}`; got != want {
					t.Errorf("MaxPage 3: %s: %s, want %s", q, got, want)
				}
			}
			// With no first, the page is the server's MaxPage.
			if got, want := post(t, context.Background(), two, `{ page }`), `{"data":{"page":2}}`; got != want {
				t.Errorf("MaxPage 2, no first: %s, want %s", got, want)
			}
		})
	}
	wg.Wait()
	// The error is ErrPageTooLarge.
	ctx := veloxgql.NewExtension(db, veloxgql.WithLimits(veloxgql.Limits{MaxPage: 2})).NewContext(context.Background())
	if _, err := handlerResolvers(db, nil)["page"](ctx, map[string]any{"first": int64(3)}); !errors.Is(err, veloxgql.ErrPageTooLarge) {
		t.Errorf("first 3 under MaxPage 2: %v, want ErrPageTooLarge", err)
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
// DB given to NewExtension, not the transaction's, fails with ErrOutsideTx, before
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
		t.Errorf("a statement on the extension's DB: %v, want ErrOutsideTx", outside)
	}
}

// TestMutationWithoutTx: a mutation's resolver holds only the transaction's
// context. Under velox.WithoutTx it, its statement on the DB given to
// NewExtension runs outside the transaction: the audit row survives the
// mutation's rollback. Once the mutation has ended, the context the resolver kept marks
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
			tx, err := veloxgql.DBFromContext(ctx)
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
// response interceptor inside the extension's, rolls the transaction back and
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
// safe for concurrent use, so the extension runs a mutation's resolvers one
// at a time. Pair.a and Pair.b each wait for the other to be in flight, and then
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
		db, err := veloxgql.DBFromContext(ctx)
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

// TestTxOptionsRefuseRetry: a retry among the options is refused, since
// gqlgen makes the response once and the operation cannot run again.
func TestTxOptionsRefuseRetry(t *testing.T) {
	db, _ := handlerDB(t)
	err := veloxgql.NewExtension(db, veloxgql.WithTxOptions(velox.TxTimeout(time.Second), velox.TxRetrySerializable(3))).Validate(nil)
	want := "veloxgql.WithTxOptions: TxRetrySerializable(3) would run the mutation again, and gqlgen makes its response once"
	if err == nil || err.Error() != want {
		t.Errorf("Validate = %v, want %q", err, want)
	}
	for name, opts := range map[string][]velox.TxOption{
		"the last retry asks for none": {velox.TxRetrySerializable(3), velox.TxRetrySerializable(0)},
		"isolation":                    {velox.TxIsolation(sql.LevelSerializable)},
	} {
		if err := veloxgql.NewExtension(db, veloxgql.WithTxOptions(opts...)).Validate(nil); err != nil {
			t.Errorf("%s: Validate = %v, want nil", name, err)
		}
	}
	// Given twice, the last wins.
	e := veloxgql.NewExtension(db, veloxgql.WithTxOptions(velox.TxRetrySerializable(3)), veloxgql.WithTxOptions(velox.TxReadOnly()))
	if err := e.Validate(nil); err != nil {
		t.Errorf("the last WithTxOptions should win: %v", err)
	}
}

// TestTxOptionsApply: the options reach the mutation's transaction.
func TestTxOptionsApply(t *testing.T) {
	// post returns when the response is written, after the resolvers and the
	// hooks have returned, so these are read from the test's goroutine only
	// once the server's are done; the mutex states it for the race detector.
	var mu sync.Mutex
	var iso sql.IsolationLevel
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	db := velox.Open(velox.SQLite, sqlDB, velox.WithTxAround(func(ctx context.Context, tx *velox.TxInfo, next func(context.Context) error) error {
		mu.Lock()
		iso = tx.Isolation
		mu.Unlock()
		return next(ctx)
	}))
	var left time.Duration
	var has bool
	resolvers := map[string]resolver{
		"add": func(ctx context.Context, args map[string]any) (any, error) {
			d, ok := ctx.Deadline()
			mu.Lock()
			has, left = ok, time.Until(d)
			mu.Unlock()
			return args["id"], nil
		},
	}
	srv := serve(db, resolvers, veloxgql.WithTxOptions(velox.TxIsolation(sql.LevelSerializable)))
	_ = post(t, context.Background(), srv, `mutation { add(id: 1) }`)
	mu.Lock()
	defer mu.Unlock()
	if iso != sql.LevelSerializable {
		t.Errorf("isolation %v, want serializable", iso)
	}
	srv = serve(db, resolvers, veloxgql.WithTxOptions(velox.TxTimeout(50*time.Millisecond)))
	mu.Unlock()
	_ = post(t, context.Background(), srv, `mutation { add(id: 1) }`)
	mu.Lock()
	if !has || left > 50*time.Millisecond {
		t.Errorf("deadline %v (set %v), want at most 50ms away", left, has)
	}
}

// TestTxFilter: a mutation the filter leaves out runs with no transaction.
func TestTxFilter(t *testing.T) {
	db, l := handlerDB(t)
	var names []string
	srv := serve(db, handlerResolvers(db, nil), veloxgql.WithTxFilter(func(ctx context.Context, op *ast.OperationDefinition) bool {
		names = append(names, op.Name)
		return op.Name != "Login"
	}))
	if got, want := post(t, context.Background(), srv, `mutation Login { add(id: 1) }`), `{"data":{"add":1}}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	stmts, txs := l.take()
	if !slices.Equal(stmts, []string{"insert"}) || len(txs) != 0 {
		t.Errorf("Login: statements %q, transactions %q; want one insert outside a transaction", stmts, txs)
	}
	_ = post(t, context.Background(), srv, `mutation Other { add(id: 2) }`)
	stmts, txs = l.take()
	if !slices.Equal(stmts, []string{"insert in tx"}) || !slices.Equal(txs, []string{"commit"}) {
		t.Errorf("Other: statements %q, transactions %q; want one insert in one transaction", stmts, txs)
	}
	if !slices.Equal(names, []string{"Login", "Other"}) {
		t.Errorf("the filter saw %q, want each operation once", names)
	}
	// A nil filter means every mutation runs in a transaction.
	srv = serve(db, handlerResolvers(db, nil), veloxgql.WithTxFilter(nil))
	_ = post(t, context.Background(), srv, `mutation Login { add(id: 3) }`)
	if _, txs = l.take(); len(txs) != 1 {
		t.Errorf("nil filter: transactions %q, want one", txs)
	}
}

// TestTxFilterReadsItsOwnWrites: a mutation the filter leaves out runs in a
// session, so that on a DB with replicas its read after its write goes to the
// primary, not to a replica that does not have the row yet.
func TestTxFilterReadsItsOwnWrites(t *testing.T) {
	replica, err := sql.Open("sqlite", "file:replica?mode=memory&cache=private")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = replica.Close() })
	replica.SetMaxOpenConns(1)
	if _, err := replica.Exec(`CREATE TABLE people (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	db, _ := handlerDB(t, velox.WithReplicas(velox.SQLConn(replica)))
	resolvers := handlerResolvers(db, nil)
	addCount := func(ctx context.Context, args map[string]any) (any, error) {
		if _, err := resolvers["add"](ctx, args); err != nil {
			return nil, err
		}
		return resolvers["count"](ctx, nil)
	}
	srv := serve(db, map[string]resolver{
		"add": addCount,
	}, veloxgql.WithTxFilter(func(context.Context, *ast.OperationDefinition) bool { return false }))
	if got, want := post(t, context.Background(), srv, `mutation { add(id: 1) }`), `{"data":{"add":1}}`; got != want {
		t.Errorf("got %s, want %s: the read after the write did not go to the primary", got, want)
	}
}

// TestTxFilterSkipsTheFieldLock: the resolvers of a mutation the filter
// leaves out run concurrently, as a query's do, since no transaction's DB is
// shared.
func TestTxFilterSkipsTheFieldLock(t *testing.T) {
	db, _ := handlerDB(t)
	var arrived atomic.Int64
	side := func(context.Context, map[string]any) (any, error) {
		arrived.Add(1)
		for end := time.Now().Add(10 * time.Second); arrived.Load() < 2 && time.Now().Before(end); {
			time.Sleep(time.Millisecond)
		}
		return arrived.Load(), nil
	}
	srv := serve(db, map[string]resolver{
		"pair":   func(context.Context, map[string]any) (any, error) { return struct{}{}, nil },
		"Pair.a": side,
		"Pair.b": side,
	}, veloxgql.WithTxFilter(func(context.Context, *ast.OperationDefinition) bool { return false }))
	if got, want := post(t, context.Background(), srv, `mutation { pair { a b } }`), `{"data":{"pair":{"a":2,"b":2}}}`; got != want {
		t.Errorf("got %s, want %s: both resolvers should be in flight together", got, want)
	}
}

// TestTooCostlyRunsNothing: a request over MaxCost is refused before any
// resolver runs, with COST_LIMIT_EXCEEDED and data null (the server's
// presenter is gqlgen's here, which shows the message as it is; see
// TestTooCostlyPresented for ErrorPresenter); one at the limit runs (Review Focus 4).
func TestTooCostlyRunsNothing(t *testing.T) {
	db, l := handlerDB(t)
	srv := serve(db, handlerResolvers(db, nil), veloxgql.WithLimits(veloxgql.Limits{MaxCost: 30}))

	// people(first: 30) { totalCount } costs 1 + 30 * 1.
	got := post(t, context.Background(), srv, `{ people(first: 30) { totalCount } }`)
	const want = `{"errors":[{"message":"veloxgql: request is too costly: it costs 31, and the most is 30","extensions":{"code":"COST_LIMIT_EXCEEDED"}}],"data":null}`
	if got != want {
		t.Errorf("cost 31:\n got %s\nwant %s", got, want)
	}
	if stmts, _ := l.take(); len(stmts) != 0 {
		t.Errorf("cost 31 ran statements: %v", stmts)
	}

	got = post(t, context.Background(), srv, `{ people(first: 29) { totalCount } }`)
	if want := `{"data":{"people":{"totalCount":0}}}`; got != want {
		t.Errorf("cost 30:\n got %s\nwant %s", got, want)
	}
	if stmts, _ := l.take(); len(stmts) != 1 {
		t.Errorf("cost 30 ran %v, want one select", stmts)
	}

	// A mutation is checked as well, before its transaction.
	got = post(t, context.Background(), srv, `mutation { a: page(first: 1) b: page(first: 1) c: page(first: 1) d: page(first: 1) e: page(first: 1) f: page(first: 1) g: page(first: 1) h: page(first: 1) i: page(first: 1) j: page(first: 1) k: page(first: 1) l: page(first: 1) m: page(first: 1) n: page(first: 1) o: page(first: 1) p: page(first: 1) q: page(first: 1) r: page(first: 1) s: page(first: 1) t: page(first: 1) u: page(first: 1) v: page(first: 1) w: page(first: 1) x: page(first: 1) y: page(first: 1) z: page(first: 1) a1: page(first: 1) b1: page(first: 1) c1: page(first: 1) d1: page(first: 1) e1: page(first: 1) }`)
	if !strings.Contains(got, `COST_LIMIT_EXCEEDED`) {
		t.Errorf("a mutation of cost 31: %s", got)
	}
	if stmts, txs := l.take(); len(stmts) != 0 || len(txs) != 0 {
		t.Errorf("a refused mutation ran %v, %v", stmts, txs)
	}
}
