package veloxgql_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/syssam/velox2/contrib/graphql/veloxgql"
)

func TestLimits(t *testing.T) {
	if veloxgql.MaxPage != 100 || veloxgql.MaxFilterDepth != 5 || veloxgql.MaxFilterWidth != 100 || veloxgql.MaxOrderTerms != 5 || veloxgql.MaxFilterNodes != 1000 {
		t.Errorf("limits = %d, %d, %d, %d, %d, want 100, 5, 100, 5, 1000", veloxgql.MaxPage, veloxgql.MaxFilterDepth, veloxgql.MaxFilterWidth, veloxgql.MaxOrderTerms, veloxgql.MaxFilterNodes)
	}
}

// TestNothingRunsAtStart pins the rule that nothing runs at program start:
// no file of the package has an init function, and no package-level variable
// is initialised by a call or by a map literal (the compiler builds a map in
// init). Two calls are allowed, because the compiler initialises them
// statically (go build -gcflags=-S shows veloxgql's init empty, with the
// values in the data section):
//   - errors.New of a string literal, errors being the standard library's,
//     as velox's own sentinel errors are;
//   - the conversion of nil to a pointer type, (*T)(nil), for the
//     compile-time interface assertions.
func TestNothingRunsAtStart(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var srcs []source
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		srcs = append(srcs, source{name, string(src)})
	}
	if len(srcs) == 0 {
		t.Fatal("found no source files")
	}
	for _, bad := range startWork(t, srcs) {
		t.Error(bad)
	}
}

// The check finds what it is for.
func TestStartWorkFindsIt(t *testing.T) {
	const a = `package p

import ("errors"; "fmt"; "regexp"; "github.com/99designs/gqlgen/graphql")

type T struct{}
type M map[string]int

var ok = errors.New("fine")
var _ graphql.Marshaler = (*T)(nil)
var (
	re   = regexp.MustCompile("a")
	n, m = 1, len("ab")
	e    = fmt.Errorf("x")
	f    = func() int { return 1 }
	s    = []error{errors.New(fmt.Sprint(1))}
	lit  = map[string]int{"a": 1}
	nm   = &M{}
	conv = T(struct{}{})
	ptr  = (*T)(&T{})
)

func init() {}
`
	const b = `package p

import errors "github.com/pkg/errors"

var notStd = errors.New("not static")
`
	got := startWork(t, []source{{"a.go", a}, {"b.go", b}})
	want := []string{"re", "m", "e", "s", "lit", "nm", "conv", "ptr", "init", "notStd"}
	if len(got) != len(want) {
		t.Fatalf("found %q, want one each for %q", got, want)
	}
	for i, w := range want {
		if !strings.Contains(got[i], " "+w+" ") && !strings.HasSuffix(got[i], " "+w) {
			t.Errorf("finding %d = %q, want one naming %s", i, got[i], w)
		}
	}
}

type source struct{ name, src string }

// startWork returns a line for each init function and each package-level
// variable whose initialiser runs code, in the files srcs of one package.
func startWork(t *testing.T, srcs []source) []string {
	t.Helper()
	fset := token.NewFileSet()
	files := make([]*ast.File, len(srcs))
	maps := map[string]bool{} // the package's map types
	for i, s := range srcs {
		f, err := parser.ParseFile(fset, s.name, s.src, 0)
		if err != nil {
			t.Fatal(err)
		}
		files[i] = f
		for _, d := range f.Decls {
			if d, ok := d.(*ast.GenDecl); ok && d.Tok == token.TYPE {
				for _, spec := range d.Specs {
					ts := spec.(*ast.TypeSpec)
					if _, ok := ts.Type.(*ast.MapType); ok {
						maps[ts.Name.Name] = true
					}
				}
			}
		}
	}
	var out []string
	for _, f := range files {
		imports := map[string]string{} // the name a file refers to an import by → its path
		for _, im := range f.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			name := path.Base(p)
			if im.Name != nil {
				name = im.Name.Name
			}
			imports[name] = p
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.Name == "init" {
					out = append(out, fset.Position(d.Pos()).String()+": func init")
				}
			case *ast.GenDecl:
				if d.Tok != token.VAR {
					continue
				}
				for _, spec := range d.Specs {
					vs := spec.(*ast.ValueSpec)
					for i, v := range vs.Values {
						if why := runs(v, imports, maps); why != "" {
							n := vs.Names[min(i, len(vs.Names)-1)].Name
							out = append(out, fset.Position(v.Pos()).String()+": var "+n+" "+why)
						}
					}
				}
			}
		}
	}
	return out
}

// runs says what in e runs at start, outside function literals: a call other
// than the static ones, or a map literal; "" when nothing does.
func runs(e ast.Expr, imports map[string]string, maps map[string]bool) string {
	why := ""
	ast.Inspect(e, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false // runs when called, not at start
		case *ast.CallExpr:
			if !staticErrorsNew(n, imports) && !nilPointer(n) {
				why = "is initialised by a call"
			}
		case *ast.CompositeLit:
			switch ty := n.Type.(type) {
			case *ast.MapType:
				why = "is initialised by a map literal"
			case *ast.Ident:
				if maps[ty.Name] {
					why = "is initialised by a map literal"
				}
			}
		}
		return why == ""
	})
	return why
}

// staticErrorsNew reports whether c is the standard library's errors.New of
// a string literal.
func staticErrorsNew(c *ast.CallExpr, imports map[string]string) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "New" || len(c.Args) != 1 {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	lit, isLit := c.Args[0].(*ast.BasicLit)
	return ok && imports[pkg.Name] == "errors" && isLit && lit.Kind == token.STRING
}

// nilPointer reports whether c is (*T)(nil).
func nilPointer(c *ast.CallExpr) bool {
	paren, ok := c.Fun.(*ast.ParenExpr)
	if !ok || len(c.Args) != 1 {
		return false
	}
	_, star := paren.X.(*ast.StarExpr)
	arg, isIdent := c.Args[0].(*ast.Ident)
	return star && isIdent && arg.Name == "nil"
}
