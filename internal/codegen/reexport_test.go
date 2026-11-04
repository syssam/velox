package codegen

import (
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox/internal/tempmod"
)

// reexportSrc is a file of package entity with every kind of exported
// declaration Reexport handles.
const reexportSrc = `package entity

import (
	"context"
	vx "strings"
)

// Page is a generic type.
type Page[T any, K comparable] struct {
	Items []T
	Key   K
}

// Len is a method, which comes with the alias.
func (p Page[T, K]) Len() int { return len(p.Items) }

type inner struct{}

// Plain is a plain type.
type Plain struct{ N int }

type (
	// A is one of a group.
	A int
	b int
)

// The levels.
const (
	// Low is low.
	Low A = iota
	High
	hidden
)

// Max is alone.
const Max = 9

var One, two, Three = 1, 2, 3

// Show is generic.
func Show[T any](p *T) any { return p }

// Join is variadic and names its import.
func Join(sep string, xs ...string) string { return vx.Join(xs, sep) }

// Ctx has unnamed parameters and named results.
func Ctx(context.Context, int) (n int, err error) { return 0, nil }

// Named has a blank parameter, and one named like the package.
func Named(_ string, entity bool) bool { return entity }

// Make names an unexported type.
func Make(inner) int { return 0 }

// Nothing returns nothing.
func Nothing() {}

// Build names a package by its import name.
func Build(b *vx.Builder) string { return b.String() }

func unexported() {}
`

// Reexport writes, for each exported name, the declaration gen re-exports it
// with, keeping the doc comments; a program that uses every re-export
// through gen compiles and gets entity's values.
func TestReexport(t *testing.T) {
	f, ok, err := Reexport("x_velox.go", []File{{Name: "x_velox.go", Data: []byte(reexportSrc)}}, "gen", "example.com/tmp/gen/entity")
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	got := string(f.Data)
	for _, want := range []string{
		"type (\n\t// Page is a generic type.\n\tPage[T any, K comparable] = entity.Page[T, K]\n",
		"\n\t// Plain is a plain type.\n\tPlain = entity.Plain\n",
		"\n\t// A is one of a group.\n\tA = entity.A\n)",
		"// Max is alone.\nconst Max = entity.Max\n\n// The levels.\nconst (\n\t// Low is low.\n\tLow  = entity.Low\n\tHigh = entity.High\n)",
		"var (\n\tOne   = entity.One\n\tThree = entity.Three\n)",
		"func Show[T any](p *T) any {\n\treturn entity.Show[T](p)\n}",
		"func Join(sep string, xs ...string) string {\n\treturn entity.Join(sep, xs...)\n}",
		"func Ctx(p0 context.Context, p1 int) (int, error) {\n\treturn entity.Ctx(p0, p1)\n}",
		"func Named(p0 string, p1 bool) bool {\n\treturn entity.Named(p0, p1)\n}",
		"// Make names an unexported type.\nvar Make = entity.Make",
		"func Nothing() {\n\tentity.Nothing()\n}",
		`vx "strings"`,
		"func Build(b *vx.Builder) string {\n\treturn entity.Build(b)\n}",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	for _, not := range []string{"hidden", "func unexported", "Len", "inner", "two", " b "} {
		if strings.Contains(got, not) {
			t.Errorf("re-exports %s:\n%s", not, got)
		}
	}
	names, err := ReexportedNames("x_velox.go", []byte(reexportSrc))
	if want := []string{"A", "Build", "Ctx", "High", "Join", "Low", "Make", "Max", "Named", "Nothing", "One", "Page", "Plain", "Show", "Three"}; err != nil || !slices.Equal(slices.Sorted(slices.Values(names)), want) {
		t.Errorf("names %v %v, want %v", names, err, want)
	}
	// A file that exports nothing has no re-export.
	if _, ok, err := Reexport("y_velox.go", []File{{Name: "y_velox.go", Data: []byte("package entity\n\nfunc (Plain) M() {}\n")}}, "gen", "example.com/tmp/gen/entity"); ok || err != nil {
		t.Errorf("a file of methods: %v %v", ok, err)
	}
	if testing.Short() {
		return
	}
	dir := module(t, map[string]string{
		"gen/entity/x_velox.go": reexportSrc,
		"gen/x_velox.go":        got,
		"use/use.go": `package use

import (
	"context"

	"example.com/tmp/gen"
)

// Use calls every re-export.
func Use() (int, any, string) {
	p := gen.Page[int, string]{Items: []int{1}}
	var a gen.A = gen.High
	n, _ := gen.Ctx(context.Background(), 1)
	_ = gen.Named("", true)
	gen.Nothing()
	_ = gen.Plain{N: gen.Max}
	_ = gen.Make
	return p.Len() + int(a) + n + gen.One + gen.Three, gen.Show(&p), gen.Join(",", "a", "b")
}
`,
	})
	if out, err := tempmod.Go(dir, "vet", "./..."); err != nil {
		t.Fatalf("go vet:\n%s", out)
	}
}

// A generic function whose signature names an unexported type cannot be
// re-exported, and says so.
func TestReexportRefusesAGenericOfAnUnexportedType(t *testing.T) {
	src := "package entity\n\ntype inner struct{}\n\n// G is generic.\nfunc G[T any](inner) T { var t T; return t }\n"
	if _, _, err := Reexport("z_velox.go", []File{{Name: "z_velox.go", Data: []byte(src)}}, "gen", "m/gen/entity"); err == nil || !strings.Contains(err.Error(), "the generic function G names an unexported type in its signature") {
		t.Fatalf("got %v", err)
	}
}

// The re-exports of many files are one file: one type declaration, one
// const and one var declaration, in the order of the files, then the
// functions. Two files that import different packages by one name are
// refused.
func TestReexportMany(t *testing.T) {
	a := File{Name: "a_velox.go", Data: []byte("package entity\n\nimport \"strings\"\n\n// A is a.\ntype A int\n\n// As is a's.\nvar As = 1\n\n// F joins.\nfunc F(b *strings.Builder) {}\n")}
	b := File{Name: "b_velox.go", Data: []byte("package entity\n\n// B is b.\ntype B int\n\n// Bs is b's.\nvar Bs = 2\n\n// K is b's const.\nconst K = 3\n")}
	f, ok, err := Reexport("p_velox.go", []File{a, b}, "gen", "m/gen/entity")
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	want := "type (\n\t// A is a.\n\tA = entity.A\n\n\t// B is b.\n\tB = entity.B\n)\n\n" +
		"// K is b's const.\nconst K = entity.K\n\n" +
		"var (\n\t// As is a's.\n\tAs = entity.As\n\n\t// Bs is b's.\n\tBs = entity.Bs\n)\n\n" +
		"// F joins.\nfunc F(b *strings.Builder) {\n\tentity.F(b)\n}\n"
	if got := string(f.Data); !strings.HasSuffix(got, want) || !strings.Contains(got, "\t\"strings\"\n") {
		t.Errorf("got\n%s\nwant the suffix\n%s", got, want)
	}
	c := File{Name: "c_velox.go", Data: []byte("package entity\n\nimport strings \"example.com/strings\"\n\n// G is g.\nfunc G(*strings.Builder) {}\n")}
	if _, _, err := Reexport("p_velox.go", []File{a, c}, "gen", "m/gen/entity"); err == nil || !strings.Contains(err.Error(), "c_velox.go: imports example.com/strings as strings") {
		t.Fatalf("two packages named strings: %v", err)
	}
}
