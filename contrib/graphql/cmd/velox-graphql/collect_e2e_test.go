package main

import (
	"os"
	"strings"
	"testing"
)

// e2eCollectSchema is the schema of the collection integration test: nested connections
// and edges served through real gqlgen. Its tables share no name with the
// other e2e apps'.
//   - Author and Book are tenant-scoped.
//   - Genre is the M2M's other side, and Review a list edge (not a
//     connection) of Book; Review's policy, in gen/review_policy.go, hides
//     the reviews of 0 stars.
const e2eCollectSchema = `package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type Author struct{ schema.Schema }

func (Author) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Tenant(),
		field.String("name").Annotations(graphql.OrderField("NAME")),
	}
}

func (Author) Edges() []schema.Edge {
	return []schema.Edge{edge.To("books", Book.Type)}
}

func (Author) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.QueryField("authors")}
}

type Book struct{ schema.Schema }

func (Book) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Tenant(),
		field.Int64("author_id"),
		field.String("title").Annotations(graphql.OrderField("TITLE"), graphql.WhereOps()),
	}
}

func (Book) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("author", Author.Type).Ref("books").Unique().Required().Field("author_id"),
		edge.To("genres", Genre.Type),
		edge.To("reviews", Review.Type).Annotations(graphql.Connection(false)),
	}
}

type Genre struct{ schema.Schema }

func (Genre) Fields() []schema.Field {
	return []schema.Field{field.String("name").Annotations(graphql.OrderField("NAME"))}
}

func (Genre) Edges() []schema.Edge {
	return []schema.Edge{edge.From("books", Book.Type).Ref("genres")}
}

type Review struct{ schema.Schema }

func (Review) Fields() []schema.Field {
	return []schema.Field{field.Int("stars"), field.Int64("book_id")}
}

func (Review) Edges() []schema.Edge {
	return []schema.Edge{edge.From("book", Book.Type).Ref("reviews").Unique().Required().Field("book_id")}
}
`

// e2eCollectPolicy is Review's policy, written by hand in the generated
// package: a review of 0 stars is withdrawn, and no read sees it.
const e2eCollectPolicy = `package gen

import (
	"context"

	"github.com/syssam/velox"
)

// Policy hides the withdrawn reviews, those of 0 stars.
func (*Review) Policy() velox.Policy[Review] {
	return velox.Policy[Review]{
		Read: func(context.Context) (velox.Pred[Review], error) { return Reviews.Stars.NEQ(0), nil },
	}
}
`

// e2eCollectGqlgen is the application's gqlgen.yml.
const e2eCollectGqlgen = `# gqlgen's configuration, as examples/graphql/gqlgen.yml has it. skip_mod_tidy:
# the test runs offline, and go mod tidy would look for the test dependencies
# of gqlgen's own dependencies.
skip_mod_tidy: true
schema:
  - gen/*.graphqls
exec:
  filename: graph/generated.go
  package: graph
model:
  filename: graph/models_gen.go
  package: graph
resolver:
  layout: follow-schema
  dir: graph
  package: graph
  filename_template: schema.resolvers.go
autobind:
  - example.com/app/gen
models:
  Node:
    model: github.com/syssam/velox/contrib/graphql/veloxgql.Noder
  Int:
    model:
      - github.com/99designs/gqlgen/graphql.Int
      - github.com/99designs/gqlgen/graphql.Int8
      - github.com/99designs/gqlgen/graphql.Int16
      - github.com/99designs/gqlgen/graphql.Int32
      - github.com/99designs/gqlgen/graphql.Int64
      - github.com/99designs/gqlgen/graphql.Uint8
      - github.com/99designs/gqlgen/graphql.Uint16
      - github.com/99designs/gqlgen/graphql.Uint32
  Float:
    model:
      - github.com/99designs/gqlgen/graphql.FloatContext
      - github.com/syssam/velox/contrib/graphql/veloxgql.Float32
  Uint64:
    model:
      - github.com/syssam/velox/contrib/graphql/veloxgql.Uint64
      - github.com/syssam/velox/contrib/graphql/veloxgql.Uint
  Time:
    model: github.com/syssam/velox/contrib/graphql/veloxgql.Time
  UUID:
    model: github.com/syssam/velox/contrib/graphql/veloxgql.UUID
  Bytes:
    model: github.com/syssam/velox/contrib/graphql/veloxgql.Bytes
  JSON:
    model: github.com/syssam/velox/contrib/graphql/veloxgql.JSON
  Cursor:
    model: github.com/syssam/velox/contrib/graphql/veloxgql.Cursor
  OrderDirection:
    model: github.com/syssam/velox/contrib/graphql/veloxgql.OrderDirection
  PageInfo:
    model: github.com/syssam/velox/contrib/graphql/veloxgql.PageInfo
`

