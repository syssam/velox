package codegen

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
)

// esCheckSchema is users in groups through Membership, an edge schema whose
// mixins and methods each case of TestEdgeSchemaBehaviourRefused declares.
const esCheckSchema = `package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type User struct{ schema.Schema }

func (User) Edges() []schema.Edge {
	return []schema.Edge{edge.To("groups", Group.Type).Through("memberships", Membership.Type)}
}

type Group struct{ schema.Schema }

func (Group) Edges() []schema.Edge { return []schema.Edge{edge.From("users", User.Type).Ref("groups")} }

type Membership struct{ schema.Schema }

func (Membership) Fields() []schema.Field {
	return []schema.Field{field.Int64("user_id"), field.Int64("group_id")}
}

func (Membership) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("user", User.Type).Unique().Required().Field("user_id"),
		edge.To("group", Group.Type).Unique().Required().Field("group_id"),
	}
}
`

// esCheckHeader starts schema/membership.go, before a case's declarations.
const esCheckHeader = `package schema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/privacy"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/mixin"
	"github.com/syssam/velox/schema/mixin/softdelete"
)

var (
	_ context.Context
	_ = velox.FieldEQ
	_ privacy.QueryPolicy
	_ schema.Mixin
	_ mixin.Schema
	_ softdelete.Mixin
)

`

// esCheck loads esCheckSchema with decls, and returns what CheckBehaviour
// returns for it.
func esCheck(t *testing.T, decls string) error {
	t.Helper()
	dir := module(t, map[string]string{"schema/schema.go": esCheckSchema, "schema/membership.go": esCheckHeader + decls})
	m, err := Load(dir, "./schema", offline())
	if err != nil {
		t.Fatal(err)
	}
	g, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := SchemaMethods(filepath.Join(dir, "schema"), []string{"User", "Group", "Membership"})
	if err != nil {
		t.Fatal(err)
	}
	mixins, err := MixinBehaviour(dir, "./schema", g, offline())
	if err != nil {
		t.Fatal(err)
	}
	_, err = CheckBehaviour(dir, "./schema", g, bound, mixins, offline())
	return err
}

// Rule 70: an edge schema cannot narrow what a read sees, by a Read or Query
// policy, Interceptors or softdelete.Mixin, its own or a mixin's. A Write
// or Insert policy and hooks are allowed.
func TestEdgeSchemaBehaviourRefused(t *testing.T) {
	readPolicy := func(key string) string {
		return "func (Membership) Policy() velox.Policy {\n\treturn velox.Policy{" + key + ": func(context.Context) (velox.Predicate, error) { // mark\n\t\treturn velox.FieldEQ(\"user_id\", 1), nil\n\t}}\n}\n"
	}
	interceptors := "func (Membership) Interceptors() []velox.Interceptor { return nil }\n"
	for _, c := range []struct{ name, decls, want string }{
		{"read", readPolicy("Read"), "schema Membership: an edge schema's Policy cannot set Read or Query: Has and With through its many-to-many relation read its rows through the relation's two sides and would not apply it; put the condition in the two sides' policies"},
		{"query", "func (Membership) Policy() velox.Policy {\n\treturn velox.Policy{Query: privacy.QueryPolicy{}} // mark\n}\n", "an edge schema's Policy cannot set Read or Query"},
		{"interceptors", interceptors, "schema Membership: an edge schema cannot have Interceptors: Has and With"},
		{"softdelete", "func (Membership) Mixin() []schema.Mixin { return []schema.Mixin{softdelete.Mixin{}} }\n",
			"schema Membership: an edge schema cannot list softdelete.Mixin: Remove would keep the link's row, which Has and With through its many-to-many relation would still read; delete links for good"},
		{"mixin read", "type Hidden struct{ mixin.Schema }\n\nfunc (Membership) Mixin() []schema.Mixin { return []schema.Mixin{Hidden{}} }\n\n" + strings.ReplaceAll(readPolicy("Read"), "(Membership)", "(Hidden)"),
			"schema Membership: mixin Hidden's Policy sets Read or Query, which an edge schema cannot have"},
		{"mixin interceptors", "type Hidden struct{ mixin.Schema }\n\nfunc (Membership) Mixin() []schema.Mixin { return []schema.Mixin{Hidden{}} }\n\n" + strings.ReplaceAll(interceptors, "(Membership)", "(Hidden)"),
			"schema Membership: mixin Hidden has Interceptors, which an edge schema cannot have"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := esCheck(t, c.decls)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v\nwant %s", err, c.want)
			}
			if strings.Contains(c.decls, "// mark") {
				line := strings.Count(esCheckHeader+c.decls[:strings.Index(c.decls, "// mark")], "\n") + 1
				if at := "membership.go:" + strconv.Itoa(line) + ": "; !strings.Contains(err.Error(), at) {
					t.Errorf("the error does not name %s: %v", at, err)
				}
			}
		})
	}
	t.Run("allowed", func(t *testing.T) {
		decls := "func (Membership) Policy() velox.Policy {\n\treturn velox.Policy{Write: func(context.Context) (velox.Predicate, error) {\n\t\treturn velox.FieldEQ(\"user_id\", 1), nil\n\t}}\n}\n\n" +
			"func (Membership) Hooks() []velox.Hook { return nil }\n"
		if err := esCheck(t, decls); err != nil {
			t.Fatal(err)
		}
	})
}

