package repocheck

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var updateAPI = flag.Bool("update-api", false, "rewrite testdata/api.txt from the source")

// apiPackages are the packages whose exported names are the API that 1.0 keeps,
// relative to the repository. gqlimpl, schema/load and everything under an
// internal directory are not in it: the first two are for generated code only,
// and say so.
var apiPackages = []string{
	".", "veloxtype", "veloxpgx", "schema", "schema/edge", "schema/field", "schema/index", "schema/mixin",
	"migrate", "plugin", "scope", "contrib/graphql", "contrib/graphql/veloxgql", "contrib/veloxotel",
}

// TestPublicAPISnapshot compares the exported API with testdata/api.txt, so that
// a change to it is a line in a diff that someone reviews, and an accidental one
// fails. The members of an alias of an engine type are listed under the alias:
// they are public, though they are declared in an internal package.
func TestPublicAPISnapshot(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	for _, dir := range apiPackages {
		fmt.Fprintf(&b, "package %s\n", dir)
		for _, line := range apiLines(t, dir) {
			fmt.Fprintf(&b, "%s\n", line)
		}
		b.WriteString("\n")
	}
	got := b.String()
	path := filepath.Join("testdata", "api.txt")
	if *updateAPI {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == string(want) {
		return
	}
	gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
	for _, l := range wl {
		if l != "" && !slices.Contains(gl, l) {
			t.Errorf("removed from the API: %s", l)
		}
	}
	for _, l := range gl {
		if l != "" && !slices.Contains(wl, l) {
			t.Errorf("added to the API: %s", l)
		}
	}
	t.Errorf("the public API differs from %s. If that is meant, run go test ./internal/repocheck -run TestPublicAPISnapshot -update-api, and remember that after 1.0 a removal or a changed signature breaks users", path)
}

// TestAPILinesSeeThroughAliases pins what the snapshot rests on: a field and a
// method of an engine type appear under its alias, and an unexported one does not.
func TestAPILinesSeeThroughAliases(t *testing.T) {
	t.Parallel()
	lines := apiLines(t, ".")
	for _, want := range []string{"type Statement = engine.Statement", "Statement.SQL string", "EntityInfo.Column"} {
		if !slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(l, want) }) {
			t.Errorf("no line starts %q", want)
		}
	}
	if slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(l, "Statement.ran") }) {
		t.Error("an unexported field is in the API")
	}
}

// TestAPILinesSeePromotedMethods pins the other thing it rests on: the methods
// most of the schema DSL gets from a type it embeds that is not exported, and
// what an alias of a non-struct is.
func TestAPILinesSeePromotedMethods(t *testing.T) {
	t.Parallel()
	has := func(dir, want string) {
		t.Helper()
		if !slices.ContainsFunc(apiLines(t, dir), func(l string) bool { return strings.HasPrefix(l, want) }) {
			t.Errorf("%s: no line starts %q", dir, want)
		}
	}
	has("schema/field", "StringBuilder embeds common[*StringBuilder]")
	has("schema/field", "promoted func (c common[B]) Optional() B")
	has("schema/field", "promoted func (t typed[B, V]) Default(v V) B")
	has("schema", "Annotation is interface")
}

// apiLines is the sorted exported API of the package in dir.
func apiLines(t *testing.T, dir string) []string {
	t.Helper()
	fset := token.NewFileSet()
	var lines []string
	engine := map[string]bool{}
	for _, f := range parseDir(t, fset, dir) {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if line := funcLine(fset, d); line != "" {
					lines = append(lines, line)
				}
			case *ast.GenDecl:
				lines = append(lines, genLines(fset, d)...)
				for _, name := range engineAliases(d) {
					engine[name] = true
				}
			}
		}
	}
	if len(engine) > 0 {
		lines = append(lines, membersIn(t, "internal/engine", engine)...)
	}
	files := parseDir(t, fset, dir)
	lines = append(lines, promotedLines(fset, files)...)
	if names := descAliases(files); len(names) > 0 {
		lines = append(lines, membersIn(t, "schema/internal/desc", names)...)
	}
	slices.Sort(lines)
	return slices.Compact(lines)
}

func parseDir(t *testing.T, fset *token.FileSet, dir string) []*ast.File {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(repoRoot(), dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatalf("no Go files in %s", dir)
	}
	return files
}

// source prints node on one line.
func source(fset *token.FileSet, node any) string {
	var b bytes.Buffer
	if err := printer.Fprint(&b, fset, node); err != nil {
		panic(err)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// receiver is the name of d's receiver's type; "" for a function.
func receiver(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return ""
	}
	x := d.Recv.List[0].Type
	if s, ok := x.(*ast.StarExpr); ok {
		x = s.X
	}
	switch x := x.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.IndexExpr:
		return x.X.(*ast.Ident).Name
	case *ast.IndexListExpr:
		return x.X.(*ast.Ident).Name
	}
	return ""
}

func funcLine(fset *token.FileSet, d *ast.FuncDecl) string {
	if !d.Name.IsExported() {
		return ""
	}
	recv := receiver(d)
	if d.Recv != nil && !ast.IsExported(recv) {
		return ""
	}
	bare := *d
	bare.Body, bare.Doc = nil, nil
	return source(fset, &bare)
}

