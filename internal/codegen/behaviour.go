package codegen

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"slices"
	"strconv"
	"strings"
)

// behaviourMethods are the methods a schema package declares an entity's
// behaviour with. The loader leaves them, and what only they use, out of the
// program it compiles, and package entity binds them.
var behaviourMethods = []string{"Hooks", "Policy"}

// SchemaMethods reads the schema package's files in dir, as userFiles does,
// and reports which of entities have a Hooks or a Policy method there. An
// entity's method is declared on the type of its name in the schema package:
// a schema's on the schema, and a join entity's, which has no schema, on a
// type the user declares for it, such as type UserFriend struct{}.
//
// The method takes nothing and returns []velox.Hook[entity.E] or
// velox.Policy[entity.E], entity being the package of the generated
// entities and E the entity; another shape is a *FileError, and so is a
// Hooks or Policy method on a type that is not an entity's. The receiver may
// be a value or a pointer: package entity calls the method on a new *E of
// the schema package.
//
// The result holds only the entities that have at least one of the methods.
//
// genPkg is the name of the entities' package, which the errors use; "" is
// entity.
func SchemaMethods(dir, genPkg string, entities []string) (map[string]Bound, error) {
	if genPkg == "" {
		genPkg = EntityDir
	}
	out := map[string]Bound{}
	err := userFiles(dir, 0, func(fset *token.FileSet, path string, f *ast.File) error {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || !slices.Contains(behaviourMethods, fd.Name.Name) {
				continue
			}
			e, ptr := receiver(fd)
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
				b.Policy, b.PolicyPtr = true, ptr
				if pos := policyConds(f, fd, e); pos.IsValid() {
					b.PolicyConds = fmt.Sprintf("%s:%d", path, fset.Position(pos).Line)
				}
			} else {
				b.Hooks, b.HooksPtr = true, ptr
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

// Behaviour reads the schema package that pattern names, from the module
// that holds dir, and says how the package at genPath, the generated one, is
// written. bound is SchemaMethods. split is true when bound is not empty, or
// when the schema package imports genPath/entity, directly or through other
// packages: the entities then go in package entity, which the schema package
// imports, and the generated package re-exports them (Config.Split).
//
// The schema package never imports the generated package itself: in split
// mode it imports the schema package, and without split it may. An import of
// it, by a file of the schema package, is a *FileError at its line that
// names genPath/entity; one through other packages is an error that names
// the chain of imports. env is the environment of go list; nil means
// os.Environ().
func Behaviour(dir, pattern, genPath string, env, entities []string) (bound map[string]Bound, split bool, err error) {
	pkgDir, entity, err := checkGenImports(dir, pattern, genPath, env)
	if err != nil {
		return nil, false, err
	}
	if bound, err = SchemaMethods(pkgDir, EntityDir, entities); err != nil {
		return nil, false, err
	}
	return bound, len(bound) > 0 || entity, nil
}

// CheckGenImports refuses a schema package, at pattern, that imports
// genPath, the generated package, directly or through other packages, as
// Behaviour does. velox-gen runs it when the loader fails too, since go list
// reports such an import cycle with no chain. env is the environment of go
// list.
func CheckGenImports(dir, pattern, genPath string, env []string) error {
	_, _, err := checkGenImports(dir, pattern, genPath, env)
	return err
}

// checkGenImports is CheckGenImports, which also returns the schema
// package's directory and whether it imports genPath/entity.
func checkGenImports(dir, pattern, genPath string, env []string) (pkgDir string, entity bool, err error) {
	pkg, err := list(dir, pattern, env)
	if err != nil {
		return "", false, err
	}
	entityPath := genPath + "/" + EntityDir
	err = userFiles(pkg.Dir, parser.ImportsOnly, func(fset *token.FileSet, path string, f *ast.File) error {
		for _, im := range f.Imports {
			if p, _ := strconv.Unquote(im.Path.Value); p == genPath {
				return &FileError{Path: path, Line: fset.Position(im.Pos()).Line, Reason: fmt.Sprintf(
					"the schema package imports %s, the generated package, which imports the schema package to bind its Hooks and Policy: "+
						"import %s, which holds the entities, and write %s.User for %s.User", strconv.Quote(genPath), strconv.Quote(entityPath), EntityDir, guessName(genPath))}
			}
		}
		return nil
	})
	if err != nil {
		return "", false, err
	}
	chain, entity, err := genImports(pkg.Dir, pkg.ImportPath, genPath, entityPath, env)
	if err != nil {
		return "", false, err
	}
	if len(chain) > 0 {
		return "", false, fmt.Errorf("%s: the generated package imports the schema package to bind its Hooks and Policy, so a package the schema imports must import %s, which holds the entities, instead",
			strings.Join(chain, " imports "), strconv.Quote(entityPath))
	}
	return pkg.Dir, entity, nil
}

// genImports lists the packages the schema package at importPath imports,
// directly and through others, with go list run in dir, and returns the
// shortest chain of imports from it to genPath, or nil, and whether it
// reaches entityPath. A package go list cannot find, such as a generated
// package not written yet, is still a node of the graph.
func genImports(dir, importPath, genPath, entityPath string, env []string) (chain []string, entity bool, err error) {
	out, stderr, err := run(dir, env, "list", "-e", "-deps", "-json=ImportPath,Imports", importPath)
	if err != nil {
		return nil, false, fmt.Errorf("go list -deps %s: %w\n%s", importPath, err, strings.TrimSpace(stderr))
	}
	imports := map[string][]string{}
	dec := json.NewDecoder(strings.NewReader(out))
	for {
		var p struct {
			ImportPath string
			Imports    []string
		}
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, false, fmt.Errorf("reading go list output: %w", err)
		}
		imports[p.ImportPath] = p.Imports
	}
	prev := map[string]string{importPath: ""}
	queue := []string{importPath}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, q := range imports[p] {
			if _, seen := prev[q]; seen {
				continue
			}
			prev[q] = p
			switch q {
			case entityPath:
				entity = true
			case genPath:
				if chain == nil {
					for x := q; x != ""; x = prev[x] {
						chain = append([]string{x}, chain...)
					}
				}
				continue // what gen imports is not the schema's
			}
			queue = append(queue, q)
		}
	}
	return chain, entity, nil
}