// e2eCollectResolvers are the root query's resolvers, one line each; gqlgen
// keeps them when it generates.
const e2eCollectResolvers = `package graph

import (
	"context"

	"example.com/app/gen"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// Node is the resolver for the node field.
func (r *queryResolver) Node(ctx context.Context, id string) (veloxgql.Noder, error) {
	return gen.Node(ctx, id)
}

// Nodes is the resolver for the nodes field.
func (r *queryResolver) Nodes(ctx context.Context, ids []string) ([]veloxgql.Noder, error) {
	return gen.Nodes(ctx, ids)
}

// Authors is the resolver for the authors field.
func (r *queryResolver) Authors(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy *gen.AuthorOrder) (*gen.AuthorConnection, error) {
	return gen.AuthorsConnection(ctx, after, first, before, last, orderBy)
}

// Query returns QueryResolver implementation.
func (r *Resolver) Query() QueryResolver { return &queryResolver{r} }

type queryResolver struct{ *Resolver }
`

// e2eCollectRun is the application's test: nested connections and edges
// through gqlgen, run on each of targets' databases.
const e2eCollectRun = `package app_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	_ "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"example.com/app/graph"
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/veloxpgx"
)

// TestMain refuses to run, when VELOX_ITEST_REQUIRE=1, as CI and
// scripts/ci-local.sh set it, without both databases: a missing variable would
// leave out the PostgreSQL or the MySQL subtests without a failure.
func TestMain(m *testing.M) {
	if os.Getenv("VELOX_ITEST_REQUIRE") == "1" {
		var missing []string
		for _, v := range []string{"VELOX_TEST_POSTGRES", "VELOX_TEST_MYSQL"} {
			if os.Getenv(v) == "" {
				missing = append(missing, v)
			}
		}
		if len(missing) > 0 {
			fmt.Fprintf(os.Stderr, "app: VELOX_ITEST_REQUIRE=1 but %s unset: the PostgreSQL and MySQL tests would be left out\n", strings.Join(missing, ", "))
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

// A target is a database the tests run on.
type target struct {
	name    string
	dialect string // sqlite, postgres or mysql
	// open opens the database: raw, through database/sql, for the DDL, the
	// seed and the tests' own queries, and db, through which the server reads.
	open func(t *testing.T, opts ...velox.Option) (raw *sql.DB, db *velox.DB)
}

// targets are the databases the tests run on: SQLite always; PostgreSQL,
// through native pgx and through database/sql, when VELOX_TEST_POSTGRES
// names it; and MySQL when VELOX_TEST_MYSQL does. Its DSN needs
// clientFoundRows=true, as velox's Open says.
func targets(t *testing.T) []target {
	t.Helper()
	ts := []target{{
		name: "sqlite", dialect: "sqlite",
		open: func(t *testing.T, opts ...velox.Option) (*sql.DB, *velox.DB) {
			db := sqlOpen(t, "sqlite", "file::memory:")
			db.SetMaxOpenConns(1)
			return db, velox.Open(velox.SQLite, db, opts...)
		},
	}}
	if dsn := os.Getenv("VELOX_TEST_POSTGRES"); dsn != "" {
		ts = append(ts, target{
			name: "pgx", dialect: "postgres",
			open: func(t *testing.T, opts ...velox.Option) (*sql.DB, *velox.DB) {
				pool, err := pgxpool.New(context.Background(), dsn)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(pool.Close)
				return sqlOpen(t, "pgx", dsn), veloxpgx.Open(pool, opts...)
			},
		}, target{
			name: "postgres", dialect: "postgres",
			open: func(t *testing.T, opts ...velox.Option) (*sql.DB, *velox.DB) {
				db := sqlOpen(t, "pgx", dsn)
				return db, velox.Open(velox.Postgres, db, opts...)
			},
		})
	}
	if dsn := os.Getenv("VELOX_TEST_MYSQL"); dsn != "" {
		ts = append(ts, target{
			name: "mysql", dialect: "mysql",
			open: func(t *testing.T, opts ...velox.Option) (*sql.DB, *velox.DB) {
				db := sqlOpen(t, "mysql", dsn)
				return db, velox.Open(velox.MySQL, db, opts...)
			},
		})
	}
	return ts
}

// sqlOpen opens a database/sql handle, closed when the test ends.
func sqlOpen(t *testing.T, driver, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// ddl are the tables of gen in a dialect, the join table book_genres among
// them.
func ddl(dialect string) []string {
	id, num, text := "INTEGER PRIMARY KEY", "INTEGER", "TEXT"
	switch dialect {
	case "postgres":
		id, num, text = "bigint PRIMARY KEY", "bigint", "text"
	case "mysql":
		id, num, text = "BIGINT PRIMARY KEY", "BIGINT", "VARCHAR(255)"
	}
	return []string{
		"CREATE TABLE authors (id " + id + ", org_id " + num + " NOT NULL, name " + text + " NOT NULL)",
		"CREATE TABLE books (id " + id + ", org_id " + num + " NOT NULL, author_id " + num + " NOT NULL, title " + text + " NOT NULL)",
		"CREATE TABLE genres (id " + id + ", name " + text + " NOT NULL)",
		"CREATE TABLE book_genres (book_id " + num + " NOT NULL, genre_id " + num + " NOT NULL, PRIMARY KEY (book_id, genre_id))",
		"CREATE TABLE reviews (id " + id + ", stars " + num + " NOT NULL, book_id " + num + " NOT NULL)",
	}
}

// drop drops the tables, before they are made: the server databases outlive
// a test.
var drop = []string{
	"DROP TABLE IF EXISTS reviews",
	"DROP TABLE IF EXISTS book_genres",
	"DROP TABLE IF EXISTS genres",
	"DROP TABLE IF EXISTS books",
	"DROP TABLE IF EXISTS authors",
}

// env is a test's database and the server over it; stmts are the statements
// velox sent since the last request.
type env struct {
	target
	raw *sql.DB
	db  *velox.DB
	url string
	mu  sync.Mutex
	sql []string
	log []string // the errors the presenter logged, which the client does not see
}

// q is s with ? for each placeholder, in the dialect's own.
func (e *env) q(s string) string {
	if e.dialect != "postgres" {
		return s
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		if r == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (e *env) exec(t *testing.T, s string, args ...any) {
	t.Helper()
	if _, err := e.raw.Exec(e.q(s), args...); err != nil {
		t.Fatalf("%s: %v", s, err)
	}
}

// The seed: two orgs. Authors 1 to 3 are acme's (org 1), 4 to 6 globex's
// (org 2), and each has four books; a book of the other org takes an
// author's place now and then, and sorts first, so that a window that
// ranked it would show. Author 6's books are all acme's: globex sees
// none. Author 2's titles tie.
var (
	authorRows = []struct {
		id, org int64
		name    string
	}{{1, 1, "Ann"}, {2, 1, "Bob"}, {3, 1, "Cy"}, {4, 2, "Di"}, {5, 2, "Ed"}, {6, 2, "Flo"}}
	bookRows = []struct {
		id, org, author int64
		title           string
	}{
		{1, 1, 1, "Beta"}, {2, 1, 1, "Alpha"}, {3, 1, 1, "Gamma"}, {4, 2, 1, "Aardvark"},
		{5, 1, 2, "Dup"}, {6, 1, 2, "Dup"}, {7, 1, 2, "Dup"}, {8, 2, 2, "Aardvark"},
		{9, 1, 3, "Cat"}, {10, 1, 3, "Bat"}, {11, 1, 3, "Ant"}, {12, 1, 3, "Zed"},
		{13, 2, 4, "Delta"}, {14, 2, 4, "Echo"}, {15, 2, 4, "Foxtrot"}, {16, 1, 4, "Aaa"},
		{17, 2, 5, "Quill"}, {18, 2, 5, "Rune"}, {19, 2, 5, "Scroll"}, {20, 2, 5, "Tome"},
		{21, 1, 6, "Uno"}, {22, 1, 6, "Dos"}, {23, 1, 6, "Tres"}, {24, 1, 6, "Cuatro"},
	}
	genreNames = []string{"Drama", "Epic", "Farce"}
)

// genresOfBook are the genres of book b: b mod 4 of them, from b mod 3 on.
func genresOfBook(b int64) []int64 {
	var out []int64
	for i := range b % 4 {
		out = append(out, (b%3+i)%3+1)
	}
	return out
}

// starsOfBook are the stars of the reviews of book b, in the order of their
// keys: b mod 3 of them, book 5 three; and, first, a withdrawn review (0
// stars, which Review's policy hides) on each fifth book.
func starsOfBook(b int64) []int64 {
	var out []int64
	if b%5 == 0 {
		out = append(out, 0)
	}
	n := b % 3
	if b == 5 {
		n = 3
	}
	for i := range n {
		out = append(out, (b+i)%5+1)
	}
	return out
}

// newEnv opens the target, makes and seeds the tables, and serves the schema
// on them, with opts.
func newEnv(t *testing.T, tg target, opts ...veloxgql.Option) *env {
	t.Helper()
	e := &env{target: tg}
	raw, db := tg.open(t, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		e.mu.Lock()
		e.sql = append(e.sql, st.SQL)
		e.mu.Unlock()
		return next(ctx)
	}))
	e.raw, e.db = raw, db
	for _, s := range append(slices.Clone(drop), ddl(tg.dialect)...) {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	for _, a := range authorRows {
		e.exec(t, "INSERT INTO authors (id, org_id, name) VALUES (?, ?, ?)", a.id, a.org, a.name)
	}
	var reviewID int64
	for _, b := range bookRows {
		e.exec(t, "INSERT INTO books (id, org_id, author_id, title) VALUES (?, ?, ?, ?)", b.id, b.org, b.author, b.title)
		for _, g := range genresOfBook(b.id) {
			e.exec(t, "INSERT INTO book_genres (book_id, genre_id) VALUES (?, ?)", b.id, g)
		}
		for _, s := range starsOfBook(b.id) {
			reviewID++
			e.exec(t, "INSERT INTO reviews (id, stars, book_id) VALUES (?, ?, ?)", reviewID, s, b.id)
		}
	}
	for i, n := range genreNames {
		e.exec(t, "INSERT INTO genres (id, name) VALUES (?, ?)", i+1, n)
	}

	srv := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{}}))
	srv.Use(veloxgql.NewExtension(db, opts...))
	srv.SetErrorPresenter(veloxgql.ErrorPresenter(func(_ context.Context, err error) {
		e.mu.Lock()
		e.log = append(e.log, err.Error())
		e.mu.Unlock()
	}))
	srv.AddTransport(transport.POST{})
	// The tenant is the organization X-Org-ID names.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		org, err := strconv.ParseInt(r.Header.Get("X-Org-ID"), 10, 64)
		if err != nil {
			http.Error(w, "X-Org-ID is not a number", http.StatusBadRequest)
			return
		}
		srv.ServeHTTP(w, r.WithContext(velox.WithTenant(r.Context(), org)))
	}))
	t.Cleanup(ts.Close)
	e.url = ts.URL
	return e
}

// each runs fn on each target, with a fresh database and a server of opts.
func each(t *testing.T, fn func(t *testing.T, e *env), opts ...veloxgql.Option) {
	for _, tg := range targets(t) {
		t.Run(tg.name, func(t *testing.T) { fn(t, newEnv(t, tg, opts...)) })
	}
}

// response is a GraphQL response.
type response struct {
	Data   json.RawMessage
	Errors []struct {
		Message    string
		Path       []any
		Extensions map[string]any
	}
}

// do sends the query as the organization org, and returns the response with
// its data decoded into data (when it is not nil) and the statements the
// request sent.
func (e *env) do(t *testing.T, org int, query string, vars map[string]any, data any) (response, []string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, e.url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Org-ID", strconv.Itoa(org))
	e.mu.Lock()
	e.sql, e.log = nil, nil
	e.mu.Unlock()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var r response
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, b)
	}
	e.mu.Lock()
	stmts := slices.Clone(e.sql)
	e.mu.Unlock()
	if data != nil {
		if len(r.Errors) > 0 {
			t.Fatalf("%s\nerrors: %s", query, b)
		}
		if err := json.Unmarshal(r.Data, data); err != nil {
			t.Fatalf("data: %v\n%s", err, b)
		}
	}
	return r, stmts
}

func bookID(id int64) string { return veloxgql.EncodeID("Book", id) }

// The shapes of the responses.
type (
	genrePage struct {
		TotalCount int
		Edges      []struct{ Node struct{ Name string } }
	}
	reviewList []struct{ Stars int }
	bookNode   struct {
		Title   string
		Author  struct{ Name string }
		Genres  genrePage
		Reviews reviewList
	}
	bookPage struct {
		TotalCount int
		PageInfo   struct{ HasNextPage, HasPreviousPage bool }
		Edges      []struct {
			Node   bookNode
			Cursor string
		}
	}
	authorsOf[T any] struct {
		Authors struct {
			Edges []struct{ Node T }
		}
	}
	nestedAuthor struct {
		Name  string
		Books bookPage
	}
)

// bookRow is a book, as the tests' own queries read it.
type bookRow struct {
	ID    int64
	Title string
}

// authorID is the key of the author named name.
func authorID(name string) int64 {
	for _, a := range authorRows {
		if a.name == name {
			return a.id
		}
	}
	panic("no author " + name)
}

// books are the books of the author, in the organization, that the query
// (a WHERE and the rest, with the author and the organization as its first
// two arguments) reads, as the unbatched query of the author itself.
func (e *env) books(t *testing.T, author, org int64, rest string, args ...any) []bookRow {
	t.Helper()
	rows, err := e.raw.Query(e.q("SELECT id, title FROM books WHERE author_id = ? AND org_id = ?"+rest), append([]any{author, org}, args...)...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []bookRow
	for rows.Next() {
		var b bookRow
		if err := rows.Scan(&b.ID, &b.Title); err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func (e *env) count(t *testing.T, s string, args ...any) int {
	t.Helper()
	var n int
	if err := e.raw.QueryRow(e.q(s), args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *env) strings(t *testing.T, s string, args ...any) []string {
	t.Helper()
	rows, err := e.raw.Query(e.q(s), args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var x string
		if err := rows.Scan(&x); err != nil {
			t.Fatal(err)
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func titles(bs []bookRow) []string {
	out := []string{}
	for _, b := range bs {
		out = append(out, b.Title)
	}
	return out
}

func pageTitles(p bookPage) []string {
	out := []string{}
	for _, e := range p.Edges {
		out = append(out, e.Node.Title)
	}
	return out
}

func orgOf(author int64) int64 {
	for _, a := range authorRows {
		if a.id == author {
			return a.org
		}
	}
	panic("no author")
}

func equal(t *testing.T, what string, got, want any) {
	t.Helper()
	if g, w := fmt.Sprint(got), fmt.Sprint(want); g != w {
		t.Errorf("%s:\n got  %s\n want %s", what, g, w)
	}
}

// TestNestedPerLevel: a nested request sends one statement per edge per
// level, however many parents: the authors' page, the books' load, count,
// authors, genres load and count, and reviews load. Each author's books and
// each book's genres and reviews are what that parent's own query returns.
func TestNestedPerLevel(t *testing.T) {
	const q = "{ authors(first: %d) { edges { node { name books(first: 2, orderBy: {field: TITLE}) { totalCount pageInfo { hasNextPage } edges { node { title author { name } genres(first: 1) { totalCount edges { node { name } } } reviews { stars } } } } } } } }"
	each(t, func(t *testing.T, e *env) {
		for _, org := range []int{1, 2} {
			var one authorsOf[nestedAuthor]
			if _, stmts := e.do(t, org, fmt.Sprintf(q, 1), nil, &one); len(stmts) != 7 {
				t.Errorf("org %d, one author: %d statements, want 7:\n%s", org, len(stmts), strings.Join(stmts, "\n"))
			}
			var data authorsOf[nestedAuthor]
			_, stmts := e.do(t, org, fmt.Sprintf(q, 6), nil, &data)
			if len(stmts) != 7 {
				t.Errorf("org %d, all authors: %d statements, want 7:\n%s", org, len(stmts), strings.Join(stmts, "\n"))
			}
			if len(data.Authors.Edges) != 3 {
				t.Fatalf("org %d: %d authors, want 3", org, len(data.Authors.Edges))
			}
			for _, ae := range data.Authors.Edges {
				a, id := ae.Node, authorID(ae.Node.Name)
				want := e.books(t, id, int64(org), " ORDER BY title, id LIMIT 2")
				total := e.count(t, "SELECT COUNT(*) FROM books WHERE author_id = ? AND org_id = ?", id, org)
				equal(t, a.Name+" books", pageTitles(a.Books), titles(want))
				equal(t, a.Name+" totalCount", a.Books.TotalCount, total)
				equal(t, a.Name+" hasNextPage", a.Books.PageInfo.HasNextPage, total > 2)
				for i, be := range a.Books.Edges {
					b, bid := be.Node, want[i].ID
					equal(t, a.Name+" book author", b.Author.Name, a.Name)
					names := e.strings(t, "SELECT g.name FROM genres g JOIN book_genres bg ON bg.genre_id = g.id WHERE bg.book_id = ? ORDER BY g.id LIMIT 1", bid)
					var got []string
					for _, ge := range b.Genres.Edges {
						got = append(got, ge.Node.Name)
					}
					equal(t, fmt.Sprintf("book %d genres", bid), got, names)
					equal(t, fmt.Sprintf("book %d genres totalCount", bid), b.Genres.TotalCount, e.count(t, "SELECT COUNT(*) FROM book_genres WHERE book_id = ?", bid))
					var stars []int
					for _, r := range b.Reviews {
						stars = append(stars, r.Stars)
					}
					var wantStars []int
					for _, s := range starsOfBook(bid) {
						if s != 0 {
							wantStars = append(wantStars, int(s))
						}
					}
					equal(t, fmt.Sprintf("book %d reviews", bid), stars, wantStars)
				}
			}
		}
	})
}

// TestBackwardAndCursorNested: last and after batch under the authors, and
// give each author the page its own query does.
func TestBackwardAndCursorNested(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		var last authorsOf[nestedAuthor]
		_, stmts := e.do(t, 1, "{ authors(first: 6) { edges { node { name books(last: 2, orderBy: {field: TITLE}) { pageInfo { hasPreviousPage hasNextPage } edges { node { title } } } } } } }", nil, &last)
		if len(stmts) != 2 {
			t.Errorf("last: %d statements, want 2 (the authors, one load):\n%s", len(stmts), strings.Join(stmts, "\n"))
		}
		for _, ae := range last.Authors.Edges {
			a, id := ae.Node, authorID(ae.Node.Name)
			want := e.books(t, id, 1, " ORDER BY title DESC, id DESC LIMIT 2")
			slices.Reverse(want)
			equal(t, a.Name+" last", pageTitles(a.Books), titles(want))
			equal(t, a.Name+" hasPreviousPage", a.Books.PageInfo.HasPreviousPage, e.count(t, "SELECT COUNT(*) FROM books WHERE author_id = ? AND org_id = ?", id, 1) > 2)
			equal(t, a.Name+" hasNextPage", a.Books.PageInfo.HasNextPage, false)
		}

		// One cursor, the first book by title of the first author, is the
		// field's argument for every author.
		var first authorsOf[nestedAuthor]
		e.do(t, 1, "{ authors(first: 1) { edges { node { name books(first: 1, orderBy: {field: TITLE}) { edges { cursor node { title } } } } } } }", nil, &first)
		cursor := first.Authors.Edges[0].Node.Books.Edges[0].Cursor
		const after = "query($c: Cursor) { authors(first: 6) { edges { node { name books(first: 2, after: $c, orderBy: {field: TITLE}) { pageInfo { hasNextPage } edges { node { title } } } } } } }"
		var data authorsOf[nestedAuthor]
		_, stmts = e.do(t, 1, after, map[string]any{"c": cursor}, &data)
		if len(stmts) != 2 {
			t.Errorf("after: %d statements, want 2:\n%s", len(stmts), strings.Join(stmts, "\n"))
		}
		// The cursor is that of ("Alpha", book 2).
		const past = " AND (title > ? OR (title = ? AND id > ?))"
		for _, ae := range data.Authors.Edges {
			a, id := ae.Node, authorID(ae.Node.Name)
			want := e.books(t, id, 1, past+" ORDER BY title, id LIMIT 2", "Alpha", "Alpha", 2)
			equal(t, a.Name+" after", pageTitles(a.Books), titles(want))
			equal(t, a.Name+" hasNextPage", a.Books.PageInfo.HasNextPage, len(e.books(t, id, 1, past, "Alpha", "Alpha", 2)) > 2)
		}
	})
}

// TestAliasesNested: the same edge twice under different arguments is two
// batches, each with its own arguments' page.
func TestAliasesNested(t *testing.T) {
	type aliased struct {
		Name string
		A, B bookPage
	}
	each(t, func(t *testing.T, e *env) {
		var data authorsOf[aliased]
		_, stmts := e.do(t, 1, "{ authors(first: 6) { edges { node { name a: books(first: 1) { edges { node { title } } } b: books(where: {title: \"Beta\"}) { edges { node { title } } } } } } }", nil, &data)
		if len(stmts) != 3 {
			t.Errorf("%d statements, want 3 (the authors, a, b):\n%s", len(stmts), strings.Join(stmts, "\n"))
		}
		got := map[string][2][]string{}
		for _, ae := range data.Authors.Edges {
			got[ae.Node.Name] = [2][]string{pageTitles(ae.Node.A), pageTitles(ae.Node.B)}
		}
		equal(t, "pages", got, map[string][2][]string{
			"Ann": {{"Beta"}, {"Beta"}},
			"Bob": {{"Dup"}, {}},
			"Cy":  {{"Cat"}, {}},
		})
	})
}

// TestTenantInsideWindow: a book another organization owns takes no rank in
// a window, and is not counted: ann's first book by title overall is
// globex's "Aardvark", and her page under acme starts at "Alpha".
func TestTenantInsideWindow(t *testing.T) {
	type summary struct {
		Name  string
		Books bookPage
	}
	const q = "{ authors(first: 6) { edges { node { name books(first: 1, orderBy: {field: TITLE}) { totalCount pageInfo { hasNextPage } edges { node { title } } } } } } }"
	type want struct {
		titles  []string
		total   int
		hasNext bool
	}
	wants := map[int]map[string]want{
		1: {"Ann": {[]string{"Alpha"}, 3, true}, "Bob": {[]string{"Dup"}, 3, true}, "Cy": {[]string{"Ant"}, 4, true}},
		2: {"Di": {[]string{"Delta"}, 3, true}, "Ed": {[]string{"Quill"}, 4, true}, "Flo": {[]string{}, 0, false}},
	}
	each(t, func(t *testing.T, e *env) {
		for org, w := range wants {
			var data authorsOf[summary]
			e.do(t, org, q, nil, &data)
			got := map[string]want{}
			for _, ae := range data.Authors.Edges {
				b := ae.Node.Books
				got[ae.Node.Name] = want{pageTitles(b), b.TotalCount, b.PageInfo.HasNextPage}
			}
			equal(t, "org "+strconv.Itoa(org), got, w)
		}
	})
}

// TestPolicyHidden: a review Review's policy hides is on no list, and takes
// no place in a window: book 5 has three reviews and a withdrawn one before
// them, book 10 one and a withdrawn one.
func TestPolicyHidden(t *testing.T) {
	type bookReviews struct{ Reviews reviewList }
	each(t, func(t *testing.T, e *env) {
		var data struct{ Nodes []bookReviews }
		_, stmts := e.do(t, 1, "query($ids: [ID!]!) { nodes(ids: $ids) { ... on Book { reviews { stars } } } }", map[string]any{"ids": []string{bookID(5), bookID(10), bookID(1)}}, &data)
		var got [][]int
		for _, b := range data.Nodes {
			var s []int
			for _, r := range b.Reviews {
				s = append(s, r.Stars)
			}
			got = append(got, s)
		}
		equal(t, "stars", got, [][]int{{1, 2, 3}, {1}, {2}})
		if len(stmts) != 2 {
			t.Errorf("%d statements, want 2 (the books, their reviews):\n%s", len(stmts), strings.Join(stmts, "\n"))
		}
	})
}

// TestCostRefused: three levels of full pages cost more than the default
// MaxCost, and the request is refused, before any statement.
func TestCostRefused(t *testing.T) {
	each(t, func(t *testing.T, e *env) {
		r, stmts := e.do(t, 1, "{ authors(first: 100) { edges { node { books(first: 100) { edges { node { genres(first: 100) { edges { node { name } } } } } } } } } }", nil, nil)
		if len(r.Errors) != 1 || r.Errors[0].Extensions["code"] != "COST_LIMIT_EXCEEDED" {
			t.Errorf("errors: %+v, want COST_LIMIT_EXCEEDED", r.Errors)
		}
		if string(r.Data) != "" && string(r.Data) != "null" {
			t.Errorf("data: %s", r.Data)
		}
		if len(stmts) != 0 {
			t.Errorf("%d statements, want none:\n%s", len(stmts), strings.Join(stmts, "\n"))
		}
		// Two levels cost less, and run.
		if r, _ := e.do(t, 1, "{ authors(first: 100) { edges { node { books(first: 100) { edges { node { title } } } } } } }", nil, nil); len(r.Errors) != 0 {
			t.Errorf("two levels: %+v", r.Errors)
		}
	})
}

// TestListEdgeCap: a list edge reads MaxList + 1 rows of each parent, and a
// parent with more fails alone: book 5 has three reviews.
func TestListEdgeCap(t *testing.T) {
	type bookReviews struct {
		Title   string
		Reviews reviewList
	}
	each(t, func(t *testing.T, e *env) {
		var data struct{ Nodes []*bookReviews }
		r, _ := e.do(t, 1, "query($ids: [ID!]!) { nodes(ids: $ids) { ... on Book { title reviews { stars } } } }", map[string]any{"ids": []string{bookID(1), bookID(5), bookID(2)}}, nil)
		if err := json.Unmarshal(r.Data, &data); err != nil {
			t.Fatal(err)
		}
		if len(data.Nodes) != 3 || data.Nodes[0] == nil || data.Nodes[1] != nil || data.Nodes[2] == nil {
			t.Fatalf("nodes: %s", r.Data)
		}
		equal(t, "book 1", fmt.Sprintf("%s %v", data.Nodes[0].Title, data.Nodes[0].Reviews), "Beta [{2}]")
		equal(t, "book 2", fmt.Sprintf("%s %v", data.Nodes[2].Title, data.Nodes[2].Reviews), "Alpha [{3} {4}]")
		// The client sees an internal error at the field; the log names the edge.
		if len(r.Errors) != 1 || fmt.Sprint(r.Errors[0].Path) != "[nodes 1 reviews]" {
			t.Errorf("errors: %+v, want one at nodes.1.reviews", r.Errors)
		}
		e.mu.Lock()
		logged := strings.Join(e.log, "\n")
		e.mu.Unlock()
		if !strings.Contains(logged, "Book.reviews") || !strings.Contains(logged, "more than 2") {
			t.Errorf("the error logged does not name Book.reviews and the cap:\n%s", logged)
		}
	}, veloxgql.WithLimits(veloxgql.Limits{MaxList: 2}))
}
`

