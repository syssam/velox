package velox_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStatementsRenderThroughQueryErr guards the packages that execute SQL
// (runtime, dialect/sql/sqlgraph) against rendering a statement with a bare
// Query(). SQL builders record errors instead of returning them — an unknown
// column in an aggregate, a failed subquery, a denied edge-predicate policy —
// and several surface only while rendering. A bare Query() runs the broken
// statement anyway: three bugs of that shape shipped (aggregate errors
// ignored, subquery errors dropped, Err() read before rendering), one of
// which would have run a DELETE with its policy filter missing. Render with
// sql.QueryErr, which returns the recorded error.
func TestStatementsRenderThroughQueryErr(t *testing.T) {
	for _, dir := range []string{"runtime", "dialect/sql/sqlgraph"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, src, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) != 0 {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Query" {
					t.Errorf("%s: statement rendered with a bare Query(); use sql.QueryErr so a recorded builder error is returned, not executed",
						fset.Position(call.Pos()))
				}
				return true
			})
		}
	}
}
