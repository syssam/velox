package codegen

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"
)

// behaviourMethods are the methods a schema package declares an entity's
// behaviour with. The loader leaves them, and what only they use, out of the
// program it compiles, and package runtime binds them.
var behaviourMethods = []string{"Hooks", "Policy"}

// SchemaMethods reads the schema package's files in dir, as userFiles does,
// and reports which of entities have a Hooks or a Policy method there. An
// entity's method is declared on the type of its name in the schema package:
// a schema's on the schema, and a join entity's, which has no schema, on a
// type the user declares for it, such as type UserFriend struct{}.
//
// The method takes nothing and returns []velox.Hook[gen.E] or
// velox.Policy[gen.E], gen being the generated package and E the entity; another shape is a *FileError, and
// so is a Hooks or Policy method on a type that is not an entity's. The
// receiver may be a value or a pointer: package runtime calls the method on a
// new *E of the schema package.
//
// The result holds only the entities that have at least one of the methods.
//
// genPkg is the generated package's name, which the errors use; "" is gen.
func SchemaMethods(dir, genPkg string, entities []string) (map[string]Bound, error) {
	if genPkg == "" {
		genPkg = "gen"
	}
	out := map[string]Bound{}
	err := userFiles(dir, 0, func(fset *token.FileSet, path string, f *ast.File) error {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || !slices.Contains(behaviourMethods, fd.Name.Name) {
				continue
			}
			e, _ := receiver(fd)
			if e == "" {
				continue
			}
			m := fd.Name.Name
			line := fset.Position(fd.Pos()).Line
			if !slices.Contains(entities, e) {
				return &FileError{Path: path, Line: line, Reason: fmt.Sprintf("%s.%s: %s is not an entity; Hooks and Policy are declared on an entity's schema, or on a type named like a join entity", e, m, e)}
			}
			if !behaviourSignature(fd, e) {
				want := "[]velox.Hook[" + genPkg + "." + e + "]"
				if m == "Policy" {
					want = "velox.Policy[" + genPkg + "." + e + "]"
				}
				return &FileError{Path: path, Line: line, Reason: fmt.Sprintf("%s.%s must take nothing and return %s (func (%[1]s) %[2]s() %[3]s)", e, m, want)}
			}
			b := out[e]
			if m == "Policy" {
				b.Policy = true
				if pos := policyConds(f, fd, e); pos.IsValid() {
					b.PolicyConds = fmt.Sprintf("%s:%d", path, fset.Position(pos).Line)
				}
			} else {
				b.Hooks = true
			}
			out[e] = b
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// behaviourSignature reports whether fd, a Hooks or Policy method of entity
// e, takes nothing and returns []<x>.Hook[<y>.e] or <x>.Policy[<y>.e]. It
// reads names only; the compiler checks the packages when it builds package
// runtime.
func behaviourSignature(fd *ast.FuncDecl, e string) bool {
	t := fd.Type
	if t.TypeParams != nil || len(t.Params.List) != 0 || t.Results == nil || len(t.Results.List) != 1 || len(t.Results.List[0].Names) > 1 {
		return false
	}
	r := t.Results.List[0].Type
	name := "Policy"
	if fd.Name.Name == "Hooks" {
		at, ok := r.(*ast.ArrayType)
		if !ok || at.Len != nil {
			return false
		}
		r, name = at.Elt, "Hook"
	}
	ix, ok := r.(*ast.IndexExpr)
	if !ok {
		return false
	}
	if sel, ok := ix.X.(*ast.SelectorExpr); !ok || sel.Sel.Name != name {
		return false
	}
	return entityArg(ix.Index, e)
}

// entityArg reports whether x names entity e of the generated package: <y>.e.
func entityArg(x ast.Expr, e string) bool {
	sel, ok := x.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != e {
		return false
	}
	_, ok = sel.X.(*ast.Ident)
	return ok
}

// Behaviour is SchemaMethods of the schema package that pattern names, from
// the module that holds dir. env is the environment of go list; nil means
// os.Environ().
func Behaviour(dir, pattern, genPkg string, env, entities []string) (map[string]Bound, error) {
	pkg, err := list(dir, pattern, env)
	if err != nil {
		return nil, err
	}
	return SchemaMethods(pkg.Dir, genPkg, entities)
}