// TestCollectRun is the collection integration test: it generates the application with
// velox-gen and the plugin, then gqlgen, and runs its tests, which serve the
// schema through gqlgen on SQLite always and on PostgreSQL (pgx and
// database/sql) and MySQL when VELOX_TEST_POSTGRES and VELOX_TEST_MYSQL name
// them; with VELOX_ITEST_REQUIRE=1, the application's TestMain fails unless
// both do.
func TestCollectRun(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	_, run := app(t, map[string]string{
		"schema/schema.go":          e2eCollectSchema,
		"gen/review_policy.go":      e2eCollectPolicy,
		"gqlgen.yml":                e2eCollectGqlgen,
		"graph/schema.resolvers.go": e2eCollectResolvers,
		"collect_test.go":           e2eCollectRun,
	}, true)
	if out, err := run("tool", "velox-gen", "-out", "gen", "-plugin", "velox-graphql", "./schema"); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	if out, err := run("tool", "gqlgen", "generate"); err != nil {
		t.Fatalf("gqlgen generate: %v\n%s", err, out)
	}
	if out, err := run("mod", "tidy", "-e"); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
	if out, err := run("vet", "./..."); err != nil {
		t.Fatalf("go vet: %v\n%s", err, out)
	}
	targets := []string{"sqlite"}
	if os.Getenv("VELOX_TEST_POSTGRES") != "" {
		targets = append(targets, "pgx", "postgres")
	}
	if os.Getenv("VELOX_TEST_MYSQL") != "" {
		targets = append(targets, "mysql")
	}
	t.Logf("targets: %v", targets)
	out, err := run("test", "-count=1", "-v", ".")
	if err != nil {
		t.Fatalf("go test: %v\n%s", err, out)
	}
	for _, name := range []string{"TestNestedPerLevel", "TestBackwardAndCursorNested", "TestAliasesNested", "TestTenantInsideWindow", "TestPolicyHidden", "TestCostRefused", "TestListEdgeCap"} {
		if !strings.Contains(out, "--- PASS: "+name+" ") {
			t.Errorf("no PASS of %s in\n%s", name, out)
		}
		for _, tg := range targets {
			if !strings.Contains(out, "--- PASS: "+name+"/"+tg+" (") {
				t.Errorf("no PASS of %s/%s in\n%s", name, tg, out)
			}
		}
	}
	if strings.Contains(out, "--- SKIP") {
		t.Errorf("a test skipped:\n%s", out)
	}
}
