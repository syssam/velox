package orm

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
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
	// Said at the edge, a load is the same thing: the conditions, the order
	// and the limit of the neighbors, and their own edges.
	author := &ToOne[postPred, userPred, int]{Meta: &EdgeMeta{Name: "author", Owner: user}}
	q = &QueryOf[*struct{}, userPred, int]{}
	q.With(
		posts.Where(title.EQ("a")).OrderBy(title.Desc()).Limit(2).Columns("title").With(author.Columns("name")),
		posts.Limit(1),
		posts.OrderBy(title.Asc()),
		boss.With(posts),
	)
	if len(q.c.With) != 4 {
		t.Fatalf("%d loads, want 4", len(q.c.With))
	}
	if l := q.c.With[0]; l.Edge != posts.Meta || len(l.Preds) != 1 || len(l.Orders) != 1 || l.Limit != 2 ||
		len(l.Cols) != 1 || len(l.With) != 1 || l.With[0].Edge != author.Meta || len(l.With[0].Cols) != 1 {
		t.Errorf("a load said at the edge: %+v", l)
	}
	if l := q.c.With[1]; l.Edge != posts.Meta || l.Limit != 1 || l.Preds != nil {
		t.Errorf("a load that starts at Limit: %+v", l)
	}
	if l := q.c.With[2]; len(l.Orders) != 1 || l.Limit != 0 {
		t.Errorf("a load that starts at OrderBy: %+v", l)
	}
	if l := q.c.With[3]; l.Edge != boss.Meta || len(l.With) != 1 || l.With[0].Edge != posts.Meta {
		t.Errorf("a to-one edge with an edge of its neighbor: %+v", l)
	}
	// As with a query, the load is what was said when With was called.
	later := posts.Where(title.EQ("a"))
	q = (&QueryOf[*struct{}, userPred, int]{}).With(later)
	later.Where(title.EQ("b"))
	if l := q.c.With[0]; len(l.Preds) != 1 {
		t.Errorf("a condition added after With reached the load: %+v", l)
	}

	var none *QueryOf[*struct{}, postPred, int]
	q.With(posts.Load(none))
	if l := q.c.With[len(q.c.With)-1]; l.Edge != posts.Meta || l.Preds != nil {
		t.Errorf("a nil query describes nothing: %+v", l)
	}
}

// What a builder is given back is of its types by construction. When it is
// not (a client declared with the Meta of another entity than its types
// name), the panic says what to do.
func TestBuilderOfAnotherEntitysMeta(t *testing.T) {
	defer func() {
		msg, _ := recover().(string)
		if !strings.Contains(msg, "the engine read a string where the builder's type is *int") || !strings.Contains(msg, "generate the code again") {
			t.Errorf("panic: %q", msg)
		}
	}()
	as[*int]("a row")
}
