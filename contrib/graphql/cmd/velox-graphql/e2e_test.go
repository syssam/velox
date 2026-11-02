package main

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql/internal/sdl"
	"github.com/syssam/velox/internal/tempmod"
)

const e2eSchema = `package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type User struct{ schema.Schema }

func (User) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Immutable().Annotations(graphql.Skip()),
		field.String("name").Comment("The display name.").Annotations(graphql.FieldName("displayName"), graphql.OrderField("NAME"), graphql.WhereOps()),
		field.Enum("role").Values("admin", "in_review").Default("admin").Annotations(graphql.EnumValues(map[string]string{"in_review": "REVIEWING"})),
		field.String("password_hash").Sensitive(),
		field.Uint64("quota"),
		field.Time("seen_at").Optional().Nillable(),
	}
}

func (User) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("posts", Post.Type).Annotations(graphql.WhereEdge()),
		edge.To("groups", Group.Type).Annotations(graphql.Connection(false), graphql.WhereEdge()),
	}
}

func (User) Annotations() []schema.Annotation {
	return []schema.Annotation{
		schema.Comment("A person who writes posts."),
		graphql.Type("Account"),
		graphql.QueryField("accounts"),
	}
}

type Post struct{ schema.Schema }

func (Post) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("author_id").Annotations(graphql.WhereOps()),
		field.String("title").Annotations(graphql.OrderField("TITLE"), graphql.WhereOps()),
		field.String("draft").Annotations(graphql.Skip()),
	}
}

func (Post) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("author", User.Type).Ref("posts").Unique().Required().Field("author_id"),
	}
}

func (Post) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField("posts"),
		graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate, graphql.MutationDelete),
	}
}

type Group struct{ schema.Schema }

func (Group) Fields() []schema.Field { return []schema.Field{field.String("name")} }

// peers is many-to-many with no inverse edge (an edge to its own schema
// without edge.From): the edge method reads the join table through the
// relation.
func (Group) Edges() []schema.Edge {
	return []schema.Edge{edge.From("users", User.Type).Ref("groups"), edge.To("peers", Group.Type)}
}

// Category's self-edges: FieldOn names Category for each, so only OwnsKey
// says whose row holds the key. Immutable keys make it show: a key on the
// row is set on create only, a key on other rows gets no input at all.
type Category struct{ schema.Schema }

// mentor_id is the mentee/mentor pair's key, on the mentee's row: mentor,
// declared second, owns it, so its where predicates are mentorID….
func (Category) Fields() []schema.Field {
	return []schema.Field{
		field.String("title"),
		field.Int64("mentor_id").Optional().Nillable().Immutable().Annotations(graphql.WhereOps(graphql.OpsEquality)),
	}
}

func (Category) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("children", Category.Type),
		edge.From("parent", Category.Type).Ref("children").Unique().Immutable(),
		edge.To("mentee", Category.Type).Unique(),
		edge.From("mentor", Category.Type).Ref("mentee").Unique().Immutable().Field("mentor_id"),
		edge.To("twin", Category.Type).Unique().Immutable().Annotations(graphql.Directives(graphql.Directive{Name: "deprecated", Args: map[string]any{"reason": "Use mentor."}})),
	}
}

func (Category) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate)}
}

type Secret struct{ schema.Schema }

func (Secret) Fields() []schema.Field { return []schema.Field{field.String("value")} }

func (Secret) Annotations() []schema.Annotation { return []schema.Annotation{graphql.Skip()} }
`

// e2eScope is the hand-written file of the gen package that confines the
// users to the org the context names, as examples/tenant does.
const e2eScope = `package gen

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/scope"
)

var Org = scope.NewKey[int64]("org")

var userScope = scope.By(Org, Users.OrgID.EQ, func(x *User) *int64 { return &x.OrgID })

func (*User) Policy() velox.Policy[User] { return userScope.Policy() }

func (*User) Hooks() []velox.Hook[User] { return []velox.Hook[User]{userScope.Fill()} }
`

// app writes a module with velox-gen and velox-graphql as tools, and the
// files (the schema package among them), and returns its directory and a
// runner of go in it, offline.
//
// withGqlgen also makes gqlgen a tool of the module and gives it the go.sum of
// example, which holds gqlgen's own dependencies.
func app(t *testing.T, files map[string]string, withGqlgen ...bool) (string, func(args ...string) (string, error)) {
	t.Helper()
	gql := len(withGqlgen) > 0 && withGqlgen[0]
	repo, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var sum []byte
	sums := []string{"go.sum", "contrib/graphql/go.sum"}
	tools := "\tgithub.com/syssam/velox/cmd/velox-gen\n" +
		"\tgithub.com/syssam/velox/contrib/graphql/cmd/velox-graphql\n"
	pins := ""
	if gql {
		sums = append(sums, "examples/go.sum")
		tools += "\tgithub.com/99designs/gqlgen\n"
		pins = gqlgenPins(t, repo)
	}
	for _, p := range sums {
		b, err := os.ReadFile(filepath.Join(repo, p))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		sum = append(sum, b...)
	}
	mod := "module example.com/app\n\ngo 1.27.0\n\n" +
		"tool (\n" + tools + ")\n\n" +
		"require (\n" +
		"\tgithub.com/syssam/velox v0.0.0\n" +
		"\tgithub.com/syssam/velox/contrib/graphql v0.0.0\n" +
		")\n\n" + pins +
		"replace (\n" +
		"\tgithub.com/syssam/velox => " + repo + "\n" +
		"\tgithub.com/syssam/velox/contrib/graphql => " + filepath.Join(repo, "contrib", "graphql") + "\n" +
		")\n"
	all := map[string]string{"go.mod": mod, "go.sum": string(sum)}
	maps.Copy(all, files)
	for name, src := range all {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) (string, error) {
		t.Helper()
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off", "GOTOOLCHAIN=local", "GOWORK=off")
		out, err := cmd.CombinedOutput()
		return string(out) + tempmod.Hint(string(out)), err
	}
	// -e: tidy also looks for the test dependencies of gqlparser's
	// levenshtein, whose go.mod predates module graph pruning; offline, with a
	// module cache that holds only what builds need (as in CI), it cannot find
	// them. Nothing here builds them, and a dependency a build needs that is
	// missing fails the next step.
	if out, err := run("mod", "tidy", "-e"); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
	return dir, run
}

// gqlgenPins are requirements that pin golang.org/x/tools and golang.org/x/mod,
// which gqlgen's tool packages import, to the versions contrib/graphql's own
// module graph selects, as a require block. Left alone, the module would ask
// for the older ones gqlgen's go.mod names, whose sources the module cache
// holds only if something else fetched them, and an offline build cannot.
func gqlgenPins(t *testing.T, repo string) string {
	t.Helper()
	cmd := exec.Command("go", "list", "-m", "-f", "\t{{.Path}} {{.Version}}", "golang.org/x/tools", "golang.org/x/mod")
	cmd.Dir = filepath.Join(repo, "contrib", "graphql")
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off", "GOTOOLCHAIN=local", "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -m: %v\n%s%s", err, out, tempmod.Hint(string(out)))
	}
	return "require (\n" + string(out) + ")\n\n"
}

