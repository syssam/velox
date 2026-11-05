package codegen

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// checkSchema is a schema of three entities, for CheckBehaviour's column
// checks: a user has docs, a doc has comments, and Doc and Comment list
// InOrg, which adds org_id.
const checkSchema = `package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type InOrg struct{ mixin.Schema }

func (InOrg) Fields() []schema.Field { return []schema.Field{field.Int64("org_id").Immutable()} }

type User struct{ schema.Schema }

func (User) Fields() []schema.Field {
	return []schema.Field{
		field.String("name"),
		field.Int8("level"),
		field.String("nick").Optional().Nillable(),
		field.Float32("score"),
		field.Int64("rank"),
		field.Uint8("flags"),
	}
}

func (User) Edges() []schema.Edge { return []schema.Edge{edge.To("docs", Doc.Type)} }

type Doc struct{ schema.Schema }

func (Doc) Mixin() []schema.Mixin { return []schema.Mixin{InOrg{}} }

func (Doc) Fields() []schema.Field {
	return []schema.Field{field.Int64("owner_id"), field.String("title")}
}

func (Doc) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("owner", User.Type).Ref("docs").Unique().Required().Field("owner_id"),
		edge.To("comments", Comment.Type),
	}
}

type Comment struct{ schema.Schema }

func (Comment) Mixin() []schema.Mixin { return []schema.Mixin{InOrg{}} }

func (Comment) Fields() []schema.Field { return []schema.Field{field.Int64("doc_id")} }

func (Comment) Edges() []schema.Edge {
	return []schema.Edge{edge.From("doc", Doc.Type).Ref("comments").Unique().Required().Field("doc_id")}
}
`

// checkHeader starts schema/behaviour.go, before a case's declarations.
const checkHeader = `package schema

import (
	"context"

	"github.com/syssam/velox"
)

var (
	_ context.Context
	_ = velox.FieldEQ
)

`

// checkEnv is a module of checkSchema, loaded once; each case writes its
// behaviour and runs CheckBehaviour on it.
type checkEnv struct {
	dir string
	g   *Graph
}

func newCheckEnv(t *testing.T) *checkEnv {
	t.Helper()
	dir := module(t, map[string]string{"schema/schema.go": checkSchema})
	m, err := Load(dir, "./schema", offline())
	if err != nil {
		t.Fatal(err)
	}
	g, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	return &checkEnv{dir: dir, g: g}
}

// run writes decls after checkHeader as schema/behaviour.go and returns
// what CheckBehaviour returns for it.
func (c *checkEnv) run(t *testing.T, decls string) (map[string][]string, []string, error) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(c.dir, "schema", "behaviour.go"), []byte(checkHeader+decls), 0o644); err != nil {
		t.Fatal(err)
	}
	bound, err := SchemaMethods(filepath.Join(c.dir, "schema"), []string{"User", "Doc", "Comment"})
	if err != nil {
		t.Fatal(err)
	}
	mixins, err := MixinBehaviour(c.dir, "./schema", c.g, offline())
	if err != nil {
		t.Fatal(err)
	}
	return CheckBehaviour(c.dir, "./schema", c.g, bound, mixins, offline())
}

// lineOf is the line of behaviour.go, checkHeader and decls, that holds
// marker.
func lineOf(t *testing.T, decls, marker string) int {
	t.Helper()
	for i, l := range strings.Split(checkHeader+decls, "\n") {
		if strings.Contains(l, marker) {
			return i + 1
		}
	}
	t.Fatalf("no %q in %s", marker, decls)
	return 0
}

// read is a Policy of entity whose Read returns cond.
func read(entity, cond string) string {
	return "func (" + entity + ") Policy() velox.Policy {\n\treturn velox.Policy{Read: func(context.Context) (velox.Predicate, error) {\n\t\treturn " + cond + ", nil\n\t}}\n}\n"
}

// hook is a Hooks of entity whose one hook runs body on m.
func hook(entity, body string) string {
	return "func (" + entity + ") Hooks() []velox.Hook {\n\treturn []velox.Hook{velox.Before(func(_ context.Context, m *velox.Mutation) error {\n\t\t" + body + "\n\t}, velox.OpInsert)}\n}\n"
}