// A cascading pair of keys for the warning test: Place's keys both declare
// OnDelete. Place has a unique code, a unique index over it and a key, and
// the unique index over its pair in the reversed order.
type Viewer struct{ schema.Schema }

func (Viewer) Edges() []schema.Edge {
	return []schema.Edge{edge.To("shows", Show.Type).Through("places", Place.Type)}
}

type Show struct{ schema.Schema }

func (Show) Edges() []schema.Edge {
	return []schema.Edge{edge.From("viewers", Viewer.Type).Ref("shows")}
}

type Place struct{ schema.Schema }

func (Place) Fields() []schema.Field {
	return []schema.Field{field.Int64("viewer_id"), field.Int64("show_id"), field.String("code").Unique()}
}
func (Place) Indexes() []schema.Index {
	return []schema.Index{index.Fields("code", "show_id").Unique(), index.Fields("show_id", "viewer_id").Unique()}
}
func (Place) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("viewer", Viewer.Type).Unique().Required().Field("viewer_id").OnDelete(edge.Cascade),
		edge.To("show", Show.Type).Unique().Required().Field("show_id").OnDelete(edge.Cascade),
	}
}

// An edge schema whose keys declare no OnDelete is warned of once, naming
// both sides and both key edges; one whose keys cascade is not. Each unique
// field and unique index of an edge schema but the pair's, in either order,
// is warned of for MySQL.
func TestEdgeSchemaWarnings(t *testing.T) {
	g := mustBuild(t, Learner{}, Course{}, Enrollment{}, Fan{}, Follow{}, Viewer{}, Show{}, Place{})
	got := EdgeSchemaWarnings(g)
	const drops = "on MySQL, Add's ON DUPLICATE KEY UPDATE applies to every unique key, so a new link whose row collides with another only on this key is dropped without an error"
	want := []string{
		`edgeschema_test.go:57: schema Enrollment: deleting Learner or Course rows fails while Enrollment rows name them; declare OnDelete(edge.Cascade) on edges "learner" and "course" to delete their links with them`,
		`edgeschema_test.go:82: schema Follow: deleting Fan rows fails while Follow rows name them; declare OnDelete(edge.Cascade) on edges "fan" and "follower" to delete their links with them`,
		`schema Place: field "code": a unique field of an edge schema: ` + drops,
		`schema Place: index 0: a unique index of an edge schema other than its pair of keys': ` + drops,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d warnings, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i, w := range want {
		if !strings.HasSuffix(got[i], w) {
			t.Errorf("warning %d:\n got %s\nwant …%s", i, got[i], w)
		}
	}
}
