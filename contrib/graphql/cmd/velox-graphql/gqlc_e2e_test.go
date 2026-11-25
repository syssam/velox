package main

import (
	"fmt"
	goast "go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/syssam/velox/contrib/graphql/internal/gogen"
	"github.com/syssam/velox/contrib/graphql/internal/modeltest"
	"github.com/syssam/velox/contrib/graphql/internal/sdl"
	"github.com/syssam/velox/internal/tempmod"
)

// gqlcSchema is a schema whose GraphQL surface engine=graphql-go writes in
// full: every scalar velox binds, a JSON field, which gqlc puts on the
// Resolver, an enum, an Int field of a Go type
// graphql-go does not bind to Int itself (int8), an optional list, unique,
// connection and many-to-many edges, a multi-field order and mutation
// inputs whose fields' Go names graphql-go would derive otherwise
// (authorID, tagIDs).
const gqlcSchema = `package schema

import (
	"github.com/google/uuid"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type User struct{ velox.Schema }

func (User) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").Annotations(graphql.OrderField("NAME"), graphql.WhereOps()),
		field.Enum("role").Values("admin", "member").Default("member").Annotations(graphql.WhereOps()),
		field.Time("joined_at").Optional().Nillable(),
		field.Uint64("quota").Optional().Default(0),
		field.UUID("ref", uuid.UUID{}).Optional().Nillable(),
		field.Bytes("blob").Optional().Nillable(),
		field.Float("score").Optional().Default(0),
		field.Bool("active").Default(true),
		field.JSON("prefs", map[string]any{}),
	}
}

func (User) Edges() []velox.Edge {
	return []velox.Edge{edge.To("posts", Post.Type).Annotations(graphql.WhereEdge()), edge.From("liked", Post.Type).Ref("fans")}
}

func (User) Annotations() []velox.Annotation {
	return []velox.Annotation{graphql.QueryField("users"), graphql.RelayConnection()}
}

type Post struct{ velox.Schema }

func (Post) Fields() []velox.Field {
	return []velox.Field{
		field.Int64("author_id").Annotations(graphql.WhereOps(graphql.OpsEquality)),
		field.String("title").Annotations(graphql.OrderField("TITLE"), graphql.WhereOps()),
		field.Enum("status").Values("draft", "published").Default("draft").Annotations(graphql.WhereOps()),
		field.Int64("views").Optional().Default(0).Annotations(graphql.OrderField("VIEWS")),
		field.Strings("keywords").Optional().Default([]string{}).Annotations(graphql.WhereOps(graphql.OpsJSONArray)),
	}
}

func (Post) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("author", User.Type).Ref("posts").Unique().Required().Field("author_id"),
		edge.To("tags", Tag.Type).Annotations(graphql.WhereEdge()),
		edge.To("fans", User.Type).Through("likes", Like.Type),
	}
}

func (Post) Annotations() []velox.Annotation {
	return []velox.Annotation{graphql.QueryField("posts"), graphql.RelayConnection(), graphql.MultiOrder(), graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate)}
}

type Tag struct{ velox.Schema }

func (Tag) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").Annotations(graphql.OrderField("NAME"), graphql.WhereOps()),
		field.Int8("rank").Annotations(graphql.WhereOps()),
	}
}

func (Tag) Edges() []velox.Edge {
	return []velox.Edge{edge.From("posts", Post.Type).Ref("tags")}
}

func (Tag) Annotations() []velox.Annotation {
	return []velox.Annotation{graphql.Type("Label"), graphql.QueryField("labels"), graphql.RelayConnection()}
}

// Like is the edge schema of Post.fans: its note has no default, so the
// post inputs have no field that adds fans.
type Like struct{ velox.Schema }

func (Like) Fields() []velox.Field {
	return []velox.Field{field.String("note"), field.Int64("post_id"), field.Int64("user_id")}
}

func (Like) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("post", Post.Type).Unique().Required().Field("post_id"),
		edge.To("user", User.Type).Unique().Required().Field("user_id"),
	}
}
`