// insert is a Policy of entity whose Insert runs body on r.
func insert(entity, body string) string {
	return "func (" + entity + ") Policy() velox.Policy {\n\treturn velox.Policy{Insert: func(_ context.Context, r velox.Row) error {\n\t\t" + body + "\n\t}}\n}\n"
}

// checkRefusal is a case that CheckBehaviour refuses: the error names the line
// of marker in decls and holds want.
type checkRefusal struct{ name, decls, marker, want string }

func (c *checkEnv) refuses(t *testing.T, cases []checkRefusal) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := c.run(t, tc.decls)
			at := "behaviour.go:" + strconv.Itoa(lineOf(t, tc.decls, tc.marker)) + ": "
			if err != nil {
				for l := range strings.SplitSeq(err.Error(), "\n") {
					if strings.Contains(l, at) && strings.Contains(l, tc.want) {
						return
					}
				}
			}
			t.Fatalf("got %v\nwant %s…%s", err, at, tc.want)
		})
	}
}

func (c *checkEnv) accepts(t *testing.T, cases map[string]string) {
	t.Helper()
	for name, decls := range cases {
		t.Run(name, func(t *testing.T) {
			if _, warnings, err := c.run(t, decls); err != nil || len(warnings) != 0 {
				t.Fatalf("refused: %v %q", err, warnings)
			}
		})
	}
}

// Each call form that names a column or a relation is refused at its line
// when the entity has none of that name, with the entity and the method.
func TestCheckColumnsRefusesUnknown(t *testing.T) {
	c := newCheckEnv(t)
	c.refuses(t, []checkRefusal{
		{"FieldEQ", read("User", `velox.FieldEQ("nme", "x")`), `"nme"`, `velox.FieldEQ("nme", "x"): User has no column "nme" (User.Policy)`},
		{"in Interceptors", "func (User) Interceptors() []velox.Interceptor {\n\t_ = velox.FieldIsNull(\"nme\")\n\treturn nil\n}\n", `"nme"`, `velox.FieldIsNull("nme"): User has no column "nme" (User.Interceptors)`},
		{"HasEdge", read("Comment", `velox.HasEdge("dco")`), `"dco"`, `velox.HasEdge("dco"): Comment has no relation "dco" (Comment.Policy)`},
		{"Mutation.Set", hook("Doc", `return m.Set("titel", "x")`), `"titel"`, `m.Set("titel", "x"): Doc has no column "titel" (Doc.Hooks)`},
		{"Mutation.Fill", hook("Doc", `return m.Fill("titel", "x")`), `"titel"`, `m.Fill("titel", "x"): Doc has no column "titel" (Doc.Hooks)`},
		{"Mutation.Value", hook("Doc", "_, _ = m.Value(\"titel\")\n\t\treturn nil"), `"titel"`, `m.Value("titel"): Doc has no column "titel" (Doc.Hooks)`},
		{"Row.Value", insert("Doc", "_ = r.Value(\"titel\")\n\t\treturn nil"), `"titel"`, `r.Value("titel"): Doc has no column "titel" (Doc.Policy)`},
		{"Row.IsZero", insert("Doc", "_ = r.IsZero(\"titel\")\n\t\treturn nil"), `"titel"`, `r.IsZero("titel"): Doc has no column "titel" (Doc.Policy)`},
		{"Row.Set", insert("Doc", `return r.Set("titel", "x")`), `"titel"`, `r.Set("titel", "x"): Doc has no column "titel" (Doc.Policy)`},
	})
	// Every refusal is reported, joined.
	decls := read("User", `velox.And(velox.FieldEQ("a", 1), velox.FieldEQ("b", 1))`)
	if _, _, err := c.run(t, decls); err == nil || !strings.Contains(err.Error(), `no column "a"`) || !strings.Contains(err.Error(), `no column "b"`) {
		t.Errorf("got %v", err)
	}
}

