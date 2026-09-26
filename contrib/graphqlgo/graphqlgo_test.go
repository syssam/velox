package graphqlgo_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	gqlgen "github.com/99designs/gqlgen/graphql"
	graphql "github.com/syssam/graphql-go"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/validator"

	"github.com/syssam/velox/contrib/graphql/gqlrelay"
	"github.com/syssam/velox/contrib/graphqlgo"
	"github.com/syssam/velox/runtime"
)

// The same query text is executed by graphql-go with Collect installed, and
// parsed into gqlgen's request context, and velox must plan the same loads
// from both: the columns each query reads, the edges it eager-loads and each
// edge's per-parent limit. The queries are written the way large clients
// write them -- variables for page sizes, named fragments, aliases of one
// connection, @include on a variable -- because each is a place where two
// engines can disagree about what was selected.

const sdl = `
type Query {
  user: User
  posts(first: Int, after: String): PostConnection!
  node: Pinned
}
type User {
  id: ID!
  name: String!
  email: String!
  avatarURL: String
  company: Company
  comments: [Comment!]!
  posts(first: Int, after: String, where: PostWhere): PostConnection!
  pinned: Pinned
}
interface Pinned { id: ID! }
type Company { id: ID! name: String! }
type Comment implements Pinned { id: ID! title: String! upvotes: Int! }
type PostConnection { edges: [PostEdge!]! pageInfo: PageInfo! totalCount: Int! }
type PostEdge { node: Post! }
type Post implements Pinned { id: ID! title: String! body: String! author: User }
type PageInfo { hasNextPage: Boolean! }
input PostWhere { titleContains: String }
`

// Metadata as velox generates it for this schema.
func userMeta() *runtime.CollectMeta {
	return &runtime.CollectMeta{
		FieldColumns: map[string]string{"name": "name", "email": "email"},
		Edges: map[string]runtime.EdgeMeta{
			"company":  {Name: "company", Unique: true, FKColumns: []string{"user_company"}},
			"comments": {Name: "comments"},
			"posts":    {Name: "posts", Relay: true, PagesLoaded: true},
		},
		// A graphql.InterfaceField backed by two edges. satisfies lists the
		// interface and its implementors, which is where the engines differ:
		// gqlgen matches fragment type conditions against it, graphql-go
		// keys the selection by object type.
		InterfaceFields: map[string]runtime.InterfaceFieldMeta{
			"pinned": {Edges: []string{"posts", "comments"}, Satisfies: []string{"Pinned", "Post", "Comment"}},
		},
	}
}

var childMeta = map[string]*runtime.CollectMeta{
	"company":  {FieldColumns: map[string]string{"name": "name"}},
	"comments": {FieldColumns: map[string]string{"title": "title"}},
	"posts": {
		FieldColumns: map[string]string{"title": "title", "body": "body"},
		Edges:        map[string]runtime.EdgeMeta{"author": {Name: "author", Unique: true, FKColumns: []string{"post_author"}}},
	},
	"author": {FieldColumns: map[string]string{"name": "name", "email": "email"}},
}

func postMeta() *runtime.CollectMeta { return childMeta["posts"] }

// recQuery is a runtime.FieldCollectable that records what the collector
// asks of it, as a generated query would carry it out.
type recQuery struct {
	ctx      runtime.QueryContext
	meta     *runtime.CollectMeta
	children map[string]*recQuery
}

func newRecQuery(meta *runtime.CollectMeta) *recQuery {
	return &recQuery{meta: meta, children: map[string]*recQuery{}}
}

func (q *recQuery) GetIDColumn() string               { return "id" }
func (q *recQuery) GetCtx() *runtime.QueryContext     { return &q.ctx }
func (q *recQuery) CollectMeta() *runtime.CollectMeta { return q.meta }

func (q *recQuery) WithEdgeLoad(name string, opts ...runtime.LoadOption) runtime.FieldCollectable {
	child := q.children[name]
	if child == nil {
		child = newRecQuery(childMeta[name])
		child.ctx.EdgeLoadCreated = true
		q.children[name] = child
	}
	if cfg := runtime.NewLoadConfig(opts...); cfg.Limit != nil {
		n := *cfg.Limit
		child.ctx.PartitionLimit = &n
	}
	return child
}

