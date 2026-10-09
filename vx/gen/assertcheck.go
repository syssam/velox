package gen

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
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

	var found []string
	for _, p := range pkgs {
		if p.PkgPath == ormPath || p.TypesInfo == nil {
			continue
		}
		report := func(pos token.Pos, operand ast.Expr, target ast.Expr) {
			subject := subjectOf(p.TypesInfo.TypeOf(operand))
			names := assertSubjects[subject]
			t := p.TypesInfo.TypeOf(target)
			if names == nil || t == nil {
				return
			}
			var missing []string
			for _, name := range names {
				miss, ok := satisfies(concrete[name], t)
				if ok {
					return // it holds for a query or for a mutation
				}
				missing = miss
			}
			what := "to " + types.TypeString(t, types.RelativeTo(p.Types))
			if len(missing) > 0 {
				what = "to an interface with " + strings.Join(missing, ", ")
			}
			found = append(found, fmt.Sprintf("%s: a %s is asserted %s, which nothing this generator passes has: the assertion never holds",
				p.Fset.Position(pos), subject[strings.LastIndex(subject, "/")+1:], what))
		}
		for _, f := range p.Syntax {
			ast.Inspect(f, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.TypeAssertExpr:
					if n.Type != nil { // not the x.(type) of a switch
						report(n.Pos(), n.X, n.Type)
					}
				case *ast.TypeSwitchStmt:
					operand := switchOperand(n)
					if operand == nil {
						break
					}
					for _, c := range n.Body.List {
						clause, isCase := c.(*ast.CaseClause)
						if !isCase {
							continue
						}
						for _, target := range clause.List {
							if id, isNil := target.(*ast.Ident); isNil && id.Name == "nil" {
								continue
							}
							report(target.Pos(), operand, target)
						}
					}
				}
				return true
			})
		}
	}
	if len(found) == 0 {
		return nil
	}
	sort.Strings(found)
	return fmt.Errorf("vx: the schema's code relies on methods generated code does not have here (a typed setter or getter per field). "+
		"Read and write a field of a mutation by name instead: m.Field(\"workspace_id\"), m.SetField(\"workspace_id\", id), m.OldField(ctx, \"status\"); "+
		"narrow a filter with f.WhereP. These work with velox's generated code too.\n\t%s", strings.Join(found, "\n\t"))
}

// subjectOf names the interface a hook, rule or interceptor is handed, when
// t is one.
func subjectOf(t types.Type) string {
	named, ok := types.Unalias(t).(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return ""
	}
	return named.Obj().Pkg().Path() + "." + named.Obj().Name()
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