// What names a real column with a value that fits, or that velox-gen
// cannot read, is accepted, and the relations it names are linked.
func TestCheckColumnsAccepts(t *testing.T) {
	c := newCheckEnv(t)
	c.accepts(t, map[string]string{
		"conditions": read("User", `velox.And(velox.FieldEQ("name", "a"), velox.FieldLT("level", 100), velox.FieldGTE("level", -128),
			velox.FieldIn("rank", 1, int32(2), uint8(3)), velox.FieldGT("score", 1.5), velox.FieldEQ("nick", "n"),
			velox.FieldIsNull("nick"), velox.FieldHasPrefix("name", "a"), velox.FieldEQ("flags", 255), velox.FieldEQ("id", 7))`),
		"values velox-gen cannot read": "var xs = []any{1}\n\n" + read("User",
			"velox.And(velox.FieldIn(\"rank\", xs...), velox.FieldEQ(\"rank\", xs[0]), velox.FieldEQ(\"level\", any(300)))"),
		"mutation":  hook("User", "if err := m.Set(\"nick\", nil); err != nil {\n\t\t\treturn err\n\t\t}\n\t\t_, _ = m.Value(\"rank\")\n\t\treturn m.Fill(\"level\", 3)"),
		"row":       insert("User", "_ = r.Value(\"nick\")\n\t\t_ = r.IsZero(\"rank\")\n\t\treturn nil"),
		"unreached": "func unused() velox.Predicate { return velox.FieldEQ(\"nope\", 1) }\n",
	})
	linked, _, err := c.run(t, read("User", `velox.HasEdgeWith("docs", velox.HasEdgeWith("comments", velox.FieldEQ("doc_id", 1)))`))
	if want := map[string][]string{"User": {"docs"}, "Doc": {"comments"}}; err != nil || !reflect.DeepEqual(linked, want) {
		t.Fatalf("got %v, %v, want %v", linked, err, want)
	}
}

// A mixin's call is checked against each entity that lists the mixin: one
// that lacks the column is refused, by name, and one that has it is not.
func TestCheckColumnsPerMixinEntity(t *testing.T) {
	c := newCheckEnv(t)
	decls := read("InOrg", `velox.FieldEQ("owner_id", 1)`)
	_, _, err := c.run(t, decls)
	want := "behaviour.go:" + strconv.Itoa(lineOf(t, decls, "owner_id")) + `: velox.FieldEQ("owner_id", 1): Comment, which lists InOrg, has no column "owner_id" (InOrg.Policy)`
	if err == nil || !strings.HasSuffix(err.Error(), want) || strings.Contains(err.Error(), "\n") {
		t.Fatalf("got %v\nwant %s", err, want)
	}
	c.accepts(t, map[string]string{"the mixin's column": read("InOrg", `velox.FieldEQ("org_id", 1)`)})
}

// A call in a function, a method or a variable of the schema package that
// a behaviour method names is checked against each entity whose method
// reaches it.
func TestCheckColumnsFollowsHelpers(t *testing.T) {
	c := newCheckEnv(t)
	helpers := "func member() velox.Predicate { return authoredBy() }\n\nfunc authoredBy() velox.Predicate { return velox.FieldEQ(\"owner_id\", 1) }\n\n"
	c.accepts(t, map[string]string{"Doc's column": helpers + read("Doc", "member()")})
	c.refuses(t, []checkRefusal{
		{"function", helpers + read("Doc", "member()") + read("User", "member()"), `velox.FieldEQ("owner_id"`, `velox.FieldEQ("owner_id", 1): User has no column "owner_id" (User.Policy)`},
		{"method", "func (User) own() velox.Predicate { return velox.FieldEQ(\"owner\", 1) }\n\n" + read("User", "User{}.own()"), `velox.FieldEQ("owner"`, `velox.FieldEQ("owner", 1): User has no column "owner" (User.Policy)`},
		{"variable", "var titled = velox.FieldEQ(\"titel\", \"x\")\n\n" + read("Doc", `titled`), `velox.FieldEQ("titel"`, `velox.FieldEQ("titel", "x"): Doc has no column "titel" (Doc.Policy)`},
	})
}

