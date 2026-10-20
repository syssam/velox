package velox

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestGodocCitesNoSpec: no comment in the root package, tests included, cites
// a section of a design document or a numbered ruling. A reader of the godoc
// has neither; what a comment needs from them, it says.
func TestGodocCitesNoSpec(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(repoRoot(), "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go files in %s: %v", repoRoot(), err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				for _, bad := range []string{"§", "Ruling"} {
					if strings.Contains(c.Text, bad) {
						t.Errorf("%s: comment cites %q: %.60s", fset.Position(c.Pos()), bad, c.Text)
					}
				}
			}
		}
	}
}

// TestGeneratedOnlyAPIIsMarked: every declaration the package documentation
// lists as generated-code API opens its documentation with "For generated
// code.", and the package documentation names them all.
func TestGeneratedOnlyAPIIsMarked(t *testing.T) {
	names := []string{"Desc", "ColumnDesc", "Relation", "EdgeTo", "EdgeThrough", "Meta", "MetaOnce",
		"BindPolicy", "BindHooks", "BoundPolicy", "BoundHooks", "Lazy", "GeneratedCodeIsVersion4"}
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, repoRoot(), nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	docs := map[string]string{}
	var pkgDoc string
	for _, f := range pkgs["velox"].Files {
		if f.Doc != nil {
			pkgDoc += f.Doc.Text()
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					docs[d.Name.Name] = d.Doc.Text()
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						docs[s.Name.Name] = firstDoc(s.Doc, d.Doc)
					case *ast.ValueSpec:
						for _, n := range s.Names {
							docs[n.Name] = firstDoc(s.Doc, d.Doc)
						}
					}
				}
			}
		}
	}
	for _, n := range names {
		if !strings.HasPrefix(docs[n], "For generated code.") {
			t.Errorf("%s: documentation opens %.40q, want \"For generated code.\"", n, docs[n])
		}
		if !strings.Contains(pkgDoc, n) {
			t.Errorf("the package documentation does not name %s", n)
		}
	}
}

func firstDoc(gs ...*ast.CommentGroup) string {
	for _, g := range gs {
		if g != nil {
			return g.Text()
		}
	}
	return ""
}