// TestPluginThroughVeloxGen runs the plugin the way an application does: a
// module with velox-gen and velox-graphql as tools, and a schema package with
// annotations.
func TestPluginThroughVeloxGen(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir, run := app(t, map[string]string{"schema/schema.go": e2eSchema})

	if out, err := run("tool", "velox-gen", "-out", "gen", "-plugin", "velox-graphql", "./schema"); err != nil || !strings.Contains(out, "gen/velox.graphqls\n") {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(dir, "gen", "velox.graphqls"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	const header = "# Code generated by velox-gen. DO NOT EDIT.\n\n"
	if !strings.HasPrefix(got, header) || strings.Count(got, "DO NOT EDIT") != 1 {
		t.Errorf("velox.graphqls does not start with the one header:\n%s", got)
	}
	if _, err := sdl.Check(got); err != nil {
		t.Errorf("velox.graphqls does not load: %v\n%s", err, got)
	}
	for _, want := range []string{
		"\"\"\"A person who writes posts.\"\"\"\ntype Account implements Node {\n",
		"  displayName: String! @goField(name: \"Name\")\n",
		"  id: ID! @goField(name: \"IDGQL\")\n",
		"  role: UserRole!\n",
		"  quota: Uint64!\n",
		"  seenAt: Time\n",
		"  posts(after: Cursor, first: Int, before: Cursor, last: Int, orderBy: PostOrder, where: PostWhereInput): PostConnection! @goField(name: \"PostsGQL\")\n",
		"  groups: [Group!]! @goField(name: \"GroupsGQL\")\n",
		"  author: Account @goField(name: \"AuthorGQL\")\n",
		"enum UserRole {\n  ADMIN\n  REVIEWING\n}\n",
		"  accounts(after: Cursor, first: Int, before: Cursor, last: Int, orderBy: AccountOrder, where: AccountWhereInput): AccountConnection!\n",
		"  posts(after: Cursor, first: Int, before: Cursor, last: Int, orderBy: PostOrder, where: PostWhereInput): PostConnection!\n",
		"input AccountOrder {\n",
		"  direction: OrderDirection! = ASC\n  \"\"\"The field to order by.\"\"\"\n  field: AccountOrderField!\n",
		"enum AccountOrderField {\n  NAME\n}\n",
		"enum PostOrderField {\n  TITLE\n}\n",
		"  displayNameHasSuffix: String\n  hasPosts: Boolean\n  hasPostsWith: [PostWhereInput!]\n  hasGroups: Boolean\n}\n",
		"  or: [PostWhereInput!]\n  id: ID\n  idNEQ: ID\n  idIn: [ID!]\n  idNotIn: [ID!]\n  idGT: ID\n  idGTE: ID\n  idLT: ID\n  idLTE: ID\n  authorID: ID\n  authorIDNEQ: ID\n  authorIDIn: [ID!]\n  authorIDNotIn: [ID!]\n  title: String\n",
		"type Mutation {\n",
		"  createPost(input: CreatePostInput!): Post!\n",
		"  updatePost(id: ID!, input: UpdatePostInput!): Post!\n",
		"  deletePost(id: ID!): ID!\n",
		"input CreatePostInput {\n  title: String!\n  authorID: ID!\n}\n",
		"input UpdatePostInput {\n  title: String\n  authorID: ID\n}\n",
		// Category's self-edges, read through OwnsKey: parent, mentor and the
		// bidirectional twin hold their immutable keys on the row, so they are
		// set on create only; children and mentee would re-point other rows'
		// immutable keys, so they have no input. An edge's directives reach
		// its inputs.
		"input CreateCategoryInput {\n  title: String!\n  parentID: ID\n  mentorID: ID\n  twinID: ID @deprecated(reason: \"Use mentor.\")\n}\n",
		"input UpdateCategoryInput {\n  title: String\n}\n",
		// The key's where predicates are named after the edge that owns it.
		"  or: [CategoryWhereInput!]\n  id: ID\n  idNEQ: ID\n  idIn: [ID!]\n  idNotIn: [ID!]\n  idGT: ID\n  idGTE: ID\n  idLT: ID\n  idLTE: ID\n  mentorID: ID\n  mentorIDNEQ: ID\n  mentorIDIn: [ID!]\n  mentorIDNotIn: [ID!]\n}\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
	}
	for _, absent := range []string{"orgID", "passwordHash", "authorID: Int", "menteeID", "mentorID: Int", "draft", "Secret", "UserGroup", "type User", "AccountRole"} {
		if strings.Contains(got, absent) {
			t.Errorf("%q is in\n%s", absent, got)
		}
	}

	if out, err := run("tool", "velox-gen", "-check", "-out", "gen", "-plugin", "velox-graphql", "./schema"); err != nil || out != "" {
		t.Errorf("-check: %v\n%s", err, out)
	}
}

// e2eEvent adds what e2eSchema has not to the Go the plugin writes: a UUID
// key, an enum of the application's own Go type, a String enum, a where
// predicate on each scalar veloxgql binds, and createEvent, whose input
// converts to the application's own Go types.
const e2eEvent = `package schema

import (
	"github.com/google/uuid"

	"example.com/app/ids"
	"example.com/app/levels"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type Event struct{ schema.Schema }

func (Event) Fields() []schema.Field {
	return []schema.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Annotations(graphql.WhereOps()),
		field.Enum("kind").Values("start", "in_flight").Annotations(graphql.WhereOps()),
		field.Enum("level").GoType(levels.Level("")).Annotations(graphql.WhereOps()),
		field.Enum("state").Values("on", "off").Default("on").Annotations(graphql.Skip(graphql.SkipEnumField), graphql.WhereOps()),
		field.Int8("rank").Annotations(graphql.WhereOps(graphql.OpsAll), graphql.OrderField("RANK")),
		field.Uint64("size").Annotations(graphql.WhereOps()),
		field.Time("at").Optional().Nillable().Annotations(graphql.WhereOps()),
		field.Bool("done").Annotations(graphql.WhereOps()),
		field.Float32("score").Annotations(graphql.WhereOps()),
		field.UUID("ref", uuid.UUID{}).Annotations(graphql.WhereOps()),
		field.String("name").Annotations(graphql.WhereOps(graphql.OpsAll)),
		field.String("code").GoType(ids.Code("")).Optional().Nillable().Annotations(graphql.WhereOps(graphql.OpsAll)),
		field.Int64("owner_id").GoType(ids.UserID(0)).Optional().Nillable().Annotations(graphql.WhereOps()),
	}
}

func (Event) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("owner", Owner.Type).Ref("events").Unique().Field("owner_id").Annotations(graphql.WhereEdge()),
	}
}

func (Event) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.QueryField("events"), graphql.Mutations(graphql.MutationCreate)}
}

// Owner's key is of a type from a package named ids, as Nodes's parameter
// is; its other fields' Go types are the application's own, or JSON, and
// bind through getters.
type Owner struct{ schema.Schema }

func (Owner) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("id").GoType(ids.UserID(0)).Annotations(graphql.WhereOps()),
		field.String("email").GoType(ids.Email("")).Optional().Nillable().Annotations(graphql.WhereOps(graphql.OpEQ)),
		field.String("code").GoType(ids.Code("")),
		field.Strings("tags"),
		field.Bytes("blob").Optional().Nillable(),
		field.Float32("ratio"),
		field.Enum("tier").GoType(levels.Level("")).Annotations(graphql.Skip(graphql.SkipEnumField), graphql.WhereOps()),
		field.Int32("rank").GoType(ids.Rank(0)).Annotations(graphql.WhereOps()),
	}
}

func (Owner) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("events", Event.Type).Annotations(graphql.WhereEdge()),
	}
}

func (Owner) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.QueryField("owners")}
}
`

// e2eIDs is the package of Owner's own Go types.
const e2eIDs = `package ids

type (
	UserID int64
	Email  string
	Code   string
	Rank   int32
)
`

// e2eLevels is the package of Event's own enum type.
const e2eLevels = `package levels

type Level string

func (Level) Values() []string { return []string{"low", "high"} }
`

// TestGoCompiles runs velox-gen with the plugin and builds and vets the
// generated package: the plugin's Go compiles with velox-gen's.
func TestGoCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir, run := app(t, map[string]string{"schema/schema.go": e2eSchema, "schema/event.go": e2eEvent, "levels/levels.go": e2eLevels, "ids/ids.go": e2eIDs})
	out, err := run("tool", "velox-gen", "-out", "gen", "-plugin", "velox-graphql", "./schema")
	if err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	for _, name := range []string{"node_gql.go", "user_gql.go", "post_gql.go", "group_gql.go", "category_gql.go", "event_gql.go", "owner_gql.go"} {
		if !strings.Contains(out, "gen/"+name+"\n") {
			t.Errorf("no gen/%s in\n%s", name, out)
		}
		b, err := os.ReadFile(filepath.Join(dir, "gen", name))
		if err != nil {
			t.Fatal(err)
		}
		if got := string(b); !strings.HasPrefix(got, "// Code generated by velox-gen. DO NOT EDIT.\n\npackage gen\n") || strings.Count(got, "DO NOT EDIT") != 1 {
			t.Errorf("%s does not start with the one header:\n%s", name, got)
		}
	}
	for _, name := range []string{"secret_gql.go", "user_group_gql.go", "group_peer_gql.go"} {
		if _, err := os.Stat(filepath.Join(dir, "gen", name)); !os.IsNotExist(err) {
			t.Errorf("%s: %v, want none: the type is skipped, or a join entity", name, err)
		}
	}
	// The fields whose Go type does not bind to their scalar bind to getters.
	sdlText, err := os.ReadFile(filepath.Join(dir, "gen", "velox.graphqls"))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := os.ReadFile(filepath.Join(dir, "gen", "owner_gql.go"))
	if err != nil {
		t.Fatal(err)
	}
	for field, getter := range map[string]string{"email: String": "EmailGQL() *string", "code: String!": "CodeGQL() string", "tags: JSON!": "TagsGQL() (veloxgql.JSON, error)", "tier: String!": "TierGQL() string"} {
		method, _, _ := strings.Cut(getter, "(")
		if !strings.Contains(string(sdlText), "  "+field+" @goField(name: \""+method+"\")\n") {
			t.Errorf("no %s bound to %s in\n%s", field, method, sdlText)
		}
		if !strings.Contains(string(owner), "func (x *Owner) "+getter+" {") {
			t.Errorf("no getter %s in\n%s", getter, owner)
		}
	}
	for _, own := range []string{"  blob: Bytes\n", "  ratio: Float!\n"} {
		if !strings.Contains(string(sdlText), own) {
			t.Errorf("no %q, bound to the field as it is, in\n%s", own, sdlText)
		}
	}
	if out, err := run("build", "./gen"); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	if out, err := run("vet", "./gen"); err != nil {
		t.Fatalf("go vet: %v\n%s", err, out)
	}
	if out, err := run("tool", "velox-gen", "-check", "-out", "gen", "-plugin", "velox-graphql", "./schema"); err != nil || out != "" {
		t.Errorf("-check: %v\n%s", err, out)
	}
}

// e2eRun is a test of the application's module that runs the generated
// connections and edge methods of e2eSchema on SQLite.
const e2eRun = `package app_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	_ "modernc.org/sqlite"

	"example.com/app/gen"
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

const ddl = ` + "`" + `
CREATE TABLE users (id INTEGER PRIMARY KEY, org_id INTEGER NOT NULL, name TEXT NOT NULL, role TEXT NOT NULL,
	password_hash TEXT NOT NULL, quota INTEGER NOT NULL, seen_at DATETIME);
CREATE TABLE posts (id INTEGER PRIMARY KEY, author_id INTEGER NOT NULL, title TEXT NOT NULL, draft TEXT NOT NULL);
CREATE TABLE "groups" (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE user_groups (user_id INTEGER NOT NULL, group_id INTEGER NOT NULL, PRIMARY KEY (user_id, group_id));
CREATE TABLE categories (id INTEGER PRIMARY KEY, title TEXT NOT NULL, mentor_id INTEGER, category_children INTEGER, category_twin INTEGER);
INSERT INTO users VALUES (1, 7, 'ann', 'admin', '', 0, NULL), (2, 7, 'bob', 'admin', '', 0, NULL), (3, 8, 'cid', 'admin', '', 0, NULL);
CREATE TABLE group_peers (group_id INTEGER NOT NULL, peer_id INTEGER NOT NULL, PRIMARY KEY (group_id, peer_id));
INSERT INTO group_peers VALUES (1, 2), (2, 1), (2, 3);
INSERT INTO posts VALUES (1, 1, 'b', ''), (2, 1, 'a', ''), (3, 2, 'c', ''), (4, 1, 'a', ''), (5, 3, 'z', '');
INSERT INTO "groups" VALUES (1, 'g1'), (2, 'g2'), (3, 'g3');
INSERT INTO user_groups VALUES (1, 1), (1, 2), (2, 2);
INSERT INTO categories VALUES (1, 'root', NULL, NULL, NULL), (2, 'kid', 1, 1, NULL), (3, 'kid2', NULL, 1, NULL);
` + "`" + `

func intp(n int) *int { return &n }

// keys are the keys of a connection's page, and its page info.
func keys[E any](t *testing.T, ctx context.Context, c interface {
	PageInfo(context.Context) (*veloxgql.PageInfo, error)
}, edges func(context.Context) ([]*E, error), key func(*E) int64) ([]int64, *veloxgql.PageInfo) {
	t.Helper()
	es, err := edges(ctx)
	if err != nil {
		t.Fatal(err)
	}
	info, err := c.PageInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var out []int64
	for _, e := range es {
		out = append(out, key(e))
	}
	return out, info
}

func TestConnections(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	var ran atomic.Int64
	db := velox.Open(velox.SQLite, sqlDB, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		ran.Add(1)
		return next(ctx)
	}))
	ctx := gen.Org.With(veloxgql.NewExtension(db).NewContext(context.Background()), int64(7))
	user := func(e *gen.AccountEdge) int64 { return e.Node.ID }
	post := func(e *gen.PostEdge) int64 { return e.Node.ID }

	// The root connection, in the org: ann and bob, not cid.
	byName := &gen.AccountOrder{Direction: veloxgql.OrderDirectionDesc, Field: gen.AccountOrderFieldName}
	us, err := gen.AccountsConnection(ctx, nil, intp(1), nil, nil, byName, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, info := keys(t, ctx, us, us.Edges, user); !slices.Equal(got, []int64{2}) || !info.HasNextPage {
		t.Errorf("users by name DESC, first 1: %v %+v", got, info)
	}
	if n, err := us.TotalCount(ctx); err != nil || n != 2 {
		t.Errorf("users: totalCount %d %v", n, err)
	}
	// The last post, by key.
	ps, err := gen.PostsConnection(ctx, nil, nil, nil, intp(1), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, info := keys(t, ctx, ps, ps.Edges, post); !slices.Equal(got, []int64{5}) || !info.HasPreviousPage {
		t.Errorf("posts, last 1: %v %+v", got, info)
	}
	// A where that sets nothing is no condition.
	if ps, err = gen.PostsConnection(ctx, nil, nil, nil, nil, nil, &gen.PostWhereInput{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := keys(t, ctx, ps, ps.Edges, post); !slices.Equal(got, []int64{1, 2, 3, 4, 5}) {
		t.Errorf("posts, an empty where: %v", got)
	}

	ann, err := db.Query[gen.User]().Where(gen.Users.ID.EQ(1)).One(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := db.Query[gen.User]().Where(gen.Users.ID.EQ(2)).One(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// A connection edge, paged forward by title through a cursor off the wire.
	byTitle := &gen.PostOrder{Direction: veloxgql.OrderDirectionAsc, Field: gen.PostOrderFieldTitle}
	c, err := ann.PostsGQL(ctx, nil, intp(2), nil, nil, byTitle, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, info := keys(t, ctx, c, c.Edges, post)
	if !slices.Equal(got, []int64{2, 4}) || !info.HasNextPage {
		t.Errorf("ann's posts by title, first 2: %v %+v", got, info)
	}
	if n, err := c.TotalCount(ctx); err != nil || n != 3 {
		t.Errorf("ann's posts: totalCount %d %v", n, err)
	}
	var after veloxgql.Cursor
	if s, err := veloxgql.EncodeCursor(*info.EndCursor); err != nil {
		t.Fatal(err)
	} else if err := after.UnmarshalGQLContext(ctx, s); err != nil {
		t.Fatal(err)
	}
	if c, err = ann.PostsGQL(ctx, &after, intp(2), nil, nil, byTitle, nil); err != nil {
		t.Fatal(err)
	}
	if got, info := keys(t, ctx, c, c.Edges, post); !slices.Equal(got, []int64{1}) || info.HasNextPage {
		t.Errorf("ann's posts by title after 4: %v %+v", got, info)
	}
	// The cursor under another order fails.
	if _, err := ann.PostsGQL(ctx, &after, intp(2), nil, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "malformed cursor") {
		t.Errorf("a title cursor under the key's order: %v", err)
	}

	// Unique edges: a key on the owner's row, and none.
	p3, err := db.Query[gen.Post]().Where(gen.Posts.ID.EQ(3)).One(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if a, err := p3.AuthorGQL(ctx); err != nil || a == nil || a.ID != 2 {
		t.Errorf("post 3's author: %+v %v", a, err)
	}
	// Many-to-many, as a list and as a connection, through the join table.
	groupIDs := func(gs []*gen.Group) (out []int64) {
		for _, g := range gs {
			out = append(out, g.ID)
		}
		return out
	}
	if gs, err := ann.GroupsGQL(ctx); err != nil || !slices.Equal(groupIDs(gs), []int64{1, 2}) {
		t.Errorf("ann's groups: %v %v", groupIDs(gs), err)
	}
	if gs, err := bob.GroupsGQL(ctx); err != nil || !slices.Equal(groupIDs(gs), []int64{2}) {
		t.Errorf("bob's groups: %v %v", groupIDs(gs), err)
	}
	// A list edge reads at most MaxList rows, the DB's cap being none: more
	// fail with ErrTooManyRows, and the error names the edge.
	one := veloxgql.NewExtension(db, veloxgql.WithLimits(veloxgql.Limits{MaxList: 1})).NewContext(ctx)
	_, err = ann.GroupsGQL(one)
	if gs, err2 := bob.GroupsGQL(one); err2 != nil || !slices.Equal(groupIDs(gs), []int64{2}) {
		t.Errorf("bob's groups under MaxList 1: %v %v", groupIDs(gs), err2)
	}
	if !errors.Is(err, velox.ErrTooManyRows) || !strings.Contains(err.Error(), "veloxgql: Account.groups: ") {
		t.Errorf("ann's two groups under MaxList 1: %v, want ErrTooManyRows naming Account.groups", err)
	}
	g2, err := db.Query[gen.Group]().Where(gen.Groups.ID.EQ(2)).One(ctx)
	if err != nil {
		t.Fatal(err)
	}
	members, err := g2.UsersGQL(ctx, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := keys(t, ctx, members, members.Edges, user); !slices.Equal(got, []int64{1, 2}) {
		t.Errorf("g2's users: %v", got)
	}
	// Many-to-many with no inverse edge, through the join table.
	peers, err := g2.PeersGQL(ctx, nil, intp(1), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, info := keys(t, ctx, peers, peers.Edges, func(e *gen.GroupEdge) int64 { return e.Node.ID }); !slices.Equal(got, []int64{1}) || !info.HasNextPage {
		t.Errorf("g2's peers, first 1: %v %+v", got, info)
	}
	if n, err := peers.TotalCount(ctx); err != nil || n != 2 {
		t.Errorf("g2's peers: totalCount %d %v", n, err)
	}

	// Category's self-edges, each way round.
	cat := func(id int64) *gen.Category {
		x, err := db.Query[gen.Category]().Where(gen.Categories.ID.EQ(id)).One(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	root, kid := cat(1), cat(2)
	ran.Store(0)
	if p, err := root.ParentGQL(ctx); p != nil || err != nil || ran.Load() != 0 {
		t.Errorf("root's parent: %+v %v, %d statements", p, err, ran.Load())
	}
	if p, err := kid.ParentGQL(ctx); err != nil || p == nil || p.ID != 1 {
		t.Errorf("kid's parent: %+v %v", p, err)
	}
	if m, err := kid.MentorGQL(ctx); err != nil || m == nil || m.ID != 1 {
		t.Errorf("kid's mentor: %+v %v", m, err)
	}
	if m, err := root.MenteeGQL(ctx); err != nil || m == nil || m.ID != 2 {
		t.Errorf("root's mentee: %+v %v", m, err)
	}
	if m, err := kid.MenteeGQL(ctx); err != nil || m != nil {
		t.Errorf("kid's mentee: %+v %v", m, err)
	}
	kids, err := root.ChildrenGQL(ctx, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := keys(t, ctx, kids, kids.Edges, func(e *gen.CategoryEdge) int64 { return e.Node.ID }); !slices.Equal(got, []int64{2, 3}) {
		t.Errorf("root's children: %v", got)
	}

	// A relation a With loaded, filtered: the edge methods read the edge,
	// not the loaded page.
	loaded, err := db.Query[gen.User]().Where(gen.Users.ID.EQ(1)).With(gen.Users.Posts.Where(gen.Posts.Title.EQ("b"))).With(gen.Users.Groups.Where(gen.Groups.ID.EQ(2))).One(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ran.Store(0)
	c, err = loaded.PostsGQL(ctx, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := keys(t, ctx, c, c.Edges, post); !slices.Equal(got, []int64{1, 2, 4}) {
		t.Errorf("ann's loaded posts: %v", got)
	}
	if n, err := c.TotalCount(ctx); err != nil || n != 3 {
		t.Errorf("ann's loaded posts: totalCount %d %v", n, err)
	}
	if gs, err := loaded.GroupsGQL(ctx); err != nil || !slices.Equal(groupIDs(gs), []int64{1, 2}) {
		t.Errorf("ann's loaded groups: %v %v", groupIDs(gs), err)
	}
	if ran.Load() != 3 {
		t.Errorf("the edges of loaded relations ran %d statements, want 3: the page, the count and the list", ran.Load())
	}
}

// TestConnectionOf composes a connection from the application's own query.
func TestConnectionOf(t *testing.T) {
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
	ctx := gen.Org.With(veloxgql.NewExtension(db).NewContext(context.Background()), int64(7))
	post := func(e *gen.PostEdge) int64 { return e.Node.ID }

	c, err := gen.PostsConnectionOf(ctx, db.Query[gen.Post]().Where(gen.Posts.Title.EQ("a")), nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := keys(t, ctx, c, c.Edges, post); !slices.Equal(got, []int64{2, 4}) {
		t.Errorf("posts titled a: %v", got)
	}
	if n, err := c.TotalCount(ctx); err != nil || n != 2 {
		t.Errorf("totalCount %d %v", n, err)
	}
}

// TestConnectionOfLimitOffset: the page is the connection's. A base query's
// Offset is cleared and its Limit replaced, so paging through the whole list
// neither skips nor repeats a row.
func TestConnectionOfLimitOffset(t *testing.T) {
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
	ctx := gen.Org.With(veloxgql.NewExtension(db).NewContext(context.Background()), int64(7))
	post := func(e *gen.PostEdge) int64 { return e.Node.ID }

	var all []int64
	var after *veloxgql.Cursor
	for range 5 {
		c, err := gen.PostsConnectionOf(ctx, db.Query[gen.Post]().Limit(1).Offset(1), after, intp(2), nil, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, info := keys(t, ctx, c, c.Edges, post)
		all = append(all, got...)
		if n, err := c.TotalCount(ctx); err != nil || n != 5 {
			t.Fatalf("totalCount %d %v", n, err)
		}
		if !info.HasNextPage {
			break
		}
		after = new(veloxgql.Cursor)
		if s, err := veloxgql.EncodeCursor(*info.EndCursor); err != nil {
		t.Fatal(err)
	} else if err := after.UnmarshalGQLContext(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(all, []int64{1, 2, 3, 4, 5}) {
		t.Errorf("paged through a base query with a Limit and an Offset: %v", all)
	}
}
`

// e2eWhere is a test of the application's module that runs the generated
// where inputs of e2eSchema and e2eEvent on SQLite, and Nodes.
const e2eWhere = `package app_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"example.com/app/gen"
	"example.com/app/ids"
	"example.com/app/levels"
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

const ddl = ` + "`" + `
CREATE TABLE users (id INTEGER PRIMARY KEY, org_id INTEGER NOT NULL, name TEXT NOT NULL, role TEXT NOT NULL,
	password_hash TEXT NOT NULL, quota INTEGER NOT NULL, seen_at DATETIME);
CREATE TABLE posts (id INTEGER PRIMARY KEY, author_id INTEGER NOT NULL, title TEXT NOT NULL, draft TEXT NOT NULL);
CREATE TABLE "groups" (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE user_groups (user_id INTEGER NOT NULL, group_id INTEGER NOT NULL, PRIMARY KEY (user_id, group_id));
CREATE TABLE categories (id INTEGER PRIMARY KEY, title TEXT NOT NULL, mentor_id INTEGER, category_children INTEGER, category_twin INTEGER);
CREATE TABLE owners (id INTEGER PRIMARY KEY, email TEXT, code TEXT NOT NULL, tags TEXT NOT NULL, blob BLOB, ratio REAL NOT NULL,
	tier TEXT NOT NULL, rank INTEGER NOT NULL);
CREATE TABLE events (id TEXT PRIMARY KEY, kind TEXT NOT NULL, level TEXT NOT NULL, state TEXT NOT NULL, rank INTEGER NOT NULL,
	size INTEGER NOT NULL, at DATETIME, done BOOLEAN NOT NULL, score REAL NOT NULL, ref TEXT NOT NULL, name TEXT NOT NULL,
	code TEXT, owner_id INTEGER);
INSERT INTO users VALUES (1, 7, 'ann', 'admin', '', 0, NULL), (2, 7, 'bob', 'admin', '', 0, NULL), (3, 8, 'cid', 'admin', '', 0, NULL),
	(4, 7, 'dan', 'admin', '', 0, NULL);
INSERT INTO posts VALUES (1, 1, 'xa', ''), (2, 1, 'b', ''), (3, 2, 'c', ''), (4, 3, 'xz', '');
INSERT INTO "groups" VALUES (1, 'g1'), (2, 'g2');
INSERT INTO user_groups VALUES (1, 1), (2, 2);
INSERT INTO owners VALUES (1, 'a@x', 'c1', '["x","y"]', NULL, 0, 'low', 1), (2, NULL, 'c2', '[]', NULL, 0, 'high', 2), (3, 'b@x', 'c3', 'null', NULL, 0, 'low', 3);
INSERT INTO categories VALUES (1, 'root', NULL, NULL, NULL), (2, 'kid', 1, 1, NULL), (3, 'kid2', NULL, 1, NULL), (4, 'kid3', 2, 1, NULL);
` + "`" + `

// statements counts the statements the DB runs.
var statements atomic.Int64

func open(t *testing.T, opts ...velox.Option) (context.Context, *velox.DB) {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	if _, err := sqlDB.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	opts = append(opts, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		statements.Add(1)
		return next(ctx)
	}))
	db := velox.Open(velox.SQLite, sqlDB, opts...)
	ctx := gen.Org.With(veloxgql.NewExtension(db).NewContext(context.Background()), int64(7))
	return ctx, db
}

// The events' times, keys and refs: event n's key ends in n.
var (
	t0     = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	hour   = func(n int) time.Time { return t0.Add(time.Duration(n) * time.Hour) }
	evKey  = func(n byte) uuid.UUID { return uuid.UUID{15: n} }
	r1, r2 = uuid.UUID{0: 1}, uuid.UUID{0: 2}
)

func ptr[T any](v T) *T { return &v }

// seed inserts the events through velox, so that each value is stored as
// velox stores it.
func seed(t *testing.T, ctx context.Context, db *velox.DB) {
	t.Helper()
	events := []gen.Event{
		{ID: evKey(1), Kind: gen.EventKindStart, Level: "low", State: gen.EventStateOn, Rank: 1, Size: 10, At: ptr(hour(1)), Done: true, Score: 0.5, Ref: r1, Name: "alpha", Code: ptr(ids.Code("A1")), OwnerID: ptr(ids.UserID(1))},
		{ID: evKey(2), Kind: gen.EventKindInFlight, Level: "high", State: gen.EventStateOff, Rank: 2, Size: 20, At: ptr(hour(2)), Done: false, Score: 1.5, Ref: r2, Name: "beta", OwnerID: ptr(ids.UserID(1))},
		{ID: evKey(3), Kind: gen.EventKindStart, Level: "high", State: gen.EventStateOn, Rank: 3, Size: 30, Done: true, Score: 2.5, Ref: r1, Name: "gamma", Code: ptr(ids.Code("B2")), OwnerID: ptr(ids.UserID(2))},
		{ID: evKey(4), Kind: gen.EventKindInFlight, Level: "low", State: gen.EventStateOff, Rank: -4, Size: 40, At: ptr(hour(4)), Done: false, Score: 3.5, Ref: r2, Name: "alphabet", Code: ptr(ids.Code("A3"))},
		{ID: evKey(5), Kind: gen.EventKindStart, Level: "low", State: gen.EventStateOn, Rank: 5, Size: 50, At: ptr(hour(5)), Done: true, Score: 4.5, Ref: r1, Name: "delta", Code: ptr(ids.Code("C_%"))},
	}
	for i := range events {
		if err := db.Insert(&events[i]).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

// page is the keys a connection's page holds, through key.
func page[C interface {
	Edges(context.Context) ([]*E, error)
}, E any](ctx context.Context, c C, err error, key func(*E) int64) ([]int64, error) {
	if err != nil {
		return nil, err
	}
	es, err := c.Edges(ctx)
	if err != nil {
		return nil, err
	}
	out := []int64{}
	for _, e := range es {
		out = append(out, key(e))
	}
	return out, nil
}

func events(ctx context.Context, w *gen.EventWhereInput) ([]int64, error) {
	c, err := gen.EventsConnection(ctx, nil, nil, nil, nil, &gen.EventOrder{Field: gen.EventOrderFieldRank}, w)
	ks, err := page(ctx, c, err, func(e *gen.EventEdge) int64 { return int64(e.Node.ID[15]) })
	slices.Sort(ks)
	return ks, err
}

func owners(ctx context.Context, w *gen.OwnerWhereInput) ([]int64, error) {
	c, err := gen.OwnersConnection(ctx, nil, nil, nil, nil, w)
	return page(ctx, c, err, func(e *gen.OwnerEdge) int64 { return int64(e.Node.ID) })
}

func accounts(ctx context.Context, w *gen.AccountWhereInput) ([]int64, error) {
	c, err := gen.AccountsConnection(ctx, nil, nil, nil, nil, nil, w)
	return page(ctx, c, err, func(e *gen.AccountEdge) int64 { return e.Node.ID })
}

func posts(ctx context.Context, w *gen.PostWhereInput) ([]int64, error) {
	c, err := gen.PostsConnection(ctx, nil, nil, nil, nil, nil, w)
	return page(ctx, c, err, func(e *gen.PostEdge) int64 { return e.Node.ID })
}

func categories(ctx context.Context, w *gen.CategoryWhereInput) ([]int64, error) {
	c, err := gen.CategoriesConnection(ctx, nil, nil, nil, nil, w)
	return page(ctx, c, err, func(e *gen.CategoryEdge) int64 { return e.Node.ID })
}

// The global IDs of the rows.
func eventID(n byte) string  { return veloxgql.EncodeID("Event", [16]byte(evKey(n))) }
func ownerID(n int64) string { return veloxgql.EncodeID("Owner", n) }
func userID(n int64) string  { return veloxgql.EncodeID("Account", n) }
func catID(n int64) string   { return veloxgql.EncodeID("Category", n) }

type whereCase struct {
	name string
	run  func(context.Context) ([]int64, error)
	want []int64
}

func ev(name string, w *gen.EventWhereInput, want ...int64) whereCase {
	return whereCase{"Event." + name, func(ctx context.Context) ([]int64, error) { return events(ctx, w) }, want}
}

func ow(name string, w *gen.OwnerWhereInput, want ...int64) whereCase {
	return whereCase{"Owner." + name, func(ctx context.Context) ([]int64, error) { return owners(ctx, w) }, want}
}

func ac(name string, w *gen.AccountWhereInput, want ...int64) whereCase {
	return whereCase{"Account." + name, func(ctx context.Context) ([]int64, error) { return accounts(ctx, w) }, want}
}

func po(name string, w *gen.PostWhereInput, want ...int64) whereCase {
	return whereCase{"Post." + name, func(ctx context.Context) ([]int64, error) { return posts(ctx, w) }, want}
}

func ca(name string, w *gen.CategoryWhereInput, want ...int64) whereCase {
	return whereCase{"Category." + name, func(ctx context.Context) ([]int64, error) { return categories(ctx, w) }, want}
}

func check(t *testing.T, ctx context.Context, cases []whereCase) {
	t.Helper()
	for _, c := range cases {
		got, err := c.run(ctx)
		if c.want == nil {
			c.want = []int64{}
		}
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("%s: %v %v, want %v", c.name, got, err, c.want)
		}
	}
}

// TestWhereOps runs every predicate of every field type the where inputs
// have, through the generated Pred, on rows that make each select a known
// subset.
func TestWhereOps(t *testing.T) {
	ctx, db := open(t)
	seed(t, ctx, db)
	low, high := levels.Level("low"), levels.Level("high")
	check(t, ctx, []whereCase{
		// The key, a UUID.
		ev("id", &gen.EventWhereInput{ID: ptr(eventID(3))}, 3),
		ev("idNEQ", &gen.EventWhereInput{IDNEQ: ptr(eventID(3))}, 1, 2, 4, 5),
		ev("idIn", &gen.EventWhereInput{IDIn: []string{eventID(1), eventID(5)}}, 1, 5),
		ev("idNotIn", &gen.EventWhereInput{IDNotIn: []string{eventID(1), eventID(5)}}, 2, 3, 4),
		// velox-gen's enum.
		ev("kind", &gen.EventWhereInput{Kind: ptr(gen.EventKindStart)}, 1, 3, 5),
		ev("kindNEQ", &gen.EventWhereInput{KindNEQ: ptr(gen.EventKindStart)}, 2, 4),
		ev("kindIn", &gen.EventWhereInput{KindIn: []gen.EventKind{gen.EventKindInFlight}}, 2, 4),
		ev("kindNotIn", &gen.EventWhereInput{KindNotIn: []gen.EventKind{gen.EventKindInFlight}}, 1, 3, 5),
		// The application's enum.
		ev("level", &gen.EventWhereInput{Level: &high}, 2, 3),
		ev("levelNEQ", &gen.EventWhereInput{LevelNEQ: &high}, 1, 4, 5),
		ev("levelIn", &gen.EventWhereInput{LevelIn: []levels.Level{low}}, 1, 4, 5),
		ev("levelNotIn", &gen.EventWhereInput{LevelNotIn: []levels.Level{low}}, 2, 3),
		// velox-gen's enum as a String.
		ev("state", &gen.EventWhereInput{State: ptr(gen.EventStateOff)}, 2, 4),
		ev("stateNEQ", &gen.EventWhereInput{StateNEQ: ptr(gen.EventStateOn)}, 2, 4),
		ev("stateIn", &gen.EventWhereInput{StateIn: []gen.EventState{gen.EventStateOn}}, 1, 3, 5),
		ev("stateNotIn", &gen.EventWhereInput{StateNotIn: []gen.EventState{gen.EventStateOn, gen.EventStateOff}}),
		// int8.
		ev("rank", &gen.EventWhereInput{Rank: ptr[int8](3)}, 3),
		ev("rankNEQ", &gen.EventWhereInput{RankNEQ: ptr[int8](3)}, 1, 2, 4, 5),
		ev("rankIn", &gen.EventWhereInput{RankIn: []int8{-4, 5}}, 4, 5),
		ev("rankNotIn", &gen.EventWhereInput{RankNotIn: []int8{-4, 5}}, 1, 2, 3),
		ev("rankGT", &gen.EventWhereInput{RankGT: ptr[int8](2)}, 3, 5),
		ev("rankGTE", &gen.EventWhereInput{RankGTE: ptr[int8](2)}, 2, 3, 5),
		ev("rankLT", &gen.EventWhereInput{RankLT: ptr[int8](1)}, 4),
		ev("rankLTE", &gen.EventWhereInput{RankLTE: ptr[int8](1)}, 1, 4),
		// uint64.
		ev("size", &gen.EventWhereInput{Size: ptr[uint64](20)}, 2),
		ev("sizeNEQ", &gen.EventWhereInput{SizeNEQ: ptr[uint64](20)}, 1, 3, 4, 5),
		ev("sizeIn", &gen.EventWhereInput{SizeIn: []uint64{10, 50}}, 1, 5),
		ev("sizeNotIn", &gen.EventWhereInput{SizeNotIn: []uint64{10, 50}}, 2, 3, 4),
		ev("sizeGT", &gen.EventWhereInput{SizeGT: ptr[uint64](30)}, 4, 5),
		ev("sizeGTE", &gen.EventWhereInput{SizeGTE: ptr[uint64](30)}, 3, 4, 5),
		ev("sizeLT", &gen.EventWhereInput{SizeLT: ptr[uint64](20)}, 1),
		ev("sizeLTE", &gen.EventWhereInput{SizeLTE: ptr[uint64](20)}, 1, 2),
		// An optional time.
		ev("at", &gen.EventWhereInput{At: ptr(hour(2))}, 2),
		ev("atNEQ", &gen.EventWhereInput{AtNEQ: ptr(hour(2))}, 1, 4, 5),
		ev("atIn", &gen.EventWhereInput{AtIn: []time.Time{hour(1), hour(5)}}, 1, 5),
		ev("atNotIn", &gen.EventWhereInput{AtNotIn: []time.Time{hour(1)}}, 2, 4, 5),
		ev("atGT", &gen.EventWhereInput{AtGT: ptr(hour(2))}, 4, 5),
		ev("atGTE", &gen.EventWhereInput{AtGTE: ptr(hour(2))}, 2, 4, 5),
		ev("atLT", &gen.EventWhereInput{AtLT: ptr(hour(2))}, 1),
		ev("atLTE", &gen.EventWhereInput{AtLTE: ptr(hour(2))}, 1, 2),
		ev("atIsNil true", &gen.EventWhereInput{AtIsNil: ptr(true)}, 3),
		ev("atIsNil false", &gen.EventWhereInput{AtIsNil: ptr(false)}, 1, 2, 4, 5),
		ev("atNotNil true", &gen.EventWhereInput{AtNotNil: ptr(true)}, 1, 2, 4, 5),
		ev("atNotNil false", &gen.EventWhereInput{AtNotNil: ptr(false)}, 3),
		// bool.
		ev("done", &gen.EventWhereInput{Done: ptr(true)}, 1, 3, 5),
		ev("doneNEQ", &gen.EventWhereInput{DoneNEQ: ptr(true)}, 2, 4),
		// float32.
		ev("score", &gen.EventWhereInput{Score: ptr[float32](1.5)}, 2),
		ev("scoreNEQ", &gen.EventWhereInput{ScoreNEQ: ptr[float32](1.5)}, 1, 3, 4, 5),
		ev("scoreIn", &gen.EventWhereInput{ScoreIn: []float32{0.5, 4.5}}, 1, 5),
		ev("scoreNotIn", &gen.EventWhereInput{ScoreNotIn: []float32{0.5, 4.5}}, 2, 3, 4),
		ev("scoreGT", &gen.EventWhereInput{ScoreGT: ptr[float32](2.5)}, 4, 5),
		ev("scoreGTE", &gen.EventWhereInput{ScoreGTE: ptr[float32](2.5)}, 3, 4, 5),
		ev("scoreLT", &gen.EventWhereInput{ScoreLT: ptr[float32](1.5)}, 1),
		ev("scoreLTE", &gen.EventWhereInput{ScoreLTE: ptr[float32](1.5)}, 1, 2),
		// A UUID.
		ev("ref", &gen.EventWhereInput{Ref: &r2}, 2, 4),
		ev("refNEQ", &gen.EventWhereInput{RefNEQ: &r2}, 1, 3, 5),
		ev("refIn", &gen.EventWhereInput{RefIn: []uuid.UUID{r1}}, 1, 3, 5),
		ev("refNotIn", &gen.EventWhereInput{RefNotIn: []uuid.UUID{r1}}, 2, 4),
		// A string.
		ev("name", &gen.EventWhereInput{Name: ptr("beta")}, 2),
		ev("nameNEQ", &gen.EventWhereInput{NameNEQ: ptr("beta")}, 1, 3, 4, 5),
		ev("nameIn", &gen.EventWhereInput{NameIn: []string{"alpha", "delta"}}, 1, 5),
		ev("nameNotIn", &gen.EventWhereInput{NameNotIn: []string{"alpha", "delta"}}, 2, 3, 4),
		ev("nameGT", &gen.EventWhereInput{NameGT: ptr("beta")}, 3, 5),
		ev("nameGTE", &gen.EventWhereInput{NameGTE: ptr("beta")}, 2, 3, 5),
		ev("nameLT", &gen.EventWhereInput{NameLT: ptr("beta")}, 1, 4),
		ev("nameLTE", &gen.EventWhereInput{NameLTE: ptr("beta")}, 1, 2, 4),
		ev("nameContains", &gen.EventWhereInput{NameContains: ptr("lph")}, 1, 4),
		ev("nameHasPrefix", &gen.EventWhereInput{NameHasPrefix: ptr("alpha")}, 1, 4),
		ev("nameHasSuffix", &gen.EventWhereInput{NameHasSuffix: ptr("bet")}, 4),
		// An optional string of the application's own Go type, converted.
		ev("code", &gen.EventWhereInput{Code: ptr("A1")}, 1),
		ev("codeNEQ", &gen.EventWhereInput{CodeNEQ: ptr("A1")}, 3, 4, 5),
		ev("codeIn", &gen.EventWhereInput{CodeIn: []string{"A1", "B2"}}, 1, 3),
		ev("codeNotIn", &gen.EventWhereInput{CodeNotIn: []string{"A1"}}, 3, 4, 5),
		ev("codeGT", &gen.EventWhereInput{CodeGT: ptr("B")}, 3, 5),
		ev("codeGTE", &gen.EventWhereInput{CodeGTE: ptr("B2")}, 3, 5),
		ev("codeLT", &gen.EventWhereInput{CodeLT: ptr("B")}, 1, 4),
		ev("codeLTE", &gen.EventWhereInput{CodeLTE: ptr("A3")}, 1, 4),
		ev("codeContains", &gen.EventWhereInput{CodeContains: ptr("_%")}, 5),
		ev("codeHasPrefix", &gen.EventWhereInput{CodeHasPrefix: ptr("A")}, 1, 4),
		ev("codeHasSuffix", &gen.EventWhereInput{CodeHasSuffix: ptr("%")}, 5),
		ev("codeHasPrefix _", &gen.EventWhereInput{CodeHasPrefix: ptr("_")}),
		ev("codeIsNil", &gen.EventWhereInput{CodeIsNil: ptr(true)}, 2),
		ev("codeNotNil", &gen.EventWhereInput{CodeNotNil: ptr(true)}, 1, 3, 4, 5),
		// An optional foreign key of the application's own Go type, by the
		// IDs of its edge's node.
		ev("ownerID", &gen.EventWhereInput{OwnerID: ptr(ownerID(1))}, 1, 2),
		ev("ownerIDNEQ", &gen.EventWhereInput{OwnerIDNEQ: ptr(ownerID(1))}, 3),
		ev("ownerIDIn", &gen.EventWhereInput{OwnerIDIn: []string{ownerID(1), ownerID(2)}}, 1, 2, 3),
		ev("ownerIDNotIn", &gen.EventWhereInput{OwnerIDNotIn: []string{ownerID(2)}}, 1, 2),
		ev("ownerIDIsNil", &gen.EventWhereInput{OwnerIDIsNil: ptr(true)}, 4, 5),
		ev("ownerIDNotNil", &gen.EventWhereInput{OwnerIDNotNil: ptr(true)}, 1, 2, 3),
		// A key of the application's own Go type.
		ow("id", &gen.OwnerWhereInput{ID: ptr(ownerID(2))}, 2),
		ow("idNEQ", &gen.OwnerWhereInput{IDNEQ: ptr(ownerID(1))}, 2, 3),
		ow("idIn", &gen.OwnerWhereInput{IDIn: []string{ownerID(1), ownerID(3)}}, 1, 3),
		ow("idNotIn", &gen.OwnerWhereInput{IDNotIn: []string{ownerID(1)}}, 2, 3),
		ow("email", &gen.OwnerWhereInput{Email: ptr("a@x")}, 1),
		// The application's enum as a String, converted.
		ow("tier", &gen.OwnerWhereInput{Tier: ptr("low")}, 1, 3),
		ow("tierNEQ", &gen.OwnerWhereInput{TierNEQ: ptr("low")}, 2),
		ow("tierIn", &gen.OwnerWhereInput{TierIn: []string{"high"}}, 2),
		ow("tierNotIn", &gen.OwnerWhereInput{TierNotIn: []string{"low"}}, 2),
		// An int32 of the application's own Go type, converted.
		ow("rank", &gen.OwnerWhereInput{Rank: ptr[int32](2)}, 2),
		ow("rankNEQ", &gen.OwnerWhereInput{RankNEQ: ptr[int32](2)}, 1, 3),
		ow("rankIn", &gen.OwnerWhereInput{RankIn: []int32{1, 3}}, 1, 3),
		ow("rankNotIn", &gen.OwnerWhereInput{RankNotIn: []int32{1, 3}}, 2),
		ow("rankGT", &gen.OwnerWhereInput{RankGT: ptr[int32](1)}, 2, 3),
		ow("rankGTE", &gen.OwnerWhereInput{RankGTE: ptr[int32](2)}, 2, 3),
		ow("rankLT", &gen.OwnerWhereInput{RankLT: ptr[int32](2)}, 1),
		ow("rankLTE", &gen.OwnerWhereInput{RankLTE: ptr[int32](2)}, 1, 2),
		// A string, in the org: cid, of org 8, is never there.
		ac("displayName", &gen.AccountWhereInput{DisplayName: ptr("ann")}, 1),
		ac("displayName of another org", &gen.AccountWhereInput{DisplayName: ptr("cid")}),
		ac("displayNameNEQ", &gen.AccountWhereInput{DisplayNameNEQ: ptr("ann")}, 2, 4),
		ac("displayNameIn", &gen.AccountWhereInput{DisplayNameIn: []string{"ann", "cid"}}, 1),
		ac("displayNameNotIn", &gen.AccountWhereInput{DisplayNameNotIn: []string{"ann"}}, 2, 4),
		ac("displayNameContains", &gen.AccountWhereInput{DisplayNameContains: ptr("n")}, 1, 4),
		ac("displayNameHasPrefix", &gen.AccountWhereInput{DisplayNameHasPrefix: ptr("b")}, 2),
		ac("displayNameHasSuffix", &gen.AccountWhereInput{DisplayNameHasSuffix: ptr("n")}, 1, 4),
		// A required foreign key, by the IDs of its edge's node.
		po("authorID", &gen.PostWhereInput{AuthorID: ptr(userID(1))}, 1, 2),
		po("authorIDNEQ", &gen.PostWhereInput{AuthorIDNEQ: ptr(userID(1))}, 3), // 4's author is in another org: it reads as null, as a policy's hidden row does
		po("authorIDIn", &gen.PostWhereInput{AuthorIDIn: []string{userID(1), userID(2)}}, 1, 2, 3),
		po("authorIDNotIn", &gen.PostWhereInput{AuthorIDNotIn: []string{userID(1)}}, 3),
		po("title", &gen.PostWhereInput{Title: ptr("b")}, 2),
		po("titleContains", &gen.PostWhereInput{TitleContains: ptr("x")}, 1, 4),
		// An optional foreign key to the entity itself.
		ca("mentorID", &gen.CategoryWhereInput{MentorID: ptr(catID(1))}, 2),
		ca("mentorIDNEQ", &gen.CategoryWhereInput{MentorIDNEQ: ptr(catID(1))}, 4),
		ca("mentorIDIn", &gen.CategoryWhereInput{MentorIDIn: []string{catID(1), catID(2)}}, 2, 4),
		ca("mentorIDNotIn", &gen.CategoryWhereInput{MentorIDNotIn: []string{catID(1)}}, 4),
	})
}

// TestHasEdge: has<Edge> and has<Edge>With, through a foreign key on the
// target's rows, on the owner's row, and a join table.
func TestHasEdge(t *testing.T) {
	ctx, db := open(t)
	seed(t, ctx, db)
	check(t, ctx, []whereCase{
		ac("hasPosts true", &gen.AccountWhereInput{HasPosts: ptr(true)}, 1, 2),
		ac("hasPosts false", &gen.AccountWhereInput{HasPosts: ptr(false)}, 4),
		ac("hasPostsWith titleContains x", &gen.AccountWhereInput{HasPostsWith: []*gen.PostWhereInput{{TitleContains: ptr("x")}}}, 1),
		ac("hasPostsWith both", &gen.AccountWhereInput{HasPostsWith: []*gen.PostWhereInput{{TitleContains: ptr("x")}, {Title: ptr("b")}}}),
		ac("hasPostsWith []", &gen.AccountWhereInput{HasPostsWith: []*gen.PostWhereInput{}}, 1, 2, 4),
		ac("hasPostsWith {}", &gen.AccountWhereInput{HasPostsWith: []*gen.PostWhereInput{{}}}, 1, 2),
		ac("hasGroups true", &gen.AccountWhereInput{HasGroups: ptr(true)}, 1, 2),
		ac("hasGroups false", &gen.AccountWhereInput{HasGroups: ptr(false)}, 4),
		ow("hasEvents true", &gen.OwnerWhereInput{HasEvents: ptr(true)}, 1, 2),
		ow("hasEvents false", &gen.OwnerWhereInput{HasEvents: ptr(false)}, 3),
		ow("hasEventsWith", &gen.OwnerWhereInput{HasEventsWith: []*gen.EventWhereInput{{Done: ptr(false)}}}, 1),
		ow("hasEventsWith each", &gen.OwnerWhereInput{HasEventsWith: []*gen.EventWhereInput{{Done: ptr(true)}, {RankGT: ptr[int8](2)}}}, 2),
		ev("hasOwner true", &gen.EventWhereInput{HasOwner: ptr(true)}, 1, 2, 3),
		ev("hasOwner false", &gen.EventWhereInput{HasOwner: ptr(false)}, 4, 5),
		ev("hasOwnerWith", &gen.EventWhereInput{HasOwnerWith: []*gen.OwnerWhereInput{{Tier: ptr("high")}}}, 3),
		ev("hasOwnerWith email", &gen.EventWhereInput{HasOwnerWith: []*gen.OwnerWhereInput{{Email: ptr("a@x")}}}, 1, 2),
		// A connection edge's where: the relation's condition and the filter.
		{"Account.postsWhere", func(ctx context.Context) ([]int64, error) {
			ann, err := db.Query[gen.User]().Where(gen.Users.ID.EQ(1)).One(ctx)
			if err != nil {
				return nil, err
			}
			c, err := ann.PostsGQL(ctx, nil, nil, nil, nil, nil, &gen.PostWhereInput{TitleContains: ptr("x")})
			return page(ctx, c, err, func(e *gen.PostEdge) int64 { return e.Node.ID })
		}, []int64{1}},
	})
}

// TestWhereLogic: not, and, or, their nesting, and an input that sets
// nothing, which means every row.
func TestWhereLogic(t *testing.T) {
	ctx, db := open(t)
	seed(t, ctx, db)
	type in = gen.EventWhereInput
	check(t, ctx, []whereCase{
		ev("nil", nil, 1, 2, 3, 4, 5),
		ev("empty", &in{}, 1, 2, 3, 4, 5),
		ev("fields", &in{Done: ptr(true), RankGT: ptr[int8](1)}, 3, 5),
		ev("and", &in{And: []*in{{Done: ptr(true)}, {RankGT: ptr[int8](1)}}}, 3, 5),
		ev("or", &in{Or: []*in{{Name: ptr("beta")}, {Rank: ptr[int8](5)}}}, 2, 5),
		ev("not", &in{Not: &in{Done: ptr(true)}}, 2, 4),
		ev("nested", &in{Not: &in{Or: []*in{{Kind: ptr(gen.EventKindInFlight)}, {And: []*in{{Done: ptr(true)}, {AtIsNil: ptr(true)}}}}}}, 1, 5),
		ev("fields and or", &in{Done: ptr(true), Or: []*in{{Rank: ptr[int8](1)}, {Rank: ptr[int8](3)}, {Rank: ptr[int8](4)}}}, 1, 3),
		ev("and []", &in{And: []*in{}}, 1, 2, 3, 4, 5),
		ev("or []", &in{Or: []*in{}}),
		ev("not {}", &in{Not: &in{}}),
		ev("or with {}", &in{Or: []*in{{}, {Rank: ptr[int8](1)}}}, 1, 2, 3, 4, 5),
		ev("in []", &in{RankIn: []int8{}}),
		ev("notIn []", &in{RankNotIn: []int8{}}, 1, 2, 3, 4, 5),
		ow("through an edge", &gen.OwnerWhereInput{Or: []*gen.OwnerWhereInput{
			{HasEventsWith: []*gen.EventWhereInput{{Not: &in{Done: ptr(true)}}}},
			{Not: &gen.OwnerWhereInput{HasEvents: ptr(true)}},
		}}, 1, 3),
	})
}

// chain is a where on Owner whose deepest input is n levels down: through
// not, when not is true, else through hasEventsWith and hasOwnerWith in
// turn, an Owner's input at an even depth and an Event's at an odd one.
func chain(n int, not bool) *gen.OwnerWhereInput {
	o := &gen.OwnerWhereInput{Rank: ptr[int32](1)}
	if not {
		for range n {
			o = &gen.OwnerWhereInput{Not: o}
		}
		return o
	}
	e := &gen.EventWhereInput{Done: ptr(true)}
	for d := n - 1; d >= 0; d-- {
		if d%2 == 0 {
			o = &gen.OwnerWhereInput{HasEventsWith: []*gen.EventWhereInput{e}}
		} else {
			e = &gen.EventWhereInput{HasOwnerWith: []*gen.OwnerWhereInput{o}}
		}
	}
	return o
}

// TestWhereDepthAndWidth is Review Focus 4: a where nested deeper than the
// MaxFilterDepth of veloxgql.LimitsFromContext(ctx), an and, or or has…With
// list of more than its MaxFilterWidth inputs, and a where argument over its
// MaxFilterNodes, fail before any statement runs.
func TestWhereDepthAndWidth(t *testing.T) {
	ctx, db := open(t)
	seed(t, ctx, db)
	// Depth 5 is the most: the owners with an event whose owner has an
	// event whose owner has a done event; and five nots.
	check(t, ctx, []whereCase{
		ow("depth 4 through edges", chain(4, false), 1),
		ow("depth 5 through edges", chain(5, false), 1, 2),
		ow("depth 5 not", chain(5, true), 2, 3),
	})
	statements.Store(0)
	for name, w := range map[string]*gen.OwnerWhereInput{"depth 6 through edges": chain(6, false), "depth 6 not": chain(6, true)} {
		if _, err := owners(ctx, w); !errors.Is(err, veloxgql.ErrFilterTooDeep) {
			t.Errorf("%s: %v, want ErrFilterTooDeep", name, err)
		}
	}
	many := func(n int) []*gen.EventWhereInput {
		ws := make([]*gen.EventWhereInput, n)
		for i := range ws {
			ws[i] = &gen.EventWhereInput{Rank: ptr(int8(i))}
		}
		return ws
	}
	// One input used 100 times at each of 5 levels, as a GraphQL variable
	// used at many places unmarshals: over MaxFilterNodes long before its
	// 100^5 inputs.
	shared := &gen.EventWhereInput{NameIn: []string{"alpha", "beta"}}
	for range 5 {
		ws := make([]*gen.EventWhereInput, 100)
		for i := range ws {
			ws[i] = shared
		}
		shared = &gen.EventWhereInput{Or: ws}
	}
	if _, err := events(ctx, shared); !errors.Is(err, veloxgql.ErrFilterTooLarge) {
		t.Errorf("a variable 100^5 times: %v, want ErrFilterTooLarge", err)
	}
	for name, w := range map[string]*gen.EventWhereInput{
		"rankIn of 101":  {RankIn: make([]int8, 101)},
		"idNotIn of 101":         {IDNotIn: make([]string, 101)},
		"or of 101":              {Or: many(101)},
		"and of 101":             {And: many(101)},
		"hasOwnerWith of 101": {HasOwnerWith: func() []*gen.OwnerWhereInput {
			ws := make([]*gen.OwnerWhereInput, 101)
			for i := range ws {
				ws[i] = &gen.OwnerWhereInput{}
			}
			return ws
		}()},
	} {
		if _, err := events(ctx, w); !errors.Is(err, veloxgql.ErrFilterTooWide) {
			t.Errorf("%s: %v, want ErrFilterTooWide", name, err)
		}
	}
	if n := statements.Load(); n != 0 {
		t.Errorf("the refused filters ran %d statements", n)
	}
	check(t, ctx, []whereCase{ev("or of 100", &gen.EventWhereInput{Or: many(100)}, 1, 2, 3, 5)})
}

// TestWhereIDs is Review Focus 1 for where: an ID of another type in a
// where ID predicate, of a key or of a foreign key, fails with
// veloxgql.ErrWrongIDType, and a malformed one with veloxgql.ErrBadID,
// before any statement runs.
func TestWhereIDs(t *testing.T) {
	ctx, db := open(t)
	seed(t, ctx, db)
	statements.Store(0)
	post := veloxgql.EncodeID("Post", int64(1))
	for name, run := range map[string]func() error{
		"Owner.id":              func() error { _, err := owners(ctx, &gen.OwnerWhereInput{ID: &post}); return err },
		"Owner.idIn":            func() error { _, err := owners(ctx, &gen.OwnerWhereInput{IDIn: []string{ownerID(1), post}}); return err },
		"Event.id":              func() error { _, err := events(ctx, &gen.EventWhereInput{ID: ptr(ownerID(1))}); return err },
		"Owner.idIn of Event":   func() error { _, err := owners(ctx, &gen.OwnerWhereInput{IDIn: []string{eventID(1)}}); return err },
		"Owner.idGT":            func() error { _, err := owners(ctx, &gen.OwnerWhereInput{IDGT: &post}); return err },
		"Category.idLTE":        func() error { _, err := categories(ctx, &gen.CategoryWhereInput{IDLTE: ptr(userID(1))}); return err },
		"Event.ownerIDNotIn":    func() error { _, err := events(ctx, &gen.EventWhereInput{OwnerIDNotIn: []string{post}}); return err },
		"Post.authorID":         func() error { _, err := posts(ctx, &gen.PostWhereInput{AuthorID: &post}); return err },
		"Category.mentorID":     func() error { _, err := categories(ctx, &gen.CategoryWhereInput{MentorID: ptr(userID(1))}); return err },
		"in an or":              func() error { _, err := posts(ctx, &gen.PostWhereInput{Or: []*gen.PostWhereInput{{}, {AuthorIDNEQ: &post}}}); return err },
		"in a hasPostsWith":     func() error { _, err := accounts(ctx, &gen.AccountWhereInput{HasPostsWith: []*gen.PostWhereInput{{AuthorID: &post}}}); return err },
	} {
		if err := run(); !errors.Is(err, veloxgql.ErrWrongIDType) {
			t.Errorf("%s: %v, want ErrWrongIDType", name, err)
		}
	}
	// The error names the fields that held the ID.
	if _, err := owners(ctx, &gen.OwnerWhereInput{HasEventsWith: []*gen.EventWhereInput{{OwnerID: &post}}}); err == nil ||
		err.Error() != "veloxgql: OwnerWhereInput.hasEventsWith[0]: EventWhereInput.ownerID: Owner ID: ID of another type" {
		t.Errorf("the error of a nested ID: %v", err)
	}
	if _, err := owners(ctx, &gen.OwnerWhereInput{ID: ptr("not an ID")}); !errors.Is(err, veloxgql.ErrBadID) {
		t.Errorf("a malformed ID: %v, want ErrBadID", err)
	}
	if _, err := events(ctx, &gen.EventWhereInput{ID: ptr(veloxgql.EncodeID("Event", "x"))}); !errors.Is(err, veloxgql.ErrBadID) {
		t.Errorf("an Event ID whose key is no UUID: %v, want ErrBadID", err)
	}
	if n := statements.Load(); n != 0 {
		t.Errorf("the refused IDs ran %d statements", n)
	}

	// The node key's predicates: a uuid key has the four of equality, and
	// an integer key the ordered ones too, through IDAs when its Go type is
	// the application's own (Owner's ids.UserID) and as it is when not.
	for _, op := range []string{"IDGT", "IDGTE", "IDLT", "IDLTE"} {
		if _, ok := reflect.TypeFor[gen.EventWhereInput]().FieldByName(op); ok {
			t.Errorf("EventWhereInput has %s: a UUID key is not ordered", op)
		}
	}
	check(t, ctx, []whereCase{
		ev("id of 4", &gen.EventWhereInput{ID: ptr(eventID(4))}, 4),
		ow("idGT", &gen.OwnerWhereInput{IDGT: ptr(ownerID(2))}, 3),
		ow("idGTE", &gen.OwnerWhereInput{IDGTE: ptr(ownerID(2))}, 2, 3),
		ow("idLT", &gen.OwnerWhereInput{IDLT: ptr(ownerID(2))}, 1),
		ow("idLTE", &gen.OwnerWhereInput{IDLTE: ptr(ownerID(2))}, 1, 2),
		// A key with no annotation of its own.
		ca("idGT", &gen.CategoryWhereInput{IDGT: ptr(catID(2))}, 3, 4),
		ca("idLTE", &gen.CategoryWhereInput{IDLTE: ptr(catID(2))}, 1, 2),
		ca("idIn", &gen.CategoryWhereInput{IDIn: []string{catID(1), catID(4)}}, 1, 4),
		ca("idNotIn", &gen.CategoryWhereInput{IDNotIn: []string{catID(1), catID(4)}}, 2, 3),
	})
}

// TestOwnerTags: Owner.tags, a json field, is read by velox from the JSON
// text stored, and bound to the schema's JSON scalar by the
// generated getter: the stored JSON (a JSON null for a nil slice) and one
// velox wrote.
func TestOwnerTags(t *testing.T) {
	ctx, db := open(t)
	tags := func(id ids.UserID) (veloxgql.JSON, error) {
		o, err := db.Query[gen.Owner]().Where(gen.Owners.ID.EQ(id)).One(ctx)
		if err != nil {
			return nil, err
		}
		return o.TagsGQL()
	}
	for id, want := range map[ids.UserID]string{1: ` + "`" + `["x","y"]` + "`" + `, 2: ` + "`" + `[]` + "`" + `, 3: ` + "`" + `null` + "`" + `} {
		got, err := tags(id)
		if err != nil || string(got) != want {
			t.Errorf("owner %d: TagsGQL %q, %v, want %s", id, got, err, want)
		}
	}
	w := gen.Owner{ID: 9, Code: "c9", Tags: []string{"w", "é"}, Ratio: 1, Tier: "low", Rank: 9}
	if err := db.Insert(&w).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := tags(9); err != nil || string(got) != ` + "`" + `["w","é"]` + "`" + ` {
		t.Errorf("a written owner: TagsGQL %q, %v", got, err)
	}
}

// TestCategoryTwin: Category.twin is a bidirectional self-edge, a O2O with no
// inverse whose key, category_twin, is on the row. CreateCategory with a
// twinID sets the new row's key to the twin, and the twin's to the new row:
// the mirror velox writes, which the where inputs then see on both rows.
func TestCategoryTwin(t *testing.T) {
	ctx, db := open(t)
	c1 := &gen.Category{Title: "c1"}
	if err := db.Insert(c1).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	c2, err := gen.CreateCategory(ctx, gen.CreateCategoryInput{Title: "c2", TwinID: ptr(catID(c1.ID))})
	if err != nil {
		t.Fatal(err)
	}
	twin := func(id int64) string {
		x, err := db.Query[gen.Category]().Where(gen.Categories.ID.EQ(id)).One(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if x.CategoryTwin == nil {
			return "NULL"
		}
		return fmt.Sprint(*x.CategoryTwin)
	}
	if got, want := twin(c2.ID), fmt.Sprint(c1.ID); got != want {
		t.Errorf("the new row's category_twin = %s, want c1 %s", got, want)
	}
	if got, want := twin(c1.ID), fmt.Sprint(c2.ID); got != want {
		t.Errorf("c1's category_twin = %s, want the new row %s", got, want)
	}
	// The seeded rows are untouched.
	for id := int64(1); id <= 4; id++ {
		if got := twin(id); got != "NULL" {
			t.Errorf("category %d's category_twin = %s, want NULL", id, got)
		}
	}
}

// TestNodesUnderTheRowCap: Nodes reads one row for each ID it has, even
// when that is more than the DB's WithMaxRows cap.
func TestNodesUnderTheRowCap(t *testing.T) {
	ctx, db := open(t, velox.WithMaxRows(2))
	if _, err := db.Query[gen.Post]().Unbounded().All(ctx); !errors.Is(err, velox.ErrTooManyRows) {
		t.Fatalf("four posts under a cap of 2: %v, want ErrTooManyRows", err)
	}
	ids := []string{
		veloxgql.EncodeID("Post", int64(1)), veloxgql.EncodeID("Post", int64(2)), veloxgql.EncodeID("Post", int64(3)),
		veloxgql.EncodeID("Post", int64(2)), veloxgql.EncodeID("Post", int64(9)), userID(1),
	}
	nodes, err := gen.Nodes(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, n := range nodes {
		got = append(got, fmt.Sprintf("%v", n != nil))
		if n != nil {
			got[len(got)-1] = n.IDGQL()
		}
	}
	want := []string{ids[0], ids[1], ids[2], ids[3], "false", ids[5]}
	if !slices.Equal(got, want) {
		t.Errorf("Nodes: %v, want %v", got, want)
	}
}
`

// TestWhereRun runs every predicate of the where inputs through their
// generated Pred on SQLite (TestWhereOps), with not, and and or
// (TestWhereLogic), has<Edge> and has<Edge>With (TestHasEdge), the depth and
// width limits (TestWhereDepthAndWidth) and IDs of the wrong type
// (TestWhereIDs); Nodes under a DB's row cap; and CreateCategory's twinID,
// which sets the key of a bidirectional self-edge on both rows
// (TestCategoryTwin).
func TestWhereRun(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	_, run := app(t, map[string]string{"schema/schema.go": e2eSchema, "schema/event.go": e2eEvent, "levels/levels.go": e2eLevels, "ids/ids.go": e2eIDs, "gen/scope.go": e2eScope, "where_test.go": e2eWhere})
	if out, err := run("tool", "velox-gen", "-out", "gen", "-plugin", "velox-graphql", "./schema"); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	if out, err := run("mod", "tidy", "-e"); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
	out, err := run("test", "-count=1", "-v", ".")
	if err != nil {
		t.Fatalf("go test: %v\n%s", err, out)
	}
	for _, name := range []string{"TestWhereOps", "TestHasEdge", "TestWhereLogic", "TestWhereDepthAndWidth", "TestWhereIDs", "TestNodesUnderTheRowCap", "TestOwnerTags", "TestCategoryTwin"} {
		if !strings.Contains(out, "--- PASS: "+name+" ") {
			t.Errorf("no PASS of %s in\n%s", name, out)
		}
	}
}

// TestConnectionsRun runs the generated connections and edge methods on
// SQLite: paging and ordering through cursors off the wire, totalCount, each
// way a relation holds its key (the owner's row, the target's, a join
// table), the org scope, and that a relation a With loaded is not read.
func TestConnectionsRun(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	_, run := app(t, map[string]string{"schema/schema.go": e2eSchema, "gen/scope.go": e2eScope, "app_test.go": e2eRun})
	if out, err := run("tool", "velox-gen", "-out", "gen", "-plugin", "velox-graphql", "./schema"); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	if out, err := run("mod", "tidy", "-e"); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
	out, err := run("test", "-count=1", "-v", "-run", "TestConnections$|TestConnectionOf", ".")
	if err != nil || !strings.Contains(out, "--- PASS: TestConnections") || !strings.Contains(out, "--- PASS: TestConnectionOfLimitOffset") {
		t.Fatalf("go test: %v\n%s", err, out)
	}
}
