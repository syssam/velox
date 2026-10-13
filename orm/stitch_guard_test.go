package orm

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An entity is filled in with the code of its schema when it is first used
// (Meta.stitch), so whatever reads that code must stitch first: a read that
// does not sees an entity without its policy, and answers every caller.
//
// The hooks and the policy are read through Meta.hooks and Meta.policy and
// nowhere else in the package; a function that reads a field's Default,
// UpdateDefault or Validate calls stitch itself.
func TestSchemaCodeIsReadAfterStitching(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			var fields, whole []string // what of the schema's code the function reads
			stitches := false
			calls := map[ast.Expr]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok {
					calls[c.Fun] = true
				}
				return true
			})
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "stitch":
					stitches = true
				case "Default", "UpdateDefault", "Validate":
					fields = append(fields, sel.Sel.Name)
				case "Policy":
					whole = append(whole, sel.Sel.Name)
				case "Hooks":
					// c.Hooks() is the hooks of a database handle.
					if !calls[sel] {
						whole = append(whole, sel.Sel.Name)
					}
				}
				return true
			})
			at := fset.Position(fn.Pos())
			accessor := name == "meta.go" && fn.Recv != nil && (fn.Name.Name == "hooks" || fn.Name.Name == "policy")
			if len(whole) > 0 && !accessor {
				t.Errorf("%s: %s reads the %s of a Meta itself: it goes through Meta.hooks or Meta.policy, which stitch first", at, fn.Name.Name, strings.Join(whole, ", "))
			}
			if len(fields) > 0 {
				checked++
				if !stitches {
					t.Errorf("%s: %s reads a field's %s and does not stitch the entity first", at, fn.Name.Name, strings.Join(fields, ", "))
				}
			}
			if accessor {
				checked++
				if !stitches {
					t.Errorf("%s: %s does not stitch", at, fn.Name.Name)
				}
			}
		}
	}
	// The functions known to read it today: fewer means the test no longer
	// sees them.
	if checked < 6 {
		t.Errorf("only %d functions that read the schema's code were found", checked)
	}
}
