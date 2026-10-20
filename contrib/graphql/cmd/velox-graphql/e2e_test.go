package main

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syssam/velox2/contrib/graphql/internal/sdl"
)

const e2eSchema = `package schema

import (
	"github.com/syssam/velox2/contrib/graphql"
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
)

type User struct{ schema.Schema }

func (User) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Tenant(),
		field.String("name").Comment("The display name.").Annotations(graphql.FieldName("displayName"), graphql.OrderField("NAME"), graphql.WhereOps()),
		field.Enum("role").Values("admin", "in_review").Default("admin").Annotations(graphql.EnumValues(map[string]string{"in_review": "REVIEWING"})),
		field.String("password_hash").Sensitive(),
		field.Uint64("quota"),
		field.Time("seen_at").Optional(),
	}
}

func (User) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("posts", Post.Type).Annotations(graphql.WhereEdge()),
		edge.To("groups", Group.Type).Annotations(graphql.Connection(false)),
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
		field.String("title").Annotations(graphql.OrderField("TITLE")),
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
		graphql.Mutations(graphql.Create(), graphql.Update(), graphql.Delete()),
	}
}

type Group struct{ schema.Schema }

func (Group) Fields() []schema.Field { return []schema.Field{field.String("name")} }

func (Group) Edges() []schema.Edge {
	return []schema.Edge{edge.From("users", User.Type).Ref("groups")}
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
		field.Int64("mentor_id").Optional().Immutable().Annotations(graphql.WhereOps(graphql.OpsEquality)),
	}
}

func (Category) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("children", Category.Type),
		edge.From("parent", Category.Type).Ref("children").Unique().Immutable(),
		edge.To("mentee", Category.Type).Unique(),
		edge.From("mentor", Category.Type).Ref("mentee").Unique().Immutable().Field("mentor_id"),
		edge.To("twin", Category.Type).Unique().Immutable(),
	}
}

func (Category) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.Mutations(graphql.Create(), graphql.Update())}
}

type Secret struct{ schema.Schema }

func (Secret) Fields() []schema.Field { return []schema.Field{field.String("value")} }

func (Secret) Annotations() []schema.Annotation { return []schema.Annotation{graphql.Skip()} }
`

