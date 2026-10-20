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
	dir, run := app(t, map[string]string{"schema/schema.go": e2eSchema, "schema/event.go": e2eEvent, "levels/levels.go": e2eLevels})
	out, err := run("tool", "velox-gen", "-out", "gen", "-plugin", "velox-graphql", "./schema")
	if err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	for _, name := range []string{"gql_node.go", "gql_user.go", "gql_post.go", "gql_group.go", "gql_category.go", "gql_event.go"} {
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
