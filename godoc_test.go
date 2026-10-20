package velox

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// citation matches a reference to a design document, a section of one or a
// numbered ruling. A reader of the godoc has none of them; what a comment or
// a message needs from them, it says. It does not match the ordinary words
// "design" and "spec".
var citation = regexp.MustCompile(`\x{00a7}|\bRuling \d|\b(design|spec) (section|note)\b|\bsection \d|\b(schema|policy|routing|observability|core|graphql) (design|spec)\b|\bdocs/(specs|plans|reviews)\b`)

// TestGodocCitesNoSpec: no comment in the public packages of this module (the
// root package, tests included; schema and its subpackages; plugin; and
// cmd/velox-gen) and no string literal in them, which holds the messages a
// user sees, cites a design document or a ruling.
func TestGodocCitesNoSpec(t *testing.T) {
	root := repoRoot()
	var files []string
	add := func(dir string, tests, recursive bool) {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return err
			case d.IsDir():
				if p != filepath.Join(root, dir) && (!recursive || d.Name() == "testdata" || d.Name() == "internal") {
					return filepath.SkipDir
				}
			case strings.HasSuffix(p, ".go") && (tests || !strings.HasSuffix(p, "_test.go")):
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	add(".", true, false)
	add("schema", false, true)
	add("plugin", false, false)
	add("cmd/velox-gen", false, false)
	if len(files) < 50 {
		t.Fatalf("scanned only %d files: %v", len(files), files)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				if m := citation.FindString(c.Text); m != "" {
					t.Errorf("%s: comment cites %q", fset.Position(c.Pos()), m)
				}
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if l, ok := n.(*ast.BasicLit); ok && l.Kind == token.STRING {
				if m := citation.FindString(l.Value); m != "" {
					t.Errorf("%s: string cites %q", fset.Position(l.Pos()), m)
				}
			}
			return true
		})
	}
}

// TestGeneratedOnlyAPIIsMarked: exactly the declarations that the package
// documentation lists under "Generated code" open their documentation with
// "For generated code.", and the section names each of them as a whole word.
func TestGeneratedOnlyAPIIsMarked(t *testing.T) {
	names := []string{"Desc", "ColumnDesc", "Relation", "EdgeTo", "EdgeThrough", "Meta", "MetaOnce",
		"BindPolicy", "BindHooks", "BoundPolicy", "BoundHooks", "Lazy", "GeneratedCodeIsVersion4"}
	listed := map[string]bool{}
	for _, n := range names {
		listed[n] = true
	}
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, repoRoot(), func(fi fs.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, parser.ParseComments) //nolint:staticcheck // one directory, one build; go/packages is not a dependency
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
				name := d.Name.Name
				if d.Recv != nil {
					name = "(method) " + name
				}
				docs[name] = d.Doc.Text()
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
	const marker = "For generated code."
	for name, doc := range docs {
		if strings.HasPrefix(doc, marker) && !listed[name] {
			t.Errorf("%s is marked %q but is not in the generated-code list", name, marker)
		}
		if strings.Contains(doc, marker) && !strings.HasPrefix(doc, marker) {
			t.Errorf("%s mentions %q but does not open with it", name, marker)
		}
	}
	_, section, ok := strings.Cut(pkgDoc, "# Generated code\n")
	if !ok {
		t.Fatal("the package documentation has no Generated code section")
	}
	section, _, _ = strings.Cut(section, "\n# ")
	for _, n := range names {
		if !strings.HasPrefix(docs[n], marker) {
			t.Errorf("%s: documentation opens %.40q, want %q", n, docs[n], marker)
		}
		if !regexp.MustCompile(`\b` + n + `\b`).MatchString(section) {
			t.Errorf("the Generated code section does not name %s", n)
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
