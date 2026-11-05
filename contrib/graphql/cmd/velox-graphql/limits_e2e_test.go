package main

import (
	"strings"
	"testing"
)

// e2eLimitsSchema is the schema of TestLimitsServed. Its tables are its own.
//   - Post, Ticket and Remark are scoped by org_id (schema/scope.go); for Post it is
//     the key of a Required edge to Org that the scope fills, and Post
//     has a Required edge to Member, whose policy, in schema/member_policy.go, hides
//     the member named "hidden".
//   - Acct has a string and a time with a Default.
//   - Type, Field, Schema, Directive, EnumValue, InputValue, and the enums
//     TypeKind and DirectiveLocation of two of them, are named as gqlgen's
//     autobind would bind GraphQL's own introspection types.
const e2eLimitsSchema = `package schema

import (
	"net"
	"time"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"

	"example.com/app/shapes"
)

type Org struct{ schema.Schema }

func (Org) Fields() []schema.Field { return []schema.Field{field.String("name")} }

func (Org) Edges() []schema.Edge { return []schema.Edge{edge.To("posts", Post.Type)} }

type Member struct{ schema.Schema }

func (Member) Fields() []schema.Field { return []schema.Field{field.String("name")} }

func (Member) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("posts", Post.Type),
		edge.To("docs", Doc.Type),
		edge.To("groups", Group.Type).Annotations(graphql.Connection(false), graphql.MaxList(2)),
	}
}

func (Member) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.QueryField("members")}
}

type Group struct{ schema.Schema }

func (Group) Fields() []schema.Field { return []schema.Field{field.String("name")} }

func (Group) Edges() []schema.Edge {
	return []schema.Edge{edge.From("members", Member.Type).Ref("groups").Annotations(graphql.Connection(false), graphql.MaxList(2))}
}

type Ticket struct{ schema.Schema }

func (Ticket) Fields() []schema.Field {
	return []schema.Field{field.Int64("org_id").Immutable().Annotations(graphql.Skip()), field.String("title")}
}

func (Ticket) Edges() []schema.Edge { return []schema.Edge{edge.To("remarks", Remark.Type)} }

type Remark struct{ schema.Schema }

func (Remark) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Immutable().Annotations(graphql.Skip()),
		field.String("body"),
		field.Int64("ticket_id").Optional().Nillable().Annotations(graphql.WhereOps(graphql.OpsEquality | graphql.OpsNullable)),
	}
}

func (Remark) Edges() []schema.Edge {
	return []schema.Edge{edge.From("ticket", Ticket.Type).Ref("remarks").Unique().Field("ticket_id").Annotations(graphql.WhereEdge())}
}

func (Remark) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.QueryField("remarks")}
}

type Doc struct{ schema.Schema }

func (Doc) Fields() []schema.Field {
	return []schema.Field{
		field.String("title"),
		field.Int64("member_id").Optional().Nillable().Annotations(graphql.WhereOps(graphql.OpsEquality | graphql.OpsNullable)),
	}
}

func (Doc) Edges() []schema.Edge {
	return []schema.Edge{edge.From("member", Member.Type).Ref("docs").Unique().Field("member_id").Annotations(graphql.WhereEdge())}
}

func (Doc) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.QueryField("docs"), graphql.Mutations(graphql.MutationUpdate)}
}

type Item struct{ schema.Schema }

func (Item) Fields() []schema.Field {
	return []schema.Field{
		field.String("s_nil").Optional().Nillable(),
		field.JSON("j_map", map[string]any{}).Optional(),
		field.Strings("ss").Optional(),
		field.JSON("j_struct", shapes.Shape{}).Optional().Nillable(),
		field.Uint64("big").Optional(),
		field.Bytes("ip").GoType(net.IP{}).Optional().Nillable(),
		field.JSON("trio", [3]int{}).Optional().Nillable(),
	}
}

func (Item) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.QueryField("items"), graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate)}
}

type Post struct{ schema.Schema }

func (Post) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Immutable(),
		field.Int64("member_id"),
		field.String("title").Annotations(graphql.WhereOps(graphql.OpEQ)),
	}
}

func (Post) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("org", Org.Type).Ref("posts").Unique().Required().Immutable().Field("org_id").Annotations(graphql.Skip(graphql.SkipMutationCreateInput)),
		edge.From("member", Member.Type).Ref("posts").Unique().Required().Field("member_id"),
	}
}

func (Post) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.QueryField("posts"), graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate)}
}

type Thing struct{ schema.Schema }

func (Thing) Fields() []schema.Field {
	return []schema.Field{
		field.Bytes("b"),
		field.Bytes("maybe").Optional().Nillable(),
		field.Strings("j"),
		field.Int("n").Optional().Nillable().Default(7),
	}
}

func (Thing) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.QueryField("things"), graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate)}
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

// Flag has what ent users write: a bool with a default true, through
// graphql.CreateDefault, zero Defaults, JSON lists and a nullable order.
type Flag struct{ schema.Schema }

func (Flag) Fields() []schema.Field {
	return []schema.Field{
		field.Bool("active").Annotations(graphql.CreateDefault(true)),
		field.Int64("views").Default(0),
		field.Bool("off").Default(false),
		field.Strings("tags").Optional(),
		field.Ints("nums"),
		field.Floats("fs").Optional(),
		field.JSON("bs", []bool{}).Optional(),
		field.Int("rank").Optional().Nillable().Annotations(graphql.OrderField("RANK")),
	}
}

func (Flag) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.QueryField("flags"), graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate)}
}

// Pin belongs to a flag, and connections order it by the flag's nullable
// rank: an OrderVia of a Nillable field.
type Pin struct{ schema.Schema }

func (Pin) Fields() []schema.Field { return []schema.Field{field.Int64("flag_id")} }

func (Pin) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("flag", Flag.Type).Unique().Required().Field("flag_id").Annotations(graphql.OrderVia("rank", "FLAG_RANK")),
	}
}

func (Pin) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.QueryField("pins")}
}

// Page has a required slug the create input leaves out, as entgql users write
// it, for the resolver to set.
type Page struct{ schema.Schema }

func (Page) Fields() []schema.Field {
	return []schema.Field{
		field.String("title"),
		field.String("slug").NotEmpty().Annotations(graphql.Skip(graphql.SkipMutationCreateInput)),
	}
}

func (Page) Annotations() []schema.Annotation {
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

// e2eLimitsShapes is the Go type of Item's JSON field j_struct, in a package
// of its own: gen imports it, and the schema package, which imports gen,
// cannot hold it.
const e2eLimitsShapes = `package shapes

