package velox

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestMethodsOfGenericTypesAreGeneric enforces design §3 rule 1: a method of
// a generic type is a generic method, compiled only where it is called. The
// one exception is column, which makes a handle a Column[E].
func TestMethodsOfGenericTypesAreGeneric(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil {
				continue
			}
			rt := fd.Recv.List[0].Type
			if s, ok := rt.(*ast.StarExpr); ok {
				rt = s.X
			}
			switch rt.(type) {
			case *ast.IndexExpr, *ast.IndexListExpr:
			default:
				continue
			}
			if fd.Name.Name == "column" {
				continue
			}
			if fd.Type.TypeParams == nil || fd.Type.TypeParams.NumFields() == 0 {
				t.Errorf("%s: method %s of a generic type is not a generic method; add [_ Lazy[...]] (design §3 rule 1)",
					fset.Position(fd.Pos()), fd.Name.Name)
			}
		}
	}
}