// genLines is the lines of a type, const or var declaration. A const or var is
// its name and declared type: its value may change without breaking a caller.
func genLines(fset *token.FileSet, d *ast.GenDecl) []string {
	var lines []string
	for _, s := range d.Specs {
		switch s := s.(type) {
		case *ast.TypeSpec:
			if s.Name.IsExported() {
				lines = append(lines, "type "+typeLine(fset, s))
			}
		case *ast.ValueSpec:
			for _, n := range s.Names {
				if !n.IsExported() {
					continue
				}
				line := d.Tok.String() + " " + n.Name
				if s.Type != nil {
					line += " " + source(fset, s.Type)
				}
				lines = append(lines, line)
			}
		}
	}
	return lines
}

// typeLine prints s with its unexported struct fields left out.
func typeLine(fset *token.FileSet, s *ast.TypeSpec) string {
	c := *s
	c.Doc, c.Comment = nil, nil
	if st, ok := s.Type.(*ast.StructType); ok {
		var kept []*ast.Field
		for _, f := range st.Fields.List {
			var names []*ast.Ident
			for _, n := range f.Names {
				if n.IsExported() {
					names = append(names, n)
				}
			}
			if len(f.Names) == 0 && exportedEmbedded(f.Type) || len(names) > 0 {
				kept = append(kept, &ast.Field{Names: names, Type: f.Type})
			}
		}
		c.Type = &ast.StructType{Fields: &ast.FieldList{List: kept}}
	}
	return source(fset, &c)
}

func exportedEmbedded(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.StarExpr:
		return exportedEmbedded(x.X)
	case *ast.Ident:
		return x.IsExported()
	case *ast.SelectorExpr:
		return x.Sel.IsExported()
	case *ast.IndexExpr:
		return exportedEmbedded(x.X)
	}
	return false
}

// engineAliases is the names d declares as aliases of a type of internal/engine.
func engineAliases(d *ast.GenDecl) []string {
	var names []string
	for _, s := range d.Specs {
		ts, ok := s.(*ast.TypeSpec)
		if !ok || !ts.Name.IsExported() || !ts.Assign.IsValid() {
			continue
		}
		x := ts.Type
		if i, ok := x.(*ast.IndexExpr); ok {
			x = i.X
		}
		if sel, ok := x.(*ast.SelectorExpr); ok && aliasedPackage(sel) == "engine" && sel.Sel.Name == ts.Name.Name {
			names = append(names, ts.Name.Name)
		}
	}
	return names
}

// descAliases is the names files declare as aliases of a type of schema/internal/desc.
func descAliases(files []*ast.File) map[string]bool {
	names := map[string]bool{}
	for _, f := range files {
		for _, d := range f.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, s := range g.Specs {
				if ts, ok := s.(*ast.TypeSpec); ok && ts.Assign.IsValid() && ts.Name.IsExported() && aliasedPackage(ts.Type) == "desc" {
					names[ts.Name.Name] = true
				}
			}
		}
	}
	return names
}

// promotedLines is what an exported struct gets from a type of its package that
// it embeds and that is not exported, which go doc shows as its own: "T embeds
// base[*T]" for each embedding, and the exported methods of base, at any depth.
func promotedLines(fset *token.FileSet, files []*ast.File) []string {
	specs := map[string]*ast.StructType{}
	var methods []*ast.FuncDecl
	for _, f := range files {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv != nil && d.Name.IsExported() {
					methods = append(methods, d)
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					if ts, ok := s.(*ast.TypeSpec); ok {
						if st, ok := ts.Type.(*ast.StructType); ok {
							specs[ts.Name.Name] = st
						}
					}
				}
			}
		}
	}
	var lines []string
	bases := map[string]bool{}
	var embed func(owner string, st *ast.StructType)
	embed = func(owner string, st *ast.StructType) {
		for _, fl := range st.Fields.List {
			if len(fl.Names) > 0 || exportedEmbedded(fl.Type) {
				continue
			}
			lines = append(lines, owner+" embeds "+source(fset, fl.Type))
			x := fl.Type
			if s, ok := x.(*ast.StarExpr); ok {
				x = s.X
			}
			switch i := x.(type) {
			case *ast.IndexExpr:
				x = i.X
			case *ast.IndexListExpr:
				x = i.X
			}
			if id, ok := x.(*ast.Ident); ok && !bases[id.Name] && specs[id.Name] != nil {
				bases[id.Name] = true
				embed(id.Name, specs[id.Name])
			}
		}
	}
	for name, st := range specs {
		if ast.IsExported(name) {
			embed(name, st)
		}
	}
	for _, m := range methods {
		if bases[receiver(m)] {
			bare := *m
			bare.Body, bare.Doc = nil, nil
			lines = append(lines, "promoted "+source(fset, &bare))
		}
	}
	return lines
}

// membersIn is the exported fields and methods of the named types of the package
// in dir, or, for a type that is not a struct, what it is.
func membersIn(t *testing.T, dir string, names map[string]bool) []string {
	t.Helper()
	fset := token.NewFileSet()
	var lines []string
	for _, f := range parseDir(t, fset, dir) {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if r := receiver(d); names[r] && d.Name.IsExported() {
					lines = append(lines, r+"."+d.Name.Name+strings.TrimPrefix(source(fset, d.Type), "func"))
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					ts, ok := s.(*ast.TypeSpec)
					if !ok || !names[ts.Name.Name] {
						continue
					}
					st, ok := ts.Type.(*ast.StructType)
					if !ok {
						lines = append(lines, ts.Name.Name+" is "+source(fset, ts.Type))
						continue
					}
					for _, fl := range st.Fields.List {
						for _, n := range fl.Names {
							if n.IsExported() {
								lines = append(lines, ts.Name.Name+"."+n.Name+" "+source(fset, fl.Type))
							}
						}
					}
				}
			}
		}
	}
	return lines
}
