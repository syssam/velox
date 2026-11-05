package codegen

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox/internal/codegen/loadjson"
)

// mixinEntities is a schema package of two entities: Project lists the
// mixins A, B and C, then declares a Policy of its own (Config.Bound says
// so); Label lists B. The mixins themselves are in mixins.go, which each
// test writes.
const mixinEntities = `package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

type Project struct{ schema.Schema }

func (Project) Mixin() []schema.Mixin { return []schema.Mixin{A{}, B{}, C{}} }

func (Project) Fields() []schema.Field { return []schema.Field{field.String("name")} }

type Label struct{ schema.Schema }

func (Label) Mixin() []schema.Mixin { return []schema.Mixin{B{}} }
`

// mixinTypes declares A, B and C with fields only, so that the package
// loads whatever behaviour a test then writes in place of it.
const mixinTypes = `package schema

import (
	"time"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

var _ time.Time

type A struct{ mixin.Schema }

func (A) Fields() []schema.Field {
	return []schema.Field{field.Int64("org_id").Immutable(), field.Time("deleted_at").Optional().Nillable()}
}

type B struct{ mixin.Schema }

type C struct{ mixin.Schema }
`

// mixinModule writes and loads the schema package of mixinEntities and
// mixinTypes, and returns its graph and the module's directory.
func mixinModule(t *testing.T) (*Graph, string) {
	t.Helper()
	dir := module(t, map[string]string{"schema/entities.go": mixinEntities, "schema/mixins.go": mixinTypes})
	m, err := Load(dir, "./schema", offline())
	if err != nil {
		t.Fatal(err)
	}
	g, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	return g, dir
}