// Shape is the Go type of a JSON field.
type Shape struct {
	A string
	N *int
}
`

// e2eLimitsPolicy hides the member named "hidden".
const e2eLimitsPolicy = `package schema

import (
	"context"

	"github.com/syssam/velox"
)

func (Member) Policy() velox.Policy {
	return velox.Policy{Read: func(context.Context) (velox.Cond, error) {
		return velox.C("name").NEQ("hidden"), nil
	}}
}
`

// e2eLimitsScope scopes Post, Ticket and Remark to the org the context names,
// in the schema package. Its key is OrgKey: the schema declares Org.
const e2eLimitsScope = `package schema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/scope"
)

var OrgKey = scope.NewKey[int64]("org")

// orgPolicy confines reads, updates and deletes to the context's org, and
// refuses an insert into another one.
func orgPolicy() velox.Policy {
	return velox.Policy{
		Read: func(ctx context.Context) (velox.Cond, error) {
			org, err := OrgKey.Require(ctx)
			if err != nil {
				return velox.Cond{}, err
			}
			return velox.C("org_id").EQ(org), nil
		},
		Insert: func(ctx context.Context, r velox.Row) error { return OrgKey.Match(ctx, r.Value("org_id")) },
	}
}

// orgHooks fill org_id on insert from the context.
func orgHooks() []velox.Hook {
	return []velox.Hook{velox.Before(func(ctx context.Context, m *velox.Mutation) error {
		if org, ok := OrgKey.From(ctx); ok {
			return m.Fill("org_id", org)
		}
		return nil
	}, velox.OpInsert)}
}

func (Post) Policy() velox.Policy   { return orgPolicy() }
func (Post) Hooks() []velox.Hook    { return orgHooks() }
func (Ticket) Policy() velox.Policy { return orgPolicy() }
func (Ticket) Hooks() []velox.Hook  { return orgHooks() }
func (Remark) Policy() velox.Policy { return orgPolicy() }
func (Remark) Hooks() []velox.Hook  { return orgHooks() }
`

// e2eLimitsResolvers are the resolvers TestLimitsServed uses, one line each;
// gqlgen stubs the others.
const e2eLimitsResolvers = `package graph

import (
	"context"
	"strings"

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
	return input.Create(ctx, nil)
}

// UpdatePost is the resolver for the updatePost field.
func (r *mutationResolver) UpdatePost(ctx context.Context, id string, input gen.UpdatePostInput) (*gen.Post, error) {
	return input.Update(ctx, id)
}

// Things is the resolver for the things field.
func (r *queryResolver) Things(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int) (*gen.ThingConnection, error) {
	return gen.ThingsConnection(ctx, after, first, before, last)
}

// CreateThing is the resolver for the createThing field.
func (r *mutationResolver) CreateThing(ctx context.Context, input gen.CreateThingInput) (*gen.Thing, error) {
	return input.Create(ctx, nil)
}

// UpdateThing is the resolver for the updateThing field.
func (r *mutationResolver) UpdateThing(ctx context.Context, id string, input gen.UpdateThingInput) (*gen.Thing, error) {
	return input.Update(ctx, id)
}

// CreateAcct is the resolver for the createAcct field.
func (r *mutationResolver) CreateAcct(ctx context.Context, input gen.CreateAcctInput) (*gen.Acct, error) {
	return input.Create(ctx, nil)
}

// UpdateAcct is the resolver for the updateAcct field.
func (r *mutationResolver) UpdateAcct(ctx context.Context, id string, input gen.UpdateAcctInput) (*gen.Acct, error) {
	return input.Update(ctx, id)
}

// Docs is the resolver for the docs field.
func (r *queryResolver) Docs(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, where *gen.DocWhereInput) (*gen.DocConnection, error) {
	return gen.DocsConnection(ctx, after, first, before, last, where)
}

// UpdateDoc is the resolver for the updateDoc field.
func (r *mutationResolver) UpdateDoc(ctx context.Context, id string, input gen.UpdateDocInput) (*gen.Doc, error) {
	return input.Update(ctx, id)
}

// Members is the resolver for the members field.
func (r *queryResolver) Members(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int) (*gen.MemberConnection, error) {
	return gen.MembersConnection(ctx, after, first, before, last)
}

// Remarks is the resolver for the remarks field.
func (r *queryResolver) Remarks(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, where *gen.RemarkWhereInput) (*gen.RemarkConnection, error) {
	return gen.RemarksConnection(ctx, after, first, before, last, where)
}

// Items is the resolver for the items field.
func (r *queryResolver) Items(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int) (*gen.ItemConnection, error) {
	return gen.ItemsConnection(ctx, after, first, before, last)
}

// CreateItem is the resolver for the createItem field.
func (r *mutationResolver) CreateItem(ctx context.Context, input gen.CreateItemInput) (*gen.Item, error) {
	return input.Create(ctx, nil)
}

// UpdateItem is the resolver for the updateItem field.
func (r *mutationResolver) UpdateItem(ctx context.Context, id string, input gen.UpdateItemInput) (*gen.Item, error) {
	return input.Update(ctx, id)
}

// Flags is the resolver for the flags field.
func (r *queryResolver) Flags(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy *gen.FlagOrder) (*gen.FlagConnection, error) {
	return gen.FlagsConnection(ctx, after, first, before, last, orderBy)
}

