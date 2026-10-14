package velox

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// recvName is the name of the generic type a method's receiver type names.
func recvName(rt ast.Expr) string {
	var x ast.Expr
	switch r := rt.(type) {
	case *ast.IndexExpr:
		x = r.X
	case *ast.IndexListExpr:
		x = r.X
	}
	if id, ok := x.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// TestMethodsOfGenericTypesAreGeneric enforces design §3 rule 1: a method of
// a generic type is a generic method, compiled only where it is called. The
// exceptions are column, which makes a handle a Column[E], and the MarshalJSON
// of One and Many.
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
			// One and Many have an ordinary MarshalJSON: encoding/json finds
			// it by name (spec §3).
			if fd.Name.Name == "MarshalJSON" && (recvName(rt) == "One" || recvName(rt) == "Many") {
				continue
			}
			if fd.Type.TypeParams == nil || fd.Type.TypeParams.NumFields() == 0 {
				t.Errorf("%s: method %s of a generic type is not a generic method; add [_ Lazy[...]] (design §3 rule 1)",
					fset.Position(fd.Pos()), fd.Name.Name)
			}
		}
	}
}
