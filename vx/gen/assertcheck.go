package gen

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// What a hook, a policy rule and an interceptor of the schema receive, and
// the one Go type behind each here. velox generates a mutation, a query and a
// filter type per entity, with a method per field (SetWorkspaceID,
// WhereWorkspaceID); this generator has one type for all entities and none of
// those methods.
var assertSubjects = map[string][]string{
	"github.com/syssam/velox.Mutation":       {"Mutation"},
	"github.com/syssam/velox.Query":          {"Query"},
	"github.com/syssam/velox/privacy.Filter": {"Query", "Mutation"},
}

// checkAssertions reads the source of the schema package, and of the packages
// of its module it uses, for type assertions that can never hold in generated
// code of this generator, and returns them as one error.
//
// A hook shared by many entities is usually written the way Ent's
// documentation shows: assert the mutation to an interface of the typed
// accessors the entities have in common,
//
//	ml, ok := m.(interface{ SetWorkspaceID(int64) })
//	if !ok {
//		return next.Mutate(ctx, m)
//	}
//
// and skip the ones that do not have them. No mutation has such a method
// here, so every entity is skipped: the hook compiles, runs, and does
// nothing, and what it was for (the tenant of a new row, who deleted one) is
// silently not written. That cannot be left to a sentence in a README. The
// form that works with both generators is m.Field("workspace_id") and
// m.SetField("workspace_id", id).
func checkAssertions(schemaPkg string) error {
	// The packages to read: the schema's, and those of the same module it
	// imports (rules and hooks are often in packages of their own).
	listed, err := packages.Load(&packages.Config{
		Mode: packages.NeedName | packages.NeedImports | packages.NeedDeps | packages.NeedModule,
	}, schemaPkg)
	if err != nil {
		return fmt.Errorf("vx: reading the schema's source: %w", err)
	}
	var paths []string
	seen := map[string]bool{}
	var walk func(p *packages.Package, module string)
	walk = func(p *packages.Package, module string) {
		if seen[p.PkgPath] {
			return
		}
		seen[p.PkgPath] = true
		if p.Module == nil || p.Module.Path != module {
			return
		}
		paths = append(paths, p.PkgPath)
		for _, dep := range p.Imports {
			walk(dep, module)
		}
	}
	for _, p := range listed {
		if len(p.Errors) > 0 {
			return fmt.Errorf("vx: reading the schema's source: %s", p.Errors[0])
		}
		if p.Module != nil {
			walk(p, p.Module.Path)
		}
	}
	if len(paths) == 0 {
		return fmt.Errorf("vx: reading the schema's source: package %s is in no module", schemaPkg)
	}
	sort.Strings(paths)
	pkgs, err := packages.Load(&packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedImports |
			packages.NeedTypes | packages.NeedTypesSizes | packages.NeedSyntax | packages.NeedTypesInfo,
	}, append(paths, ormPath)...)
	if err != nil {
		return fmt.Errorf("vx: reading the schema's source: %w", err)
	}
	// One load, so that a type two packages name is one object.
	concrete := map[string]types.Type{}
	for _, p := range pkgs {
		if p.PkgPath != ormPath || p.Types == nil {
			continue
		}
		for _, name := range []string{"Mutation", "Query"} {
			if obj := p.Types.Scope().Lookup(name); obj != nil {
				concrete[name] = types.NewPointer(obj.Type())
			}
		}
	}
	if len(concrete) != 2 {
		return fmt.Errorf("vx: reading the schema's source: package %s did not load", ormPath)
	}

	for _, p := range pkgs {
		for _, e := range p.Errors {
			// What does not type-check cannot be checked, and must not pass
			// for checked.
			return fmt.Errorf("vx: reading the schema's source: %s", e)
		}
	}
	// The interfaces themselves, to recognize an operand that is one of
	// them under another name (an interface that embeds velox.Mutation).
	subjects := map[string]*types.Interface{}
	visited := map[*types.Package]bool{}
	var visit func(tp *types.Package)
	visit = func(tp *types.Package) {
		if tp == nil || visited[tp] {
			return
		}
		visited[tp] = true
		for name := range assertSubjects {
			k := strings.LastIndex(name, ".")
			if tp.Path() == name[:k] {
				if obj := tp.Scope().Lookup(name[k+1:]); obj != nil {
					if iface, ok := obj.Type().Underlying().(*types.Interface); ok {
						subjects[name] = iface
					}
				}
			}
		}
		for _, dep := range tp.Imports() {
			visit(dep)
		}
	}
	for _, p := range pkgs {
		visit(p.Types)
	}

	var found []string
	// holds reports whether a value this generator passes for the subject
	// can be asserted to t, and what t wants that it lacks.
	holds := func(subject string, t types.Type) (bool, []string) {
		var missing []string
		for _, name := range assertSubjects[subject] {
			miss, ok := satisfies(concrete[name], t)
			if ok {
				return true, nil
			}
			missing = miss
		}
		return false, missing
	}
	// Assertions to a type parameter are judged where the function is
	// instantiated: as[interface{ SetName(string) }](m).
	type generic struct {
		fn      *types.Func
		param   int
		subject string
	}
	var generics []generic
	for _, p := range pkgs {
		if p.PkgPath == ormPath || p.TypesInfo == nil {
			continue
		}
		info := p.TypesInfo
		// subjectOf names the interface an operand is, looking through a
		// conversion to an interface (any(m)) and at interfaces that embed
		// one of the subjects.
		var subjectOf func(e ast.Expr) string
		subjectOf = func(e ast.Expr) string {
			e = ast.Unparen(e)
			if call, ok := e.(*ast.CallExpr); ok && len(call.Args) == 1 {
				if tv, ok := info.Types[call.Fun]; ok && tv.IsType() {
					return subjectOf(call.Args[0])
				}
			}
			t := info.TypeOf(e)
			if t == nil {
				return ""
			}
			if named, ok := types.Unalias(t).(*types.Named); ok && named.Obj().Pkg() != nil {
				if name := named.Obj().Pkg().Path() + "." + named.Obj().Name(); assertSubjects[name] != nil {
					return name
				}
			}
			if _, isInterface := t.Underlying().(*types.Interface); isInterface {
				// The narrowest first: a Mutation is also a Filter's
				// superset in methods only by accident of the three.
				for _, name := range []string{"github.com/syssam/velox.Mutation", "github.com/syssam/velox.Query", "github.com/syssam/velox/privacy.Filter"} {
					if iface := subjects[name]; iface != nil && iface.NumMethods() > 0 && types.Implements(t, iface) {
						return name
					}
				}
			}
			return ""
		}
		skip := func(pos token.Pos) bool { return nocheck(p.Fset, p.Syntax, pos) }
		say := func(pos token.Pos, subject string, t types.Type, missing []string) {
			what := "to " + types.TypeString(t, types.RelativeTo(p.Types))
			if len(missing) > 0 {
				what = "to an interface with " + strings.Join(missing, ", ")
			}
			found = append(found, fmt.Sprintf("%s: a %s is asserted %s, which nothing this generator passes has: the assertion never holds",
				p.Fset.Position(pos), subject[strings.LastIndex(subject, "/")+1:], what))
		}
		for _, f := range p.Syntax {
			var funcs []*types.Func // the functions around the node being visited
			var stack []ast.Node
			ast.Inspect(f, func(n ast.Node) bool {
				if n == nil {
					if _, ok := stack[len(stack)-1].(*ast.FuncDecl); ok {
						funcs = funcs[:len(funcs)-1]
					}
					stack = stack[:len(stack)-1]
					return true
				}
				stack = append(stack, n)
				if decl, ok := n.(*ast.FuncDecl); ok {
					fn, _ := info.Defs[decl.Name].(*types.Func)
					funcs = append(funcs, fn)
				}
				// check looks at the types of one assertion or one case:
				// it is fine when any of them can hold.
				check := func(pos token.Pos, operand ast.Expr, targets []ast.Expr) {
					subject := subjectOf(operand)
					if subject == "" || skip(pos) {
						return
					}
					var t0 types.Type
					var missing []string
					for _, target := range targets {
						if id, isNil := target.(*ast.Ident); isNil && id.Name == "nil" {
							return
						}
						t := info.TypeOf(target)
						if t == nil {
							return
						}
						if tp, isParam := types.Unalias(t).(*types.TypeParam); isParam {
							if len(funcs) > 0 && funcs[len(funcs)-1] != nil {
								generics = append(generics, generic{funcs[len(funcs)-1], tp.Index(), subject})
							}
							return
						}
						ok, miss := holds(subject, t)
						if ok {
							return
						}
						t0, missing = t, miss
					}
					if t0 != nil {
						say(pos, subject, t0, missing)
					}
				}
				switch n := n.(type) {
				case *ast.TypeAssertExpr:
					if n.Type != nil { // not the x.(type) of a switch
						check(n.Pos(), n.X, []ast.Expr{n.Type})
					}
				case *ast.TypeSwitchStmt:
					operand := switchOperand(n)
					if operand == nil {
						break
					}
					for _, c := range n.Body.List {
						if clause, isCase := c.(*ast.CaseClause); isCase && len(clause.List) > 0 {
							check(clause.Pos(), operand, clause.List)
						}
					}
				}
				return true
			})
		}
	}
	for _, p := range pkgs {
		if p.PkgPath == ormPath || p.TypesInfo == nil {
			continue
		}
		for id, inst := range p.TypesInfo.Instances {
			fn, _ := p.TypesInfo.Uses[id].(*types.Func)
			for _, g := range generics {
				if fn == nil || fn.Origin() != g.fn || g.param >= inst.TypeArgs.Len() || nocheck(p.Fset, p.Syntax, id.Pos()) {
					continue
				}
				t := inst.TypeArgs.At(g.param)
				if ok, missing := holds(g.subject, t); !ok {
					what := "to " + types.TypeString(t, types.RelativeTo(p.Types))
					if len(missing) > 0 {
						what = "to an interface with " + strings.Join(missing, ", ")
					}
					found = append(found, fmt.Sprintf("%s: through %s, a %s is asserted %s, which nothing this generator passes has: the assertion never holds",
						p.Fset.Position(id.Pos()), fn.Name(), g.subject[strings.LastIndex(g.subject, "/")+1:], what))
				}
			}
		}
	}
	if len(found) == 0 {
		return nil
	}
	sort.Strings(found)
	found = slices.Compact(found)
	return fmt.Errorf("vx: the schema's code relies on methods generated code does not have here (a typed setter or getter per field). "+
		"Read and write a field of a mutation by name instead: m.Field(\"workspace_id\"), m.SetField(\"workspace_id\", id), m.OldField(ctx, \"status\"); "+
		"narrow a filter with f.WhereP. These work with velox's generated code too. "+
		"An assertion that is meant to fail here (the typed way first, the way by name after it) is marked with a comment on its line or the line above: //vx:nocheck\n\t%s", strings.Join(found, "\n\t"))
}

