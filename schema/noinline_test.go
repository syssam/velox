package schema_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestSchemaBuildersAreNotInlined pins the //go:noinline on every exported
// function and method a schema is written with: the field, edge and index
// builders and the annotation constructors.
//
// A schema package is thousands of chains of them
// (field.String("name").MaxLen(64).Optional()), each call small enough for
// the compiler to inline, so it spent most of the package's compile time
// copying their bodies into Fields() and Edges(). A 450-entity schema
// compiled in 3.7 s with the builders inlinable and 1.8 s without, a real
// 454-entity one in 4.0-4.7 s and 2.2-2.4 s; its object file halved (54 to
// 28 MB). That compile is on the path of every generation, which builds a
// program around the schema to read it. The builders run once, when the
// program starts and when the generator loads the schema: an inlined call
// buys nothing there.
func TestSchemaBuildersAreNotInlined(t *testing.T) {
	for file, min := range map[string]int{
		"field/field.go":                     150,
		"field/numeric.go":                   250,
		"edge/edge.go":                       25,
		"index/index.go":                     8,
		"../contrib/graphql/annotation.go":   100,
		"../dialect/sqlschema/annotation.go": 40,
	} {
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		checked := 0
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !fn.Name.IsExported() {
				continue
			}
			checked++
			marked := false
			if fn.Doc != nil {
				for _, c := range fn.Doc.List {
					marked = marked || strings.TrimSpace(c.Text) == "//go:noinline"
				}
			}
			if !marked {
				t.Errorf("%s: %s must carry //go:noinline", file, fn.Name.Name)
			}
		}
		if checked < min {
			t.Errorf("%s: found %d exported functions, expected at least %d: the scan no longer matches the source", file, checked, min)
		}
	}
}
