package main

import (
	"strings"
	"testing"
)

// e2eLimitsSchema is the schema of TestLimitsServed. Its tables are its own.
//   - Post is tenant-scoped by org_id, the key of a Required edge to Org, and
//     has a Required edge to Member, whose policy, in gen/member_policy.go, hides
//     the member named "hidden".
//   - Acct has a string and a time with a Default.
//   - Type, Field, Schema, Directive, EnumValue, InputValue, and the enums
//     TypeKind and DirectiveLocation of two of them, are named as gqlgen's
//     autobind would bind GraphQL's own introspection types.
const e2eLimitsSchema = `package schema

import (
	"time"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type Org struct{ schema.Schema }

func (Org) Fields() []schema.Field { return []schema.Field{field.String("name")} }

func (Org) Edges() []schema.Edge { return []schema.Edge{edge.To("posts", Post.Type)} }

type Member struct{ schema.Schema }

func (Member) Fields() []schema.Field { return []schema.Field{field.String("name")} }

func (Member) Edges() []schema.Edge { return []schema.Edge{edge.To("posts", Post.Type)} }

type Post struct{ schema.Schema }

func (Post) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Tenant(),
		field.Int64("member_id"),
		field.String("title").Annotations(graphql.WhereOps(graphql.OpEQ)),
	}
}

func (Post) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("org", Org.Type).Ref("posts").Unique().Required().Field("org_id"),
		edge.From("member", Member.Type).Ref("posts").Unique().Required().Field("member_id"),
	}
}

func (Post) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.QueryField("posts"), graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate)}
}

type Acct struct{ schema.Schema }

func (Acct) Fields() []schema.Field {
	return []schema.Field{
		field.String("handle").Optional().Default("anon"),
		field.Time("joined").Default(time.Now),
	}
}

func (Acct) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate)}
}

type Type struct{ schema.Schema }

func (Type) Fields() []schema.Field {
	return []schema.Field{field.String("name"), field.Enum("kind").Values("a", "b")}
}

type Field struct{ schema.Schema }

func (Field) Fields() []schema.Field { return []schema.Field{field.String("name")} }

type Schema struct{ schema.Schema }

func (Schema) Fields() []schema.Field { return []schema.Field{field.String("name")} }

type Directive struct{ schema.Schema }

func (Directive) Fields() []schema.Field {
	return []schema.Field{field.String("name"), field.Enum("location").Values("a", "b")}
}

type EnumValue struct{ schema.Schema }

func (EnumValue) Fields() []schema.Field { return []schema.Field{field.String("name")} }

type InputValue struct{ schema.Schema }

func (InputValue) Fields() []schema.Field { return []schema.Field{field.String("name")} }
`

// e2eLimitsPolicy hides the member named "hidden".
const e2eLimitsPolicy = `package gen

import (
	"context"

	"github.com/syssam/velox"
)

func (*Member) Policy() velox.Policy[Member] {
	return velox.Policy[Member]{Read: func(context.Context) (velox.Pred[Member], error) {
		return Members.Name.NEQ("hidden"), nil
	}}
}
`

// e2eLimitsResolvers are the resolvers TestLimitsServed uses, one line each;
// gqlgen stubs the others.
const e2eLimitsResolvers = `package graph

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

// Posts is the resolver for the posts field.
func (r *queryResolver) Posts(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, where *gen.PostWhereInput) (*gen.PostConnection, error) {
	return gen.PostsConnection(ctx, after, first, before, last, where)
}

// CreatePost is the resolver for the createPost field.
func (r *mutationResolver) CreatePost(ctx context.Context, input gen.CreatePostInput) (*gen.Post, error) {
	return gen.CreatePost(ctx, input)
}

// UpdatePost is the resolver for the updatePost field.
func (r *mutationResolver) UpdatePost(ctx context.Context, id string, input gen.UpdatePostInput) (*gen.Post, error) {
	return gen.UpdatePost(ctx, id, input)
}

// CreateAcct is the resolver for the createAcct field.
func (r *mutationResolver) CreateAcct(ctx context.Context, input gen.CreateAcctInput) (*gen.Acct, error) {
	return gen.CreateAcct(ctx, input)
}

// UpdateAcct is the resolver for the updateAcct field.
func (r *mutationResolver) UpdateAcct(ctx context.Context, id string, input gen.UpdateAcctInput) (*gen.Acct, error) {
	return gen.UpdateAcct(ctx, id, input)
}

// Mutation returns MutationResolver implementation.
func (r *Resolver) Mutation() MutationResolver { return &mutationResolver{r} }

// Query returns QueryResolver implementation.
func (r *Resolver) Query() QueryResolver { return &queryResolver{r} }

type mutationResolver struct{ *Resolver }
type queryResolver struct{ *Resolver }
`