// Pins is the resolver for the pins field.
func (r *queryResolver) Pins(ctx context.Context, after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int, orderBy *gen.PinOrder) (*gen.PinConnection, error) {
	return gen.PinsConnection(ctx, after, first, before, last, orderBy)
}

// CreateFlag is the resolver for the createFlag field.
func (r *mutationResolver) CreateFlag(ctx context.Context, input gen.CreateFlagInput) (*gen.Flag, error) {
	return input.Create(ctx, nil)
}

// UpdateFlag is the resolver for the updateFlag field.
func (r *mutationResolver) UpdateFlag(ctx context.Context, id string, input gen.UpdateFlagInput) (*gen.Flag, error) {
	return input.Update(ctx, id)
}

// CreatePage is the resolver for the createPage field: it sets the slug the
// input leaves out.
func (r *mutationResolver) CreatePage(ctx context.Context, input gen.CreatePageInput) (*gen.Page, error) {
	return input.Create(ctx, &gen.Page{Slug: strings.ToLower(input.Title)})
}

// UpdatePage is the resolver for the updatePage field.
func (r *mutationResolver) UpdatePage(ctx context.Context, id string, input gen.UpdatePageInput) (*gen.Page, error) {
	return input.Update(ctx, id)
}

// Mutation returns MutationResolver implementation.
func (r *Resolver) Mutation() MutationResolver { return &mutationResolver{r} }

// Query returns QueryResolver implementation.
func (r *Resolver) Query() QueryResolver { return &queryResolver{r} }

type mutationResolver struct{ *Resolver }
type queryResolver struct{ *Resolver }
`

// e2eLimitsSDL is the application's own schema: the mutations it serves,
// which velox-graphql leaves to it, each a resolver calling its input's
// Create or Update.
const e2eLimitsSDL = `type Mutation {
  createPost(input: CreatePostInput!): Post!
  updatePost(id: ID!, input: UpdatePostInput!): Post!
  createThing(input: CreateThingInput!): Thing!
  updateThing(id: ID!, input: UpdateThingInput!): Thing!
  createAcct(input: CreateAcctInput!): Acct!
  updateAcct(id: ID!, input: UpdateAcctInput!): Acct!
  updateDoc(id: ID!, input: UpdateDocInput!): Doc!
  createItem(input: CreateItemInput!): Item!
  updateItem(id: ID!, input: UpdateItemInput!): Item!
  createFlag(input: CreateFlagInput!): Flag!
  updateFlag(id: ID!, input: UpdateFlagInput!): Flag!
  createPage(input: CreatePageInput!): Page!
  updatePage(id: ID!, input: UpdatePageInput!): Page!
}
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
	"example.com/app/schema"
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

type served struct {
	t      *testing.T
	db     *sql.DB
	doVars func(query string, vars map[string]any) string
}

