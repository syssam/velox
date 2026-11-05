package load_test

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/syssam/velox/internal/codegen"
	"github.com/syssam/velox/internal/codegen/loadjson"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/load"
	"github.com/syssam/velox/schema/mixin"
)

type User struct{ schema.Schema }

func (User) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Immutable(),
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

// decode marshals ss and decodes the model, errors included.
func decode(t *testing.T, ss ...schema.Interface) *loadjson.Model {
	t.Helper()
	b, err := load.Marshal("example.com/app/schema", ss...)
	if err != nil {
		t.Fatal(err)
	}
	var m loadjson.Model
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return &m
}

// marshal is decode for a model that must have no errors.
func marshal(t *testing.T, ss ...schema.Interface) *loadjson.Model {
	t.Helper()
	m := decode(t, ss...)
	if len(m.Errors) > 0 {
		t.Fatalf("errors %+v", m.Errors)
	}
	return m
}

// errorText is the model's errors, one per line, as velox-gen prints them
// without the position.
func errorText(m *loadjson.Model) string {
	var b strings.Builder
	for _, e := range m.Errors {
		b.WriteString("schema " + e.Schema + ": ")
		if e.Where != "" {
			b.WriteString(e.Where + ": ")
		}
		b.WriteString(e.Reason + "\n")
	}
	return b.String()
}

// here returns the line it is called on.
func here() int {
	_, _, line, _ := runtime.Caller(1)
	return line
}

