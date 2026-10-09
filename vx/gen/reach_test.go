package gen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"
	"testing"
)

// TestOneWayToQueryAnEdge pins the spelling: u.QueryPosts(ex), a method of
// the entity, and nothing of the same name on the root package's clients.
// (The client method existed, and was cheaper to compile against: see the
// note on the edges in model.go. Two spellings of one thing were not wanted.)
func TestOneWayToQueryAnEdge(t *testing.T) {
	count := func(dir string, receiver func(string) bool) int {
		paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil || len(paths) == 0 {
			t.Fatal(dir, err)
		}
		n := 0
		for _, p := range paths {
			f, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range f.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || fn.Recv == nil || !strings.HasPrefix(fn.Name.Name, "Query") || fn.Name.Name == "Query" {
					continue
				}
				recv := fn.Recv.List[0].Type
				if star, ok := recv.(*ast.StarExpr); ok {
					recv = star.X
				}
				if id, ok := recv.(*ast.Ident); ok && receiver(id.Name) {
					n++
				}
			}
		}
		return n
	}
	for _, dir := range []string{"../tests/db", "../tests/shapes/db"} {
		if n := count(dir, func(name string) bool { return strings.HasSuffix(name, "Client") }); n != 0 {
			t.Errorf("%s: %d Query<Edge> methods on clients", dir, n)
		}
		if n := count(filepath.Join(dir, "entity"), func(name string) bool { return !strings.HasSuffix(name, "Query") }); n < 5 {
			t.Errorf("%s: %d Query<Edge> methods on entities", dir, n)
		}
	}
}

// TestNoGenericTypeWithMethodsInSignatures pins which generic types the
// generated signatures may name. A generic type's methods are compiled by
// every package that can reach one of its instantiations, whether it calls
// them or not; iter.Seq2 has none. (A page was orm.Page[T] with two methods
// once: half the compile time of every service.) Another generic type here
// needs the same check before it is allowed.
func TestNoGenericTypeWithMethodsInSignatures(t *testing.T) {
	allowed := map[string]bool{"iter.Seq2": true}
	for _, dir := range []string{"../tests/db/entity", "../tests/db", "../tests/shapes/db/entity", "../tests/shapes/db"} {
		paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil || len(paths) == 0 {
			t.Fatal(dir, err)
		}
		seen := map[string]int{}
		for _, p := range paths {
			f, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range f.Decls {
				var sig ast.Node
				switch d := d.(type) {
				case *ast.FuncDecl:
					sig = d.Type
				case *ast.GenDecl:
					if d.Tok != token.TYPE {
						continue
					}
					sig = d
				}
				ast.Inspect(sig, func(n ast.Node) bool {
					var x ast.Expr
					switch n := n.(type) {
					case *ast.IndexExpr:
						x = n.X
					case *ast.IndexListExpr:
						x = n.X
					default:
						return true
					}
					// An array type is not an index expression; a selector
					// followed by type arguments is an instantiation.
					if sel, ok := x.(*ast.SelectorExpr); ok {
						name := types.ExprString(sel)
						seen[name]++
						if !allowed[name] {
							t.Errorf("%s: a signature names the generic type %s", p, name)
						}
					}
					return true
				})
			}
		}
		if strings.HasSuffix(dir, "entity") && seen["iter.Seq2"] == 0 {
			t.Errorf("%s: found %v: the test no longer reads the signatures", dir, seen)
		}
	}
}

// TestPackageEntityExportsNoConstants pins where a set of constants goes: the
// entity's own package (user.RoleAdmin, user.OrderFieldCreatedAt), never
// package entity. A package pays for every exported name of each package it
// imports, used or not, and nearly every package of an application imports
// package entity; one entity's package is imported by the code that works
// with that entity. (The fields a GraphQL list can be ordered by were
// entity.UserOrderFieldCreatedAt once: 12,079 constants on a 454-entity
// schema, and 0.07 of the 0.38 CPU-seconds a package that only returns an
// entity took to compile.)
func TestPackageEntityExportsNoConstants(t *testing.T) {
	for _, dir := range []string{"../tests/db/entity", "../tests/shapes/db/entity"} {
		paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil || len(paths) == 0 {
			t.Fatal(dir, err)
		}
		for _, p := range paths {
			f, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range f.Decls {
				decl, ok := d.(*ast.GenDecl)
				if !ok || decl.Tok != token.CONST {
					continue
				}
				for _, spec := range decl.Specs {
					for _, name := range spec.(*ast.ValueSpec).Names {
						if name.IsExported() {
							t.Errorf("%s declares the constant %s", p, name.Name)
						}
					}
				}
			}
		}
	}
	// And the order fields are where they went.
	src, err := parser.ParseFile(token.NewFileSet(), "../tests/db/user/user.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	ast.Inspect(src, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == "OrderFieldCreatedAt" {
			found = true
		}
		return !found
	})
	if !found {
		t.Error("tests/db/user does not declare OrderFieldCreatedAt")
	}
}

// TestPackageEntityDoesNotImportTheFilters pins the reach of the where
// inputs: an edge method takes its filter as an interface (see gqlModel), so
// a package that works with entities does not compile against the where
// input of every entity.
func TestPackageEntityDoesNotImportTheFilters(t *testing.T) {
	paths, err := filepath.Glob("../tests/db/entity/*.go")
	if err != nil || len(paths) == 0 {
		t.Fatal(err)
	}
	whereEdges := 0
	for _, p := range paths {
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range f.Imports {
			if strings.HasSuffix(strings.Trim(im.Path.Value, `"`), "/filter") {
				t.Errorf("%s imports %s", p, im.Path.Value)
			}
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil {
				continue
			}
			for _, param := range fn.Type.Params.List {
				if _, isInterface := param.Type.(*ast.InterfaceType); isInterface && len(param.Names) == 1 && param.Names[0].Name == "where" {
					whereEdges++
				}
			}
		}
	}
	if whereEdges == 0 {
		t.Error("no edge method of tests/db/entity takes a filter: the test tree no longer covers this")
	}
}