// e2eLimitsRun is the application's test, on SQLite, through gqlgen's handler.
const e2eLimitsRun = `package app_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	_ "modernc.org/sqlite"

	"example.com/app/graph"
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

type served struct {
	t  *testing.T
	db *sql.DB
	do func(query string) string
}

func serve(t *testing.T) served {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	for _, s := range []string{
		"CREATE TABLE orgs (id INTEGER PRIMARY KEY, name TEXT NOT NULL)",
		"CREATE TABLE members (id INTEGER PRIMARY KEY, name TEXT NOT NULL)",
		"CREATE TABLE posts (id INTEGER PRIMARY KEY, org_id INTEGER NOT NULL REFERENCES orgs (id), member_id INTEGER NOT NULL REFERENCES members (id), title TEXT NOT NULL)",
		"CREATE TABLE accts (id INTEGER PRIMARY KEY, handle TEXT NOT NULL, joined DATETIME NOT NULL)",
		"CREATE TABLE types (id INTEGER PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL)",
		"INSERT INTO orgs (id, name) VALUES (1, ~o1~), (2, ~o2~)",
		"INSERT INTO members (id, name) VALUES (1, ~seen~), (2, ~hidden~)",
		"INSERT INTO posts (id, org_id, member_id, title) VALUES (1, 1, 1, ~p1~), (2, 1, 2, ~p2~)",
		"INSERT INTO types (id, name, kind) VALUES (1, ~t~, ~a~)",
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	srv := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{}}))
	srv.Use(veloxgql.NewExtension(velox.Open(velox.SQLite, db)))
	srv.SetErrorPresenter(veloxgql.ErrorPresenter(func(_ context, err error) { t.Logf("internal: %v", err) }))
	srv.AddTransport(transport.POST{})
	srv.Use(extension.Introspection{})
	do := func(query string) string {
		t.Helper()
		b, err := json.Marshal(map[string]string{"query": query})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", "/", strings.NewReader(string(b)))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(velox.WithTenant(req.Context(), int64(1)))
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w.Body.String()
	}
	return served{t, db, do}
}

func (s served) want(name, query, want string) {
	s.t.Helper()
	if got := s.do(query); got != want {
		s.t.Errorf("%s:\ngot  %s\nwant %s", name, got, want)
	}
}

func (s served) count(table string) int {
	s.t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		s.t.Fatal(err)
	}
	return n
}

// TestRequiredEdgeHidden: a Required edge whose target a policy hides reads
// null, with no error, in a page and in a mutation's result, which commits.
func TestRequiredEdgeHidden(t *testing.T) {
	s := serve(t)
	s.want("the page", "{ posts { edges { node { title member { name } } } } }",
		'{"data":{"posts":{"edges":[{"node":{"title":"p1","member":{"name":"seen"}}},{"node":{"title":"p2","member":null}}]}}}')
	id := veloxgql.EncodeID("Post", 2)
	s.want("the update", fmt.Sprintf("mutation { updatePost(id: %q, input: {title: %q}) { title member { name } } }", id, "new"),
		'{"data":{"updatePost":{"title":"new","member":null}}}')
	var title string
	if err := s.db.QueryRow("SELECT title FROM posts WHERE id = 2").Scan(&title); err != nil || title != "new" {
		t.Errorf("the update did not commit: %q %v", title, err)
	}
	// A hidden member cannot be named on create.
	hidden := veloxgql.EncodeID("Member", 2)
	if got := s.do(fmt.Sprintf("mutation { createPost(input: {title: %q, memberID: %q}) { title } }", "x", hidden)); !strings.Contains(got, "NOT_FOUND") {
		t.Errorf("a hidden member on create: %s", got)
	}
}

// TestTenantKeyFilled: the tenant column that an edge holds is no input, and
// the context's tenant fills it.
func TestTenantKeyFilled(t *testing.T) {
	s := serve(t)
	seen := veloxgql.EncodeID("Member", 1)
	s.want("create", fmt.Sprintf("mutation { createPost(input: {title: %q, memberID: %q}) { title org { name } } }", "n", seen),
		'{"data":{"createPost":{"title":"n","org":{"name":"o1"}}}}')
	if got := s.do(fmt.Sprintf("mutation { createPost(input: {title: %q, memberID: %q, orgID: %q}) { title } }", "n", seen, veloxgql.EncodeID("Org", 2))); !strings.Contains(got, "GRAPHQL_VALIDATION_FAILED") {
		t.Errorf("orgID on create: %s", got)
	}
}

// TestExplicitZeroRefused: a create that gives a field with a Default its zero
// value is refused, not silently given the Default; an update stores it.
func TestExplicitZeroRefused(t *testing.T) {
	s := serve(t)
	for name, input := range map[string]string{"empty string": 'handle: ""', "zero time": 'joined: "0001-01-01T00:00:00Z"'} {
		got := s.do("mutation { createAcct(input: {" + input + "}) { handle } }")
		if !strings.Contains(got, "BAD_USER_INPUT") || !strings.Contains(got, "CreateAcctInput.") {
			t.Errorf("%s: %s", name, got)
		}
	}
	if n := s.count("accts"); n != 0 {
		t.Errorf("%d rows after the refusals", n)
	}
	s.want("left out", "mutation { createAcct(input: {}) { handle } }", '{"data":{"createAcct":{"handle":"anon"}}}')
	s.want("given", 'mutation { createAcct(input: {handle: "x"}) { handle } }', '{"data":{"createAcct":{"handle":"x"}}}')
	id := veloxgql.EncodeID("Acct", 2)
	s.want("update", fmt.Sprintf("mutation { updateAcct(id: %q, input: {handle: %q}) { handle } }", id, ""), '{"data":{"updateAcct":{"handle":""}}}')
}

// TestOrEmptyMatchesNone: or: [] matches no row, as titleIn: [] would.
func TestOrEmptyMatchesNone(t *testing.T) {
	s := serve(t)
	s.want("or []", "{ posts(where: {or: []}) { edges { node { title } } } }", '{"data":{"posts":{"edges":[]}}}')
}

// TestIntrospectionTypeNames: entities named as GraphQL's introspection types
// serve, and so does introspection.
func TestIntrospectionTypeNames(t *testing.T) {
	s := serve(t)
	s.want("the entity", fmt.Sprintf("{ node(id: %q) { ... on Type { name kind } } }", veloxgql.EncodeID("Type", 1)),
		'{"data":{"node":{"name":"t","kind":"A"}}}')
	got := s.do('{ __type(name: "Type") { name kind fields { name type { kind ofType { name } } } } __schema { queryType { name } directives { name locations } } }')
	for _, want := range []string{'"name":"Type"', '"kind":"OBJECT"', '"queryType":{"name":"Query"}', '"locations":["FIELD_DEFINITION"'} {
		if !strings.Contains(got, want) {
			t.Errorf("no %s in %s", want, got)
		}
	}
	if strings.Contains(got, "errors") {
		t.Errorf("errors: %s", got)
	}
}
`

