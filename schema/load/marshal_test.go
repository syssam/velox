package load_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
	"github.com/syssam/velox2/schema/index"
	"github.com/syssam/velox2/schema/load"
	"github.com/syssam/velox2/schema/mixin"
)

type User struct{ schema.Schema }

func (User) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Tenant(),
		field.String("name").NotEmpty().MaxLen(100),
		field.Enum("role").Values("viewer", "admin").Default("viewer"),
	}
}
func (User) Edges() []schema.Edge {
	return []schema.Edge{edge.To("posts", Post.Type), edge.To("children", User.Type).From("parent").Unique()}
}
func (User) Indexes() []schema.Index          { return []schema.Index{index.Fields("org_id", "name").Unique()} }
func (User) Mixin() []schema.Mixin            { return []schema.Mixin{mixin.Time{}} }
func (User) Annotations() []schema.Annotation { return []schema.Annotation{schema.Table("users")} }

type Post struct{ schema.Schema }

func (Post) Fields() []schema.Field { return []schema.Field{field.String("title")} }

// compact removes the indentation Marshal's output puts inside a raw value.
func compact(raw json.RawMessage) string {
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return err.Error()
	}
	return b.String()
}

func marshal(t *testing.T, ss ...schema.Interface) *load.Model {
	t.Helper()
	b, err := load.Marshal("example.com/app/schema", ss...)
	if err != nil {
		t.Fatal(err)
	}
	var m load.Model
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return &m
}

func TestMarshal(t *testing.T) {
	m := marshal(t, User{}, Post{})
	if m.Version != load.Version || m.Package != "example.com/app/schema" || len(m.Schemas) != 2 || m.Schemas[0].Name != "Post" {
		t.Fatalf("model %+v", m)
	}
	u := m.Schemas[1]
	var names []string
	for _, f := range u.Fields {
		names = append(names, f.Name)
	}
	if got := strings.Join(names, ","); got != "created_at,updated_at,org_id,name,role" {
		t.Fatalf("fields %s: the mixin's come first", got)
	}
	if p := u.Fields[0].Position; !p.MixedIn || p.MixinIndex != 0 || p.Index != 0 {
		t.Fatalf("created_at position %+v", p)
	}
	if p := u.Fields[3].Position; p.MixedIn || p.Index != 1 {
		t.Fatalf("name position %+v", p)
	}
	if !u.Fields[0].DefaultFunc || !u.Fields[1].UpdateDefault || !u.Fields[2].Tenant {
		t.Fatalf("flags %+v %+v %+v", u.Fields[0], u.Fields[1], u.Fields[2])
	}
	if string(u.Fields[4].Default) != `"viewer"` || len(u.Fields[4].Enums) != 2 {
		t.Fatalf("role %+v", u.Fields[4])
	}
	if c := u.Fields[3].Checks; len(c) != 2 || c[1].Kind != "MaxLen" || string(c[1].Args[0]) != "100" {
		t.Fatalf("checks %+v", c)
	}
	if len(u.Edges) != 3 || u.Edges[2].Name != "parent" || !u.Edges[2].Inline || !u.Edges[2].Inverse || u.Edges[2].Ref != "children" {
		t.Fatalf("edges %+v", u.Edges)
	}
	if compact(u.Annotations["Table"]) != `{"Schema":"","Table":"users"}` {
		t.Fatalf("annotations %s", u.Annotations["Table"])
	}
	if !u.Indexes[0].Unique || u.Indexes[0].Fields[1] != "name" {
		t.Fatalf("indexes %+v", u.Indexes)
	}
}

type hooked struct{ schema.Schema }

func (hooked) Hooks() []any { return nil }

func TestMarshalRefusesBehaviour(t *testing.T) {
	_, err := load.Marshal("p", hooked{})
	if err == nil || !strings.Contains(err.Error(), "schema hooked: has a Hooks method") {
		t.Fatalf("got %v", err)
	}
}

type panics struct{ schema.Schema }

func (panics) Fields() []schema.Field { panic("boom") }

type nilField struct{ schema.Schema }

func (nilField) Fields() []schema.Field { return []schema.Field{nil} }

type badField struct{ schema.Schema }

func (badField) Fields() []schema.Field { return []schema.Field{field.Int64("n").DefaultFunc(1)} }

func TestMarshalErrors(t *testing.T) {
	for _, c := range []struct {
		s    schema.Interface
		want string
	}{
		{panics{}, "schema panics: Fields panicked: boom"},
		{nilField{}, "schema nilField: Fields()[0] is nil"},
		{badField{}, `schema badField: field "n": DefaultFunc`},
	} {
		if _, err := load.Marshal("p", c.s); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("got %v, want %q", err, c.want)
		}
	}
}

type merged struct{ N int }

func (merged) Name() string { return "M" }
func (m merged) Merge(o schema.Annotation) schema.Annotation {
	return merged{N: m.N + o.(merged).N}
}

type mixA struct{ mixin.Schema }

func (mixA) Annotations() []schema.Annotation {
	return []schema.Annotation{merged{N: 1}, schema.Table("a")}
}

type withMerge struct{ schema.Schema }

func (withMerge) Mixin() []schema.Mixin { return []schema.Mixin{mixA{}} }
func (withMerge) Annotations() []schema.Annotation {
	return []schema.Annotation{merged{N: 2}, schema.Table("b")}
}

func TestAnnotationsMerge(t *testing.T) {
	a := marshal(t, withMerge{}).Schemas[0].Annotations
	if compact(a["M"]) != `{"N":3}` || compact(a["Table"]) != `{"Schema":"","Table":"b"}` {
		t.Fatalf("%s %s", a["M"], a["Table"])
	}
}

func TestMarshalDeterministic(t *testing.T) {
	a, _ := load.Marshal("p", User{}, Post{})
	b, _ := load.Marshal("p", Post{}, User{})
	if string(a) != string(b) {
		t.Fatal("Marshal depends on argument order")
	}
}