// A column named by a constant is checked as one named by a literal.
func TestCheckColumnsConstName(t *testing.T) {
	c := newCheckEnv(t)
	c.refuses(t, []checkRefusal{
		{"const", "const titleCol = \"titel\"\n\n" + read("Doc", `velox.FieldEQ(titleCol, "x")`), "FieldEQ(titleCol", `velox.FieldEQ(titleCol, "x"): Doc has no column "titel" (Doc.Policy)`},
		{"expression", "const prefix = \"tit\"\n\n" + read("Doc", `velox.FieldEQ(prefix+"el", "x")`), `prefix+"el"`, `Doc has no column "titel"`},
	})
	c.accepts(t, map[string]string{"const": "const titleCol = \"title\"\n\n" + read("Doc", `velox.FieldEQ(titleCol, "x")`)})
}

// A value of another kind than the column's, or a constant its type cannot
// hold, is refused; a value whose type is an interface is not checked.
func TestCheckColumnsValueKind(t *testing.T) {
	c := newCheckEnv(t)
	c.refuses(t, []checkRefusal{
		{"a string for an int64", read("User", `velox.FieldEQ("rank", "7")`), `"rank"`, `velox.FieldEQ("rank", "7"): column "rank" of User is an int64, not a string (User.Policy)`},
		{"300 for an int8", read("User", `velox.FieldEQ("level", 300)`), `"level"`, `velox.FieldEQ("level", 300): column "level" of User is an int8, which cannot hold 300 (User.Policy)`},
		{"-1 for a uint8", read("User", `velox.FieldGT("flags", -1)`), `"flags"`, `column "flags" of User is a uint8, which cannot hold -1`},
		{"an int for a float32", read("User", `velox.FieldGT("score", 3)`), `"score"`, `column "score" of User is a float32, not an int`},
		{"1e300 for a float32", read("User", `velox.FieldGT("score", 1e300)`), `"score"`, `column "score" of User is a float32, which cannot hold`},
		{"each In value", read("User", `velox.FieldIn("rank", 1, "2")`), `"rank"`, `velox.FieldIn("rank", 1, "2"): column "rank" of User is an int64, not a string`},
		{"a pointer", "var s = \"n\"\n\n" + read("User", `velox.FieldEQ("nick", &s)`), `"nick"`, `column "nick" of User is a string, not a *string`},
		{"Set", hook("Doc", `return m.Set("title", 1)`), `"title"`, `m.Set("title", 1): column "title" of Doc is a string, not an int (Doc.Hooks)`},
		{"Fill out of range", hook("User", `return m.Fill("level", 1000)`), `"level"`, `column "level" of User is an int8, which cannot hold 1000`},
		{"Row.Set", insert("User", `return r.Set("rank", 1.5)`), `"rank"`, `r.Set("rank", 1.5): column "rank" of User is an int64, not a float64`},
		{"a mixin's", hook("InOrg", `return m.Fill("org_id", "7")`), `"org_id"`, `m.Fill("org_id", "7"): column "org_id" of Comment, which lists InOrg, is an int64, not a string (InOrg.Hooks)`},
	})
}

// nil fits only a Set or a Fill of a nullable column.
func TestCheckColumnsNilOnNotNull(t *testing.T) {
	c := newCheckEnv(t)
	c.refuses(t, []checkRefusal{
		{"Set", hook("Doc", `return m.Set("title", nil)`), `"title"`, `m.Set("title", nil): column "title" of Doc is not nullable: it takes no nil (Doc.Hooks)`},
		{"Fill", hook("Doc", `return m.Fill("title", nil)`), `"title"`, `column "title" of Doc is not nullable: it takes no nil`},
		{"EQ", read("User", `velox.FieldEQ("nick", nil)`), `"nick"`, `velox.FieldEQ("nick", nil): column "nick" of User is compared with nil: compare it with IsNull or NotNull`},
		{"In", read("User", `velox.FieldIn("nick", "a", nil)`), `"nick"`, `column "nick" of User is compared with nil`},
	})
	c.accepts(t, map[string]string{"nullable": hook("User", `return m.Set("nick", nil)`)})
}