// TestLimitsServed generates its schema's application with velox-gen and the
// plugin, then gqlgen over it, and runs its test through gqlgen's handler, on
// SQLite: what a Required edge reads when its target is hidden, a tenant key,
// an explicit zero value, or: [] and entities named as introspection types.
func TestLimitsServed(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	_, run := app(t, map[string]string{
		"schema/schema.go":          e2eLimitsSchema,
		"gen/member_policy.go":      e2eLimitsPolicy,
		"gqlgen.yml":                e2eCollectGqlgen,
		"graph/schema.resolvers.go": e2eLimitsResolvers,
		"limits_test.go":            strings.NewReplacer("'", "`", "~", "'", "_ context,", "_ context.Context,", `"database/sql"`, "\"context\"\n\t\"database/sql\"").Replace(e2eLimitsRun),
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
	out, err := run("test", "-count=1", "-v", ".")
	if err != nil {
		t.Fatalf("go test: %v\n%s", err, out)
	}
	for _, name := range []string{"TestRequiredEdgeHidden", "TestTenantKeyFilled", "TestExplicitZeroRefused", "TestOrEmptyMatchesNone", "TestIntrospectionTypeNames"} {
		if !strings.Contains(out, "--- PASS: "+name+" ") {
			t.Errorf("no PASS of %s in\n%s", name, out)
		}
	}
}
