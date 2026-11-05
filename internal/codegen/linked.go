package codegen

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// LinkedEdges finds the relations each entity's behaviour names with
// velox.Edge, whose targets the generated code links (velox.Linked), and
// returns their names per entity name. bound and mixins say which methods
// declare the behaviour: an entity's own (SchemaMethods) and its mixins'
// (MixinBehaviour).
//
// It type-checks, with go/types, the schema package that pattern names and
// each package that declares a mixin with behaviour, from source, against
// the export data go list, run in the module that holds dir, writes for
// their imports. A call velox.Edge(name) counts when a behaviour method
// reaches it: in its body, or, transitively, in a function, method or
// variable of the same package that the body names. Its name must be a
// constant, which velox-gen can read; another is a *FileError at its line.
// An Edge in the conditions of HasWhere names a relation of the edge's
// target. A name the entity has no relation of is left to the statement,
// which fails. A call in another package is not seen: its relation is not
// linked, and the statement that names it fails.
func LinkedEdges(dir, pattern string, g *Graph, bound map[string]Bound, mixins map[string][]MixinBound, env []string) (map[string][]string, error) {
	var roots []edgeRoot
	pkgs := []string{g.Package}
	for _, e := range g.Entities {
		for _, mb := range mixins[e.Name] {
			for _, k := range slices.Sorted(maps.Keys(mb.Methods)) {
				roots = append(roots, edgeRoot{entity: e.Name, pkg: mb.PkgPath, typ: mb.Name, method: k})
			}
			if !slices.Contains(pkgs, mb.PkgPath) {
				pkgs = append(pkgs, mb.PkgPath)
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(bound)) {
		b := bound[name]
		for k, ok := range map[string]bool{"Policy": b.Policy, "Hooks": b.Hooks, "Interceptors": b.Interceptors} {
			if ok {
				roots = append(roots, edgeRoot{entity: name, pkg: g.Package, typ: name, method: k})
			}
		}
	}
	if len(roots) == 0 {
		return nil, nil
	}
	pattern = strings.TrimSpace(pattern)
	sc, err := checkSources(dir, g.Package, pattern, pkgs, env)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(roots, func(a, b edgeRoot) int {
		return strings.Compare(a.entity+"."+a.typ+"."+a.method, b.entity+"."+b.typ+"."+b.method)
	})
	edges := map[string]map[string]string{} // entity → edge name → target
	for _, e := range g.Entities {
		edges[e.Name] = map[string]string{}
		for _, ed := range e.Edges {
			edges[e.Name][ed.Name] = ed.Target
		}
	}
	linked := map[string][]string{}
	link := func(entity string, chain []string) {
		for _, name := range chain {
			target, ok := edges[entity][name]
			if !ok {
				return
			}
			if !slices.Contains(linked[entity], name) {
				linked[entity] = append(linked[entity], name)
			}
			entity = target
		}
	}
	var errs []error
	for _, r := range roots {
		key, err := sc.methodKey(r)
		if err != nil {
			return nil, err
		}
		if key == "" {
			continue
		}
		chains, ferrs := sc.walk(key)
		errs = append(errs, ferrs...)
		for _, c := range chains {
			link(r.entity, c)
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(dedupe(errs)...)
	}
	for _, names := range linked {
		slices.Sort(names)
	}
	return linked, nil
}

// edgeRoot is a behaviour method, method of the type typ of the package at
// pkg, for entity.
type edgeRoot struct{ entity, pkg, typ, method string }

// dedupe drops the errors whose text an earlier one has: one call is
// reached from many roots.
func dedupe(errs []error) []error {
	seen := map[string]bool{}
	var out []error
	for _, e := range errs {
		if !seen[e.Error()] {
			seen[e.Error()] = true
			out = append(out, e)
		}
	}
	return out
}

// sources are type-checked packages, by import path.
type sources struct {
	fset  *token.FileSet
	pkgs  map[string]*types.Package
	info  *types.Info
	decls map[string]ast.Node // a package-level function, method or variable by objKey
	memo  map[string]walked
}

type walked struct {
	chains [][]string
	errs   []error
}

// objKey names a package-level object across packages: "path.Name", or
// "path.Type.Method" for a method. Objects that export data gives and those
// a type-check from source gives differ, but their keys are equal.
func objKey(obj types.Object) string {
	if obj == nil || obj.Pkg() == nil {
		return ""
	}
	if fn, ok := obj.(*types.Func); ok {
		if recv := fn.Signature().Recv(); recv != nil {
			t := recv.Type()
			if p, ok := t.(*types.Pointer); ok {
				t = p.Elem()
			}
			if n, ok := types.Unalias(t).(*types.Named); ok {
				return obj.Pkg().Path() + "." + n.Obj().Name() + "." + obj.Name()
			}
			return ""
		}
	}
	if obj.Parent() != obj.Pkg().Scope() {
		return ""
	}
	return obj.Pkg().Path() + "." + obj.Name()
}

// checkSources type-checks the packages at paths from source. schemaPath is
// the import path of the schema package, which pattern names in dir.
func checkSources(dir, schemaPath, pattern string, paths []string, env []string) (*sources, error) {
	args := []string{"list", "-e", "-export", "-deps", "-json=ImportPath,Export,Dir,GoFiles,Error"}
	targets := []string{pattern}
	for _, p := range paths {
		if p != schemaPath {
			targets = append(targets, p)
		}
	}
	out, stderr, err := run(dir, env, append(args, targets...)...)
	if err != nil {
		return nil, fmt.Errorf("go list -export %s: %w\n%s", strings.Join(targets, " "), err, strings.TrimSpace(stderr))
	}
	type listed struct {
		ImportPath, Export, Dir string
		GoFiles                 []string
		Error                   *struct{ Err string }
	}
	exp := map[string]string{}
	all := map[string]*listed{}
	dec := json.NewDecoder(strings.NewReader(out))
	for {
		var p listed
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("reading go list output: %w", err)
		}
		all[p.ImportPath] = &p
		if p.Export != "" {
			exp[p.ImportPath] = p.Export
		}
	}
	sc := &sources{
		fset: token.NewFileSet(),
		pkgs: map[string]*types.Package{},
		info: &types.Info{
			Types: map[ast.Expr]types.TypeAndValue{},
			Defs:  map[*ast.Ident]types.Object{},
			Uses:  map[*ast.Ident]types.Object{},
		},
		decls: map[string]ast.Node{},
		memo:  map[string]walked{},
	}
	imp := importer.ForCompiler(sc.fset, "gc", func(path string) (io.ReadCloser, error) {
		f, ok := exp[path]
		if !ok {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(f)
	})
	for _, path := range paths {
		p := all[path]
		if p == nil {
			return nil, fmt.Errorf("go list did not list %s", path)
		}
		if p.Error != nil {
			return nil, errors.New(compileError(p.Error.Err))
		}
		var files []*ast.File
		for _, name := range p.GoFiles {
			f, err := parser.ParseFile(sc.fset, filepath.Join(p.Dir, name), nil, parser.SkipObjectResolution)
			if err != nil {
				return nil, err
			}
			files = append(files, f)
		}
		tp, err := (&types.Config{Importer: imp}).Check(path, sc.fset, files, sc.info)
		if err != nil {
			return nil, fmt.Errorf("type-checking %s: %w", path, err)
		}
		sc.pkgs[path] = tp
		for _, f := range files {
			for _, d := range f.Decls {
				switch d := d.(type) {
				case *ast.FuncDecl:
					if k := objKey(sc.info.Defs[d.Name]); k != "" && d.Body != nil {
						sc.decls[k] = d.Body
					}
				case *ast.GenDecl:
					for _, s := range d.Specs {
						vs, ok := s.(*ast.ValueSpec)
						if !ok {
							continue
						}
						for _, n := range vs.Names {
							if k := objKey(sc.info.Defs[n]); k != "" {
								sc.decls[k] = vs
							}
						}
					}
				}
			}
		}
	}
	return sc, nil
}

// methodKey is the objKey of r's method, which r's type may have through
// an embedded struct; "" when it has none.
func (sc *sources) methodKey(r edgeRoot) (string, error) {
	p := sc.pkgs[r.pkg]
	if p == nil {
		return "", fmt.Errorf("velox-gen did not read package %s", r.pkg)
	}
	tn, ok := p.Scope().Lookup(r.typ).(*types.TypeName)
	if !ok {
		return "", nil
	}
	obj, _, _ := types.LookupFieldOrMethod(tn.Type(), true, p, r.method)
	return objKey(obj), nil
}

// walk returns the relations the body of the object at key names with
// velox.Edge, and those of the package-level objects it names, each as a
// chain of names: an Edge in HasWhere's conditions follows the Edge of the
// HasWhere. A call in a function the body names is a chain of its own.
func (sc *sources) walk(key string) ([][]string, []error) {
	if w, ok := sc.memo[key]; ok {
		return w.chains, w.errs
	}
	sc.memo[key] = walked{} // a cycle ends here
	var w walked
	if n := sc.decls[key]; n != nil {
		sc.inspect(n, nil, &w)
	}
	sc.memo[key] = w
	return w.chains, w.errs
}

// inspect adds to w the relations n names, after prefix.
func (sc *sources) inspect(n ast.Node, prefix []string, w *walked) {
	ast.Inspect(n, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if name, ok := sc.edgeCall(n, w); ok {
				if name != "" {
					w.chains = append(w.chains, append(slices.Clone(prefix), name))
				}
				return false
			}
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "HasWhere" {
				if inner, ok := ast.Unparen(sel.X).(*ast.CallExpr); ok {
					if name, ok := sc.edgeCall(inner, w); ok {
						if name == "" {
							return false
						}
						chain := append(slices.Clone(prefix), name)
						w.chains = append(w.chains, chain)
						for _, a := range n.Args {
							sc.inspect(a, chain, w)
						}
						return false
					}
				}
			}
		case *ast.Ident:
			if k := objKey(sc.info.Uses[n]); k != "" && sc.decls[k] != nil {
				chains, errs := sc.walk(k)
				for _, c := range chains {
					w.chains = append(w.chains, append(slices.Clone(prefix), c...))
				}
				w.errs = append(w.errs, errs...)
			}
		}
		return true
	})
}

// edgeCall reports whether c is a call of velox.Edge, and returns the
// relation's name: "" when it is not a constant, which is recorded in w as
// a *FileError.
func (sc *sources) edgeCall(c *ast.CallExpr, w *walked) (string, bool) {
	var id *ast.Ident
	switch f := ast.Unparen(c.Fun).(type) {
	case *ast.SelectorExpr:
		id = f.Sel
	case *ast.Ident:
		id = f
	default:
		return "", false
	}
	fn, ok := sc.info.Uses[id].(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != veloxPkg || fn.Name() != "Edge" || fn.Signature().Recv() != nil || len(c.Args) != 1 {
		return "", false
	}
	tv := sc.info.Types[c.Args[0]]
	if tv.Value == nil || tv.Value.Kind() != constant.String {
		pos := sc.fset.Position(c.Pos())
		w.errs = append(w.errs, &FileError{Path: pos.Filename, Line: pos.Line, Reason: fmt.Sprintf(
			"%s names its relation with a value velox-gen cannot read; name it with a constant, so that velox-gen links the relation's target", exprString(c))})
		return "", true
	}
	return constant.StringVal(tv.Value), true
}
