// Command graphql serves a GraphQL API over velox entities with gqlgen. The
// schema package declares the entities with graphql.* annotations;
// velox-gen with the velox-graphql plugin writes gen/, the entities, the
// GraphQL schema gen/velox.graphql and the Go it binds to; gqlgen writes
// graph/ from gqlgen.yml. The application declares its mutations in
// graph/app.graphql, and writes the root query's and the mutations'
// resolvers, most of them one line each, and this file.
package main

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	gqlgen "github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/vektah/gqlparser/v2/gqlerror"
	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/examples/graphql/gen"
	"github.com/syssam/velox/examples/graphql/graph"
	"github.com/syssam/velox/migrate"
)

func main() {
	addr := flag.String("addr", "localhost:8080", "the address to serve on")
	flag.Parse()
	ln, err := net.Listen("tcp", *addr)
	if err == nil {
		err = serve(context.Background(), ln)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// serve serves on ln until it fails, ctx ends or the process gets SIGINT or
// SIGTERM, then shuts the server down, so that the deferred cleanup of the
// database runs. It closes ln.
func serve(ctx context.Context, ln net.Listener) error {
	defer func() { _ = ln.Close() }() // a second Close, once Serve has closed it, is harmless
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	dir, err := os.MkdirTemp("", "velox-graphql-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	sqlDB, err := sql.Open("sqlite", filepath.Join(dir, "graphql.db"))
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
	mux.Handle("/graphql", orgFromHeader(roleFromHeader(userFromHeader(server(db)))))
	fmt.Printf("POST GraphQL to http://%s/graphql, with X-Org-ID: 1 or 2 for posts, X-Role: admin for emails, and X-User: 1, 2 or 3 to create a post\n", ln.Addr())
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-done // in-flight requests are finished before the database closes
	return nil
}

// server is the GraphQL endpoint: gqlgen's server on the generated schema,
// made safe by veloxgql.Setup. Every operation's context holds db and the default
// limits, where the generated code reads them; a request has a limit on its body,
// its query's tokens and fields, what it costs and its introspection, before any
// of it is validated or run. veloxgql.ErrorPresenter shows a client only what is
// safe, with a code: gqlgen's default presenter would show it every error's text,
// the driver's and velox's, which name tables, entities and constraints. It logs
// the rest with slog, and says "internal error". The directive @admin is the
// function admin.
func server(db *velox.DB, opts ...veloxgql.Option) http.Handler {
	srv := handler.New(graph.NewExecutableSchema(graph.Config{
		Resolvers:  &graph.Resolver{},
		Directives: graph.DirectiveRoot{Admin: admin},
	}))
	h := veloxgql.Setup(srv, db, opts...)
	srv.AddTransport(transport.POST{})
	srv.Use(extension.Introspection{})
	return h
}

// orgFromHeader puts the organization the X-Org-ID header names in the
// request's context. Every statement on Post is scoped to it, and fails with
// scope.ErrNoScope without it. This example lets the client choose, to be
// tried with curl, which is for this demo only: a client that names its own
// organization can read every organization's rows. A real server takes the
// organization from what authenticates the request.
func orgFromHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := r.Header.Get("X-Org-ID"); h != "" {
			org, err := strconv.ParseInt(h, 10, 64)
			if err != nil {
				http.Error(w, "X-Org-ID is not a number", http.StatusBadRequest)
				return
			}
			r = r.WithContext(gen.OrgKey.With(r.Context(), org))
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

// userFromHeader puts the user the X-User header names, by its key, in the
// request's context: the viewer, whom createPost makes the post's author.
// Like X-Role, it is for this demo only: a client that names its own user is
// whoever it says. A real server takes the user from what authenticates the
// request.
func userFromHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := r.Header.Get("X-User"); h != "" {
			user, err := strconv.ParseInt(h, 10, 64)
			if err != nil {
				http.Error(w, "X-User is not a number", http.StatusBadRequest)
				return
			}
			r = r.WithContext(graph.WithViewer(r.Context(), user))
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

// The tables come from migrations/, made, in the example module, by
//
//	go tool velox-gen migrate diff -dialect sqlite -name init -dir ./graphql/migrations ./graphql/schema
//
// and applied with migrate.Up. The temporary SQLite database is this demo's;
// a real application opens its own.
//
// post_tags is the join table of Post.Tags.
//
//go:embed migrations
var migrations embed.FS

// open creates the tables on sqlDB, a SQLite file, by applying migrations/,
// and opens a DB on it with opts.
func open(ctx context.Context, sqlDB *sql.DB, opts ...velox.Option) (*velox.DB, error) {
	sqlDB.SetMaxOpenConns(1) // statements and savepoints run on one connection
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, err
	}
	if _, err := migrate.Up(ctx, sqlDB, sub); err != nil {
		return nil, err
	}
	return velox.Open(velox.SQLite, sqlDB, opts...), nil
}

// The organizations: int64, the scope key's Go type.
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
		if err := db.InsertAll(posts[org]).Exec(gen.OrgKey.With(ctx, org)); err != nil {
			return err
		}
	}
	tags := []gen.Tag{{ID: 1, Name: "go", Rank: 1}, {ID: 2, Name: "sql", Rank: 2}, {ID: 3, Name: "graphql", Rank: 3}}
	if err := db.InsertAll(tags).Exec(ctx); err != nil {
		return err
	}
	links := []gen.PostTag{{PostID: 1, TagID: 1}, {PostID: 1, TagID: 3}, {PostID: 3, TagID: 2}, {PostID: 5, TagID: 1}}
	// The join table has no scope; these links span the orgs.
	return db.InsertAll(links).Exec(ctx)
}
