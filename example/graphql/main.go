// Command graphql serves a GraphQL API over velox entities with gqlgen. The
// schema package declares the entities with graphql.* annotations;
// velox-gen with the velox-graphql plugin writes gen/, the entities, the
// GraphQL schema gen/velox.graphqls and the Go it binds to; gqlgen writes
// graph/ from gqlgen.yml. The application writes the root query's
// resolvers, one line each, and this file.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	gqlgen "github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/vektah/gqlparser/v2/gqlerror"
	_ "modernc.org/sqlite"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
	"github.com/syssam/velox2/example/graphql/gen"
	"github.com/syssam/velox2/example/graphql/graph"
)

func main() {
	addr := flag.String("addr", "localhost:8080", "the address to serve on")
	flag.Parse()
	if err := serve(context.Background(), *addr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serve(ctx context.Context, addr string) error {
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	db, err := open(ctx, sqlDB)
	if err != nil {
		return err
	}
	if err := seed(ctx, db); err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/graphql", orgFromHeader(roleFromHeader(server(db))))
	fmt.Printf("POST GraphQL to http://%s/graphql, with X-Org-ID: 1 or 2 for posts, and X-Role: admin for emails\n", addr)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	return srv.ListenAndServe()
}

// complexityLimit is the most a request may cost, in gqlgen's complexity: a
// field costs 1 and its children's cost is added, so it is the number of
// fields the request selects, aliases and nested connections included.
// users, posts and tags nested, each with its edges and node, with a few
// scalars (the deepest query of main_test.go) cost 13. gqlgen counts no
// __schema field, so GraphiQL's schema query costs nothing; __type(name:)
// costs a few points, as any field does. veloxgql's Limits each bound one
// argument (MaxPage rows of a connection, and so on), so this one bounds the
// request as a whole. It does not multiply by first and last: a cost that
// does is a later veloxgql feature.
const complexityLimit = 200

// server is the GraphQL endpoint: gqlgen's server on the generated schema,
// with veloxgql.NewExtension first, so that every operation's context holds
// db and the default limits, where the generated code reads them, and a
// limit on the cost of a request. veloxgql.ErrorPresenter shows a client
// only what is safe, with a code: gqlgen's default presenter would show it
// every error's text, the driver's and velox's, which name tables, entities
// and constraints. It logs the rest with slog, and says "internal error".
// The directive @admin is the function admin.
func server(db *velox.DB) http.Handler {
	srv := handler.New(graph.NewExecutableSchema(graph.Config{
		Resolvers:  &graph.Resolver{},
		Directives: graph.DirectiveRoot{Admin: admin},
	}))
	srv.Use(veloxgql.NewExtension(db))
	srv.SetErrorPresenter(veloxgql.ErrorPresenter(nil))
	srv.AddTransport(transport.POST{})
	srv.Use(extension.FixedComplexityLimit(complexityLimit))
	srv.Use(extension.Introspection{})
	return srv
}

// orgFromHeader puts the tenant, the organization the X-Org-ID header names,
// in the request's context. Every statement on Post is scoped to it, and
// fails with velox.ErrNoTenant without it. This example lets the client
// choose, to be tried with curl, which is for this demo only: a client that
// names its own tenant can read every tenant's rows. A real server takes the
// tenant from what authenticates the request.
func orgFromHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := r.Header.Get("X-Org-ID"); h != "" {
			org, err := strconv.ParseInt(h, 10, 64)
			if err != nil {
				http.Error(w, "X-Org-ID is not a number", http.StatusBadRequest)
				return
			}
			r = r.WithContext(velox.WithTenant(r.Context(), org)) // an int64: the tenant column's Go type
		}
		next.ServeHTTP(w, r)
	})
}

// roleKey is the context key of the request's role.
type roleKey struct{}

// roleFromHeader puts the role the X-Role header names in the request's
// context, where admin reads it. Like X-Org-ID, it is for this demo only: a
// client that names its own role is whoever it says. A real server takes the
// role from what authenticates the request.
func roleFromHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := r.Header.Get("X-Role"); h != "" {
			r = r.WithContext(context.WithValue(r.Context(), roleKey{}, h))
		}
		next.ServeHTTP(w, r)
	})
}

// admin implements the directive @admin, on a field or on a where input's
// field: it resolves the field, or reads the predicate, only when the role is
// admin. Otherwise it fails with an error of the application's, which
// veloxgql.ErrorPresenter shows the client as it is, code and all.
func admin(ctx context.Context, _ any, next gqlgen.Resolver) (any, error) {
	if role, _ := ctx.Value(roleKey{}).(string); role != "admin" {
		return nil, &gqlerror.Error{
			Path:       gqlgen.GetPath(ctx),
			Message:    "the admin role is required",
			Extensions: map[string]any{"code": veloxgql.CodeForbidden},
		}
	}
	return next(ctx)
}