func (q *recQuery) plan() string {
	var b strings.Builder
	var walk func(q *recQuery, depth int)
	walk = func(q *recQuery, depth int) {
		cols := slices.Clone(q.ctx.Fields)
		slices.Sort(cols)
		fmt.Fprintf(&b, "%*scolumns %v\n", depth*2, "", cols)
		names := make([]string, 0, len(q.children))
		for name := range q.children {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			child := q.children[name]
			limit := "all"
			if child.ctx.PartitionLimit != nil {
				limit = fmt.Sprint(*child.ctx.PartitionLimit)
			}
			fmt.Fprintf(&b, "%*sload %s limit %s\n", depth*2, "", name, limit)
			walk(child, depth+1)
		}
	}
	walk(q, 0)
	return b.String()
}

// plans is what the resolvers observed.
type plans struct {
	user, posts, node string
	count             bool
}

// observe runs the collector the way the generated code does: CollectFields
// for a field returning entities, CollectConnectionFields and
// TotalCountSelected for a connection.
func observeUser(ctx context.Context, p *plans) {
	q := newRecQuery(userMeta())
	if err := gqlrelay.CollectFields(ctx, q, q.meta); err != nil {
		panic(err)
	}
	p.user = q.plan()
}

// observeNode is a node(id:) resolver that found a Post: it collects what
// applies to Post, which a Comment-only field must not unproject.
func observeNode(ctx context.Context, p *plans) {
	q := newRecQuery(postMeta())
	if err := gqlrelay.CollectFields(ctx, q, q.meta, "Pinned", "Post"); err != nil {
		panic(err)
	}
	p.node = q.plan()
}

func observePosts(ctx context.Context, p *plans) {
	q := newRecQuery(postMeta())
	if err := gqlrelay.CollectConnectionFields(ctx, q, q.meta); err != nil {
		panic(err)
	}
	p.posts = q.plan()
	p.count = gqlrelay.TotalCountSelected(ctx)
}

type (
	user     struct{}
	company  struct{}
	comment  struct{}
	conn     struct{}
	edge     struct{}
	post     struct{}
	pageInfo struct{}
	where    struct{ TitleContains *string }
	rootArgs struct {
		First *int
		After *string
	}
	userPostsArgs struct {
		First *int
		After *string
		Where *where
	}
)

