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
// behaviour with, on the entity's schema or on a mixin.
var behaviourMethods = []string{"Hooks", "Interceptors", "Policy"}

// behaviourResult is the result of a behaviour method m: velox.Policy,
// []velox.Hook or []velox.Interceptor.
func behaviourResult(m string) string {
	switch m {
	case "Policy":
		return "velox.Policy"
	case "Interceptors":
		return "[]velox.Interceptor"
	}
	return "[]velox.Hook"
}

// SchemaMethods reads the schema package's files in dir, as userFiles does,
// and reports which of entities have a Hooks, a Policy or an Interceptors
// method there. An entity's method is declared on the type of its name in
// the schema package: a schema's on the schema, and a join entity's, which
// has no schema, on a type the user declares for it, such as
// type UserFriend struct{}.
//
// The method takes nothing and returns velox.Policy, []velox.Hook or
// []velox.Interceptor; another shape is a *FileError, the typed shape of an
// older velox (velox.Policy[veloxgen.E]) among them. The receiver may be a value
// or a pointer: the generated code binds the method of a new value, or a
// new pointer, of the schema package's type. A generic behaviour method on
// another type is a *FileError too: behaviour methods are not generic. A
// method that is not generic on another type is a mixin's, which
// MixinBehaviour reads.
//
// The result holds only the entities that have at least one of the methods.
func SchemaMethods(dir string, entities []string) (map[string]Bound, error) {
	out := map[string]Bound{}
	err := userFiles(dir, 0, func(fset *token.FileSet, path string, f *ast.File) error {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || !slices.Contains(behaviourMethods, fd.Name.Name) {
				continue
			}
			e := receiver(fd)
			if e == "" {
				continue
			}
			m := fd.Name.Name
			line := fset.Position(fd.Pos()).Line
			if !slices.Contains(entities, e) {
				if fd.Type.TypeParams != nil {
					return &FileError{Path: path, Line: line, Reason: notGeneric(e, m)}
				}
				continue // a mixin's, which MixinBehaviour reads
			}
			if !behaviourSignature(fd.Type, m, localName(f, veloxPkg), localName(f, privacyPkg)) {
				return &FileError{Path: path, Line: line, Reason: fmt.Sprintf("%s.%s must take nothing and return %s: func (%[1]s) %[2]s() %[3]s", e, m, behaviourResult(m))}
			}
			b := out[e]
			switch m {
			case "Policy":
				b.Policy = true
				if pos := policyConds(f, fd); pos.IsValid() {
					b.PolicyConds = fmt.Sprintf("%s:%d", path, fset.Position(pos).Line)
				}
			case "Interceptors":
				b.Interceptors = true
			default:
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

// notGeneric is the reason a generic behaviour method m of type t is
// refused.
func notGeneric(t, m string) string {
	return fmt.Sprintf("%s.%s: behaviour methods are not generic: func (%[1]s) %[2]s() %[3]s", t, m, behaviourResult(m))
}

// behaviourSignature reports whether t, the type of a Hooks, Policy or
// Interceptors method m, takes nothing, has no type parameters and returns
// []<velox>.Hook, <velox>.Policy or []<velox>.Interceptor, velox being the
// name the file imports velox under, or any name when it is "". A Policy
// may also return <privacy>.Policy, privacy being the name the file imports
// velox's privacy package under, whose Policy is velox.Policy. It reads
// names only; the compiler checks the types when it builds the generated
// package.
func behaviourSignature(t *ast.FuncType, m, velox, privacy string) bool {
	if t.TypeParams != nil || len(t.Params.List) != 0 || t.Results == nil || len(t.Results.List) != 1 || len(t.Results.List[0].Names) > 1 {
		return false
	}
	r := t.Results.List[0].Type
	name := "Policy"
	if m != "Policy" {
		at, ok := r.(*ast.ArrayType)
		if !ok || at.Len != nil {
			return false
		}
		r, name = at.Elt, strings.TrimSuffix(m, "s")
	}
	sel, ok := r.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && (velox == "" || id.Name == velox || m == "Policy" && privacy != "" && id.Name == privacy)
}

// Behaviour reads the schema package that pattern names, from the module
// that holds dir, and returns which of entities declare behaviour methods
// there (SchemaMethods).
//
// The schema package declares behaviour by column name and does not import
// the generated package at genPath, which imports it. An import of it, by
// a file of the schema package, is a *FileError at its line; one through
// other packages is an error that names the chain of imports. env is the
// environment of go list; nil means os.Environ().
func Behaviour(dir, pattern, genPath string, env, entities []string) (map[string]Bound, error) {
	pkgDir, err := checkGenImports(dir, pattern, genPath, env)
	if err != nil {
		return nil, err
	}
	return SchemaMethods(pkgDir, entities)
}

// CheckGenImports refuses a schema package, at pattern, that imports
// genPath, directly or through other packages, as Behaviour does. velox-gen
// runs it when the loader fails too, since go list reports such an import
// cycle with no chain. env is the environment of go list.
func CheckGenImports(dir, pattern, genPath string, env []string) error {
	_, err := checkGenImports(dir, pattern, genPath, env)
	return err
}

// genImportReason is why the schema package may not import the generated
// one.
const genImportReason = "the schema declares behaviour by column name and does not use veloxgen, which imports the schema; move the code that needs veloxgen out of the schema package"

// checkGenImports is CheckGenImports, which also returns the schema
// package's directory.
func checkGenImports(dir, pattern, genPath string, env []string) (string, error) {
	pkg, err := listAny(dir, pattern, env)
	if err != nil {
		return "", err
	}
	err = userFiles(pkg.Dir, parser.ImportsOnly, func(fset *token.FileSet, path string, f *ast.File) error {
		for _, im := range f.Imports {
			if p, _ := strconv.Unquote(im.Path.Value); p == genPath {
				return &FileError{Path: path, Line: fset.Position(im.Pos()).Line, Reason: fmt.Sprintf("the schema package imports %s: %s", p, genImportReason)}
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	chain, err := genImports(dir, pkg.ImportPath, genPath, env)
	if err != nil {
		return "", err
	}
	if len(chain) > 0 {
		return "", fmt.Errorf("%s: %s", strings.Join(chain, " imports "), genImportReason)
	}
	return pkg.Dir, nil
}

// listAny is list, but for a package whose own error is go list's, such as
// an import cycle through the generated package, which checkGenImports
// explains: it returns the package go list -e reports, when it has a
// directory, and list's error otherwise.
func listAny(dir, pattern string, env []string) (*listed, error) {
	pkg, err := list(dir, pattern, env)
	if err == nil {
		return pkg, nil
	}
	out, _, lerr := run(dir, env, "list", "-e", listFields, pattern)
	if lerr != nil {
		return nil, err
	}
	var p listed
	dec := json.NewDecoder(strings.NewReader(out))
	if dec.Decode(&p) != nil || dec.More() || p.Dir == "" || p.ImportPath == "" {
		return nil, err
	}
	return &p, nil
}

// genImports lists the packages the schema package at importPath imports,
// directly and through others, with go list run in dir, and returns the
// shortest chain of imports from it to genPath, and nil when none reaches
// it. A package go list cannot find, such as a
// generated package not written yet, is still a node of the graph. What
// the generated packages import is not followed: they import the schema
// package.
func genImports(dir, importPath, genPath string, env []string) ([]string, error) {
	out, stderr, err := run(dir, env, "list", "-e", "-deps", "-json=ImportPath,Imports", importPath)
	if err != nil {
		return nil, fmt.Errorf("go list -deps %s: %w\n%s", importPath, err, strings.TrimSpace(stderr))
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
			return nil, fmt.Errorf("reading go list output: %w", err)
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
			if q == genPath {
				var chain []string
				for x := q; x != ""; x = prev[x] {
					chain = append([]string{x}, chain...)
				}
				return chain, nil
			}
			queue = append(queue, q)
		}
	}
	return nil, nil
}