// gqlcApp is the application's own SDL: the mutations, which take the
// inputs velox writes.
const gqlcApp = `type Mutation {
  createPost(input: CreatePostInput!): Post!
  updatePost(id: ID!, input: UpdatePostInput!): Post!
}
`

// gqlcConfig is gqlc.yaml with the keys the package documentation lists.
const gqlcConfig = `schema: [gen/*.graphql, graph/*.graphql]
output: graph
package: example.com/app/graph
autoBind: [./gen]
models: { ID: int64 }
modelDirective: { name: goModel, arg: model }
fieldDirective: { name: goField, forceResolverArg: forceResolver }
zeroForNullInputs: true
inputListPointers: true
inlineAccessors: true
groups: { trimPrefix: velox_ }
`

// gqlcServe is a test of the application's module: the schema gqlc
// generated, bound to velox's code and a resolver of the root fields, each
// one line, serves a query and a mutation on SQLite.
const gqlcServe = `package app_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"

	"example.com/app/gen"
	"example.com/app/graph"
	"example.com/app/graph/app"
	vg "example.com/app/graph/velox"
	"github.com/syssam/graphql-go"
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/graphqlgo"
)

type root struct{ db *velox.DB }

var (
	_ vg.Resolver  = root{}
	_ app.Resolver = root{}
)

func (r root) Posts(ctx context.Context, a vg.PostsArgs) (*gen.PostConnection, error) {
	return r.db.Query[gen.Post]().Paginate(ctx, a.After, a.First, a.Before, a.Last, gen.WithPostOrder(a.OrderBy), gen.WithPostFilter(a.Where))
}

func (r root) Labels(ctx context.Context, a vg.LabelsArgs) (*gen.LabelConnection, error) {
	return r.db.Query[gen.Tag]().Paginate(ctx, a.After, a.First, a.Before, a.Last, gen.WithLabelOrder(a.OrderBy), gen.WithLabelFilter(a.Where))
}

func (r root) Users(ctx context.Context, a vg.UsersArgs) (*gen.UserConnection, error) {
	return r.db.Query[gen.User]().Paginate(ctx, a.After, a.First, a.Before, a.Last, gen.WithUserOrder(a.OrderBy), gen.WithUserFilter(a.Where))
}

// UserPrefs is the one field gqlc leaves to the application: a JSON field,
// read through its getter.
func (root) UserPrefs(_ context.Context, obj *gen.User) (veloxgql.JSON, error) { return obj.PrefsGQL() }

func (r root) CreatePost(ctx context.Context, a app.CreatePostArgs) (*gen.Post, error) {
	x := &gen.Post{}
	return x, r.db.Insert(x).SetInput(a.Input).Exec(ctx)
}

func (r root) UpdatePost(ctx context.Context, a app.UpdatePostArgs) (*gen.Post, error) {
	return r.db.Update[gen.Post]().Where(gen.Posts.ID.EQ(a.ID)).SetInput(a.Input).One(ctx)
}

const ddl = ` + "`" + `
CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, role TEXT NOT NULL, joined_at DATETIME,
	quota INTEGER NOT NULL, ref TEXT, blob BLOB, score REAL NOT NULL, active BOOLEAN NOT NULL, prefs JSON NOT NULL);
CREATE TABLE posts (id INTEGER PRIMARY KEY, author_id INTEGER NOT NULL, title TEXT NOT NULL, status TEXT NOT NULL,
	views INTEGER NOT NULL, keywords JSON NOT NULL);
CREATE TABLE tags (id INTEGER PRIMARY KEY, name TEXT NOT NULL, rank INTEGER NOT NULL);
CREATE TABLE post_tags (post_id INTEGER NOT NULL, tag_id INTEGER NOT NULL, PRIMARY KEY (post_id, tag_id));
INSERT INTO users VALUES (1, 'ann', 'admin', NULL, 7, NULL, NULL, 1.5, 1, '{"theme":"dark"}'), (2, 'bob', 'member', NULL, 0, NULL, NULL, 0, 0, '{}');
INSERT INTO posts VALUES (1, 1, 'a', 'published', 3, '["go"]'), (2, 1, 'b', 'draft', 0, '[]'), (3, 2, 'c', 'draft', 1, '[]'), (4, 1, 'd', 'draft', 0, '[]');
INSERT INTO tags VALUES (1, 'x', 1), (2, 'y', -2);
INSERT INTO post_tags VALUES (1, 1), (1, 2), (2, 2), (4, 1);
` + "`" + `

// TestServe runs queries and mutations. A NULL blob is null, as under
// gqlgen, and the JSON field is its resolver's.
func TestServe(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	db := velox.Open(velox.SQLite, sqlDB)
	s, err := graph.NewSchema(vg.Bindings(root{db}), app.Bindings(root{db}), graphqlgo.Scalars())
	if err != nil {
		t.Fatal(err)
	}
	exec := graphql.NewExecutor(s, graphql.WithMaxDepth(12))
	for _, c := range []struct{ query, want string }{
		{
			` + "`" + `{ posts(first: 2, orderBy: [{field: TITLE, direction: DESC}], where: {authorID: 1, hasTags: true, keywordsIsEmpty: false}) {
				totalCount pageInfo { hasNextPage }
				edges { node { id title status views keywords
					author { id name role quota ref blob score active joinedAt prefs }
					tags(first: 5, orderBy: {field: NAME}, where: {rankGTE: -2}) { totalCount edges { node { id name rank } } } } } } }` + "`" + `,
			` + "`" + `{"data":{"posts":{"totalCount":1,"pageInfo":{"hasNextPage":false},"edges":[{"node":{"id":"1","title":"a","status":"PUBLISHED","views":3,"keywords":["go"],"author":{"id":"1","name":"ann","role":"ADMIN","quota":"7","ref":null,"blob":null,"score":1.5,"active":true,"joinedAt":null,"prefs":{"theme":"dark"}},"tags":{"totalCount":2,"edges":[{"node":{"id":"1","name":"x","rank":1}},{"node":{"id":"2","name":"y","rank":-2}}]}}}]}}}` + "`" + `,
		},
		{
			` + "`" + `{ labels(where: {rank: 1}) { edges { node { name posts(orderBy: [{field: VIEWS}]) { edges { node { id author { name } } } } } } } }` + "`" + `,
			` + "`" + `{"data":{"labels":{"edges":[{"node":{"name":"x","posts":{"edges":[{"node":{"id":"4","author":{"name":"ann"}}},{"node":{"id":"1","author":{"name":"ann"}}}]}}}]}}}` + "`" + `,
		},
		{
			` + "`" + `mutation { createPost(input: {title: "e", authorID: 2, keywords: ["q", "r"], tagIDs: [2]}) { id title keywords author { name } tags { totalCount } } }` + "`" + `,
			` + "`" + `{"data":{"createPost":{"id":"5","title":"e","keywords":["q","r"],"author":{"name":"bob"},"tags":{"totalCount":1}}}}` + "`" + `,
		},
		{
			` + "`" + `mutation { updatePost(id: 5, input: {status: PUBLISHED, keywords: [], addTagIDs: [1]}) { status keywords tags { totalCount } } }` + "`" + `,
			` + "`" + `{"data":{"updatePost":{"status":"PUBLISHED","keywords":[],"tags":{"totalCount":2}}}}` + "`" + `,
		},
	} {
		resp := exec.Execute(context.Background(), &graphql.Request{Query: c.query})
		got, err := json.Marshal(resp)
		resp.Release()
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.query, got, c.want)
		}
	}
}
`

