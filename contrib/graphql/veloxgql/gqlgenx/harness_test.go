package gqlgenx_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

// handlerSDL is the schema of the handler tests.
const handlerSDL = `
type Query {
	count: Int!
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

// resolverOf is the resolver of the field f, whose key in resolvers is key.
// A field of the introspection or __typename that the test gives no resolver
// is resolved as gqlgen's generated code does: __schema and __type fail with
// "introspection disabled" when the operation has introspection off, and are
// otherwise empty.
func resolverOf(opCtx *graphql.OperationContext, f graphql.CollectedField, key string, resolvers map[string]resolver) resolver {
	if r := resolvers[key]; r != nil {
		return r
	}
	switch {
	case f.Name == "__typename":
		return func(context.Context, map[string]any) (any, error) { return f.ObjectDefinition.Name, nil }
	case f.Name == "__schema" || f.Name == "__type":
		if opCtx.DisableIntrospection {
			return func(context.Context, map[string]any) (any, error) { return nil, errors.New("introspection disabled") }
		}
		return func(context.Context, map[string]any) (any, error) { return struct{}{}, nil }
	case strings.HasPrefix(f.ObjectDefinition.Name, "__"):
		return func(context.Context, map[string]any) (any, error) { return nil, nil }
	}
	return nil
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
				return resolveField(ctx, opCtx, f, resolverOf(opCtx, f, key, resolvers), resolvers)
			})
		} else {
			fs.Values[i] = resolveField(ctx, opCtx, f, resolverOf(opCtx, f, key, resolvers), resolvers)
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

// errFail is the error of the fail field.
var errFail = errors.New("fail failed")

// handlerServer serves testSchema under gqlgenx.Transactioner{DB: db}. Its
// resolvers use db, as a Resolver holding it does: add inserts a person and
// returns its id; fail and boom insert one and then fail with errFail or
// panic; cancel inserts one, calls the cancel function set in *cancel, and
// returns its id; count counts the people; page returns the number of rows
// of the people connection's page paged by first.
func handlerServer(t *testing.T, db *velox.DB, cancel *context.CancelFunc) *handler.Server {
	t.Helper()
	return serve(handlerResolvers(db, cancel), gqlgenx.Transactioner{DB: db})
}

// handlerResolvers are handlerServer's resolvers.
func handlerResolvers(db *velox.DB, cancel *context.CancelFunc) map[string]resolver {
	insert := func(ctx context.Context, args map[string]any) (int64, error) {
		id := args["id"].(int64)
		return id, db.Insert(&person{ID: id, Name: "p", Age: 1}).Exec(ctx)
	}
	count := func(ctx context.Context, _ map[string]any) (any, error) {
		return db.Query[person]().Count(ctx)
	}
	page := func(ctx context.Context, args map[string]any) (any, error) {
		var first *int
		if n, ok := args["first"].(int64); ok {
			first = new(int(n))
		}
		key := &gqlgraph.Type{Name: "Person", Key: []gqlgraph.Column{{Name: "id", Index: int(people.ID.I)}}}
		c, err := gqlgraph.Paginate(ctx, gqlgraph.Query(untyped.Query(db, untyped.TableOf[person]())), key, gqlgraph.Args{First: first})
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

// serve serves testSchema with resolvers, under the extensions exts in the
// order given.
func serve(resolvers map[string]resolver, exts ...graphql.HandlerExtension) *handler.Server {
	return serveSchema(testSchema(resolvers), exts...)
}

// serveSchema serves es under the extensions exts in the order given.
func serveSchema(es graphql.ExecutableSchema, exts ...graphql.HandlerExtension) *handler.Server {
	srv := handler.New(es)
	srv.AddTransport(transport.SSE{})
	srv.AddTransport(transport.POST{})
	srv.SetRecoverFunc(func(context.Context, any) error { return gqlerror.Errorf("internal system error") })
	for _, e := range exts {
		srv.Use(e)
	}
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
	rows, err := db.Query[person]().OrderBy(people.ID.Asc()).AllowUnbounded().All(context.Background())
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

// graphiQL is the query GraphiQL sends for the schema, as internal/cost has it.
const graphiQL = `query IntrospectionQuery {
	__schema {
		queryType { name }
		mutationType { name }
		subscriptionType { name }
		types { ...FullType }
		directives { name description locations args { ...InputValue } }
	}
}
fragment FullType on __Type {
	kind name description
	fields(includeDeprecated: true) { name description args { ...InputValue } type { ...TypeRef } isDeprecated deprecationReason }
	inputFields { ...InputValue }
	interfaces { ...TypeRef }
	enumValues(includeDeprecated: true) { name description isDeprecated deprecationReason }
	possibleTypes { ...TypeRef }
}
fragment InputValue on __InputValue { name description type { ...TypeRef } defaultValue }
fragment TypeRef on __Type {
	kind name
	ofType { kind name ofType { kind name ofType { kind name ofType { kind name ofType { kind name ofType { kind name ofType { kind name } } } } } } }
}`

// spell is f(0) to f(n-1), joined by a space.
func spell(n int, f func(i int) string) string {
	var b strings.Builder
	for i := range n {
		b.WriteString(f(i))
		b.WriteByte(' ')
	}
	return b.String()
}
