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
func mixinBehaviour(t *testing.T, g *Graph, dir, behaviour string) (map[string][]MixinBound, error) {
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
			_, err := mixinBehaviour(t, g, dir, c.src)
			if fe, ok := errors.AsType[*FileError](err); !ok || !strings.Contains(fe.Reason, c.want) || filepath.Base(fe.Path) != "mixins.go" || fe.Line == 0 {
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
	_, err = MixinBehaviour(dir, "./schema", g, offline())
	if fe, ok := errors.AsType[*FileError](err); !ok || !strings.Contains(fe.Reason, "G is a generic type, whose instance velox-gen cannot name") || fe.Line != 11 {
		t.Fatalf("got %v", err)
	}
}

// policy is a Policy method for mixin m, which names org_id.
func policy(m string) string {
	return "func (" + m + ") Policy() velox.Policy {\n\t_ = velox.FieldEQ(\"org_id\", 1)\n\treturn velox.Policy{}\n}\n"
}

// TestMixinBehaviourOrder: an entity's sources are its mixins', in Mixin
// order, each with the kinds it declares, then its own; a mixin's method
// through an embedded struct counts, and one with a pointer receiver is
// called on a pointer.
func TestMixinBehaviourOrder(t *testing.T) {
	g, dir := mixinModule(t)
	mixins, err := mixinBehaviour(t, g, dir, policy("A")+
		"func (*A) Hooks() []velox.Hook { return nil }\n"+
		"type base struct{}\n\nfunc (base) Interceptors() []velox.Interceptor { return nil }\n"+
		"func (C) Policy() velox.Policy { return velox.Policy{} }\n")
	if err != nil {
		t.Fatal(err)
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
	var names []string
	for _, mb := range mixins["Project"] {
		names = append(names, mb.Name)
	}
	if want := []string{"A", "C"}; !slices.Equal(names, want) {
		t.Errorf("mixins %v, want %v", names, want)
	}
	if a := mixins["Project"][0].Methods; len(a) != 2 || !a["Hooks"] || a["Policy"] {
		t.Errorf("A's methods %v", a)
	}
	if p.Schema != "schema.Project{}" || !p.NeedsSch {
		t.Errorf("Schema %q, NeedsSch %v", p.Schema, p.NeedsSch)
	}

	// Embedded, base's Interceptors is C's.
	if err := os.WriteFile(filepath.Join(dir, "schema", "embed.go"), []byte("package schema\n\nimport \"github.com/syssam/velox\"\n\ntype base struct{}\n\nfunc (base) Interceptors() []velox.Interceptor { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := strings.Replace(mixinTypes, "type C struct{ mixin.Schema }", "type C struct {\n\tmixin.Schema\n\tbase\n}", 1)
	if err := os.WriteFile(filepath.Join(dir, "schema", "mixins.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	mixins, err = MixinBehaviour(dir, "./schema", g, offline())
	if err != nil {
		t.Fatal(err)
	}
	if ms := mixins["Project"]; len(ms) != 1 || ms[0].Name != "C" || len(ms[0].Methods) != 1 {
		t.Errorf("embedded: %+v", ms)
	}
}

// TestSchemaValueOfMixinBehaviour: an entity whose only behaviour is a
// mixin's names its schema, through a pointer when the schema implements
// schema.Interface through one; velox reads the mixin's values from it.
func TestSchemaValueOfMixinBehaviour(t *testing.T) {
	g, dir := mixinModule(t)
	mixins, err := mixinBehaviour(t, g, dir, policy("A"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ptr := range []bool{false, true} {
		for _, e := range g.Entities {
			e.Pointer = ptr && e.Name == "Project"
		}
		o, err := Lower(g, Config{Package: "gen", GenPath: "example.com/tmp/gen", Mixins: mixins})
		if err != nil {
			t.Fatal(err)
		}
		want := "schema.Project{}"
		if ptr {
			want = "&" + want
		}
		for _, e := range o.Entities {
			if e.Name == "Project" && e.Schema != want {
				t.Errorf("pointer %v: Schema %q, want %q", ptr, e.Schema, want)
			}
			if e.Name == "Label" && e.Schema != "" {
				t.Errorf("Label, whose mixin declares nothing: Schema %q", e.Schema)
			}
		}
	}
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
	_, err := mixinBehaviour(t, g, dir, policy("A"))
	if ge, ok := errors.AsType[*Error](err); !ok || ge.Pos == "" || plain(err) != "schema Project: Mixin()[2] is A again, which declares behaviour: list a mixin once" {
		t.Fatalf("got %v", err)
	}
}

// TestMixinInOtherPackage: a mixin of another package of the module is
// read from its source. The generated code names the schema, whose Mixin
// method velox reads, and does not import the mixin's package.
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
	mixins, err := MixinBehaviour(dir, "./schema", g, offline())
	if err != nil {
		t.Fatal(err)
	}
	if ms := mixins["Note"]; len(ms) != 1 || ms[0].PkgPath != "example.com/tmp/tenancy" || ms[0].Name != "InOrg" {
		t.Fatalf("%+v", ms)
	}
	files, err := Generate(g, Config{Package: "gen", GenPath: "example.com/tmp/gen", Mixins: mixins})
	if err != nil {
		t.Fatal(err)
	}
	note := fileText(t, files, "note.go")
	for _, want := range []string{"\"example.com/tmp/schema\"\n", "Schema: schema.Note{},"} {
		if !strings.Contains(squeeze(note), want) {
			t.Errorf("note.go lacks %q:\n%s", want, note)
		}
	}
	if strings.Contains(note, "tenancy") {
		t.Errorf("note.go names the mixin's package:\n%s", note)
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
// alone gets no package runtime: each entity's descriptor names its schema,
// and its file imports the schema package.
func TestMixinBoundInEntityFile(t *testing.T) {
	g, dir := mixinModule(t)
	mixins, err := mixinBehaviour(t, g, dir, policy("A"))
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
	p := fileText(t, files, "project.go")
	for _, want := range []string{"\"example.com/tmp/schema\"\n", "Schema: schema.Project{},"} {
		if !strings.Contains(squeeze(p), want) {
			t.Errorf("project.go lacks %q", want)
		}
	}
}

// TestMixinMethodRefusesGeneric: a mixin's generic method, the shape of an
// older velox, is refused with the shape velox binds now.
func TestMixinMethodRefusesGeneric(t *testing.T) {
	g, dir := mixinModule(t)
	_, err := mixinBehaviour(t, g, dir, "func (A) Policy[E any]() velox.Policy[E] { return velox.Policy[E]{} }\n")
	if err == nil || !strings.Contains(err.Error(), "A.Policy: behaviour methods are not generic: func (A) Policy() velox.Policy") {
		t.Fatalf("got %v", err)
	}
	_, err = mixinBehaviour(t, g, dir, "func (A) Hooks() []velox.Hook[int] { return nil }\n")
	if err == nil || !strings.Contains(err.Error(), "A.Hooks must take nothing and return []velox.Hook; a mixin's method serves every entity that lists it: func (A) Hooks() []velox.Hook") {
		t.Fatalf("typed: got %v", err)
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
func mixinBehaviourB(t *testing.T, g *Graph, dir, decl, behaviour string) (map[string][]MixinBound, error) {
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
			_, err := mixinBehaviour(t, g, dir, c.src)
			if fe, ok := errors.AsType[*FileError](err); !ok || !strings.Contains(fe.Reason, c.want) {
				t.Fatalf("got %v, want %s", err, c.want)
			}
		})
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
		mixins, err := mixinBehaviourB(t, g, dir, "type B struct {\n\tmixin.Schema\n\t*inner\n}", inner+policy("inner"))
		if ms := mixins["Label"]; err != nil || len(ms) != 1 || len(ms[0].Methods) != 1 {
			t.Fatalf("%+v %v", ms, err)
		}
	})
	t.Run("of a wrong shape", func(t *testing.T) {
		_, err := mixinBehaviourB(t, g, dir, "type B struct {\n\tmixin.Schema\n\tinner\n}",
			inner+"func (inner) Policy[E any]() velox.Policy[E] { return velox.Policy[E]{} }\n")
		if fe, ok := errors.AsType[*FileError](err); !ok || !strings.Contains(fe.Reason, "inner.Policy: behaviour methods are not generic") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("hidden by the mixin's own", func(t *testing.T) {
		mixins, err := mixinBehaviourB(t, g, dir, "type B struct {\n\tmixin.Schema\n\tinner\n}",
			inner+"func (inner) Policy[E any]() velox.Policy[E] { return velox.Policy[E]{} }\n\n"+
				"func (*B) Policy() velox.Policy { return velox.Policy{} }\n")
		if ms := mixins["Label"]; err != nil || len(ms) != 1 || !ms[0].Methods["Policy"] {
			t.Fatalf("%+v %v", ms, err)
		}
	})
	t.Run("an interface and error", func(t *testing.T) {
		mixins, err := mixinBehaviourB(t, g, dir, "type B struct {\n\tmixin.Schema\n\tnamer\n\terror\n}",
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
	_, err := MixinBehaviour(dir, "./schema", g, offline())
	if fe, ok := errors.AsType[*FileError](err); !ok || filepath.Base(fe.Path) != "o.go" || fe.Line != 7 || !strings.Contains(fe.Reason, "Base.Hooks: behaviour methods are not generic") {
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
		_, err := mixinBehaviour(t, g, dir, "")
		if err == nil || !strings.HasPrefix(err.Error(), "finding mixin package example.com/tmp/gone: ") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("a bad pattern", func(t *testing.T) {
		g, dir := mixinModule(t)
		if _, err := MixinBehaviour(dir, "./gone", g, offline()); err == nil || !strings.Contains(err.Error(), "gone") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("go list fails", func(t *testing.T) {
		_, err := NewCache().listDirs(filepath.Join(t.TempDir(), "gone"), []string{"example.com/x"}, offline())
		if err == nil || !strings.HasPrefix(err.Error(), "go list example.com/x: ") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("a file of some builds", func(t *testing.T) {
		g, dir := mixinModule(t)
		if err := os.WriteFile(filepath.Join(dir, "schema", "linux.go"), []byte("//go:build linux\n\npackage schema\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := mixinBehaviour(t, g, dir, policy("A"))
		if fe, ok := errors.AsType[*FileError](err); !ok || filepath.Base(fe.Path) != "linux.go" || !strings.Contains(fe.Reason, "go leaves it out of some builds") {
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
		mixins, err := mixinBehaviour(t, g, dir, policy("B"))
		if ms := mixins["Label"]; err != nil || len(ms) != 1 || ms[0].Name != "B" {
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

// TestSchemaPackageNames: the generated code names the schema package by
// its name, and by userschema when the generated code uses that name for
// another package; velox's schema package is veloxschema when the schema
// package is named schema.
func TestSchemaPackageNames(t *testing.T) {
	g, _ := mixinModule(t)
	for _, c := range []struct {
		path, name, want, vschema string
	}{
		{"example.com/tmp/schema", "", "schema", "veloxschema"},
		{"example.com/tmp/models", "", "models", "schema"},
		{"example.com/tmp/schema", "models", "models", "schema"},
		{"example.com/tmp/gen", "", "userschema", "schema"},
		{"example.com/tmp/velox", "", "userschema", "schema"},
	} {
		l := &lowerer{g: g, cfg: Config{SchemaName: c.name}, out: &Out{Package: "gen", SchemaPath: c.path},
			owned: map[string]string{"velox": veloxPkg}}
		l.schemaNames()
		if l.out.SchemaName != c.want || l.vschema != c.vschema {
			t.Errorf("%s %q: %s and %s, want %s and %s", c.path, c.name, l.out.SchemaName, l.vschema, c.want, c.vschema)
		}
	}
	o, err := Lower(g, Config{Package: "gen", SchemaPath: "example.com/tmp/schema", SchemaName: "models", Bound: map[string]Bound{"Project": {Policy: true}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range o.Entities {
		if e.Name == "Project" && e.Schema != "models.Project{}" {
			t.Errorf("Schema %q", e.Schema)
		}
	}
	files, err := Render(o)
	if err != nil {
		t.Fatal(err)
	}
	if p := fileText(t, files, "project.go"); !strings.Contains(p, "models \"example.com/tmp/schema\"") {
		t.Errorf("project.go:\n%s", p)
	}
}

func TestGuessName(t *testing.T) {
	for p, want := range map[string]string{
		"example.com/x":         "x",
		"example.com/x/v2":      "x",
		"gopkg.in/yaml.v3":      "yaml",
		"github.com/a/go-thing": "thing",
		"example.com/a-b":       "a_b",
		"v2":                    "v2",
	} {
		if got := guessName(p); got != want {
			t.Errorf("guessName(%q) = %q, want %q", p, got, want)
		}
	}
}
