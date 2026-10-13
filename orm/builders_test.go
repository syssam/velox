package orm

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestMethodsOfGenericTypesAreGeneric pins what makes the column, edge and
// builder types cheap to compile against: every method has a type parameter
// of its own (Lazy), so it is compiled where it is called. An ordinary method
// of one of them is compiled, with the whole method set of the type, by every
// package that reaches a value of it, which for a builder is every package of
// an application. The one exception is loadOf, the three lines that make an
// edge a Loader.
func TestMethodsOfGenericTypesAreGeneric(t *testing.T) {
	ordinary := map[string]bool{"ToOne.loadOf": true, "ToMany.loadOf": true, "LoadOf.loadOf": true}
	for _, file := range []string{"columns.go", "edges.go", "builders.go"} {
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		methods := 0
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil {
				continue
			}
			recv := fn.Recv.List[0].Type
			if star, ok := recv.(*ast.StarExpr); ok {
				recv = star.X
			}
			var name string
			switch r := recv.(type) {
			case *ast.IndexExpr:
				name = r.X.(*ast.Ident).Name
			case *ast.IndexListExpr:
				name = r.X.(*ast.Ident).Name
			default:
				continue
			}
			methods++
			generic := fn.Type.TypeParams != nil && len(fn.Type.TypeParams.List) > 0
			if key := name + "." + fn.Name.Name; generic == ordinary[key] {
				t.Errorf("%s: %s: a method with a type parameter of its own: %t", file, key, generic)
			}
		}
		if methods == 0 {
			t.Errorf("%s: no method of a generic type found: the test no longer reads them", file)
		}
	}
}

// With takes an edge as it is, or with the query that describes its load, and
// the load is the query as it was then.
func TestWith(t *testing.T) {
	user := &Meta{Name: "User"}
	posts := &ToMany[userPred, postPred, int]{Meta: &EdgeMeta{Name: "posts", Owner: user}}
	boss := &ToOne[userPred, userPred, int]{Meta: &EdgeMeta{Name: "boss", Owner: user}}
	title := StringColumn[string, postPred]("title")

	sub := &QueryOf[*struct{}, postPred, int]{}
	sub.Where(title.EQ("a")).OrderBy(title.Desc()).Limit(2)
	q := &QueryOf[*struct{}, userPred, int]{}
	q.With(boss, posts.Load(sub))
	sub.Where(title.EQ("later"))

	if len(q.c.With) != 2 {
		t.Fatalf("%d loads, want 2", len(q.c.With))
	}
	if l := q.c.With[0]; l.Edge != boss.Meta || l.Preds != nil || l.Limit != 0 {
		t.Errorf("the edge as it is: %+v", l)
	}
	if l := q.c.With[1]; l.Edge != posts.Meta || len(l.Preds) != 1 || len(l.Orders) != 1 || l.Limit != 2 {
		t.Errorf("the edge with its query: %+v", l)
	}
	var none *QueryOf[*struct{}, postPred, int]
	q.With(posts.Load(none))
	if l := q.c.With[2]; l.Edge != posts.Meta || l.Preds != nil {
		t.Errorf("a nil query describes nothing: %+v", l)
	}
}
