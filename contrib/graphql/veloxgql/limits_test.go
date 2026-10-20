package veloxgql_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syssam/velox2/contrib/graphql/veloxgql"
)

func TestLimits(t *testing.T) {
	if veloxgql.MaxPage != 100 || veloxgql.MaxFilterDepth != 5 || veloxgql.MaxFilterWidth != 100 {
		t.Errorf("limits = %d, %d, %d, want 100, 5, 100", veloxgql.MaxPage, veloxgql.MaxFilterDepth, veloxgql.MaxFilterWidth)
	}
}

// TestNothingRunsAtStart pins the rule that nothing runs at program start:
// no file of the package has an init function, and no package-level variable
// is initialised by a call. The one call allowed is errors.New of a string
// literal, which the compiler initialises statically (go build -gcflags=-S
// shows an empty init and the error in the data section), as velox's own
// sentinel errors are.
func TestNothingRunsAtStart(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	seen := 0
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range startCalls(t, fset, name, src) {
			t.Error(bad)
		}
		seen++
	}
	if seen == 0 {
		t.Fatal("found no source files")
	}
}

// The check finds what it is for.
func TestStartCallsFindsCalls(t *testing.T) {
	const src = `package p

import ("errors"; "fmt"; "regexp")

var ok = errors.New("fine")
var (
	re   = regexp.MustCompile("a")
	n, m = 1, len("ab")
	e    = fmt.Errorf("x")
	f    = func() int { return 1 }
	s    = []error{errors.New(fmt.Sprint(1))}
)

func init() {}
`
	got := startCalls(t, token.NewFileSet(), "p.go", []byte(src))
	want := []string{"re", "m", "e", "s", "init"}
	if len(got) != len(want) {
		t.Fatalf("found %q, want one each for %q", got, want)
	}
	for i, w := range want {
		if !strings.Contains(got[i], w) {
			t.Errorf("finding %d = %q, want one naming %s", i, got[i], w)
		}
	}
}

// startCalls returns a line for each init function and each package-level
// variable initialised by a call in the Go source src.
func startCalls(t *testing.T, fset *token.FileSet, name string, src []byte) []string {
	t.Helper()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
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
					if hasCall(v) {
						n := vs.Names[min(i, len(vs.Names)-1)].Name
						out = append(out, fset.Position(v.Pos()).String()+": var "+n+" is initialised by a call")
					}
				}
			}
		}
	}
	return out
}

// hasCall reports whether e calls anything outside a function literal, other
// than errors.New of a string literal.
func hasCall(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false // runs when called, not at start
		case *ast.CallExpr:
			if !staticErrorsNew(n) {
				found = true
			}
		}
		return !found
	})
	return found
}

func staticErrorsNew(c *ast.CallExpr) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "New" || len(c.Args) != 1 {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	lit, isLit := c.Args[0].(*ast.BasicLit)
	return ok && pkg.Name == "errors" && isLit && lit.Kind == token.STRING
}
