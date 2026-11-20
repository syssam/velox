package repocheck

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
// root package and veloxtype, tests included; schema and its subpackages; plugin;
// and cmd/velox-gen) and no string literal in them, which holds the messages a
// user sees, cites a design document or a ruling.
func TestGodocCitesNoSpec(t *testing.T) {
	t.Parallel()
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
	add("veloxtype", true, false)
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
// word. A name that lives in veloxtype says it in both packages' documentation.
func TestGeneratedOnlyAPIIsMarked(t *testing.T) {
	t.Parallel()
	names := []string{"Descriptor", "ColumnDescriptor", "EdgeDescriptor", "EdgeTo", "EdgeBidi", "EdgeThrough", "Meta", "MetaOnce", "Show",
		"LinkEntity", "EntityLink", "EntityTable", "Lazy", "Validate", "ValidateSet",
		"GeneratedCodeIsVersion24"}
	listed := map[string]bool{}
	for _, n := range names {
		listed[n] = true
	}
	docs, pkgDoc := packageDocs(t, repoRoot(), "velox")
	veloxtypeDocs, _ := packageDocs(t, filepath.Join(repoRoot(), "veloxtype"), "veloxtype")
	if _, ok := veloxtypeDocs["ColumnDescriptor"]; !ok {
		t.Fatal("veloxtype declares no ColumnDescriptor: the check reads nothing there")
	}
	for pkg, docs := range map[string]map[string]string{"velox": docs, "veloxtype": veloxtypeDocs} {
		for name, doc := range docs {
			mentions := strings.Contains(strings.Join(strings.Fields(doc), " "), generatedMarker)
			switch {
			case mentions && !listed[name]:
				t.Errorf("%s.%s is marked %q but is not in the generated-code list", pkg, name, generatedMarker)
			case mentions && !markedGenerated(doc):
				t.Errorf("%s.%s mentions %q but not after its first sentence", pkg, name, generatedMarker)
			}
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
		if doc, ok := veloxtypeDocs[n]; ok && !strings.Contains(strings.Join(strings.Fields(doc), " "), generatedMarker) {
			t.Errorf("veloxtype.%s: documentation %.40q does not say %q", n, doc, generatedMarker)
		}
		if !markedGenerated(docs[n]) {
			t.Errorf("%s: documentation %.40q does not say %q after its first sentence", n, docs[n], generatedMarker)
		}
		if !regexp.MustCompile(`\b` + strings.TrimPrefix(n, "(method) ") + `\b`).MatchString(section) {
			t.Errorf("the Generated code section does not name %s", n)
		}
	}
}

// packageDocs reads the non-test files of the package pkg in dir: the doc
// comment of each top-level declaration by name, with a method as "(method)
// Name", and the package documentation.
func packageDocs(t *testing.T, dir, pkg string) (docs map[string]string, pkgDoc string) {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	docs = map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		if f.Name.Name != pkg {
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
	return docs, pkgDoc
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
	t.Parallel()
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
	add("veloxtype", false)
	add("migrate", false)
	add("plugin", false)
	add("schema", true)
	add("contrib/graphql", false)
	add("contrib/graphql/veloxgql", false)
	add("contrib/graphql/veloxgql/gqlgraph", false)
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

// rootDecls is the declarations of the root package's non-test files.
func rootDecls(t *testing.T, fset *token.FileSet, mode parser.Mode) []ast.Decl {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(repoRoot(), "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var decls []ast.Decl
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, mode)
		if err != nil {
			t.Fatal(err)
		}
		decls = append(decls, f.Decls...)
	}
	return decls
}

// TestAliasDocsListMembers: go doc shows neither the fields nor the methods of an
// alias of an internal type, so the doc comment of each alias of an engine type lists
// its exported ones. An alias of a veloxtype type lists none: its documentation is
// public, and links through.
func TestAliasDocsListMembers(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	docs := map[string]string{}
	for _, d := range rootDecls(t, fset, parser.ParseComments) {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.TYPE {
			for _, s := range g.Specs {
				ts := s.(*ast.TypeSpec)
				if !ts.Assign.IsValid() {
					continue
				}
				switch aliasedPackage(ts.Type) {
				case "engine":
					docs[ts.Name.Name] = g.Doc.Text()
				case "veloxtype":
					if strings.Contains(g.Doc.Text(), "Its fields:") || strings.Contains(g.Doc.Text(), "Its methods:") {
						t.Errorf("the alias %s lists members that veloxtype documents", ts.Name.Name)
					}
				}
			}
		}
	}
	// Not vacuous: the aliases that cannot move are read.
	for _, kept := range []string{"Statement", "Option", "Conn", "EntityInfo"} {
		if _, ok := docs[kept]; !ok {
			t.Errorf("%s is not an alias of an engine type: the check reads nothing", kept)
		}
	}
	files, err := filepath.Glob(filepath.Join(repoRoot(), "internal/engine/*.go"))
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
			switch d := d.(type) {
			case *ast.GenDecl:
				for _, s := range d.Specs {
					ts, ok := s.(*ast.TypeSpec)
					if !ok {
						continue
					}
					st, isStruct := ts.Type.(*ast.StructType)
					doc, aliased := docs[ts.Name.Name]
					if !isStruct || !aliased {
						continue
					}
					for _, fl := range st.Fields.List {
						for _, n := range fl.Names {
							if n.IsExported() && !strings.Contains(doc, n.Name) {
								t.Errorf("the alias %s does not list its field %s", ts.Name.Name, n.Name)
							}
						}
					}
				}
			case *ast.FuncDecl:
				if d.Recv == nil || !d.Name.IsExported() {
					continue
				}
				var recv string
				ast.Inspect(d.Recv.List[0].Type, func(c ast.Node) bool {
					if id, ok := c.(*ast.Ident); ok && recv == "" {
						recv = id.Name
					}
					return true
				})
				if doc, aliased := docs[recv]; aliased && !strings.Contains(doc, d.Name.Name) {
					t.Errorf("the alias %s does not list its method %s", recv, d.Name.Name)
				}
			}
		}
	}
}

// aliasedPackage is the package an alias's right-hand side names; "" when it
// names none.
func aliasedPackage(e ast.Expr) string {
	if ix, ok := e.(*ast.IndexExpr); ok {
		e = ix.X
	}
	if sel, ok := e.(*ast.SelectorExpr); ok {
		if id, ok := sel.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}

// TestVeloxTypeIsReExported: every exported name in veloxtype is declared by the root
// under the same name, as that name of veloxtype, so application code never needs to
// import it. veloxtype's non-test files import only the standard library.
func TestVeloxTypeIsReExported(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	dir := filepath.Join(repoRoot(), "veloxtype")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var exported []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range f.Imports {
			// A standard-library path has no dot in its first element.
			first, _, _ := strings.Cut(strings.Trim(im.Path.Value, `"`), "/")
			if strings.Contains(first, ".") {
				t.Errorf("%s imports %s: veloxtype imports only the standard library", e.Name(), im.Path.Value)
			}
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.IsExported() {
					exported = append(exported, d.Name.Name)
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						if s.Name.IsExported() {
							exported = append(exported, s.Name.Name)
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if n.IsExported() {
								exported = append(exported, n.Name)
							}
						}
					}
				}
			}
		}
	}
	if len(exported) < 25 {
		t.Fatalf("veloxtype exports only %d names: %v", len(exported), exported)
	}
	reexported := map[string]bool{}
	mark := func(name string, value ast.Expr) {
		if sel, ok := value.(*ast.SelectorExpr); ok && aliasedPackage(sel) == "veloxtype" && sel.Sel.Name == name {
			reexported[name] = true
		}
	}
	for _, d := range rootDecls(t, fset, parser.SkipObjectResolution) {
		g, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, s := range g.Specs {
			switch s := s.(type) {
			case *ast.TypeSpec:
				if s.Assign.IsValid() {
					mark(s.Name.Name, s.Type)
				}
			case *ast.ValueSpec:
				for i, n := range s.Names {
					if i < len(s.Values) {
						mark(n.Name, s.Values[i])
					}
				}
			}
		}
	}
	for _, name := range exported {
		if !reexported[name] {
			t.Errorf("the root does not declare %s = veloxtype.%s", name, name)
		}
	}
}