func TestMarshal(t *testing.T) {
	m := marshal(t, User{}, Post{})
	if m.Version != loadjson.Version || m.Package != "example.com/app/schema" || len(m.Schemas) != 2 || m.Schemas[0].Name != "Post" {
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
	if !u.Fields[0].DefaultFunc || !u.Fields[1].UpdateDefault {
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
	if compact(u.Annotations["Table"]) != `{"table":"users"}` {
		t.Fatalf("annotations %s", u.Annotations["Table"])
	}
	if !u.Indexes[0].Unique || u.Indexes[0].Fields[1] != "name" {
		t.Fatalf("indexes %+v", u.Indexes)
	}
}

func TestMarshalSchemaDefault(t *testing.T) {
	u := marshal(t, User{}, Post{}).Schemas[1]
	want := map[string]string{field.Postgres: "now()", field.MySQL: "CURRENT_TIMESTAMP(6)", field.SQLite: "(strftime('%Y-%m-%d %H:%M:%f000000+00:00','now'))"}
	if got := u.Fields[0].SchemaDefault; !reflect.DeepEqual(got, want) {
		t.Fatalf("created_at: %v", got)
	}
	b, err := json.Marshal(u.Fields[0])
	if err != nil || !strings.Contains(string(b), `"schema_default":{`) {
		t.Fatalf("created_at: %s %v", b, err)
	}
	// A field without one has no key.
	if b, err = json.Marshal(u.Fields[3]); err != nil || strings.Contains(string(b), "schema_default") {
		t.Fatalf("name: %s %v", b, err)
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
		if got := errorText(decode(t, c.s)); !strings.Contains(got, c.want) {
			t.Errorf("got %q, want %q", got, c.want)
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
	if compact(a["M"]) != `{"N":3}` || compact(a["Table"]) != `{"table":"b"}` {
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

type hooked struct{ schema.Schema }

func (hooked) Hooks() []any { return nil }

type ptrHooked struct{ schema.Schema }

func (*ptrHooked) Hooks() []any { return nil }

type policed struct{ schema.Schema }

func (policed) Policy() any { return nil }

type intercepted struct{ schema.Schema }

func (*intercepted) Interceptors() []any { return nil }

// A Hooks, Policy or Interceptors method the schema declares itself, by
// value or pointer receiver, is left to velox-gen, which reads it from the
// source: Marshal reports no error for it.
func TestMarshalLeavesOwnBehaviour(t *testing.T) {
	for _, s := range []schema.Interface{hooked{}, ptrHooked{}, &ptrHooked{}, policed{}, intercepted{}} {
		if got := errorText(decode(t, s)); got != "" {
			t.Errorf("%T: %q", s, got)
		}
	}
}

type boomAnno struct{}

func (boomAnno) Name() string                              { return "Boom" }
func (boomAnno) Merge(schema.Annotation) schema.Annotation { panic("merge boom") }

type mergePanics struct{ schema.Schema }

func (mergePanics) Annotations() []schema.Annotation {
	return []schema.Annotation{boomAnno{}, boomAnno{}}
}

type nilAnno struct{ schema.Schema }

func (nilAnno) Annotations() []schema.Annotation {
	return []schema.Annotation{(*schema.TableAnnotation)(nil)}
}

type badJSON struct{}

func (badJSON) Name() string                 { return "BadJSON" }
func (badJSON) MarshalJSON() ([]byte, error) { panic("json boom") }

type jsonPanics struct{ schema.Schema }

func (jsonPanics) Annotations() []schema.Annotation { return []schema.Annotation{badJSON{}} }

func TestMarshalRecoversAnnotationPanics(t *testing.T) {
	for _, c := range []struct {
		s    schema.Interface
		want string
	}{
		{mergePanics{}, "schema mergePanics: panicked: merge boom"},
		{jsonPanics{}, "schema jsonPanics: panicked: json boom"},
		{nilAnno{}, "schema nilAnno: Annotations()[0] is nil"},
	} {
		if got := errorText(decode(t, c.s)); !strings.Contains(got, c.want) {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

type misused struct{ schema.Schema }

// misusedLines are the lines of misused's broken constructors.
var misusedLines [3]int

func (misused) Fields() []schema.Field {
	a, la := field.Int64("a").DefaultFunc(1).Validate(nil), here()
	b, lb := field.Enum("b").NamedValues("X"), here()
	misusedLines[0], misusedLines[1] = la, lb
	return []schema.Field{a, field.String("ok"), b}
}

func (misused) Edges() []schema.Edge {
	e, le := edge.To("x", 42), here()
	misusedLines[2] = le
	return []schema.Edge{e}
}

func TestMarshalReportsEveryMisuse(t *testing.T) {
	m := decode(t, misused{}, Post{})
	if len(m.Schemas) != 2 {
		t.Fatalf("the JSON is still written: %+v", m.Schemas)
	}
	want := []struct {
		where, reason string
		line          int
	}{
		{`field "a"`, "Validate is given a nil function", misusedLines[0]},
		{`field "a"`, "DefaultFunc must be a func() int64", misusedLines[0]},
		{`field "b"`, "NamedValues needs name and value pairs, got 1 arguments", misusedLines[1]},
		{`edge "x"`, "the type must be a schema's Type method expression, such as Post.Type", misusedLines[2]},
	}
	if len(m.Errors) != len(want) {
		t.Fatalf("errors %+v", m.Errors)
	}
	for i, w := range want {
		e := m.Errors[i]
		if e.Schema != "misused" || e.Where != w.where || e.Reason != w.reason || !strings.HasSuffix(e.Pos, fmt.Sprintf("/marshal_test.go:%d", w.line)) {
			t.Errorf("error %d: %+v, want %+v", i, e, w)
		}
	}
}

type wrapped struct{ schema.Schema }

var wrappedLine int

func (wrapped) Fields() []schema.Field {
	f, l := field.Text("bio"), here()
	wrappedLine = l
	return []schema.Field{f}
}

func TestPosOfWrappedConstructor(t *testing.T) {
	m := marshal(t, wrapped{})
	if p := m.Schemas[0].Fields[0].Pos; !strings.HasSuffix(p, fmt.Sprintf("/marshal_test.go:%d", wrappedLine)) {
		t.Fatalf("pos %q, want the line of field.Text, %d", p, wrappedLine)
	}
}

func TestMarshalPositions(t *testing.T) {
	u := marshal(t, User{}, Post{}).Schemas[1]
	for _, f := range u.Fields {
		if !strings.Contains(f.Pos, ".go:") {
			t.Errorf("field %s: pos %q", f.Name, f.Pos)
		}
	}
	// A field of velox's mixin.Time is at the line of User's Mixin method,
	// which the user wrote.
	pc := reflect.ValueOf(User.Mixin).Pointer()
	_, line := runtime.FuncForPC(pc).FileLine(pc)
	if want := "marshal_test.go:" + strconv.Itoa(line); !strings.HasSuffix(u.Fields[0].Pos, want) || !strings.Contains(u.Fields[3].Pos, "/marshal_test.go:") {
		t.Errorf("a mixin's field is at the line of Mixin, %s: %q %q", want, u.Fields[0].Pos, u.Fields[3].Pos)
	}
	// The inline inverse is declared on the line of its assoc edge.
	if e := u.Edges; e[1].Pos == "" || e[2].Pos != e[1].Pos || u.Indexes[0].Pos == "" {
		t.Errorf("edges %q %q, index %q", e[1].Pos, e[2].Pos, u.Indexes[0].Pos)
	}
}

func TestMarshalDuplicateSchema(t *testing.T) {
	if got := errorText(decode(t, Post{}, Post{})); got != "schema Post: declared twice\n" {
		t.Fatalf("got %q", got)
	}
}

type Cents int64

type Status string

func (Status) Values() []string { return []string{"active", "closed"} }

type UID [16]byte

func (UID) Value() (driver.Value, error) { return nil, nil }

func newUID() UID { return UID{1} }

// pair is a schema of one field.
type pair struct {
	schema.Schema
	f schema.Field
}

func (p pair) Fields() []schema.Field { return []schema.Field{p.f} }

// Level is an int with its own JSON encoding, which a Default or a check
// argument of Number[Level] must not use.
type Level int

func (l Level) MarshalJSON() ([]byte, error) { return json.Marshal(fmt.Sprintf("L%d", int(l))) }

// A typed constructor loads to the JSON of its ent spelling, and lowers to the
// same output (Review Focus 4). A T with its own JSON or text encoding, such
// as slog.Level, stores its Default and check arguments as numbers too.
func TestTypedConstructorsEqualGoType(t *testing.T) {
	for name, c := range map[string][2]schema.Field{
		"Number": {
			field.Number[Cents]("p").Nillable().Default(5).Min(1),
			field.Int64("p").GoType(Cents(0)).Nillable().Default(5).Min(1),
		},
		"Number slog.Level": {
			field.Number[slog.Level]("lvl").Optional().Nillable().Default(slog.LevelInfo).Min(slog.LevelDebug).Max(slog.LevelError),
			field.Int("lvl").GoType(slog.Level(0)).Optional().Nillable().Default(0).Min(-4).Max(8),
		},
		"Number MarshalJSON": {
			field.Number[Level]("lvl").Nillable().Default(3).Range(1, 9),
			field.Int("lvl").GoType(Level(0)).Nillable().Default(3).Range(1, 9),
		},
		"EnumOf": {
			field.EnumOf[Status]("s").Default("closed"),
			field.Enum("s").GoType(Status("")).Default("closed"),
		},
		"UUIDOf": {
			field.UUIDOf[UID]("id").Default(newUID),
			field.UUID("id", UID{}).Default(newUID),
		},
	} {
		var out [2]string
		var lowered [2]*codegen.Out
		for i, f := range c {
			m := marshal(t, pair{f: f})
			lf := m.Schemas[0].Fields[0]
			if lf.Pos == "" {
				t.Fatalf("%s: no position", name)
			}
			g, err := codegen.Build(m)
			if err != nil {
				t.Fatalf("%s: build: %v", name, err)
			}
			if lowered[i], err = codegen.Lower(g, codegen.Config{}); err != nil {
				t.Fatalf("%s: lower: %v", name, err)
			}
			lf.Pos = ""
			b, err := json.Marshal(lf)
			if err != nil {
				t.Fatal(err)
			}
			out[i] = string(b)
		}
		if out[0] != out[1] {
			t.Errorf("%s:\n typed %s\n   ent %s", name, out[0], out[1])
		}
		if !reflect.DeepEqual(lowered[0], lowered[1]) {
			a, _ := json.Marshal(lowered[0])
			b, _ := json.Marshal(lowered[1])
			t.Errorf("%s: lowered\n typed %s\n   ent %s", name, a, b)
		}
	}
}

// Board declares its methods with pointer receivers, and embeds an
// unexported base that brings the times.
type Board struct{ boardBase }

type boardBase struct{ schema.Schema }

func (boardBase) Mixin() []schema.Mixin { return []schema.Mixin{mixin.Time{}} }

func (*Board) Fields() []schema.Field { return []schema.Field{field.String("title")} }
func (*Board) Edges() []schema.Edge   { return []schema.Edge{edge.To("posts", Post.Type)} }
func (*Board) Indexes() []schema.Index {
	return []schema.Index{index.Fields("title").Unique()}
}
func (*Board) Annotations() []schema.Annotation { return []schema.Annotation{schema.Table("boards")} }

// The program velox-gen writes passes each schema as a pointer: Marshal reads
// the methods of either receiver, and a base's, and names the schema by its
// type, not the pointer's.
func TestMarshalPointerSchema(t *testing.T) {
	m := decode(t, new(Board), new(Post))
	if len(m.Errors) != 0 || len(m.Schemas) != 2 {
		t.Fatalf("%+v", m)
	}
	b := m.Schemas[0]
	if b.Name != "Board" || len(b.Fields) != 3 || b.Fields[2].Name != "title" || len(b.Edges) != 1 || len(b.Indexes) != 1 {
		t.Fatalf("%+v", b)
	}
	if b.Fields[0].Name != "created_at" {
		t.Errorf("the base's mixin is not first: %+v", b.Fields)
	}
}

// policyBase declares a Policy that schemas embedding it would inherit.
type policyBase struct{ schema.Schema }

var policyBaseLine = here() + 2 // the line of Policy

func (policyBase) Policy() any { return nil }

type promoted struct{ policyBase }

// A Policy promoted from an embedded type is not bound: velox-gen binds the
// one the schema declares itself. The error says so, names the embedded
// type, and is at the embedded method's line.
func TestMarshalRefusesAPromotedPolicy(t *testing.T) {
	m := decode(t, promoted{})
	want := "schema promoted: has a Policy method promoted from load_test.policyBase, which velox-gen does not bind: " +
		"declare Policy on the schema itself, in the schema package: func (promoted) Policy() velox.Policy"
	if got := errorText(m); !strings.Contains(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if p := m.Errors[0].Pos; !strings.HasSuffix(p, fmt.Sprintf("/marshal_test.go:%d", policyBaseLine)) {
		t.Errorf("pos %q, want line %d", p, policyBaseLine)
	}
}
