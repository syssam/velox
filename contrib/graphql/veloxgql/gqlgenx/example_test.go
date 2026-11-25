package gqlgenx_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
)

// An ID is the entity's key. A client may send it as a string or as an
// integer, and gets it back as a string, as the GraphQL specification says.
func ExampleUnmarshalInt64ID() {
	key, err := gqlgenx.UnmarshalInt64ID("42")
	fmt.Println(key, err)
	key, err = gqlgenx.UnmarshalInt64ID(json.Number("42"))
	fmt.Println(key, err)
	_, err = gqlgenx.UnmarshalInt64ID(4.2)
	fmt.Println(err)
	var b strings.Builder
	gqlgenx.MarshalInt64ID(42).MarshalGQL(&b)
	fmt.Println(b.String())
	// Output:
	// 42 <nil>
	// 42 <nil>
	// veloxgql: ID must be a string of an integer, or an integer
	// "42"
}

const exampleSDL = `
type Query { users(first: Int): UserConnection! }
type UserConnection { edges: [UserEdge!]!, pageInfo: PageInfo! }
type UserEdge { node: User!, cursor: String! }
type PageInfo { hasNextPage: Boolean! }
type User { id: ID!, name: String!, friends(first: Int): UserConnection! }
`

// Cost prices a request before it runs: each field is 1, and a connection
// multiplies what it selects by its page size.
func ExampleCost() {
	schema := gqlparser.MustLoadSchema(&ast.Source{Input: exampleSDL})
	doc, errs := gqlparser.LoadQueryWithRules(schema, `{ users(first: 10) { edges { node { name friends(first: 5) { edges { node { name } } } } } } }`, nil)
	if errs != nil {
		fmt.Println(errs)
		return
	}
	l := veloxgql.Limits{MaxPage: 100, MaxList: 100}
	fmt.Println(gqlgenx.Cost(schema, doc.Operations[0], nil, l))
	// Output:
	// 342
}

// Transactioner runs each mutation in one transaction of the DB its
// resolvers hold, which commits when the response has no errors.
func ExampleTransactioner() {
	var (
		db *velox.DB
		es graphql.ExecutableSchema
	)
	srv := handler.New(es)
	srv.AddTransport(transport.POST{})
	srv.Use(gqlgenx.Transactioner{DB: db, Options: []velox.TxOption{velox.TxIsolation(sql.LevelSerializable)}})
}

// A server open to clients opts in to each limit it wants. The limits are the
// DB's, which veloxgql.WithLimits gives.
func Example_hardenedServer() {
	var (
		sqlDB *sql.DB
		es    graphql.ExecutableSchema
	)
	db := velox.Open(velox.Postgres, sqlDB, veloxgql.WithLimits(veloxgql.Limits{MaxCost: 50000}))
	srv := handler.New(es)
	srv.AddTransport(transport.POST{})
	srv.SetDisableSuggestion(true)
	srv.SetErrorPresenter(gqlgenx.ErrorPresenter(func(ctx context.Context, err error) {
		slog.ErrorContext(ctx, "graphql", "err", err)
	}))
	srv.Use(gqlgenx.Transactioner{DB: db})
	srv.Use(gqlgenx.RequestLimit(db))
	srv.Use(gqlgenx.CostLimit(db))
	srv.Use(gqlgenx.ResponseLimit(db))
	http.Handle("/graphql", gqlgenx.MaxBodyBytes(1<<20)(srv))
}

// MaxBodyBytes refuses a request body over its limit with 413.
func ExampleMaxBodyBytes() {
	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	h := gqlgenx.MaxBodyBytes(8)(ok)
	for _, body := range []string{`{}`, `{"query":"{ id }"}`} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
		fmt.Println(w.Code)
	}
	// Output:
	// 200
	// 413
}
