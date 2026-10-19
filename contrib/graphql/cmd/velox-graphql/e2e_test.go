package main

import (
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

type Secret struct{ schema.Schema }

func (Secret) Fields() []schema.Field { return []schema.Field{field.String("value")} }

func (Secret) Annotations() []schema.Annotation { return []schema.Annotation{graphql.Skip()} }
`

// TestPluginThroughVeloxGen runs the plugin the way an application does: a
// module with velox-gen and velox-graphql as tools, and a schema package with
// annotations.
func TestPluginThroughVeloxGen(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
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
	for name, src := range map[string]string{"go.mod": mod, "go.sum": string(sum), "schema/schema.go": e2eSchema} {
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
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
	}
	for _, absent := range []string{"orgID", "passwordHash", "authorID: Int", "draft", "Secret", "UserGroup", "type User", "AccountRole"} {
		if strings.Contains(got, absent) {
			t.Errorf("%q is in\n%s", absent, got)
		}
	}

	if out, err := run("tool", "velox-gen", "-check", "-out", "gen", "-plugin", "velox-graphql", "./schema"); err != nil || out != "" {
		t.Errorf("-check: %v\n%s", err, out)
	}
}
