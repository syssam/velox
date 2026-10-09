package sql

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestFieldPredicateConstructorsAreNotInlined pins the //go:noinline on every
// Field* predicate constructor. The generated leaf packages declare one
// generic field value per column (sql.StringField[predicate.User] and the
// like), and each package compiles the full method set of those types. With
// the constructors inlinable, every one of those methods carried a copy of
// the constructor's closure: a seven-field leaf package compiled in 0.28
// CPU-s into 1.18 MB of object, against 0.15 CPU-s and 0.60 MB with the
// directive — times every entity in the schema. A predicate is built once
// per query, so the extra call is not measurable there.
func TestFieldPredicateConstructorsAreNotInlined(t *testing.T) {
	fset := token.NewFileSet()
	checked := 0
	for _, name := range []string{"sql.go", "predicate.go"} {
		file, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Field") || !returnsSelectorFunc(fn) {
				continue
			}
			checked++
			if fn.Doc == nil || !strings.Contains(fn.Doc.Text()+commentLines(fn.Doc), "//go:noinline") {
				t.Errorf("%s: %s must carry //go:noinline", name, fn.Name.Name)
			}
		}
	}
	if checked < 20 {
		t.Fatalf("found only %d Field* predicate constructors; the scan no longer matches the source", checked)
	}
}

func returnsSelectorFunc(fn *ast.FuncDecl) bool {
	if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
		return false
	}
	ft, ok := fn.Type.Results.List[0].Type.(*ast.FuncType)
	return ok && ft.Params != nil && len(ft.Params.List) == 1 && ft.Results == nil
}

// commentLines returns the raw lines of a comment group; CommentGroup.Text
// drops directives such as //go:noinline.
func commentLines(g *ast.CommentGroup) string {
	var b strings.Builder
	for _, c := range g.List {
		b.WriteString(c.Text)
		b.WriteByte('\n')
	}
	return b.String()
}