// FieldContains, FieldHasPrefix, FieldHasSuffix and FieldEqualFold need a
// text column.
func TestCheckColumnsTextOpOnNumber(t *testing.T) {
	c := newCheckEnv(t)
	c.refuses(t, []checkRefusal{
		{"Contains", read("User", `velox.FieldContains("rank", "1")`), `"rank"`, `velox.FieldContains("rank", "1"): column "rank" of User is an int64, not text (User.Policy)`},
		{"HasSuffix", read("User", `velox.FieldHasSuffix("score", "1")`), `"score"`, `column "score" of User is a float32, not text`},
		{"EqualFold", read("User", `velox.FieldEqualFold("level", "1")`), `"level"`, `velox.FieldEqualFold("level", "1"): column "level" of User is an int8, not text (User.Policy)`},
	})
	c.accepts(t, map[string]string{"text": read("User", `velox.And(velox.FieldContains("nick", "a"), velox.FieldEqualFold("name", "A"))`)})
}

// FieldIsNull and FieldNotNull need a nullable column.
func TestCheckColumnsIsNullOnNotNull(t *testing.T) {
	c := newCheckEnv(t)
	c.refuses(t, []checkRefusal{
		{"IsNull", read("User", `velox.FieldIsNull("name")`), `"name"`, `velox.FieldIsNull("name"): column "name" of User is not nullable (User.Policy)`},
		{"the ID", read("User", `velox.FieldNotNull("id")`), `"id"`, `column "id" of User is not nullable`},
	})
}

// The conditions of a HasEdgeWith name the columns of its target.
func TestCheckColumnsHasWhereTarget(t *testing.T) {
	c := newCheckEnv(t)
	c.refuses(t, []checkRefusal{
		{"target", read("Comment", `velox.HasEdgeWith("doc", velox.FieldEQ("name", "x"))`), `"doc"`, `velox.FieldEQ("name", "x"): Doc, the target of Comment's relation doc, has no column "name" (Comment.Policy)`},
		{"two deep", read("Comment", `velox.HasEdgeWith("doc", velox.HasEdgeWith("owner", velox.FieldEQ("title", "x")))`), `"doc"`, `velox.FieldEQ("title", "x"): User, the target of Doc's relation owner, has no column "title" (Comment.Policy)`},
		{"value", read("Comment", `velox.HasEdgeWith("doc", velox.FieldEQ("title", 1))`), `"doc"`, `column "title" of Doc, the target of Comment's relation doc, is a string, not an int`},
		{"a helper", "func titled() velox.Predicate { return velox.FieldEQ(\"titel\", \"x\") }\n\n" + read("Comment", `velox.HasEdgeWith("doc", titled())`), `velox.FieldEQ("titel"`, `Doc, the target of Comment's relation doc, has no column "titel"`},
		{"unknown inner relation", read("Comment", `velox.HasEdgeWith("doc", velox.HasEdge("ownr"))`), `"doc"`, `velox.HasEdge("ownr"): Doc, the target of Comment's relation doc, has no relation "ownr" (Comment.Policy)`},
	})
	c.accepts(t, map[string]string{"target": read("Comment", `velox.HasEdgeWith("doc", velox.FieldEQ("title", "x"))`)})
}

// A column named by a value velox-gen cannot read is a warning, at its
// line, once; the statement checks it.
func TestCheckColumnsWarnsNonConstant(t *testing.T) {
	c := newCheckEnv(t)
	decls := "func col() string { return \"title\" }\n\n" + read("Doc", `velox.FieldEQ(col(), "x")`) +
		hook("Doc", "name := col()\n\t\treturn m.Set(name, \"x\")")
	_, warnings, err := c.run(t, decls)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"behaviour.go:" + strconv.Itoa(lineOf(t, decls, "velox.FieldEQ(col()")) + `: velox.FieldEQ(col(), "x") names its column with a value velox-gen cannot read; velox checks it when the statement runs`,
		"behaviour.go:" + strconv.Itoa(lineOf(t, decls, "m.Set(name")) + `: m.Set(name, "x") names its column with a value velox-gen cannot read; velox checks it when the statement runs`,
	}
	if len(warnings) != len(want) {
		t.Fatalf("warnings %q", warnings)
	}
	for i, w := range warnings {
		if !strings.HasSuffix(w, want[i]) {
			t.Errorf("warning %q, want %s", w, want[i])
		}
	}
}
