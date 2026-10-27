package velox

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// citation matches a reference to a design document, a section of one or a
// numbered ruling. A reader of the godoc has none of them; what a comment or
// a message needs from them, it says. It does not match the ordinary words
// "design" and "spec".
var citation = regexp.MustCompile(`\x{00a7}|\bRuling \d|\b(design|spec) (section|note)\b|\bsection \d|\b(schema|policy|routing|observability|core|graphql) (design|spec)\b|\bdocs/(specs|plans|reviews)\b|\b(GraphQL|API|core|policy|schema)\s+plan\s+\d`)

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
	add("migrate", false, false)
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
			// The group's text, so that a citation across a line break
			// is found.
			if m := citation.FindString(cg.Text()); m != "" {
				t.Errorf("%s: comment cites %q", fset.Position(cg.Pos()), m)
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
// documentation lists under "Generated code" say "For generated code." just
// after their first sentence, and the section names each of them as a whole
// word.
func TestGeneratedOnlyAPIIsMarked(t *testing.T) {
	names := []string{"Desc", "ColumnDesc", "EdgeDesc", "EdgeTo", "EdgeBidi", "EdgeThrough", "Meta", "MetaOnce",
		"BindPolicy", "BindHooks", "BoundPolicy", "BoundHooks", "Lazy", "Validate", "ValidateSet", "GeneratedCodeIsVersion6"}
	listed := map[string]bool{}
	for _, n := range names {
		listed[n] = true
	}
	fset := token.NewFileSet()
	entries, err := os.ReadDir(repoRoot())
	if err != nil {
		t.Fatal(err)
	}
	docs := map[string]string{}
	var pkgDoc string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(repoRoot(), name), nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		if f.Name.Name != "velox" {
			continue
		}
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
	for name, doc := range docs {
		mentions := strings.Contains(strings.Join(strings.Fields(doc), " "), generatedMarker)
		switch {
		case mentions && !listed[name]:
			t.Errorf("%s is marked %q but is not in the generated-code list", name, generatedMarker)
		case mentions && !markedGenerated(doc):
			t.Errorf("%s mentions %q but not after its first sentence", name, generatedMarker)
		}
	}
	_, section, ok := strings.Cut(pkgDoc, "# Generated code\n")
	if !ok {
		t.Fatal("the package documentation has no Generated code section")
	}
	section, _, _ = strings.Cut(section, "\n# ")
	for _, n := range names {
		if !strings.Contains(strings.Join(strings.Fields(docs[n]), " "), generatedMarker) {
			t.Errorf("%s: documentation %.40q does not say %q", n, docs[n], generatedMarker)
		}
		if !regexp.MustCompile(`\b` + n + `\b`).MatchString(section) {
			t.Errorf("the Generated code section does not name %s", n)
		}
	}
}

// generatedMarker marks a declaration that only generated code uses.
const generatedMarker = "For generated code."

// markedGenerated reports whether doc says generatedMarker just after its
// first sentence.
func markedGenerated(doc string) bool {
	_, rest, ok := strings.Cut(strings.Join(strings.Fields(doc), " "), ". ")
	return ok && strings.HasPrefix(rest, generatedMarker)
}

// TestExportedMethodsHaveDocs: every exported method of an exported type in a
// hand-written, non-test file has a doc comment that opens with its name and a
// space (strings.HasPrefix(doc, name+" ")), Error, String and Unwrap included,
// which revive's exported rule skips.
func TestExportedMethodsHaveDocs(t *testing.T) {
	root := repoRoot()
	var files []string
	add := func(dir string, recursive bool) {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return err
			case d.IsDir():
				if p != filepath.Join(root, dir) && (!recursive || d.Name() == "testdata" || d.Name() == "internal") {
					return filepath.SkipDir
				}
			case strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go"):
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	add(".", false)
	add("migrate", false)
	add("plugin", false)
	add("schema", true)
	add("contrib/graphql", false)
	add("contrib/graphql/veloxgql", false)
	add("contrib/graphql/veloxgql/gqlimpl", false)
	add("contrib/veloxotel", false)
	if len(files) < 80 {
		t.Fatalf("scanned only %d files: %v", len(files), files)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		if ast.IsGenerated(f) {
			continue
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !fn.Name.IsExported() {
				continue
			}
			// A method of an unexported type is not in the package's
			// documentation.
			recv := recvType(fn.Recv.List[0].Type)
			if !ast.IsExported(recv) {
				continue
			}
			if !strings.HasPrefix(fn.Doc.Text(), fn.Name.Name+" ") {
				pos := fset.Position(fn.Pos())
				rel, _ := filepath.Rel(root, pos.Filename)
				t.Errorf("%s:%d: %s.%s has no doc comment that opens with its name", filepath.ToSlash(rel), pos.Line, recv, fn.Name.Name)
			}
		}
	}
}

// recvType is the name of a receiver's type, without its pointer or type
// parameters.
func recvType(e ast.Expr) string {
	for {
		switch x := e.(type) {
		case *ast.StarExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.IndexListExpr:
			e = x.X
		case *ast.Ident:
			return x.Name
		default:
			return "?"
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