// ddl creates the tables by hand: planning migrations from the schemas is a
// later velox2 sub-project. post_tags is the join table of Post.Tags.
const ddl = `
CREATE TABLE users (
	id            INTEGER PRIMARY KEY,
	name          TEXT NOT NULL,
	email         TEXT NOT NULL,
	role          TEXT NOT NULL,
	joined_at     DATETIME NOT NULL,
	password_hash TEXT NOT NULL
);
CREATE TABLE posts (
	id           INTEGER PRIMARY KEY,
	org_id       INTEGER NOT NULL,
	author_id    INTEGER NOT NULL REFERENCES users (id),
	title        TEXT NOT NULL,
	status       TEXT NOT NULL,
	views        INTEGER NOT NULL,
	published_at DATETIME
);
CREATE TABLE tags (
	id   INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	rank INTEGER NOT NULL
);
CREATE TABLE post_tags (
	post_id INTEGER NOT NULL REFERENCES posts (id),
	tag_id  INTEGER NOT NULL REFERENCES tags (id),
	PRIMARY KEY (post_id, tag_id)
)`

// open creates the tables on sqlDB, an in-memory database, and opens a DB on
// it with opts.
func open(ctx context.Context, sqlDB *sql.DB, opts ...velox.Option) (*velox.DB, error) {
	sqlDB.SetMaxOpenConns(1) // an in-memory database lives as long as its one connection
	if _, err := sqlDB.ExecContext(ctx, ddl); err != nil {
		return nil, err
	}
	return velox.Open(velox.SQLite, sqlDB, opts...), nil
}

// The organizations, the tenants: int64, the tenant column's Go type.
const (
	acme   int64 = 1
	globex int64 = 2
)

// seed inserts three users, five posts (four of acme's, one of globex's) and
// three tags.
func seed(ctx context.Context, db *velox.DB) error {
	day := func(m time.Month, d int) time.Time { return time.Date(2026, m, d, 9, 0, 0, 0, time.UTC) }
	users := []gen.User{
		{ID: 1, Name: "Ada", Email: "ada@example.com", Role: gen.UserRoleAdmin, JoinedAt: day(1, 5), PasswordHash: "x"},
		{ID: 2, Name: "Brian", Email: "brian@example.com", Role: gen.UserRoleMember, JoinedAt: day(2, 10), PasswordHash: "x"},
		{ID: 3, Name: "Chen", Email: "chen@example.com", Role: gen.UserRoleMember, JoinedAt: day(3, 15), PasswordHash: "x"},
	}
	if err := db.InsertAll(users).Exec(ctx); err != nil {
		return err
	}
	pub := func(m time.Month, d int) *time.Time { t := day(m, d); return &t }
	posts := map[int64][]gen.Post{
		acme: {
			{ID: 1, AuthorID: 1, Title: "Alpha", Status: gen.PostStatusPublished, Views: 10, PublishedAt: pub(4, 1)},
			{ID: 2, AuthorID: 1, Title: "Beta", Status: gen.PostStatusDraft, Views: 5},
			{ID: 3, AuthorID: 2, Title: "Gamma", Status: gen.PostStatusPublished, Views: 10, PublishedAt: pub(5, 1)},
			{ID: 4, AuthorID: 3, Title: "Delta", Status: gen.PostStatusPublished, Views: 7, PublishedAt: pub(6, 1)},
		},
		globex: {
			{ID: 5, AuthorID: 1, Title: "Globex plans", Status: gen.PostStatusPublished, Views: 99, PublishedAt: pub(4, 2)},
		},
	}
	for _, org := range []int64{acme, globex} {
		if err := db.InsertAll(posts[org]).Exec(velox.WithTenant(ctx, org)); err != nil {
			return err
		}
	}
	tags := []gen.Tag{{ID: 1, Name: "go", Rank: 1}, {ID: 2, Name: "sql", Rank: 2}, {ID: 3, Name: "graphql", Rank: 3}}
	if err := db.InsertAll(tags).Exec(ctx); err != nil {
		return err
	}
	links := []gen.PostTag{{PostID: 1, TagID: 1}, {PostID: 1, TagID: 3}, {PostID: 3, TagID: 2}, {PostID: 5, TagID: 1}}
	if err := db.InsertAll(links).Exec(ctx); err != nil {
		return err
	}
	return nil
}