// mixinBehaviour writes behaviour after mixinTypes, and runs MixinBehaviour.
func mixinBehaviour(t *testing.T, g *Graph, dir, behaviour string) (map[string][]MixinBound, []string, error) {
	t.Helper()
	src := strings.Replace(mixinTypes, "\t\"time\"\n", "\t\"context\"\n\t\"time\"\n\n\t\"github.com/syssam/velox\"\n", 1) +
		"\nvar _ context.Context\nvar _ velox.Dialect\n" + behaviour
	if err := os.WriteFile(filepath.Join(dir, "schema", "mixins.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return MixinBehaviour(dir, "./schema", g, offline())
}

// TestMixinMethodShapeRefused: a mixin's behaviour method that is generic,
// takes something, or returns another type, is refused at its line, and so
// is one on a mixin that is a generic type.
func TestMixinMethodShapeRefused(t *testing.T) {
	g, dir := mixinModule(t)
	for _, c := range []struct{ name, src, want string }{
		{"generic", "func (B) Policy[E any]() velox.Policy[E] { return velox.Policy[E]{} }\n",
			"B.Policy: behaviour methods are not generic: func (B) Policy() velox.Policy"},
		{"typed", "func (B) Policy() velox.Policy[int] { return velox.Policy[int]{} }\n",
			"B.Policy must take nothing and return velox.Policy; a mixin's method serves every entity that lists it: func (B) Policy() velox.Policy"},
		{"a parameter", "func (B) Interceptors(n int) []velox.Interceptor { return nil }\n",
			"B.Interceptors must take nothing and return []velox.Interceptor;"},
		{"another result", "func (B) Policy() *velox.Policy { return nil }\n",
			"B.Policy must take nothing and return velox.Policy;"},
		{"a result of another kind", "func (B) Hooks() []velox.Interceptor { return nil }\n",
			"B.Hooks must take nothing and return []velox.Hook;"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := mixinBehaviour(t, g, dir, c.src)
			var fe *FileError
			if !errors.As(err, &fe) || !strings.Contains(fe.Reason, c.want) || filepath.Base(fe.Path) != "mixins.go" || fe.Line == 0 {
				t.Fatalf("got %v, want a *FileError in mixins.go: %s", err, c.want)
			}
		})
	}
}

// TestMixinGenericTypeRefused: a mixin that is a generic type and declares
// behaviour is refused, as velox-gen cannot name its instance.
func TestMixinGenericTypeRefused(t *testing.T) {
	dir := module(t, map[string]string{"schema/s.go": `package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/mixin"
)

type G[T any] struct{ mixin.Schema }

func (G[T]) Policy() velox.Policy { return velox.Policy{} }

type Note struct{ schema.Schema }

func (Note) Mixin() []schema.Mixin { return []schema.Mixin{G[int]{}} }
`})
	m, err := Load(dir, "./schema", offline())
	if err != nil {
		t.Fatal(err)
	}
	g, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = MixinBehaviour(dir, "./schema", g, offline())
	var fe *FileError
	if !errors.As(err, &fe) || !strings.Contains(fe.Reason, "G is a generic type, whose instance velox-gen cannot name") || fe.Line != 11 {
		t.Fatalf("got %v", err)
	}
}

// policy is a Policy method for mixin m, which names org_id.
func policy(m string) string {
	return "func (" + m + ") Policy() velox.Policy {\n\t_ = velox.C(\"org_id\")\n\treturn velox.Policy{}\n}\n"
}

// TestMixinBehaviourOrder: an entity's sources are its mixins', in Mixin
// order, each with the kinds it declares, then its own; a mixin's method
// through an embedded struct counts, and one with a pointer receiver is
// called on a pointer.
func TestMixinBehaviourOrder(t *testing.T) {
	g, dir := mixinModule(t)
	mixins, warnings, err := mixinBehaviour(t, g, dir, policy("A")+
		"func (*A) Hooks() []velox.Hook { return nil }\n"+
		"type base struct{}\n\nfunc (base) Interceptors() []velox.Interceptor { return nil }\n"+
		"func (C) Policy() velox.Policy { return velox.Policy{} }\n")
	if err != nil || len(warnings) != 0 {
		t.Fatal(err, warnings)
	}
	// B declares nothing: an entity that lists only it has no mixin
	// behaviour.
	if _, ok := mixins["Label"]; ok {
		t.Errorf("Label: %+v", mixins["Label"])
	}
	o, err := Lower(g, Config{Package: "gen", GenPath: "example.com/tmp/gen", Bound: map[string]Bound{"Project": {Policy: true}}, Mixins: mixins})
	if err != nil {
		t.Fatal(err)
	}
	var p *OutEntity
	for _, e := range o.Entities {
		if e.Name == "Project" {
			p = e
		}
	}
	exprs := func(srcs []OutSource) []string {
		var out []string
		for _, s := range srcs {
			out = append(out, s.Expr)
		}
		return out
	}
	if got, want := exprs(p.Policy), []string{"sch.A{}.Policy", "sch.C{}.Policy", "sch.Project{}.Policy"}; !slices.Equal(got, want) {
		t.Errorf("Policy %v, want %v", got, want)
	}
	if got, want := exprs(p.Hooks), []string{"(&sch.A{}).Hooks"}; !slices.Equal(got, want) {
		t.Errorf("Hooks %v, want %v", got, want)
	}
	if len(p.Interceptors) != 0 {
		t.Errorf("Interceptors %v: base is not embedded", exprs(p.Interceptors))
	}

	// Embedded, base's Interceptors is C's.
	if err := os.WriteFile(filepath.Join(dir, "schema", "embed.go"), []byte("package schema\n\nimport \"github.com/syssam/velox\"\n\ntype base struct{}\n\nfunc (base) Interceptors() []velox.Interceptor { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := strings.Replace(mixinTypes, "type C struct{ mixin.Schema }", "type C struct {\n\tmixin.Schema\n\tbase\n}", 1)
	if err := os.WriteFile(filepath.Join(dir, "schema", "mixins.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	mixins, _, err = MixinBehaviour(dir, "./schema", g, offline())
	if err != nil {
		t.Fatal(err)
	}
	if ms := mixins["Project"]; len(ms) != 1 || ms[0].Name != "C" || ms[0].Index != 2 || len(ms[0].Methods) != 1 {
		t.Errorf("embedded: %+v", ms)
	}
}

// TestMixinNonZeroValueUsesMixinAt: a mixin value that is not its type's
// zero value is read with schema.MixinAt, at its index, from a new value of
// the schema.
func TestMixinNonZeroValueUsesMixinAt(t *testing.T) {
	g, dir := mixinModule(t)
	mixins, _, err := mixinBehaviour(t, g, dir, policy("A"))
	if err != nil {
		t.Fatal(err)
	}
	mixins["Project"][0].NonZero = true // as the loader reports A{By: "x"}
	o, err := Lower(g, Config{Package: "gen", GenPath: "example.com/tmp/gen", Mixins: mixins})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range o.Entities {
		if e.Name != "Project" {
			continue
		}
		if len(e.Policy) != 1 || e.Policy[0].Expr != "schema.MixinAt[sch.A](sch.Project{}, 0).Policy" || e.Policy[0].Imports["schema"] != schemaPkg {
			t.Errorf("%+v", e.Policy)
		}
	}

	// A schema that implements schema.Interface through a pointer is read
	// through one.
	for _, e := range g.Entities {
		e.Pointer = e.Name == "Project"
	}
	o, err = Lower(g, Config{Package: "gen", GenPath: "example.com/tmp/gen", Mixins: mixins})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range o.Entities {
		if e.Name == "Project" && (len(e.Policy) != 1 || e.Policy[0].Expr != "schema.MixinAt[sch.A](&sch.Project{}, 0).Policy") {
			t.Errorf("pointer: %+v", e.Policy)
		}
	}
}

// TestMixinColumnLiteralChecked: a velox.Field call in a mixin's method
// whose literal name an entity listing the mixin lacks, or whose type is
// not the column's, is refused at its line; a name that is not a literal
// is a warning, with its line.
func TestMixinColumnLiteralChecked(t *testing.T) {
	g, dir := mixinModule(t)
	lower := func(behaviour string) ([]string, error) {
		t.Helper()
		mixins, warnings, err := mixinBehaviour(t, g, dir, behaviour)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Lower(g, Config{Package: "gen", GenPath: "example.com/tmp/gen", Mixins: mixins})
		return warnings, err
	}
	method := func(call string) string {
		return "func (A) Interceptors() []velox.Interceptor {\n\t_ = " + call + "\n\treturn nil\n}\n"
	}
	// The method starts on line 28 of mixins.go; the call is on the next.
	for _, c := range []struct{ name, call, want string }{
		{"a right name and type", `velox.Field[E, int64]("org_id")`, ""},
		{"the id", `velox.Field[E, int64]("id")`, ""},
		{"a nullable time", `velox.Field[E, *time.Time]("deleted_at")`, ""},
		{"a wrong name", `velox.Field[E, int64]("org")`, `mixins.go:29: schema Project: velox.Field[E, int64]("org"): Project, which lists A, has no column "org"`},
		{"a wrong type", `velox.Field[E, string]("org_id")`, `mixins.go:29: schema Project: velox.Field[E, string]("org_id"): Project's column "org_id" is of type int64`},
		{"a pointer missing", `velox.Field[E, time.Time]("deleted_at")`, `Project's column "deleted_at" is of type *time.Time`},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := lower(method(c.call))
			switch {
			case c.want == "" && err != nil:
				t.Fatal(err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("got %v, want %s", err, c.want)
			}
		})
	}
	t.Run("not a literal", func(t *testing.T) {
		warnings, err := lower("const orgCol = \"org_id\"\n\n" + method(`velox.Field[E, int64](orgCol)`))
		if err != nil {
			t.Fatal(err)
		}
		if len(warnings) != 1 || !strings.HasSuffix(warnings[0], `mixins.go:31: velox.Field[E, int64](orgCol) names its column with a value velox-gen cannot read; velox checks it when it builds each entity's metadata`) {
			t.Errorf("warnings %q", warnings)
		}
	})
}

// TestMixinListedTwiceRefused: a mixin with behaviour that one entity lists
// twice would run twice.
func TestMixinListedTwiceRefused(t *testing.T) {
	g, dir := mixinModule(t)
	for _, e := range g.Entities {
		if e.Name == "Project" {
			e.Mixins[2] = e.Mixins[0]
		}
	}
	_, _, err := mixinBehaviour(t, g, dir, policy("A"))
	if err == nil || err.Error() != "schema Project: Mixin()[2] is A again, which declares behaviour: list a mixin once" {
		t.Fatalf("got %v", err)
	}
}

// TestMixinInOtherPackage: a mixin of another package of the module is
// read from its source, and the generated code imports its package under
// its name, or another when its name is taken.
func TestMixinInOtherPackage(t *testing.T) {
	dir := module(t, map[string]string{
		"tenancy/tenancy.go": `package tenancy

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/mixin"
)

type InOrg struct{ mixin.Schema }

func (InOrg) Policy() velox.Policy { return velox.Policy{} }
`,
		"schema/s.go": `package schema

import (
	"github.com/syssam/velox/schema"

	"example.com/tmp/tenancy"
)

type Note struct{ schema.Schema }

func (Note) Mixin() []schema.Mixin { return []schema.Mixin{tenancy.InOrg{}} }
`})
	m, err := Load(dir, "./schema", offline())
	if err != nil {
		t.Fatal(err)
	}
	g, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	mixins, _, err := MixinBehaviour(dir, "./schema", g, offline())
	if err != nil {
		t.Fatal(err)
	}
	if ms := mixins["Note"]; len(ms) != 1 || ms[0].PkgPath != "example.com/tmp/tenancy" || ms[0].PkgName != "tenancy" {
		t.Fatalf("%+v", ms)
	}
	files, err := Generate(g, Config{Package: "gen", GenPath: "example.com/tmp/gen", Mixins: mixins})
	if err != nil {
		t.Fatal(err)
	}
	note := fileText(t, files, "note_velox.go")
	for _, want := range []string{"\"example.com/tmp/tenancy\"\n", "Policy: []func() velox.Policy{tenancy.InOrg{}.Policy},"} {
		if !strings.Contains(squeeze(note), want) {
			t.Errorf("note_velox.go lacks %q:\n%s", want, note)
		}
	}
	// A mixin package named velox takes another name.
	mixins["Note"][0].PkgPath, mixins["Note"][0].PkgName = "example.com/tmp/velox", "velox"
	files, err = Generate(g, Config{Package: "gen", Mixins: mixins})
	if err != nil {
		t.Fatal(err)
	}
	note = fileText(t, files, "note_velox.go")
	for _, want := range []string{"velox2 \"example.com/tmp/velox\"\n", "Policy: []func() velox.Policy{velox2.InOrg{}.Policy},"} {
		if !strings.Contains(squeeze(note), want) {
			t.Errorf("note_velox.go lacks %q:\n%s", want, note)
		}
	}
}

// squeeze is s with each run of spaces one space, so that a test does not
// depend on gofmt's alignment.
func squeeze(s string) string { return regexp.MustCompile(` +`).ReplaceAllString(s, " ") }

// fileText returns the content of the file name of files.
func fileText(t *testing.T, files []File, name string) string {
	t.Helper()
	for _, f := range files {
		if f.Name == name {
			return string(f.Data)
		}
	}
	t.Fatalf("no file %s", name)
	return ""
}

// TestMixinBoundInEntityFile: a schema whose behaviour comes from mixins
// alone gets no package runtime: each entity's descriptor binds its mixins,
// and its file imports the schema package.
func TestMixinBoundInEntityFile(t *testing.T) {
	g, dir := mixinModule(t)
	mixins, _, err := mixinBehaviour(t, g, dir, policy("A"))
	if err != nil {
		t.Fatal(err)
	}
	files, err := Generate(g, Config{Package: "gen", Mixins: mixins})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasPrefix(f.Name, "runtime/") || f.Name == "runtime_velox.go" {
			t.Errorf("wrote %s", f.Name)
		}
	}
	p := fileText(t, files, "project_velox.go")
	for _, want := range []string{"sch \"example.com/tmp/schema\"\n", "Policy: []func() velox.Policy{sch.A{}.Policy},"} {
		if !strings.Contains(squeeze(p), want) {
			t.Errorf("project_velox.go lacks %q", want)
		}
	}
}

// TestMixinMethodRefusesGeneric: a mixin's generic method, the shape of an
// older velox, is refused with the shape velox binds now.
func TestMixinMethodRefusesGeneric(t *testing.T) {
	g, dir := mixinModule(t)
	_, _, err := mixinBehaviour(t, g, dir, "func (A) Policy[E any]() velox.Policy[E] { return velox.Policy[E]{} }\n")
	if err == nil || !strings.Contains(err.Error(), "A.Policy: behaviour methods are not generic: func (A) Policy() velox.Policy") {
		t.Fatalf("got %v", err)
	}
	_, _, err = mixinBehaviour(t, g, dir, "func (A) Hooks() []velox.Hook[int] { return nil }\n")
	if err == nil || !strings.Contains(err.Error(), "A.Hooks must take nothing and return []velox.Hook; a mixin's method serves every entity that lists it: func (A) Hooks() []velox.Hook") {
		t.Fatalf("typed: got %v", err)
	}
}

// canonicalType qualifies each named type by its import path, spells byte
// and rune as uint8 and int32, and gives "" for a type it does not read.
func TestCanonicalType(t *testing.T) {
	qual := func(n string) string { return map[string]string{"time": "time", "j": "encoding/json"}[n] }
	for src, want := range map[string]string{
		"int64": "int64", "byte": "uint8", "[]rune": "[]int32", "*time.Time": "*time.Time",
		"[]Tag": "[]example.com/x.Tag", "map[string]any": "map[string]any", "interface{}": "any",
		"j.RawMessage": "encoding/json.RawMessage", "x.Y": "", "func()": "", "[2]int": "",
		"interface{ M() }": "", "map[string]func()": "", "*func()": "", "[]func()": "", "a.b.C": "",
	} {
		x, err := parser.ParseExpr(src)
		if err != nil {
			t.Fatal(err)
		}
		if got := canonicalType(x, qual, "example.com/x"); got != want {
			t.Errorf("%s: %q, want %q", src, got, want)
		}
	}
}

// localName and importPath read a mixin's source.
func TestMixinSourceHelpers(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "f.go", `package p

import (
	v "github.com/syssam/velox"
	. "example.com/dot"
	_ "example.com/blank"
	"example.com/plain"
	"example.com/go-thing"
)
`, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"github.com/syssam/velox": "v", "example.com/dot": "", "example.com/blank": "", "example.com/plain": "plain", "example.com/none": ""} {
		if got := localName(f, path); got != want {
			t.Errorf("localName(%s) = %q, want %q", path, got, want)
		}
	}
	for local, want := range map[string]string{"v": "github.com/syssam/velox", "plain": "example.com/plain", "velox": "", "thing": "example.com/go-thing", "none": ""} {
		if got := importPath(f, local, nil); got != want {
			t.Errorf("importPath(%s) = %q, want %q", local, got, want)
		}
	}
}

// mixinSourceB is mixinTypes with B declared as decl, velox imported, and
// behaviour after it.
func mixinSourceB(decl, behaviour string) string {
	src := strings.Replace(mixinTypes, "type B struct{ mixin.Schema }", decl, 1)
	return strings.Replace(src, "\t\"time\"\n", "\t\"context\"\n\t\"time\"\n\n\t\"github.com/syssam/velox\"\n", 1) +
		"\nvar _ context.Context\nvar _ velox.Dialect\n" + behaviour
}

// mixinBehaviourB is mixinBehaviour with B declared as decl.
func mixinBehaviourB(t *testing.T, g *Graph, dir, decl, behaviour string) (map[string][]MixinBound, []string, error) {
	t.Helper()
	src := mixinSourceB(decl, behaviour)
	if err := os.WriteFile(filepath.Join(dir, "schema", "mixins.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return MixinBehaviour(dir, "./schema", g, offline())
}

// TestMixinResultRefused: a behaviour method's result must be a slice of
// velox's type, not an array, and velox's type, not another package's of
// that name.
func TestMixinResultRefused(t *testing.T) {
	g, dir := mixinModule(t)
	for _, c := range []struct{ name, src, want string }{
		{"an array", "func (B) Hooks() [2]velox.Hook { return [2]velox.Hook{} }\n", "B.Hooks must take nothing and return []velox.Hook;"},
		{"another package", "func (B) Policy() context.Policy { return nil }\n", "B.Policy must take nothing and return velox.Policy;"},
		{"unqualified", "func (B) Policy() Policy { return nil }\n", "B.Policy must take nothing and return velox.Policy;"},
		{"a parameter", "func (B) Policy(int) velox.Policy { return velox.Policy{} }\n", "B.Policy must take nothing and return velox.Policy;"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := mixinBehaviour(t, g, dir, c.src)
			var fe *FileError
			if !errors.As(err, &fe) || !strings.Contains(fe.Reason, c.want) {
				t.Fatalf("got %v, want %s", err, c.want)
			}
		})
	}
}

// TestMixinBodyReadsOnlyVeloxField: a call in a method's body is a column
// check only when it is velox.Field with two type arguments and one
// argument; a method without a body names no column.
func TestMixinBodyReadsOnlyVeloxField(t *testing.T) {
	g, dir := mixinModule(t)
	mixins, warnings, err := mixinBehaviour(t, g, dir, `func pair[A, B any](string) {}

func (B) Policy() velox.Policy {
	pair[int, int64]("org_id")
	_ = velox.Field[int, int64]
	_ = velox.Desc[int, int64]("org_id")
	return velox.Policy{}
}

func (B) Hooks() []velox.Hook
`)
	if err != nil || len(warnings) != 0 {
		t.Fatal(err, warnings)
	}
	ms := mixins["Label"]
	if len(ms) != 1 || len(ms[0].Methods) != 2 || len(ms[0].Fields) != 0 {
		t.Fatalf("%+v", ms)
	}
}

// TestMixinEmbeddedMethods: a mixin has the behaviour methods of a struct
// it embeds, by value or through a pointer, and their shape is checked; its
// own method of a kind hides an embedded one, whose shape is not read; an
// embedded interface or predeclared type adds nothing.
func TestMixinEmbeddedMethods(t *testing.T) {
	g, dir := mixinModule(t)
	const inner = "type inner struct{}\n\n"
	t.Run("through a pointer", func(t *testing.T) {
		mixins, _, err := mixinBehaviourB(t, g, dir, "type B struct {\n\tmixin.Schema\n\t*inner\n}", inner+policy("inner"))
		if ms := mixins["Label"]; err != nil || len(ms) != 1 || len(ms[0].Methods) != 1 {
			t.Fatalf("%+v %v", ms, err)
		}
	})
	t.Run("of a wrong shape", func(t *testing.T) {
		_, _, err := mixinBehaviourB(t, g, dir, "type B struct {\n\tmixin.Schema\n\tinner\n}",
			inner+"func (inner) Policy[E any]() velox.Policy[E] { return velox.Policy[E]{} }\n")
		var fe *FileError
		if !errors.As(err, &fe) || !strings.Contains(fe.Reason, "inner.Policy: behaviour methods are not generic") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("hidden by the mixin's own", func(t *testing.T) {
		mixins, _, err := mixinBehaviourB(t, g, dir, "type B struct {\n\tmixin.Schema\n\tinner\n}",
			inner+"func (inner) Policy[E any]() velox.Policy[E] { return velox.Policy[E]{} }\n\n"+
				"func (*B) Policy() velox.Policy { return velox.Policy{} }\n")
		if ms := mixins["Label"]; err != nil || len(ms) != 1 || !ms[0].Methods["Policy"] {
			t.Fatalf("%+v %v", ms, err)
		}
	})
	t.Run("an interface and error", func(t *testing.T) {
		mixins, _, err := mixinBehaviourB(t, g, dir, "type B struct {\n\tmixin.Schema\n\tnamer\n\terror\n}",
			"type namer interface{ Name() string }\n")
		if _, ok := mixins["Label"]; err != nil || ok {
			t.Fatalf("%+v %v", mixins, err)
		}
	})
}

// TestMixinEmbedsOtherPackage: a mixin's struct embedded from another
// package is read from that package, found by its package name when that
// is not its path's last element, and its method's shape is checked there.
func TestMixinEmbedsOtherPackage(t *testing.T) {
	g, dir := mixinModule(t)
	if err := os.MkdirAll(filepath.Join(dir, "orgsrc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "orgsrc", "o.go"), []byte("package orgs\n\nimport \"github.com/syssam/velox\"\n\ntype Base struct{}\n\nfunc (Base) Hooks[E any]() []velox.Hook[E] { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := strings.Replace(mixinSourceB("type B struct {\n\tmixin.Schema\n\torgs.Base\n}", ""),
		"\t\"github.com/syssam/velox\"\n", "\t\"github.com/syssam/velox\"\n\t\"example.com/tmp/orgsrc\"\n", 1)
	if err := os.WriteFile(filepath.Join(dir, "schema", "mixins.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := MixinBehaviour(dir, "./schema", g, offline())
	var fe *FileError
	if !errors.As(err, &fe) || filepath.Base(fe.Path) != "o.go" || fe.Line != 7 || !strings.Contains(fe.Reason, "Base.Hooks: behaviour methods are not generic") {
		t.Fatalf("got %v", err)
	}
}

// TestMixinPackageErrors: a mixin package go list cannot find, a schema
// package pattern it cannot list and a mixin file only some builds include
// are errors; a mixin of no named type is passed over.
func TestMixinPackageErrors(t *testing.T) {
	t.Run("a missing package", func(t *testing.T) {
		g, dir := mixinModule(t)
		for _, e := range g.Entities {
			if e.Name == "Label" {
				e.Mixins = append(e.Mixins, loadjson.Mixin{PkgPath: "example.com/tmp/gone", Name: "M"})
			}
		}
		_, _, err := mixinBehaviour(t, g, dir, "")
		if err == nil || !strings.HasPrefix(err.Error(), "finding mixin package example.com/tmp/gone: ") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("a bad pattern", func(t *testing.T) {
		g, dir := mixinModule(t)
		if _, _, err := MixinBehaviour(dir, "./gone", g, offline()); err == nil || !strings.Contains(err.Error(), "gone") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("go list fails", func(t *testing.T) {
		_, err := listDirs(filepath.Join(t.TempDir(), "gone"), []string{"example.com/x"}, offline())
		if err == nil || !strings.HasPrefix(err.Error(), "go list example.com/x: ") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("a file of some builds", func(t *testing.T) {
		g, dir := mixinModule(t)
		if err := os.WriteFile(filepath.Join(dir, "schema", "linux.go"), []byte("//go:build linux\n\npackage schema\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, _, err := mixinBehaviour(t, g, dir, policy("A"))
		var fe *FileError
		if !errors.As(err, &fe) || filepath.Base(fe.Path) != "linux.go" || !strings.Contains(fe.Reason, "go leaves it out of some builds") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("a mixin of no name", func(t *testing.T) {
		g, dir := mixinModule(t)
		for _, e := range g.Entities {
			if e.Name == "Label" {
				e.Mixins = append([]loadjson.Mixin{{}}, e.Mixins...)
			}
		}
		mixins, _, err := mixinBehaviour(t, g, dir, policy("B"))
		if ms := mixins["Label"]; err != nil || len(ms) != 1 || ms[0].Name != "B" || ms[0].Index != 1 {
			t.Fatalf("%+v %v", ms, err)
		}
	})
}

// TestRecvPtr: only a method with one pointer receiver has a pointer
// receiver.
func TestRecvPtr(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "f.go", "package p\n\nfunc F() {}\n\nfunc (T) V() {}\n\nfunc (*T) P() {}\n", 0)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []bool{false, false, true} {
		if got := recvPtr(f.Decls[i].(*ast.FuncDecl)); got != want {
			t.Errorf("decl %d: %v", i, got)
		}
	}
}

// TestMixinPackageNames: the generated code names another package's mixin
// package by its name, once per package, or by its name and a number when
// the generated package, or another mixin package, has that name; a
// velox.Field call whose type velox-gen cannot read checks only the name.
func TestMixinPackageNames(t *testing.T) {
	g, _ := mixinModule(t)
	bound := func(path, name string, i int, fields ...MixinField) MixinBound {
		return MixinBound{Index: i, PkgPath: path, PkgName: guessName(path), Name: name, Methods: map[string]bool{"Policy": false}, Fields: fields}
	}
	unread := MixinField{Pos: "m.go:1", Call: `velox.Field[E, func()]("org_id")`, Name: "org_id"}
	o, err := Lower(g, Config{Package: "gen", Mixins: map[string][]MixinBound{
		"Project": {bound("example.com/a/gen", "M", 0, unread), bound("example.com/b/gen", "N", 1)},
		"Label":   {bound("example.com/a/gen", "M", 0)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range o.Entities {
		var got []string
		for _, s := range e.Policy {
			got = append(got, s.Expr)
		}
		want := map[string][]string{"Project": {"gen2.M{}.Policy", "gen3.N{}.Policy"}, "Label": {"gen2.M{}.Policy"}}[e.Name]
		if !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", e.Name, got, want)
		}
		if e.Name == "Project" && (e.ImportAs["example.com/a/gen"] != "gen2" || e.ImportAs["example.com/b/gen"] != "gen3") {
			t.Errorf("ImportAs %v", e.ImportAs)
		}
	}
}
