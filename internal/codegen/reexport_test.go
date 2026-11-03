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
	f, ok, err := Reexport("x_velox.go", []byte(reexportSrc), "gen", "example.com/tmp/gen/entity")
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	got := string(f.Data)
	for _, want := range []string{
		"type Page[T any, K comparable] = entity.Page[T, K]",
		"// Page is a generic type.\n",
		"type Plain = entity.Plain",
		"// A is one of a group.\ntype A = entity.A",
		"// The levels.\nconst (\n\t// Low is low.\n\tLow  = entity.Low\n\tHigh = entity.High\n)",
		"// Max is alone.\nconst Max = entity.Max",
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
	if _, ok, err := Reexport("y_velox.go", []byte("package entity\n\nfunc (Plain) M() {}\n"), "gen", "example.com/tmp/gen/entity"); ok || err != nil {
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
	if _, _, err := Reexport("z_velox.go", []byte(src), "gen", "m/gen/entity"); err == nil || !strings.Contains(err.Error(), "the generic function G names an unexported type in its signature") {
		t.Fatalf("got %v", err)
	}
}