// TestGqlcBinds is engine=graphql-go end to end: velox-gen writes the SDL
// and the Go, gqlc of the graphql-go version this module requires (the one
// nextapp pins) binds them with the keys the package documentation lists,
// and its Resolvers hold the root fields and the JSON field alone: every
// other field is bound to velox's struct fields and methods, every input
// and enum to velox's Go types, and no model is generated. Like is an edge
// schema and binds like any type; UpdatePostInput, which has no field that
// adds fans, binds too. The application's module then builds and serves a
// query and mutations on SQLite, and graphql-go binds
// every input of the test model's Go, under the defaults and the old
// velox's options, to its SDL by name
// (TestInputNames).
//
// It builds three tools and a module, offline, from the module cache, as
// the other end-to-end tests do, and is left out under -short.
func TestGqlcBinds(t *testing.T) {
	t.Parallel() // its own module, in its own directory
	if testing.Short() {
		t.Skip("builds a module and gqlc")
	}
	dir, run := app(t, map[string]string{"schema/schema.go": gqlcSchema, "graph/app.graphql": gqlcApp, "gqlc.yaml": gqlcConfig, "serve_test.go": gqlcServe,
		"names_test.go": inputNamesTest(t)})
	withGqlc(t, dir, run)

	if out, err := run("tool", "velox-gen", "-out", "gen", "-plugin", "velox-graphql:engine=graphql-go", "./schema"); err != nil {
		t.Fatalf("velox-gen: %v\n%s", err, out)
	}
	if out, err := run("tool", "gqlc", "-config", "gqlc.yaml"); err != nil {
		t.Fatalf("gqlc: %v\n%s", err, out)
	}
	sdlb, err := os.ReadFile(filepath.Join(dir, "gen", "velox.graphql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"type Like ", "likes(", "removeFanIDs: [ID!]"} {
		if !strings.Contains(string(sdlb), want) {
			t.Errorf("the SDL has no %q", want)
		}
	}
	if strings.Contains(string(sdlb), "addFanIDs") || strings.Contains(string(sdlb), " fanIDs") {
		t.Error("the SDL has an input that adds fans")
	}
	for group, want := range map[string][]string{"velox": {"Labels", "Posts", "UserPrefs", "Users"}, "app": {"CreatePost", "UpdatePost"}} {
		b, err := os.ReadFile(filepath.Join(dir, "graph", group, "generated.go"))
		if err != nil {
			t.Fatal(err)
		}
		if got := resolverMethods(string(b)); !slices.Equal(got, want) {
			t.Errorf("the %s group's Resolver has %v, want %v alone:\n%s", group, got, want, b)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "graph", "model")); !os.IsNotExist(err) {
		t.Errorf("gqlc generated models (%v): a type is not bound to velox's Go", err)
	}
	if out, err := run("tool", "gqlc", "-config", "gqlc.yaml", "-check"); err != nil {
		t.Errorf("gqlc -check: %v\n%s", err, out)
	}
	if out, err := run("test", "-count=1", "-run", "^(TestServe|TestInputNames)$", "."); err != nil {
		t.Fatalf("serve: %v\n%s", err, out)
	}
}

// TestGraphQLGoRefusesThroughVeloxGen: velox-gen with engine=graphql-go
// refuses a schema with a field graphql.FieldName renames, naming the
// schema, the field and the reason, and writes nothing.
func TestGraphQLGoRefusesThroughVeloxGen(t *testing.T) {
	t.Parallel() // its own module, in its own directory
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir, run := app(t, map[string]string{"schema/schema.go": e2eSchema})
	out, err := run("tool", "velox-gen", "-out", "gen", "-plugin", "velox-graphql:engine=graphql-go", "./schema")
	if err == nil || !strings.Contains(out, `schema User, field name: engine=graphql-go: graphql.FieldName("displayName") binds it to its Go field Name through @goField(name:), and gqlc does not yet honour @goField(name:)`) {
		t.Fatalf("velox-gen: %v, want the refusal:\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "gen")); !os.IsNotExist(err) {
		t.Errorf("velox-gen wrote gen (%v)", err)
	}
}

// resolverMethods are the names of the methods of the Resolver interface
// in a group's generated.go, in order.
func resolverMethods(src string) []string {
	_, body, _ := strings.Cut(src, "type Resolver interface {\n")
	body, _, _ = strings.Cut(body, "\n}\n")
	var out []string
	for _, m := range regexp.MustCompile(`(?m)^\t([A-Z]\w*)\(`).FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	slices.Sort(out)
	return out
}

// withGqlc makes gqlc a tool of the module in dir, at the version of
// graphql-go contrib/graphql requires, with graphql-go's go.sum, which holds
// gqlc's own dependencies.
func withGqlc(t *testing.T, dir string, run func(args ...string) (string, error)) {
	t.Helper()
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Version}} {{.Dir}}", "github.com/syssam/graphql-go")
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off", "GOTOOLCHAIN=local", "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -m: %v\n%s%s", err, out, tempmod.Hint(string(out)))
	}
	version, modDir, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
	sum, err := os.ReadFile(filepath.Join(modDir, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	for name, add := range map[string]string{
		"go.mod": "\ntool github.com/syssam/graphql-go/cmd/gqlc\n\nrequire github.com/syssam/graphql-go " + version + "\n",
		"go.sum": string(sum),
	} {
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(add); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := run("mod", "tidy", "-e"); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
}

// inputNamesTest is a test of the application's module in which graphql-go
// binds each input struct velox-graphql writes for the test model under
// engine=graphql-go, under the defaults and the old velox's options, to an
// input of the field names velox.graphql gives it: each Go field, with its name and its graphql tag,
// must bind to one of them, so that graphql-go's own derivation of a GraphQL
// name, and not gogen's copy of it, is checked on every input field. The
// fields' types are String: the names are what is checked.
func inputNamesTest(t *testing.T) string {
	t.Helper()
	var cases []string
	old := sdl.Config{
		Names: "schema", Filters: "lenient", Node: "on", IDOps: "equality", OrderFields: "all", QueryFields: "all",
		Optional: "nullable", RequiredEdges: "nonnull", InputKeys: "column", JSONTypes: "named",
	}
	for _, wide := range []bool{false, true} {
		cfg := sdl.Config{}
		if wide {
			cfg = old
		}
		cfg.GraphQLGo, cfg.Package = true, "example.com/app/gen"
		m := modeltest.GraphQLGo(t)
		schema, err := sdl.RenderConfig(m, cfg)
		if err != nil {
			t.Fatal(err)
		}
		doc, perr := parser.ParseSchema(&ast.Source{Input: schema})
		if perr != nil {
			t.Fatal(perr)
		}
		files, err := gogen.RenderConfig(m, cfg.Package, "gen", cfg)
		if err != nil {
			t.Fatal(err)
		}
		structs := map[string][]*goast.Field{}
		for _, f := range files {
			gf, err := goparser.ParseFile(token.NewFileSet(), f.Path, f.Content, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range gf.Decls {
				if g, ok := d.(*goast.GenDecl); ok {
					for _, sp := range g.Specs {
						if ts, ok := sp.(*goast.TypeSpec); ok {
							if st, ok := ts.Type.(*goast.StructType); ok {
								structs[ts.Name.Name] = st.Fields.List
							}
						}
					}
				}
			}
		}
		for _, def := range doc.Definitions {
			if def.Kind != ast.InputObject {
				continue
			}
			fields, ok := structs[def.Name]
			if !ok || len(fields) != len(def.Fields) {
				t.Fatalf("wide %v: input %s has %d fields, and its Go %d (found %v)", wide, def.Name, len(def.Fields), len(fields), ok)
			}
			var sdlFields, goFields []string
			for _, f := range def.Fields {
				sdlFields = append(sdlFields, f.Name+": String")
			}
			for _, f := range fields {
				tag := ""
				if f.Tag != nil {
					tag = " " + f.Tag.Value
				}
				goFields = append(goFields, f.Names[0].Name+" *string"+tag)
			}
			cases = append(cases, fmt.Sprintf("{%q, graphql.Validate(graphql.SDL(%q), graphql.Input[struct{ %s }](\"T\"), q)},",
				fmt.Sprintf("wide %v: %s", wide, def.Name),
				"input T { "+strings.Join(sdlFields, " ")+" }\ntype Query { x: String }",
				strings.Join(goFields, "; ")))
		}
	}
	return `package app_test

import (
	"context"
	"testing"

	"github.com/syssam/graphql-go"
)

func TestInputNames(t *testing.T) {
	q := graphql.Query(graphql.Resolve("x", func(context.Context, graphql.Root) (*string, error) { return nil, nil }))
	for _, c := range []struct {
		input string
		err   error
	}{
		` + strings.Join(cases, "\n\t\t") + `
	} {
		if c.err != nil {
			t.Errorf("%s: %v", c.input, c.err)
		}
	}
}
`
}