func (s served) do(query string) string { return s.doVars(query, nil) }

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
		"CREATE TABLE things (id INTEGER PRIMARY KEY, b BLOB NOT NULL, maybe BLOB, j JSON NOT NULL, n INTEGER)",
		"CREATE TABLE pages (id INTEGER PRIMARY KEY, title TEXT NOT NULL, slug TEXT NOT NULL)",
		"CREATE TABLE types (id INTEGER PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL)",
		"CREATE TABLE docs (id INTEGER PRIMARY KEY, title TEXT NOT NULL, member_id INTEGER REFERENCES members (id))",
		"CREATE TABLE items (id INTEGER PRIMARY KEY, s_nil TEXT, j_map JSON DEFAULT ~{}~, ss JSON DEFAULT ~[]~, j_struct JSON, big INTEGER DEFAULT 0, ip BLOB, trio JSON)",
		"CREATE TABLE groups (id INTEGER PRIMARY KEY, name TEXT NOT NULL)",
		"CREATE TABLE member_groups (member_id INTEGER NOT NULL, group_id INTEGER NOT NULL, PRIMARY KEY (member_id, group_id))",
		"CREATE TABLE tickets (id INTEGER PRIMARY KEY, org_id INTEGER NOT NULL, title TEXT NOT NULL)",
		"CREATE TABLE remarks (id INTEGER PRIMARY KEY, org_id INTEGER NOT NULL, body TEXT NOT NULL, ticket_id INTEGER)",
		"CREATE TABLE flags (id INTEGER PRIMARY KEY, active BOOLEAN NOT NULL, views INTEGER NOT NULL DEFAULT 0, off BOOLEAN NOT NULL DEFAULT false, tags JSON, nums JSON NOT NULL, fs JSON, bs JSON, rank INTEGER)",
		"CREATE TABLE pins (id INTEGER PRIMARY KEY, flag_id INTEGER NOT NULL REFERENCES flags (id))",
		"INSERT INTO orgs (id, name) VALUES (1, ~o1~), (2, ~o2~)",
		"INSERT INTO members (id, name) VALUES (1, ~seen~), (2, ~hidden~)",
		"INSERT INTO posts (id, org_id, member_id, title) VALUES (1, 1, 1, ~p1~), (2, 1, 2, ~p2~)",
		"INSERT INTO types (id, name, kind) VALUES (1, ~t~, ~a~)",
		"INSERT INTO docs (id, title, member_id) VALUES (1, ~d1~, 1), (4, ~d4~, 2), (5, ~d5~, NULL)",
		"INSERT INTO groups (id, name) VALUES (1, ~small~), (2, ~large~)",
		"INSERT INTO member_groups (member_id, group_id) VALUES (1, 1)",
		"INSERT INTO tickets (id, org_id, title) VALUES (1, 1, ~mine~), (2, 2, ~theirs~)",
		"INSERT INTO remarks (id, org_id, body, ticket_id) VALUES (1, 1, ~r1~, 1), (2, 1, ~r2~, 2), (3, 1, ~r3~, NULL)",
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
	do := func(query string, vars map[string]any) string {
		t.Helper()
		b, err := json.Marshal(map[string]any{"query": query, "variables": vars})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", "/", strings.NewReader(string(b)))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(schema.OrgKey.With(req.Context(), int64(1)))
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

// TestScopeKeyFilled: the scope column that an edge holds is no input, and
// the context's value fills it.
func TestScopeKeyFilled(t *testing.T) {
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

// TestSkippedFieldSetByResolver: a required field the create input leaves out
// stored "" silently, and its validator's error named CreatePageInput.slug,
// which the SDL has not. The resolver sets it through CreatePage's set; its
// validator's error names no create input field; an update names its own.
func TestSkippedFieldSetByResolver(t *testing.T) {
	s := serve(t)
	s.want("create", 'mutation { createPage(input: {title: "Hello"}) { title slug } }', '{"data":{"createPage":{"title":"Hello","slug":"hello"}}}')
	if got := s.do('mutation { createPage(input: {title: ""}) { slug } }'); !strings.Contains(got, "BAD_USER_INPUT") || strings.Contains(got, "CreatePageInput.slug") {
		t.Errorf("an empty slug on create: %s", got)
	}
	if n := s.count("pages"); n != 1 {
		t.Errorf("%d pages, want 1", n)
	}
	id := veloxgql.EncodeID("Page", 1)
	if got := s.do(fmt.Sprintf('mutation { updatePage(id: %q, input: {slug: ""}) { slug } }', id)); !strings.Contains(got, "UpdatePageInput.slug") {
		t.Errorf("an empty slug on update: %s", got)
	}
}

// TestEmptyBytes: an empty blob reads back "" in Bytes!, where SQLite's driver
// gives nil, and a page holding one has its data. A Nillable one reads null.
func TestEmptyBytes(t *testing.T) {
	s := serve(t)
	s.want("create", 'mutation { createThing(input: {b: "", j: []}) { b maybe } }', '{"data":{"createThing":{"b":"","maybe":null}}}')
	s.want("create, not empty", 'mutation { createThing(input: {b: "QQ==", j: []}) { b } }', '{"data":{"createThing":{"b":"QQ=="}}}')
	s.want("the page", "{ things { edges { node { b } } } }", '{"data":{"things":{"edges":[{"node":{"b":""}},{"node":{"b":"QQ=="}}]}}}')
	id := veloxgql.EncodeID("Thing", 2)
	s.want("update", fmt.Sprintf("mutation { updateThing(id: %q, input: {b: %q}) { b } }", id, ""), '{"data":{"updateThing":{"b":""}}}')
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM things WHERE b = x~~").Scan(&n); err != nil || n != 2 {
		t.Errorf("%d empty blobs stored: %v", n, err)
	}
}

// TestEmptyJSONLiteral: a literal [] stores [], on create and on update, as a
// variable's does.
func TestEmptyJSONLiteral(t *testing.T) {
	s := serve(t)
	s.want("create", 'mutation { createThing(input: {b: "QQ==", j: []}) { j } }', '{"data":{"createThing":{"j":[]}}}')
	s.want("create, not empty", 'mutation { createThing(input: {b: "QQ==", j: ["a"]}) { j } }', '{"data":{"createThing":{"j":["a"]}}}')
	id := veloxgql.EncodeID("Thing", 2)
	s.want("update", fmt.Sprintf("mutation { updateThing(id: %q, input: {j: []}) { j } }", id), '{"data":{"updateThing":{"j":[]}}}')
	var j string
	if err := s.db.QueryRow("SELECT j FROM things WHERE id = 2").Scan(&j); err != nil || j != "[]" {
		t.Errorf("stored %q: %v", j, err)
	}
}

// TestExplicitNullRefused: a create that gives a Nillable field with a Default
// null is refused, not silently given the Default, literal or variable; left out,
// it has the Default; an update stores NULL by clear<Field>.
func TestExplicitNullRefused(t *testing.T) {
	s := serve(t)
	const q = 'mutation ($i: CreateThingInput!) { createThing(input: $i) { n } }'
	const qn = 'mutation ($n: Int) { createThing(input: {b: "", j: [], n: $n}) { n } }'
	for name, got := range map[string]string{
		"literal":         s.do('mutation { createThing(input: {b: "", j: [], n: null}) { n } }'),
		"whole variable":  s.doVars(q, map[string]any{"i": map[string]any{"b": "", "j": []any{}, "n": nil}}),
		"one variable":    s.doVars(qn, map[string]any{"n": nil}),
	} {
		if !strings.Contains(got, "BAD_USER_INPUT") || !strings.Contains(got, "CreateThingInput.n: invalid value: null") {
			t.Errorf("%s: %s", name, got)
		}
	}
	if n := s.count("things"); n != 0 {
		t.Errorf("%d rows after the refusals", n)
	}
	s.want("left out", 'mutation { createThing(input: {b: "", j: []}) { n } }', '{"data":{"createThing":{"n":7}}}')
	if got := s.doVars(qn, nil); got != '{"data":{"createThing":{"n":7}}}' {
		t.Errorf("a variable left out: %s", got)
	}
	s.want("given", 'mutation { createThing(input: {b: "", j: [], n: 3}) { n } }', '{"data":{"createThing":{"n":3}}}')
	id := veloxgql.EncodeID("Thing", 1)
	s.want("clear", fmt.Sprintf("mutation { updateThing(id: %q, input: {clearN: true}) { n } }", id), '{"data":{"updateThing":{"n":null}}}')
}

// TestForeignKeyFilterHidden: a foreign key whose target a policy hides reads as null
// to every predicate on it, as the edge does: no filter tells the hidden row is
// linked, or which row it is.
func TestForeignKeyFilterHidden(t *testing.T) {
	s := serve(t)
	seen, hidden := veloxgql.EncodeID("Member", 1), veloxgql.EncodeID("Member", 2)
	titles := func(where string) string {
		got := s.do("{ docs(where: {" + where + "}) { edges { node { title } } } }")
		if strings.Contains(got, "errors") {
			t.Errorf("%s: %s", where, got)
		}
		var out []string
		for _, part := range strings.Split(got, '"title":"')[1:] {
			out = append(out, part[:strings.Index(part, '"')])
		}
		return strings.Join(out, ",")
	}
	for _, c := range []struct{ where, want string }{
		{fmt.Sprintf("memberID: %q", seen), "d1"},
		{fmt.Sprintf("memberID: %q", hidden), ""},
		{fmt.Sprintf("memberIDNEQ: %q", seen), ""},
		{fmt.Sprintf("memberIDNEQ: %q", hidden), "d1"},
		{fmt.Sprintf("memberIDIn: [%q, %q]", seen, hidden), "d1"},
		{fmt.Sprintf("memberIDNotIn: [%q]", hidden), "d1"},
		{"memberIDIsNil: true", "d4,d5"},
		{"memberIDIsNil: false", "d1"},
		{"memberIDNotNil: true", "d1"},
		{"memberIDNotNil: false", "d4,d5"},
		{"hasMember: false", "d4,d5"},
		{"hasMember: true", "d1"},
	} {
		if got := titles(c.where); got != c.want {
			t.Errorf("%s: %q, want %q", c.where, got, c.want)
		}
	}
	s.want("it reads null", "{ docs { edges { node { title member { name } } } } }",
		'{"data":{"docs":{"edges":[{"node":{"title":"d1","member":{"name":"seen"}}},{"node":{"title":"d4","member":null}},{"node":{"title":"d5","member":null}}]}}}')
}

// TestUpdateNullClears: an update that gives null to a nullable field clears it, literal
// or variable, as a client means by it; clear<Field> stays. Left out, it keeps it.
func TestUpdateNullClears(t *testing.T) {
	s := serve(t)
	const q = 'mutation ($id: ID!, $i: UpdateItemInput!) { updateItem(id: $id, input: $i) { sNil } }'
	const qn = 'mutation ($id: ID!, $n: String) { updateItem(id: $id, input: {sNil: $n}) { sNil } }'
	id := veloxgql.EncodeID("Item", 1)
	set := func() {
		t.Helper()
		if got := s.do(fmt.Sprintf('mutation { updateItem(id: %q, input: {sNil: "a"}) { sNil } }', id)); got != '{"data":{"updateItem":{"sNil":"a"}}}' {
			t.Fatal(got)
		}
	}
	s.want("create", 'mutation { createItem(input: {sNil: "a"}) { sNil } }', '{"data":{"createItem":{"sNil":"a"}}}')
	const cleared = '{"data":{"updateItem":{"sNil":null}}}'
	s.want("left out", fmt.Sprintf("mutation { updateItem(id: %q, input: {}) { sNil } }", id), '{"data":{"updateItem":{"sNil":"a"}}}')
	if got := s.doVars(qn, map[string]any{"id": id}); got != '{"data":{"updateItem":{"sNil":"a"}}}' {
		t.Errorf("a variable left out: %s", got)
	}
	s.want("literal", fmt.Sprintf("mutation { updateItem(id: %q, input: {sNil: null}) { sNil } }", id), cleared)
	set()
	if got := s.doVars(q, map[string]any{"id": id, "i": map[string]any{"sNil": nil}}); got != cleared {
		t.Errorf("whole variable: %s", got)
	}
	set()
	if got := s.doVars(qn, map[string]any{"id": id, "n": nil}); got != cleared {
		t.Errorf("one variable: %s", got)
	}
	set()
	s.want("clear", fmt.Sprintf("mutation { updateItem(id: %q, input: {clearSNil: true}) { sNil } }", id), cleared)
	set()
	s.want("null with clear", fmt.Sprintf("mutation { updateItem(id: %q, input: {sNil: null, clearSNil: true}) { sNil } }", id), cleared)
	set()
	if got := s.do(fmt.Sprintf('mutation { updateItem(id: %q, input: {sNil: "b", clearSNil: true}) { sNil } }', id)); !strings.Contains(got, "BAD_USER_INPUT") {
		t.Errorf("a value with clear: %s", got)
	}
	// clear<Field> is not a required field's: its null is a field not given.
	thing := veloxgql.EncodeID("Thing", 1)
	s.want("the thing", 'mutation { createThing(input: {b: "QQ==", j: []}) { b } }', '{"data":{"createThing":{"b":"QQ=="}}}')
	s.want("a required field", fmt.Sprintf("mutation { updateThing(id: %q, input: {b: null}) { b } }", thing), '{"data":{"updateThing":{"b":"QQ=="}}}')
}

// TestUpdateNullEdgeRefused: a null given to an edge's <edge>ID is refused, naming
// clear<Edge>, literal or variable: clearing an edge unlinks a row, so it is said.
func TestUpdateNullEdgeRefused(t *testing.T) {
	s := serve(t)
	doc := veloxgql.EncodeID("Doc", 1)
	if got := s.do(fmt.Sprintf("mutation { updateDoc(id: %q, input: {memberID: null}) { title } }", doc)); !strings.Contains(got, "BAD_USER_INPUT") || !strings.Contains(got, "clearMember") {
		t.Errorf("an edge: %s", got)
	}
}

// TestNullBytesGoType: a Bytes field of a named Go type reads NULL as null, and the page
// holding the row keeps its data; a value reads as written.
func TestNullBytesGoType(t *testing.T) {
	s := serve(t)
	s.want("null", 'mutation { createItem(input: {sNil: "a"}) { ip } }', '{"data":{"createItem":{"ip":null}}}')
	s.want("value", 'mutation { createItem(input: {ip: "AQIDBA=="}) { ip } }', '{"data":{"createItem":{"ip":"AQIDBA=="}}}')
	s.want("the page", "{ items { edges { node { ip } } } }", '{"data":{"items":{"edges":[{"node":{"ip":null}},{"node":{"ip":"AQIDBA=="}}]}}}')
	s.want("clear", fmt.Sprintf("mutation { updateItem(id: %q, input: {ip: null}) { ip } }", veloxgql.EncodeID("Item", 2)), '{"data":{"updateItem":{"ip":null}}}')
}

// TestForeignKeyFilterOtherScope: a foreign key to a row of another org reads as
// null to every predicate on it, as the edge does: the same hides as a policy hides.
func TestForeignKeyFilterOtherScope(t *testing.T) {
	s := serve(t)
	mine, theirs := veloxgql.EncodeID("Ticket", 1), veloxgql.EncodeID("Ticket", 2)
	for _, c := range []struct{ where, want string }{
		{fmt.Sprintf("ticketID: %q", mine), "r1"},
		{fmt.Sprintf("ticketID: %q", theirs), ""},
		{fmt.Sprintf("ticketIDNEQ: %q", theirs), "r1"},
		{fmt.Sprintf("ticketIDIn: [%q, %q]", mine, theirs), "r1"},
		{fmt.Sprintf("ticketIDNotIn: [%q]", theirs), "r1"},
		{"ticketIDIsNil: true", "r2,r3"},
		{"ticketIDNotNil: true", "r1"},
		{"hasTicket: false", "r2,r3"},
		{"hasTicket: true", "r1"},
	} {
		got := s.do("{ remarks(where: {" + c.where + "}) { edges { node { body } } } }")
		if strings.Contains(got, "errors") {
			t.Fatalf("%s: %s", c.where, got)
		}
		var out []string
		for _, part := range strings.Split(got, '"body":"')[1:] {
			out = append(out, part[:strings.Index(part, '"')])
		}
		if joined := strings.Join(out, ","); joined != c.want {
			t.Errorf("%s: %q, want %q", c.where, joined, c.want)
		}
	}
}

// TestListEdgeMaxList: graphql.MaxList lets two levels of lists through the cost, as a
// client cannot lower what they charge, and fails the edge past it: no row is dropped.
func TestListEdgeMaxList(t *testing.T) {
	s := serve(t)
	s.want("nested", "{ members(first: 1) { edges { node { name groups { name members { name } } } } } }",
		'{"data":{"members":{"edges":[{"node":{"name":"seen","groups":[{"name":"small","members":[{"name":"seen"}]}]}}]}}}')
	for _, q := range []string{"INSERT INTO groups (id, name) VALUES (3, ~g3~), (4, ~g4~)", "INSERT INTO member_groups (member_id, group_id) VALUES (1, 2), (1, 3)"} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.do("{ members(first: 1) { edges { node { groups { name } } } } }"); !strings.Contains(got, "INTERNAL_SERVER_ERROR") {
		t.Errorf("past it: %s", got)
	}
}

// TestJSONInputExact: what a JSON field stores is what the client gave, or refused:
// a number keeps its digits, a null in a list of strings is no empty string, and a
// key the type has not is not dropped.
func TestJSONInputExact(t *testing.T) {
	s := serve(t)
	stored := func() string {
		t.Helper()
		var j string
		if err := s.db.QueryRow("SELECT j_map FROM items ORDER BY id DESC LIMIT 1").Scan(&j); err != nil {
			t.Fatal(err)
		}
		return j
	}
	const q = 'mutation ($m: JSON) { createItem(input: {jMap: $m}) { id } }'
	if got := s.doVars(q, map[string]any{"m": map[string]any{"n": json.Number("9007199254740993")}}); strings.Contains(got, "errors") {
		t.Fatal(got)
	}
	if got := stored(); got != '{"n":9007199254740993}' {
		t.Errorf("a variable's number: %s", got)
	}
	if got := s.do('mutation { createItem(input: {jMap: {n: 9007199254740993}}) { id } }'); strings.Contains(got, "errors") {
		t.Fatal(got)
	}
	if got := stored(); got != '{"n":9007199254740993}' {
		t.Errorf("a literal's number: %s", got)
	}
	// The row keeps the digits. Reading it back into a map[string]any gives a float64,
	// which velox's scan decides: 9007199254740992 (0027's Consequences).
	s.want("read back", "{ items(last: 1) { edges { node { jMap } } } }", '{"data":{"items":{"edges":[{"node":{"jMap":{"n":9007199254740992}}}]}}}')
	n := s.count("items")
	for name, q := range map[string]string{
		"null in a list":  'mutation { createItem(input: {trio: [1, null, 3]}) { id } }',
		"null in a struct": 'mutation { createItem(input: {jStruct: {A: null}}) { id } }',
		"an unknown key":   'mutation { createItem(input: {jStruct: {b: 1}}) { id } }',
	} {
		if got := s.do(q); !strings.Contains(got, "BAD_USER_INPUT") {
			t.Errorf("%s: %s", name, got)
		}
	}
	// ss is [String!] now, so GraphQL refuses its null.
	if got := s.do('mutation { createItem(input: {ss: ["a", null]}) { id } }'); !strings.Contains(got, "GRAPHQL_VALIDATION_FAILED") {
		t.Errorf("null in a list of strings: %s", got)
	}
	if got := s.count("items"); got != n {
		t.Errorf("%d rows after the refusals, was %d", got, n)
	}
	for name, q := range map[string]string{
		"short": 'mutation { createItem(input: {trio: [1, 2]}) { id } }',
		"long":  'mutation { createItem(input: {trio: [1, 2, 3, 4]}) { id } }',
	} {
		if got := s.do(q); !strings.Contains(got, "BAD_USER_INPUT") || !strings.Contains(got, "CreateItemInput.trio") {
			t.Errorf("%s: %s", name, got)
		}
	}
	s.want("the length", 'mutation { createItem(input: {trio: [1, 2, 3]}) { trio } }', '{"data":{"createItem":{"trio":[1,2,3]}}}')
	s.want("a pointer may be null", 'mutation { createItem(input: {jStruct: {A: "x", N: null}}) { jStruct } }', '{"data":{"createItem":{"jStruct":{"A":"x","N":null}}}}')
}

// TestUint64HighBit: a Uint64 over 2^63-1 is the client's error, naming the limit; at
// it, it stores.
func TestUint64HighBit(t *testing.T) {
	s := serve(t)
	got := s.do('mutation { createItem(input: {big: "18446744073709551615"}) { big } }')
	if !strings.Contains(got, "BAD_USER_INPUT") || !strings.Contains(got, "9223372036854775807") {
		t.Errorf("over: %s", got)
	}
	s.want("at the limit", 'mutation { createItem(input: {big: "9223372036854775807"}) { big } }', '{"data":{"createItem":{"big":"9223372036854775807"}}}')
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

// TestCreateDefaultServed: a bool with graphql.CreateDefault(true) is optional
// in the create input, true when left out and false when given false, and stays
// Boolean! in the type; a zero Default stores the zero given.
func TestCreateDefaultServed(t *testing.T) {
	s := serve(t)
	s.want("left out", "mutation { createFlag(input: {nums: []}) { active views off } }", '{"data":{"createFlag":{"active":true,"views":0,"off":false}}}')
	s.want("given", "mutation { createFlag(input: {active: false, views: 0, off: false, nums: []}) { active views off } }", '{"data":{"createFlag":{"active":false,"views":0,"off":false}}}')
	s.want("given, not zero", "mutation { createFlag(input: {active: true, views: 3, off: true, nums: []}) { active views off } }", '{"data":{"createFlag":{"active":true,"views":3,"off":true}}}')
	got := s.do('{ __type(name: "CreateFlagInput") { inputFields { name defaultValue type { kind } } } t: __type(name: "Flag") { fields { name type { kind } } } }')
	for _, want := range []string{'{"name":"active","defaultValue":"true","type":{"kind":"NON_NULL"}}', '{"name":"active","type":{"kind":"NON_NULL"}}'} {
		if !strings.Contains(got, want) {
			t.Errorf("no %s in %s", want, got)
		}
	}
	if got := s.do("mutation { createFlag(input: {active: null, nums: []}) { active } }"); !strings.Contains(got, "errors") {
		t.Errorf("null: %s", got)
	}
}

// TestJSONListsServed: a JSON field of []string, []int, []float64 or []bool is
// a list of the scalar, both ways; an empty list stores empty, and a clear NULL.
func TestJSONListsServed(t *testing.T) {
	s := serve(t)
	s.want("create", 'mutation { createFlag(input: {tags: ["a", "b"], nums: [1, 2], fs: [1.5], bs: [true, false]}) { tags nums fs bs } }',
		'{"data":{"createFlag":{"tags":["a","b"],"nums":[1,2],"fs":[1.5],"bs":[true,false]}}}')
	s.want("left out", "mutation { createFlag(input: {nums: []}) { tags nums fs bs } }", '{"data":{"createFlag":{"tags":null,"nums":[],"fs":null,"bs":null}}}')
	id := veloxgql.EncodeID("Flag", 1)
	s.want("update to empty", fmt.Sprintf('mutation { updateFlag(id: %q, input: {tags: [], nums: [3]}) { tags nums } }', id), '{"data":{"updateFlag":{"tags":[],"nums":[3]}}}')
	s.want("clear", fmt.Sprintf('mutation { updateFlag(id: %q, input: {clearTags: true}) { tags } }', id), '{"data":{"updateFlag":{"tags":null}}}')
	s.want("null clears", fmt.Sprintf('mutation { updateFlag(id: %q, input: {fs: null}) { fs } }', id), '{"data":{"updateFlag":{"fs":null}}}')
	got := s.do('{ __type(name: "Flag") { fields { name type { kind ofType { kind ofType { kind ofType { name } } } } } } }')
	for _, want := range []string{'{"name":"tags","type":{"kind":"LIST","ofType":{"kind":"NON_NULL","ofType":{"kind":"SCALAR","ofType":null}}}}', '{"name":"nums","type":{"kind":"NON_NULL","ofType":{"kind":"LIST","ofType":{"kind":"NON_NULL","ofType":{"name":"Int"}}}}}'} {
		if !strings.Contains(got, want) {
			t.Errorf("no %s in %s", want, got)
		}
	}
	if got := s.do('mutation { createFlag(input: {tags: ["a", null], nums: []}) { tags } }'); !strings.Contains(got, "errors") {
		t.Errorf("a null element: %s", got)
	}
}

// TestNullableOrderServed: a connection ordered by a nullable field, with NULLs
// and ties, pages forward and backward exactly, two rows at a time, NULL last
// ascending and first descending.
func TestNullableOrderServed(t *testing.T) {
	s := serve(t)
	for _, r := range []string{"null", "2", "null", "1", "2", "null", "1"} {
		if got := s.do("mutation { createFlag(input: {nums: [], rank: " + r + "}) { id } }"); strings.Contains(got, "errors") {
			t.Fatal(got)
		}
	}
	type page struct {
		Data struct {
			Flags struct {
				Edges    []struct{ Node struct{ Rank *int } }
				PageInfo struct {
					HasNextPage, HasPreviousPage bool
					StartCursor, EndCursor       string
				}
			}
		}
	}
	read := func(q string) page {
		t.Helper()
		var p page
		got := s.do(q)
		if err := json.Unmarshal([]byte(got), &p); err != nil || strings.Contains(got, "errors") {
			t.Fatalf("%s: %v", got, err)
		}
		return p
	}
	ranks := func(p page) string {
		var out []string
		for _, e := range p.Data.Flags.Edges {
			if e.Node.Rank == nil {
				out = append(out, "-")
			} else {
				out = append(out, fmt.Sprint(*e.Node.Rank))
			}
		}
		return strings.Join(out, " ")
	}
	for dir, want := range map[string]string{"ASC": "1 1 2 2 - - -", "DESC": "- - - 2 2 1 1"} {
		order := "orderBy: {field: RANK, direction: " + dir + "}"
		var fwd []string
		after := ""
		for {
			p := read("{ flags(first: 2" + after + ", " + order + ") { edges { node { rank } } pageInfo { hasNextPage hasPreviousPage startCursor endCursor } } }")
			fwd = append(fwd, ranks(p))
			if !p.Data.Flags.PageInfo.HasNextPage {
				break
			}
			after = fmt.Sprintf(", after: %q", p.Data.Flags.PageInfo.EndCursor)
		}
		if got := strings.Join(fwd, " "); got != want {
			t.Errorf("%s forward: %s, want %s", dir, got, want)
		}
		var back []string
		before := ""
		for {
			p := read("{ flags(last: 2" + before + ", " + order + ") { edges { node { rank } } pageInfo { hasNextPage hasPreviousPage startCursor endCursor } } }")
			back = append([]string{ranks(p)}, back...)
			if !p.Data.Flags.PageInfo.HasPreviousPage {
				break
			}
			before = fmt.Sprintf(", before: %q", p.Data.Flags.PageInfo.StartCursor)
		}
		if got := strings.Join(back, " "); got != want {
			t.Errorf("%s backward: %s, want %s", dir, got, want)
		}
	}
}

// TestNullableViaServed: a connection ordered by a nullable field of a to-one
// edge's target (OrderVia), with NULLs and ties, pages forward and backward
// exactly, one row at a time, NULL last ascending and first descending.
func TestNullableViaServed(t *testing.T) {
	s := serve(t)
	for i, r := range []string{"null", "2", "null", "1", "2", "null", "1"} {
		if got := s.do("mutation { createFlag(input: {nums: [], rank: " + r + "}) { id } }"); strings.Contains(got, "errors") {
			t.Fatal(got)
		}
		if _, err := s.db.Exec("INSERT INTO pins (id, flag_id) VALUES (?, ?)", i+1, i+1); err != nil {
			t.Fatal(err)
		}
	}
	type page struct {
		Data struct {
			Pins struct {
				Edges    []struct{ Node struct{ Flag struct{ Rank *int } } }
				PageInfo struct {
					HasNextPage, HasPreviousPage bool
					StartCursor, EndCursor       string
				}
			}
		}
	}
	read := func(q string) page {
		t.Helper()
		var p page
		got := s.do(q)
		if err := json.Unmarshal([]byte(got), &p); err != nil || strings.Contains(got, "errors") {
			t.Fatalf("%s: %v", got, err)
		}
		return p
	}
	ranks := func(p page) string {
		var out []string
		for _, e := range p.Data.Pins.Edges {
			if e.Node.Flag.Rank == nil {
				out = append(out, "-")
			} else {
				out = append(out, fmt.Sprint(*e.Node.Flag.Rank))
			}
		}
		return strings.Join(out, " ")
	}
	const sel = ") { edges { node { flag { rank } } } pageInfo { hasNextPage hasPreviousPage startCursor endCursor } } }"
	for dir, want := range map[string]string{"ASC": "1 1 2 2 - - -", "DESC": "- - - 2 2 1 1"} {
		order := "orderBy: {field: FLAG_RANK, direction: " + dir + "}"
		var fwd []string
		after := ""
		for {
			p := read("{ pins(first: 1" + after + ", " + order + sel)
			fwd = append(fwd, ranks(p))
			if !p.Data.Pins.PageInfo.HasNextPage {
				break
			}
			after = fmt.Sprintf(", after: %q", p.Data.Pins.PageInfo.EndCursor)
		}
		if got := strings.Join(fwd, " "); got != want {
			t.Errorf("%s forward: %s, want %s", dir, got, want)
		}
		var back []string
		before := ""
		for {
			p := read("{ pins(last: 1" + before + ", " + order + sel)
			back = append([]string{ranks(p)}, back...)
			if !p.Data.Pins.PageInfo.HasPreviousPage {
				break
			}
			before = fmt.Sprintf(", before: %q", p.Data.Pins.PageInfo.StartCursor)
		}
		if got := strings.Join(back, " "); got != want {
			t.Errorf("%s backward: %s, want %s", dir, got, want)
		}
	}
}
`

// TestLimitsServed generates its schema's application with velox-gen and the
// plugin, then gqlgen over it, and runs its test through gqlgen's handler, on
// SQLite: what a Required edge reads when its target is hidden, a scope key,
// an explicit zero value, or: [] and entities named as introspection types.
func TestLimitsServed(t *testing.T) {
	t.Parallel() // its own module, in its own directory
	if testing.Short() {
		t.Skip("builds a module")
	}
	_, run := app(t, map[string]string{
		"schema/schema.go":          e2eLimitsSchema,
		"shapes/shapes.go":          e2eLimitsShapes,
		"schema/member_policy.go":   e2eLimitsPolicy,
		"schema/scope.go":           e2eLimitsScope,
		"gqlgen.yml":                e2eCollectGqlgen,
		"graph/app.graphql":         e2eLimitsSDL,
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
	for _, name := range []string{"TestRequiredEdgeHidden", "TestScopeKeyFilled", "TestExplicitZeroRefused", "TestOrEmptyMatchesNone", "TestIntrospectionTypeNames", "TestEmptyBytes", "TestEmptyJSONLiteral", "TestExplicitNullRefused", "TestForeignKeyFilterHidden", "TestUpdateNullClears", "TestUpdateNullEdgeRefused", "TestNullBytesGoType", "TestForeignKeyFilterOtherScope", "TestListEdgeMaxList", "TestJSONInputExact", "TestUint64HighBit", "TestCreateDefaultServed", "TestJSONListsServed", "TestNullableOrderServed", "TestNullableViaServed", "TestSkippedFieldSetByResolver"} {
		if !strings.Contains(out, "--- PASS: "+name+" ") {
			t.Errorf("no PASS of %s in\n%s", name, out)
		}
	}
}
