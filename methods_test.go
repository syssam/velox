package velox

import (
	"fmt"
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

// handleKinds are the ten column handle types; each has an ordinary column
// method, which makes it a Column[E].
var handleKinds = map[string]bool{
	"Col": true, "OrderedCol": true, "NumberCol": true, "StringCol": true,
	"NullCol": true, "NullOrderedCol": true, "NullNumberCol": true, "NullStringCol": true,
	"JSONCol": true, "NullJSONCol": true,
}

// ordinaryMethod reports whether the method name of the generic type recv is
// one of the exact exceptions: the handles' column, the MarshalJSON of One
// and Many (encoding/json finds it by name, spec §3), and MutateFunc's Mutate
// (interface satisfaction: Mutator's method set needs it).
func ordinaryMethod(name, recv string) bool {
	switch name {
	case "column":
		return handleKinds[recv]
	case "MarshalJSON":
		return recv == "One" || recv == "Many"
	case "Mutate":
		return recv == "MutateFunc"
	}
	return false
}

// checkMethods returns one message per method of a generic type in f that
// breaks design §3 rule 1.
func checkMethods(fset *token.FileSet, f *ast.File) []string {
	var bad []string
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
		name, recv := fd.Name.Name, recvName(rt)
		pos := fset.Position(fd.Pos())
		switch {
		case ordinaryMethod(name, recv):
		case name == "column" || name == "MarshalJSON":
			bad = append(bad, fmt.Sprintf("%s: method %s of %s is an ordinary method; only the handles' column and One/Many's MarshalJSON are allowed this name as ordinary methods", pos, name, recv))
		case !genericOwnTypeParam(fd):
			bad = append(bad, fmt.Sprintf("%s: method %s of a generic type is not a generic method; add [_ Lazy[...]] (design §3 rule 1)", pos, name))
		}
	}
	return bad
}

// genericOwnTypeParam reports whether the method has type parameters whose
// first is either _ Lazy[...] or a named parameter.
func genericOwnTypeParam(fd *ast.FuncDecl) bool {
	tp := fd.Type.TypeParams
	if tp == nil || len(tp.List) == 0 {
		return false
	}
	first := tp.List[0]
	if len(first.Names) == 0 {
		return false
	}
	if first.Names[0].Name != "_" {
		return true
	}
	// _ must be constrained by Lazy[...].
	ix, ok := first.Type.(*ast.IndexExpr)
	if !ok {
		return false
	}
	id, ok := ix.X.(*ast.Ident)
	return ok && id.Name == "Lazy"
}

// TestMethodsOfGenericTypesAreGeneric enforces design §3 rule 1: a method of
// a generic type is a generic method, compiled only where it is called. The
// exceptions are exact: column on the ten handle kinds, which makes a handle
// a Column[E], MarshalJSON on One and Many, and Mutate on MutateFunc.
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
		for _, msg := range checkMethods(fset, f) {
			t.Error(msg)
		}
	}
}

func TestMethodGuardCatchesAnOrdinaryMethod(t *testing.T) {
	src := `package velox
type Col[E, V any] struct{ I int32 }
func (c Col[E, V]) column(*E) int32 { return c.I }
func (c Col[E, V]) Bad() {}
func (c Col[E, V]) Good[_ Lazy[V]]() {}
func (c Col[E, V]) Named[K any]() {}
func (c Col[E, V]) Blank[_ any]() {}
type Rel[E, T any] struct{}
func (r Rel[E, T]) column(*E) int32 { return 0 }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(checkMethods(fset, f), "\n")
	for _, want := range []string{"method Bad of a generic type", "method Blank of a generic type", "method column of Rel is an ordinary method; only the handles' column and One/Many's MarshalJSON are allowed this name as ordinary methods"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Good") || strings.Contains(got, "Named") || strings.Contains(got, "method column of Col") {
		t.Errorf("flagged an allowed method:\n%s", got)
	}
}
