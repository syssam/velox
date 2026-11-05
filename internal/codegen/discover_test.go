package codegen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"maps"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syssam/velox/internal/codegen/loadjson"
)

// candidates keeps the types that may be schemas: a struct with an embedded
// field, at any depth or from another package, an alias, a defined type of
// another named type (Level too, which classify then passes over) and a
// type that declares Type. It refuses an unexported struct that embeds schema.Schema
// when no type embeds it, and passes an unexported base.
func TestCandidates(t *testing.T) {
	files := map[string][]byte{
		"a.go": []byte(`package s
import vs "github.com/syssam/velox/schema"
type User struct{ vs.Schema }
type notASchema struct{ Name string }
type base struct{ vs.Schema }
type Derived struct{ base }
type Other struct{ other.Base }
type Own struct{}
func (Own) Type() {}
type Plain struct{ Name string }
`),
		"b.go": []byte(`package s
import "github.com/syssam/velox/schema"
type Post struct {
	schema.Schema
}
type Alias = Post
type Ptr struct{ *schema.Schema }
type Gen[T any] struct{ schema.Schema }
type Release Post
type Draft other.Base
type Level int
type ids []int
`),
	}
	got, err := candidates(files)
	if want := "Alias,Derived,Draft,Gen,Level,Other,Own,Post,Ptr,Release,User"; err != nil || strings.Join(got, ",") != want {
		t.Fatalf("got %v %v, want %s", got, err, want)
	}
	_, err = candidates(map[string][]byte{"c.go": []byte("package s\nimport \"github.com/syssam/velox/schema\"\ntype user struct{ schema.Schema }\ntype deeper struct{ user }\n")})
	if want := "c.go:4: type deeper embeds schema.Schema and is unexported: a schema must be exported, and a shared base is embedded by one"; err == nil || err.Error() != want {
		t.Fatalf("got %v, want %s", err, want)
	}
}

func TestDecodeModel(t *testing.T) {
	m, err := decodeModel([]byte(`{"version":6,"package":"p","schemas":[]}`))
	if err != nil || m.Package != "p" {
		t.Fatalf("got %+v %v", m, err)
	}
	// A model of another version whose shape differs reports the version.
	_, err = decodeModel([]byte(`{"version":1,"package":["p"],"schemas":{}}`))
	if err == nil || !strings.Contains(err.Error(), "the loader wrote model version 1; velox-gen reads version 6: update velox-gen and the velox module together") {
		t.Fatalf("got %v", err)
	}
}

// candidates refuses a file that does not parse, takes a base embedded as
// an instance of a generic type for a base, and passes over an unexported
// struct that embeds what the package does not declare, or embeds itself
// through another.
func TestCandidatesEdges(t *testing.T) {
	if _, err := candidates(map[string][]byte{"a.go": []byte("package s\ntype")}); err == nil || !strings.HasPrefix(err.Error(), "a.go:2:") {
		t.Fatalf("got %v", err)
	}
	got, err := candidates(map[string][]byte{"a.go": []byte(`package s
import "github.com/syssam/velox/schema"
type base[T any] struct{ schema.Schema }
type User struct{ base[int] }
type x struct{ elsewhere }
type a struct{ b }
type b struct{ a }
`)})
	if err != nil || strings.Join(got, ",") != "User" {
		t.Fatalf("got %v %v", got, err)
	}
	if posString(token.Position{}) != "" {
		t.Error("an invalid position has a string")
	}
}

// classify finds no schemas in a package that does not use velox's schema
// package, and reports export data it cannot read.
func TestClassifyErrors(t *testing.T) {
	if names, err := classify("example.com/x", map[string]string{}); names != nil || err != nil {
		t.Fatalf("got %v %v", names, err)
	}
	if testing.Short() {
		t.Skip("lists velox's schema package")
	}
	exp, err := exports(repoRoot(t), loadjson.SchemaPath, offline())
	if err != nil {
		t.Fatal(err)
	}
	_, err = classify("example.com/none", exp)
	if err == nil || !strings.HasPrefix(err.Error(), "reading example.com/none: ") || !strings.HasSuffix(err.Error(), "no export data for example.com/none") {
		t.Fatalf("got %v", err)
	}
	bad := maps.Clone(exp)
	bad[loadjson.SchemaPath] = filepath.Join(t.TempDir(), "gone")
	if _, err := classify("example.com/none", bad); err == nil || !strings.HasPrefix(err.Error(), "reading "+loadjson.SchemaPath+": ") {
		t.Fatalf("got %v", err)
	}
}

// typesOf type-checks src, a package that imports nothing.
func typesOf(t *testing.T, src string) *types.Package {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := (&types.Config{}).Check("p", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

// embeddedSchema finds a schema embedded by value, through a pointer or
// through another struct; declaresType reads a named type's own methods.
func TestDiscoverTypeHelpers(t *testing.T) {
	pkg := typesOf(t, `package p
type S struct{}
type ByPtr struct{ *S }
type mid struct{ n int; S }
type Deep struct{ mid }
type Self struct{ *Self }
type Level int
type G[T any] struct{}
func (G[T]) Type() {}
type H[T any] struct{}
`)
	obj := func(n string) *types.TypeName { return pkg.Scope().Lookup(n).(*types.TypeName) }
	schemas := []*types.TypeName{obj("S")}
	for n, want := range map[string]*types.TypeName{"ByPtr": obj("S"), "Deep": obj("S"), "Self": nil, "Level": nil} {
		if got := embeddedSchema(obj(n).Type(), schemas); got != want {
			t.Errorf("embeddedSchema(%s) = %v", n, got)
		}
	}
	if !declaresType(obj("G").Type()) || declaresType(obj("H").Type()) || declaresType(types.Typ[types.Int]) {
		t.Error("declaresType")
	}
	if lowerFirst("") != "" || lowerFirst("Base") != "base" {
		t.Error("lowerFirst")
	}
}