// nocheck reports whether the line of pos, or the one above, carries the
// comment that exempts an assertion: code that tries the typed accessors of
// velox's mutations first and goes by name when the assertion fails is
// correct under both generators, and only its author knows that it is that.
func nocheck(fset *token.FileSet, files []*ast.File, pos token.Pos) bool {
	at := fset.Position(pos)
	for _, f := range files {
		if fset.Position(f.Pos()).Filename != at.Filename {
			continue
		}
		for _, group := range f.Comments {
			for _, c := range group.List {
				if line := fset.Position(c.Pos()).Line; (line == at.Line || line == at.Line-1) && strings.HasPrefix(c.Text, "//vx:nocheck") {
					return true
				}
			}
		}
	}
	return false
}

// switchOperand returns x of `switch v := x.(type)` or `switch x.(type)`.
func switchOperand(s *ast.TypeSwitchStmt) ast.Expr {
	var e ast.Expr
	switch a := s.Assign.(type) {
	case *ast.AssignStmt:
		if len(a.Rhs) == 1 {
			e = a.Rhs[0]
		}
	case *ast.ExprStmt:
		e = a.X
	}
	if ta, ok := e.(*ast.TypeAssertExpr); ok {
		return ta.X
	}
	return nil
}

// satisfies reports whether a value of the concrete type can be asserted to
// target, and the methods of target it lacks when not.
func satisfies(concrete, target types.Type) (missing []string, ok bool) {
	iface, isInterface := target.Underlying().(*types.Interface)
	if !isInterface {
		return nil, types.Identical(concrete, target)
	}
	if types.Implements(concrete, iface) {
		return nil, true
	}
	have := types.NewMethodSet(concrete)
	for i := range iface.NumMethods() {
		m := iface.Method(i)
		if sel := have.Lookup(m.Pkg(), m.Name()); sel == nil || !types.Identical(sel.Type(), m.Type()) {
			missing = append(missing, m.Name())
		}
	}
	return missing, false
}