// newSchema binds sdl, calling onUser and onPosts from the root resolvers.
func newSchema(t *testing.T, onUser, onPosts, onNode func(context.Context)) *graphql.Schema {
	t.Helper()
	s, err := graphql.NewSchema(graphql.SDL(sdl),
		graphql.Object[graphql.Root]("Query",
			graphql.Resolve("user", func(ctx context.Context, _ graphql.Root) (*user, error) {
				onUser(ctx)
				return nil, nil
			}),
			graphql.ResolveArgs("posts", func(ctx context.Context, _ graphql.Root, _ rootArgs) (*conn, error) {
				onPosts(ctx)
				return &conn{}, nil
			}),
			graphql.Resolve("node", func(ctx context.Context, _ graphql.Root) (any, error) {
				onNode(ctx)
				return nil, nil
			}),
		),
		graphql.Object[user]("User",
			graphql.Field("id", func(*user) graphql.ID { return "" }),
			graphql.Field("name", func(*user) string { return "" }),
			graphql.Field("email", func(*user) string { return "" }),
			graphql.Field("avatarURL", func(*user) *string { return nil }),
			graphql.Field("company", func(*user) *company { return nil }),
			graphql.Field("comments", func(*user) []comment { return nil }),
			graphql.ResolveArgs("posts", func(context.Context, *user, userPostsArgs) (*conn, error) { return &conn{}, nil }),
			graphql.Resolve("pinned", func(context.Context, *user) (any, error) { return nil, nil }),
		),
		graphql.Interface[any]("Pinned"),
		graphql.Object[company]("Company",
			graphql.Field("id", func(*company) graphql.ID { return "" }),
			graphql.Field("name", func(*company) string { return "" }),
		),
		graphql.Object[comment]("Comment",
			graphql.Field("id", func(*comment) graphql.ID { return "" }),
			graphql.Field("title", func(*comment) string { return "" }),
			graphql.Field("upvotes", func(*comment) int { return 0 }),
		),
		graphql.Object[conn]("PostConnection",
			graphql.Field("edges", func(*conn) []edge { return nil }),
			graphql.Field("pageInfo", func(*conn) pageInfo { return pageInfo{} }),
			graphql.Field("totalCount", func(*conn) int { return 0 }),
		),
		graphql.Object[edge]("PostEdge", graphql.Field("node", func(*edge) post { return post{} })),
		graphql.Object[post]("Post",
			graphql.Field("id", func(*post) graphql.ID { return "" }),
			graphql.Field("title", func(*post) string { return "" }),
			graphql.Field("body", func(*post) string { return "" }),
			graphql.Field("author", func(*post) *user { return nil }),
		),
		graphql.Object[pageInfo]("PageInfo", graphql.Field("hasNextPage", func(*pageInfo) bool { return false })),
		graphql.Input[where]("PostWhere", graphql.InputField("titleContains", func(w *where, v *string) { w.TitleContains = v })),
		graphql.Args[rootArgs](
			graphql.InputField("first", func(a *rootArgs, v *int) { a.First = v }),
			graphql.InputField("after", func(a *rootArgs, v *string) { a.After = v }),
		),
		graphql.Args[userPostsArgs](
			graphql.InputField("first", func(a *userPostsArgs, v *int) { a.First = v }),
			graphql.InputField("after", func(a *userPostsArgs, v *string) { a.After = v }),
			graphql.InputField("where", func(a *userPostsArgs, v *where) { a.Where = v }),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func execute(t *testing.T, exec *graphql.Executor, query string, vars map[string]any) {
	t.Helper()
	raw, err := json.Marshal(vars)
	if err != nil {
		t.Fatal(err)
	}
	if resp := exec.Execute(context.Background(), &graphql.Request{Query: query, Variables: raw}); len(resp.Errors) > 0 {
		t.Fatalf("graphql-go: %v", resp.Errors)
	}
}

// viaGraphQLGo executes query with Collect installed and returns what the
// resolvers planned.
func viaGraphQLGo(t *testing.T, query string, vars map[string]any) plans {
	t.Helper()
	var p plans
	s := newSchema(t,
		func(ctx context.Context) { observeUser(ctx, &p) },
		func(ctx context.Context) { observePosts(ctx, &p) },
		func(ctx context.Context) { observeNode(ctx, &p) })
	execute(t, graphql.NewExecutor(s, graphqlgo.Collect()), query, vars)
	return p
}

// viaGqlgen parses query as gqlgen does and runs the same observers with
// gqlgen's request context for each root field.
func viaGqlgen(t *testing.T, query string, vars map[string]any) plans {
	t.Helper()
	schema, gerr := gqlparser.LoadSchema(&ast.Source{Input: sdl})
	if gerr != nil {
		t.Fatal(gerr)
	}
	doc, errs := gqlparser.LoadQueryWithRules(schema, query, nil) // nil: the default rules
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	op := doc.Operations[0]
	coerced, verr := validator.VariableValues(schema, op, vars)
	if verr != nil {
		t.Fatal(verr)
	}
	oc := &gqlgen.OperationContext{Doc: doc, Operation: op, Variables: coerced}
	var p plans
	for _, sel := range op.SelectionSet {
		f := sel.(*ast.Field)
		ctx := gqlgen.WithOperationContext(context.Background(), oc)
		ctx = gqlgen.WithFieldContext(ctx, &gqlgen.FieldContext{Field: gqlgen.CollectedField{Field: f, Selections: f.SelectionSet}})
		switch f.Name {
		case "user":
			observeUser(ctx, &p)
		case "posts":
			observePosts(ctx, &p)
		case "node":
			observeNode(ctx, &p)
		}
	}
	return p
}

func TestCollectPlansWhatGqlgenPlans(t *testing.T) {
	const feed = `
		query Feed($recent: Int!, $more: Int, $cur: String, $withMail: Boolean!) {
			user {
				...UserHeader
				email @include(if: $withMail)
				recent: posts(first: $recent) { totalCount edges { node { title author { name } } } }
				more: posts(first: $more) { edges { node { ...PostBody } } }
				paged: posts(first: 2, after: $cur) { edges { node { title } } }
				comments { title }
			}
		}
		fragment UserHeader on User { name company { name } }
		fragment PostBody on Post { body }`

	cases := []struct {
		name  string
		query string
		vars  map[string]any
		// want pins graphql-go's plan too, so an agreement on nothing --
		// both engines planning no loads -- cannot pass.
		want plans
	}{
		{
			name:  "feed",
			query: feed,
			vars:  map[string]any{"recent": 2, "more": 5, "cur": "c1", "withMail": true},
			want: plans{user: "columns [email id name user_company]\n" +
				"load comments limit all\n  columns [id title]\n" +
				"load company limit all\n  columns [id name]\n" +
				"load posts limit all\n  columns [body id post_author title]\n  load author limit all\n    columns [id name]\n"},
		},
		{
			name:  "feed without the included field",
			query: feed,
			vars:  map[string]any{"recent": 2, "more": 5, "withMail": false},
			want: plans{user: "columns [id name user_company]\n" +
				"load comments limit all\n  columns [id title]\n" +
				"load company limit all\n  columns [id name]\n" +
				"load posts limit all\n  columns [body id post_author title]\n  load author limit all\n    columns [id name]\n"},
		},
		{
			name:  "pages without a count are limited per parent",
			query: `query($a: Int, $b: Int) { user { name x: posts(first: $a) { edges { node { title } } } y: posts(first: $b) { edges { node { title } } } } }`,
			vars:  map[string]any{"a": 2, "b": 4},
			want:  plans{user: "columns [id name]\nload posts limit 5\n  columns [id title]\n"},
		},
		{
			// Each backing edge is collected under every implementor's
			// condition, so Post's body is unknown to comments and keeps
			// that query unprojected: safe, and what gqlgen plans too.
			name:  "an interface field loads every backing edge",
			query: `{ user { name pinned { __typename ... on Post { title body } ... on Comment { title } } } }`,
			want: plans{user: "columns [id name]\n" +
				"load comments limit all\n  columns []\n" +
				"load posts limit all\n  columns [body id title]\n"},
		},
		{
			name:  "a node resolver collects what applies to its type",
			query: `{ node { id ... on Post { title author { name } } ... on Comment { upvotes } } }`,
			want:  plans{node: "columns [id post_author title]\nload author limit all\n  columns [id name]\n"},
		},
		{
			name:  "a custom field keeps SELECT *",
			query: `{ user { name avatarURL company { name } } }`,
			want:  plans{user: "columns []\nload company limit all\n  columns [id name]\n"},
		},
		{
			name:  "a page without totalCount does not count",
			query: `query($n: Int) { posts(first: $n) { edges { node { title author { name } } } pageInfo { hasNextPage } } }`,
			vars:  map[string]any{"n": 10},
			want:  plans{posts: "columns [id post_author title]\nload author limit all\n  columns [id name]\n"},
		},
		{
			name:  "a count badge reads keys only",
			query: `{ posts { totalCount } }`,
			want:  plans{posts: "columns [id]\n", count: true},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := viaGraphQLGo(t, c.query, c.vars)
			if got != c.want {
				t.Errorf("graphql-go planned\n%+v\nwant\n%+v", got, c.want)
			}
			if ref := viaGqlgen(t, c.query, c.vars); got != ref {
				t.Errorf("engines disagree:\ngraphql-go %+v\ngqlgen     %+v", got, ref)
			}
		})
	}
}

// Without Collect the collector sees no selection: correct answers, nothing
// planned, and every page counts.
func TestWithoutCollectNothingIsPlanned(t *testing.T) {
	s, err := graphql.NewSchema(graphql.SDL(`type Query { n: Int! }`),
		graphql.Object[graphql.Root]("Query", graphql.Resolve("n", func(ctx context.Context, _ graphql.Root) (int, error) {
			q := newRecQuery(userMeta())
			if err := gqlrelay.CollectFields(ctx, q, q.meta); err != nil {
				return 0, err
			}
			if len(q.ctx.Fields) != 0 || !gqlrelay.TotalCountSelected(ctx) {
				return 0, fmt.Errorf("planned %v without a source", q.ctx.Fields)
			}
			return 1, nil
		})))
	if err != nil {
		t.Fatal(err)
	}
	if resp := graphql.NewExecutor(s).Execute(context.Background(), &graphql.Request{Query: `{ n }`}); len(resp.Errors) > 0 {
		t.Fatal(resp.Errors)
	}
}

func TestNodeSelects(t *testing.T) {
	var got []bool
	s := newSchema(t, func(context.Context) {}, func(ctx context.Context) {
		got = append(got, graphqlgo.NodeSelects(ctx, "body"))
	}, func(context.Context) {})
	exec := graphql.NewExecutor(s)
	for _, q := range []string{
		`{ posts { edges { node { title } } } }`,
		`{ posts { a: edges { node { title } } b: edges { node { body } } } }`,
		`{ posts { totalCount } }`,
	} {
		execute(t, exec, q, nil)
	}
	if want := []bool{false, true, false}; !slices.Equal(got, want) {
		t.Errorf("NodeSelects(body) = %v, want %v", got, want)
	}
}