// app writes a module with velox-gen and velox-graphql as tools, and the
// files (the schema package among them), and returns its directory and a
// runner of go in it, offline.
func app(t *testing.T, files map[string]string) (string, func(args ...string) (string, error)) {
	t.Helper()
	repo, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var sum []byte
	for _, p := range []string{"go.sum", "cmd/velox-gen/go.sum", "contrib/graphql/go.sum"} {
		b, err := os.ReadFile(filepath.Join(repo, p))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		sum = append(sum, b...)
	}
	mod := "module example.com/app\n\ngo 1.27.0\n\n" +
		"tool (\n" +
		"\tgithub.com/syssam/velox2/cmd/velox-gen\n" +
		"\tgithub.com/syssam/velox2/contrib/graphql/cmd/velox-graphql\n" +
		")\n\n" +
		"require (\n" +
		"\tgithub.com/syssam/velox2 v0.0.0\n" +
		"\tgithub.com/syssam/velox2/cmd/velox-gen v0.0.0\n" +
		"\tgithub.com/syssam/velox2/contrib/graphql v0.0.0\n" +
		")\n\n" +
		"replace (\n" +
		"\tgithub.com/syssam/velox2 => " + repo + "\n" +
		"\tgithub.com/syssam/velox2/cmd/velox-gen => " + filepath.Join(repo, "cmd", "velox-gen") + "\n" +
		"\tgithub.com/syssam/velox2/contrib/graphql => " + filepath.Join(repo, "contrib", "graphql") + "\n" +
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
		return string(out), err
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
		"  author: Account! @goField(name: \"AuthorGQL\")\n",
		"enum UserRole {\n  ADMIN\n  REVIEWING\n}\n",
		"  accounts(after: Cursor, first: Int, before: Cursor, last: Int, orderBy: AccountOrder, where: AccountWhereInput): AccountConnection!\n",
		"  posts(after: Cursor, first: Int, before: Cursor, last: Int, orderBy: PostOrder, where: PostWhereInput): PostConnection!\n",
		"input AccountOrder {\n",
		"  direction: OrderDirection! = ASC\n  \"\"\"The field to order by.\"\"\"\n  field: AccountOrderField!\n",
		"enum AccountOrderField {\n  NAME\n}\n",
		"enum PostOrderField {\n  TITLE\n}\n",
		"  displayNameHasSuffix: String\n  hasPosts: Boolean\n  hasPostsWith: [PostWhereInput!]\n}\n",
		"  or: [PostWhereInput!]\n  authorID: ID\n  authorIDNEQ: ID\n  authorIDIn: [ID!]\n  authorIDNotIn: [ID!]\n}\n",
		"type Mutation {\n",
		"  createPost(input: CreatePostInput!): Post!\n",
		"  updatePost(id: ID!, input: UpdatePostInput!): Post!\n",
		"  deletePost(id: ID!): ID!\n",
		"input CreatePostInput {\n  title: String!\n  authorID: ID!\n}\n",
		"input UpdatePostInput {\n  title: String\n  authorID: ID\n}\n",
		// Category's self-edges, read through OwnsKey: parent, mentor and the
		// bidirectional twin hold their immutable keys on the row, so they are
		// set on create only; children and mentee would re-point other rows'
		// immutable keys, so they have no input.
		"input CreateCategoryInput {\n  title: String!\n  parentID: ID\n  mentorID: ID\n  twinID: ID\n}\n",
		"input UpdateCategoryInput {\n  title: String\n}\n",
		// The key's where predicates are named after the edge that owns it.
		"  or: [CategoryWhereInput!]\n  mentorID: ID\n  mentorIDNEQ: ID\n  mentorIDIn: [ID!]\n  mentorIDNotIn: [ID!]\n}\n",
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
// key, an enum of the application's own Go type, a String enum, and a where
// predicate on each scalar veloxgql binds.
const e2eEvent = `package schema

import (
	"github.com/google/uuid"

	"example.com/app/ids"
	"example.com/app/levels"
	"github.com/syssam/velox2/contrib/graphql"
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/field"
)

type Event struct{ schema.Schema }

func (Event) Fields() []schema.Field {
	return []schema.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.Enum("kind").Values("start", "in_flight").Annotations(graphql.WhereOps()),
		field.Enum("level").GoType(levels.Level("")).Annotations(graphql.WhereOps()),
		field.Enum("state").Values("on", "off").Default("on").Annotations(graphql.Skip(graphql.SkipEnumField), graphql.WhereOps()),
		field.Int8("rank").Annotations(graphql.WhereOps(graphql.OpsAll), graphql.OrderField("RANK")),
		field.Uint64("size").Annotations(graphql.WhereOps()),
		field.Time("at").Optional().Annotations(graphql.WhereOps()),
		field.Bool("done").Annotations(graphql.WhereOps()),
		field.Float32("score").Annotations(graphql.WhereOps()),
		field.UUID("ref", uuid.UUID{}).Annotations(graphql.WhereOps()),
	}
}

func (Event) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.QueryField("events")}
}

// Owner's key is of a type from a package named ids, as Nodes's parameter
// is; its other fields' Go types are the application's own, or JSON, and
// bind through getters.
type Owner struct{ schema.Schema }

func (Owner) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("id").GoType(ids.UserID(0)).Annotations(graphql.WhereOps(graphql.OpEQ)),
		field.String("email").GoType(ids.Email("")).Optional().Nillable().Annotations(graphql.WhereOps(graphql.OpEQ)),
		field.String("code").GoType(ids.Code("")),
		field.Strings("tags"),
		field.Bytes("blob").Optional(),
		field.Float32("ratio"),
		field.Enum("tier").GoType(levels.Level("")).Annotations(graphql.Skip(graphql.SkipEnumField)),
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
	for _, name := range []string{"gql_node.go", "gql_user.go", "gql_post.go", "gql_group.go", "gql_category.go", "gql_event.go", "gql_owner.go"} {
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
	for _, name := range []string{"gql_secret.go", "gql_user_group.go"} {
		if _, err := os.Stat(filepath.Join(dir, "gen", name)); !os.IsNotExist(err) {
			t.Errorf("%s: %v, want none: the type is skipped, or a join entity", name, err)
		}
	}
	// The fields whose Go type does not bind to their scalar bind to getters.
	sdlText, err := os.ReadFile(filepath.Join(dir, "gen", "velox.graphqls"))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := os.ReadFile(filepath.Join(dir, "gen", "gql_owner.go"))
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
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	_ "modernc.org/sqlite"

	"example.com/app/gen"
	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
)

const ddl = ` + "`" + `
CREATE TABLE users (id INTEGER PRIMARY KEY, org_id INTEGER NOT NULL, name TEXT NOT NULL, role TEXT NOT NULL,
	password_hash TEXT NOT NULL, quota INTEGER NOT NULL, seen_at DATETIME);
CREATE TABLE posts (id INTEGER PRIMARY KEY, author_id INTEGER NOT NULL, title TEXT NOT NULL, draft TEXT NOT NULL);
CREATE TABLE "groups" (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE user_groups (user_id INTEGER NOT NULL, group_id INTEGER NOT NULL, PRIMARY KEY (user_id, group_id));
CREATE TABLE categories (id INTEGER PRIMARY KEY, title TEXT NOT NULL, mentor_id INTEGER, category_children INTEGER, category_twin INTEGER);
INSERT INTO users VALUES (1, 7, 'ann', 'admin', '', 0, NULL), (2, 7, 'bob', 'admin', '', 0, NULL), (3, 8, 'cid', 'admin', '', 0, NULL);
INSERT INTO posts VALUES (1, 1, 'b', ''), (2, 1, 'a', ''), (3, 2, 'c', ''), (4, 1, 'a', ''), (5, 3, 'z', '');
INSERT INTO "groups" VALUES (1, 'g1'), (2, 'g2');
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
	ctx := velox.WithTenant(veloxgql.WithDB(context.Background(), db), int64(7))
	user := func(e *gen.AccountEdge) int64 { return e.Node.ID }
	post := func(e *gen.PostEdge) int64 { return e.Node.ID }

	// The root connection, in the tenant: ann and bob, not cid.
	byName := &gen.AccountOrder{Direction: veloxgql.OrderDirectionDesc, Field: gen.AccountOrderFieldName}
	us, err := gen.UsersConnection(ctx, nil, intp(1), nil, nil, byName, nil)
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
	// A where argument fails before any query, in this build.
	ran.Store(0)
	if _, err := gen.PostsConnection(ctx, nil, nil, nil, nil, nil, &gen.PostWhereInput{}); err == nil || !strings.Contains(err.Error(), "not implemented") || ran.Load() != 0 {
		t.Errorf("a where: %v, %d statements", err, ran.Load())
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
	if err := after.UnmarshalGQL(veloxgql.EncodeCursor(*info.EndCursor)); err != nil {
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
	// Many-to-many, as a list and as a connection, through the inverse edge.
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

	// A relation a With loaded: the edge method runs no query.
	loaded, err := db.Query[gen.User]().Where(gen.Users.ID.EQ(1)).With(gen.Users.Posts).With(gen.Users.Groups).One(ctx)
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
	if ran.Load() != 0 {
		t.Errorf("the loaded relations ran %d statements", ran.Load())
	}
}
`

// TestConnectionsRun runs the generated connections and edge methods on
// SQLite: paging and ordering through cursors off the wire, totalCount, each
// way a relation holds its key (the owner's row, the target's, a join
// table), the tenant scope, and the relations a With loaded.
func TestConnectionsRun(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	_, run := app(t, map[string]string{"schema/schema.go": e2eSchema, "app_test.go": e2eRun})
	if out, err := run("tool", "velox-gen", "-out", "gen", "-plugin", "velox-graphql", "./schema"); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	if out, err := run("mod", "tidy", "-e"); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
	out, err := run("test", "-count=1", "-v", "-run", "TestConnections$", ".")
	if err != nil || !strings.Contains(out, "--- PASS: TestConnections") {
		t.Fatalf("go test: %v\n%s", err, out)
	}
}
