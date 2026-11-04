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
// program it compiles, and package runtime binds them.
var behaviourMethods = []string{"Hooks", "Interceptors", "Policy"}

// behaviourResult is the result method m of entity e must return, in the
// generated package genPkg: []velox.Hook[gen.E], velox.Policy[gen.E] or
// []velox.Interceptor[gen.E].
func behaviourResult(m, genPkg, e string) string {
	switch m {
	case "Policy":
		return "velox.Policy[" + genPkg + "." + e + "]"
	case "Interceptors":
		return "[]velox.Interceptor[" + genPkg + "." + e + "]"
	}
	return "[]velox.Hook[" + genPkg + "." + e + "]"
}

// SchemaMethods reads the schema package's files in dir, as userFiles does,
// and reports which of entities have a Hooks, a Policy or an Interceptors
// method there. An
// entity's method is declared on the type of its name in the schema package:
// a schema's on the schema, and a join entity's, which has no schema, on a
// type the user declares for it, such as type UserFriend struct{}.
//
// The method takes nothing and returns []velox.Hook[gen.E],
// velox.Policy[gen.E] or []velox.Interceptor[gen.E], gen being the generated
// package and E the entity; another shape is a *FileError, and so is such a
// method on a type that is not an entity's. The receiver may be a value or a pointer: package
// runtime binds the method of a new value, or a new pointer, of the schema
// package's type.
//
// The result holds only the entities that have at least one of the methods.
//
// genPkg is the name of the generated package, which the errors use; "" is
// gen.
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
			e, ptr := receiver(fd)
			if e == "" {
				continue
			}
			m := fd.Name.Name
			line := fset.Position(fd.Pos()).Line
			if !slices.Contains(entities, e) {
				return &FileError{Path: path, Line: line, Reason: fmt.Sprintf("%s.%s: %s is not an entity; Hooks, Policy and Interceptors are declared on an entity's schema, or on a type named like a join entity", e, m, e)}
			}
			if !behaviourSignature(fd, e) {
				want := behaviourResult(m, genPkg, e)
				return &FileError{Path: path, Line: line, Reason: fmt.Sprintf("%s.%s must take nothing and return %s (func (%[1]s) %[2]s() %[3]s)", e, m, want)}
			}
			b := out[e]
			switch m {
			case "Policy":
				b.Policy, b.PolicyPtr = true, ptr
				if pos := policyConds(f, fd, e); pos.IsValid() {
					b.PolicyConds = fmt.Sprintf("%s:%d", path, fset.Position(pos).Line)
				}
			case "Interceptors":
				b.Interceptors, b.InterceptorsPtr = true, ptr
			default:
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

// behaviourSignature reports whether fd, a Hooks, Policy or Interceptors
// method of entity e, takes nothing and returns []<x>.Hook[<y>.e],
// <x>.Policy[<y>.e] or []<x>.Interceptor[<y>.e]. It reads names only; the
// compiler checks the packages when it builds package runtime.
func behaviourSignature(fd *ast.FuncDecl, e string) bool {
	t := fd.Type
	if t.TypeParams != nil || len(t.Params.List) != 0 || t.Results == nil || len(t.Results.List) != 1 || len(t.Results.List[0].Names) > 1 {
		return false
	}
	r := t.Results.List[0].Type
	name := "Policy"
	if m := fd.Name.Name; m != "Policy" {
		at, ok := r.(*ast.ArrayType)
		if !ok || at.Len != nil {
			return false
		}
		r, name = at.Elt, strings.TrimSuffix(m, "s")
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
// written. bound is SchemaMethods. importsGen is true when bound is not
// empty, or when the schema package imports genPath, directly or through
// other packages: the generated package then does not import the schema
// package, and package runtime, below it, binds the schema
// (Config.SchemaImportsGen).
//
// The schema package never imports package runtime, which imports it. An
// import of it, by a file of the schema package, is a *FileError at its
// line; one through other packages is an error that names the chain of
// imports. env is the environment of go list; nil means os.Environ().
func Behaviour(dir, pattern, genPath string, env, entities []string) (bound map[string]Bound, importsGen bool, err error) {
	pkgDir, importsGen, err := checkGenImports(dir, pattern, genPath, env)
	if err != nil {
		return nil, false, err
	}
	if bound, err = SchemaMethods(pkgDir, guessName(genPath), entities); err != nil {
		return nil, false, err
	}
	return bound, len(bound) > 0 || importsGen, nil
}

// CheckGenImports refuses a schema package, at pattern, that imports
// genPath/runtime, directly or through other packages, as Behaviour does.
// velox-gen runs it when the loader fails too, since go list reports such an
// import cycle with no chain. env is the environment of go list.
func CheckGenImports(dir, pattern, genPath string, env []string) error {
	_, _, err := checkGenImports(dir, pattern, genPath, env)
	return err
}

// checkGenImports is CheckGenImports, which also returns the schema
// package's directory and whether it imports genPath.
func checkGenImports(dir, pattern, genPath string, env []string) (pkgDir string, importsGen bool, err error) {
	pkg, err := list(dir, pattern, env)
	if err != nil {
		return "", false, err
	}
	rtPath := genPath + "/" + RuntimeDir
	err = userFiles(pkg.Dir, parser.ImportsOnly, func(fset *token.FileSet, path string, f *ast.File) error {
		for _, im := range f.Imports {
			if p, _ := strconv.Unquote(im.Path.Value); p == rtPath {
				return &FileError{Path: path, Line: fset.Position(im.Pos()).Line, Reason: fmt.Sprintf(
					"the schema package imports %s, which imports the schema package to bind its Hooks and Policy: "+
						"import %s for the entities, and %s only in the program's main package and its tests", strconv.Quote(rtPath), strconv.Quote(genPath), strconv.Quote(rtPath))}
			}
		}
		return nil
	})
	if err != nil {
		return "", false, err
	}
	chain, importsGen, err := genImports(pkg.Dir, pkg.ImportPath, genPath, rtPath, env)
	if err != nil {
		return "", false, err
	}
	if len(chain) > 0 {
		return "", false, fmt.Errorf("%s: package runtime imports the schema package to bind its Hooks and Policy, so no package the schema imports may import it; import %s for the entities",
			strings.Join(chain, " imports "), strconv.Quote(genPath))
	}
	return pkg.Dir, importsGen, nil
}

// genImports lists the packages the schema package at importPath imports,
// directly and through others, with go list run in dir, and returns the
// shortest chain of imports from it to rtPath, or nil, and whether it
// reaches genPath. A package go list cannot find, such as a generated
// package not written yet, is still a node of the graph. What genPath and
// rtPath import is not followed: an older generated package may import the
// schema package.
func genImports(dir, importPath, genPath, rtPath string, env []string) (chain []string, importsGen bool, err error) {
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
			case genPath:
				importsGen = true
				continue
			case rtPath:
				if chain == nil {
					for x := q; x != ""; x = prev[x] {
						chain = append([]string{x}, chain...)
					}
				}
				continue
			}
			queue = append(queue, q)
		}
	}
	return chain, importsGen, nil
}
