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

// CheckBehaviour reads the behaviour each entity's schema declares: its own
// Policy, Hooks and Interceptors methods (bound, from SchemaMethods) and
// its mixins' (mixins, from MixinBehaviour). It returns, per entity name,
// the relations that behaviour names with velox.HasEdge or HasEdgeWith,
// whose targets the
// generated code links (velox.Linked), and a warning for each column it
// names with a value velox-gen cannot read.
//
// It type-checks, with go/types, the schema package that pattern names and
// each package that declares a mixin with behaviour, from source, against
// the export data go list, run in the module that holds dir, writes for
// their imports. A call counts when a behaviour method reaches it: in its
// body, or, transitively, in a function, method or variable of the same
// package that the body names. It is checked against every entity whose
// method reaches it:
//
//   - velox.HasEdge(name) and HasEdgeWith(name, ...) name a relation of
//     the entity. The name must be a constant, which velox-gen can read,
//     since velox-gen links the relation's target. The conditions of
//     HasEdgeWith name the target's columns.
//   - velox.FieldEQ(name, v) and the other Field functions name a column.
//     The value fits the column: FieldEQ, FieldNEQ, FieldGT, FieldGTE,
//     FieldLT and FieldLTE's, and each of FieldIn and FieldNotIn's.
//     FieldContains, FieldHasPrefix, FieldHasSuffix and FieldEqualFold need
//     a text column; FieldIsNull and FieldNotNull a nullable one.
//   - A *velox.Mutation's Set, Fill and Value, and a velox.Row's Value,
//     IsZero and Set, name a column of the entity; a Set or Fill value fits
//     it.
//
// A name is checked when go/types gives it a constant value. A value is
// checked when its type is not an interface: it must be of the column's
// kind, integers of any size being one kind and floats another, as velox's
// conversion at run time requires, and a constant must fit the column's
// type. nil fits only a Set or Fill of a nullable column. A refusal is a
// *FileError at the call's line that names the entity and the method; all
// are joined. A column named by a value velox-gen cannot read is a warning,
// returned with any refusal, by file and line; the statement checks it
// when it runs. A call in another package is not seen: it is checked at
// run time, and a relation it names is not linked, so the statement that
// names it fails.
func CheckBehaviour(dir, pattern string, g *Graph, bound map[string]Bound, mixins map[string][]MixinBound, env []string) (map[string][]string, []string, error) {
	var roots []edgeRoot
	pkgs := []string{g.Package}
	for _, e := range g.Entities {
		for _, mb := range mixins[e.Name] {
			for _, k := range slices.Sorted(maps.Keys(mb.Methods)) {
				roots = append(roots, edgeRoot{entity: e.Name, pkg: mb.PkgPath, typ: mb.Name, method: k, mixin: true})
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
		return nil, nil, nil
	}
	pattern = strings.TrimSpace(pattern)
	sc, err := checkSources(dir, g.Package, pattern, pkgs, env)
	if err != nil {
		return nil, nil, err
	}
	slices.SortFunc(roots, func(a, b edgeRoot) int {
		return strings.Compare(a.entity+"."+a.typ+"."+a.method, b.entity+"."+b.typ+"."+b.method)
	})
	ck := newBehaviourCheck(sc, g)
	for _, r := range roots {
		key, err := sc.methodKey(r)
		if err != nil {
			return nil, nil, err
		}
		if key == "" {
			continue
		}
		w := sc.walk(key)
		ck.errs = append(ck.errs, w.errs...)
		for _, u := range w.uses {
			ck.check(r, u)
		}
	}
	if len(ck.errs) > 0 {
		return nil, ck.warningList(), errors.Join(byLine(dedupe(ck.errs))...)
	}
	if len(ck.linked) == 0 {
		return nil, ck.warningList(), nil
	}
	for _, names := range ck.linked {
		slices.Sort(names)
	}
	return ck.linked, ck.warningList(), nil
}

// edgeRoot is a behaviour method, method of the type typ of the package at
// pkg, for entity; mixin says typ is a mixin the entity lists.
type edgeRoot struct {
	entity, pkg, typ, method string
	mixin                    bool
}

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

// walked is what a body names: its calls that name relations and columns,
// and the errors of the relations it names with a value velox-gen cannot
// read.
type walked struct {
	uses []use
	errs []error
}

// use is one call that names a relation or a column.
type use struct {
	kind useKind
	// chain are the relations from the root's entity to the entity the
	// call names: those of the HasEdgeWith calls that hold it. A
	// mutation's or a row's call names the root's entity, whatever chain.
	chain []string
	pos   token.Pos
	call  ast.Expr // the call, for errors
	named ast.Expr // the call that names the column, for the errors of its name
	name  string   // the name, "" when it is not a constant
	op    string   // the operator, FieldEQ's EQ, or the method: Set, Value, …
	args  []ast.Expr
	// spread says the values are passed as a slice (FieldIn(name, vs...)), which
	// velox-gen does not read.
	spread bool
}

// useKind is what a use names.
type useKind uint8

const (
	useEdge     useKind = iota // velox.HasEdge(name) or HasEdgeWith(name, ...)
	useColumn                  // velox.FieldEQ(name, v) and the other Field functions
	useMutation                // a *velox.Mutation's or a velox.Row's method
)

// prefixed is u after the relations prefix.
func (u use) prefixed(prefix []string) use {
	if len(prefix) > 0 {
		u.chain = append(slices.Clone(prefix), u.chain...)
	}
	return u
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

// walk returns the calls the body of the object at key makes that name
// relations and columns, and those of the package-level objects it names:
// a call in a function the body names counts as the body's.
func (sc *sources) walk(key string) walked {
	if w, ok := sc.memo[key]; ok {
		return w
	}
	sc.memo[key] = walked{} // a cycle ends here
	var w walked
	if n := sc.decls[key]; n != nil {
		sc.inspect(n, nil, &w)
	}
	sc.memo[key] = w
	return w
}

// inspect adds to w the calls n makes, after the relations prefix.
func (sc *sources) inspect(n ast.Node, prefix []string, w *walked) {
	ast.Inspect(n, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			return sc.call(n, prefix, w)
		case *ast.Ident:
			if k := objKey(sc.info.Uses[n]); k != "" && sc.decls[k] != nil {
				sub := sc.walk(k)
				for _, u := range sub.uses {
					w.uses = append(w.uses, u.prefixed(prefix))
				}
				w.errs = append(w.errs, sub.errs...)
			}
		}
		return true
	})
}

// call adds to w the call c, when it names a relation or a column, and
// reports whether ast.Inspect should look inside c: false when call has
// inspected what c holds itself.
func (sc *sources) call(c *ast.CallExpr, prefix []string, w *walked) bool {
	switch fn := sc.veloxFunc(c); {
	case fn == "HasEdge" || fn == "HasEdgeWith":
		name := sc.constName(c, w)
		if name != "" {
			w.uses = append(w.uses, use{kind: useEdge, chain: prefix, pos: c.Pos(), call: c, name: name})
			sc.inspectAll(c.Args[1:], append(slices.Clone(prefix), name), w)
		}
		return false
	case fieldOps[fn]:
		name := ""
		if tv := sc.info.Types[c.Args[0]]; tv.Value != nil && tv.Value.Kind() == constant.String {
			name = constant.StringVal(tv.Value)
		}
		w.uses = append(w.uses, use{kind: useColumn, chain: prefix, pos: c.Pos(), call: c, named: c, name: name,
			op: strings.TrimPrefix(fn, "Field"), args: c.Args[1:], spread: c.Ellipsis.IsValid()})
		sc.inspectAll(c.Args, prefix, w)
		return false
	}
	sel, ok := ast.Unparen(c.Fun).(*ast.SelectorExpr)
	if !ok {
		return true
	}
	fn, _ := sc.info.Uses[sel.Sel].(*types.Func)
	switch recvName(fn) {
	case veloxPkg + ".Mutation", enginePkg + ".Row":
		switch fn.Name() {
		case "Set", "Fill", "Value", "IsZero":
		default:
			return true
		}
		if len(c.Args) == 0 {
			return true
		}
		name := ""
		if tv := sc.info.Types[c.Args[0]]; tv.Value != nil && tv.Value.Kind() == constant.String {
			name = constant.StringVal(tv.Value)
		}
		w.uses = append(w.uses, use{kind: useMutation, pos: c.Pos(), call: c, name: name, op: fn.Name(), args: c.Args[1:]})
		sc.inspectAll([]ast.Expr{sel.X}, prefix, w)
		sc.inspectAll(c.Args, prefix, w)
		return false
	}
	return true
}

// inspectAll inspects each of xs.
func (sc *sources) inspectAll(xs []ast.Expr, prefix []string, w *walked) {
	for _, x := range xs {
		sc.inspect(x, prefix, w)
	}
}

// recvName is "path.Type" of the type whose method fn is, "" when fn is
// not a method of a named type.
func recvName(fn *types.Func) string {
	if fn == nil || fn.Signature().Recv() == nil {
		return ""
	}
	t := fn.Signature().Recv().Type()
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := types.Unalias(t).(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return ""
	}
	return n.Obj().Pkg().Path() + "." + n.Obj().Name()
}

// fieldOps are velox's functions that name a column.
var fieldOps = map[string]bool{
	"FieldEQ": true, "FieldNEQ": true, "FieldGT": true, "FieldGTE": true, "FieldLT": true, "FieldLTE": true,
	"FieldIn": true, "FieldNotIn": true, "FieldContains": true, "FieldHasPrefix": true, "FieldHasSuffix": true,
	"FieldEqualFold": true, "FieldIsNull": true, "FieldNotNull": true,
}

// veloxFunc returns the name of velox's function that c calls with at
// least one argument, "" when c calls another.
func (sc *sources) veloxFunc(c *ast.CallExpr) string {
	var id *ast.Ident
	switch f := ast.Unparen(c.Fun).(type) {
	case *ast.SelectorExpr:
		id = f.Sel
	case *ast.Ident:
		id = f
	default:
		return ""
	}
	fn, ok := sc.info.Uses[id].(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != veloxPkg || fn.Signature().Recv() != nil || len(c.Args) == 0 {
		return ""
	}
	return fn.Name()
}

// constName returns the relation c's first argument names: "" when it is
// not a constant, which is recorded in w as a *FileError.
func (sc *sources) constName(c *ast.CallExpr, w *walked) string {
	tv := sc.info.Types[c.Args[0]]
	if tv.Value != nil && tv.Value.Kind() == constant.String {
		return constant.StringVal(tv.Value)
	}
	pos := sc.fset.Position(c.Pos())
	w.errs = append(w.errs, &FileError{Path: pos.Filename, Line: pos.Line, Reason: fmt.Sprintf(
		"%s names its relation with a value velox-gen cannot read; name it with a constant, so that velox-gen links the relation's target", exprString(c))})
	return ""
}
