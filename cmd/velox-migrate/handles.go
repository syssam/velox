package main

import (
	"go/ast"
	"go/token"
	"go/types"
)

// A function that took a *Client could be given a transaction's client,
// tx.Client(), which was a *Client too. The two are different types now, a
// *orm.DB and a *orm.Tx, and what both are is an orm.Executor. A parameter
// can take that type when the function only queries on it: selects an
// entity's client from it, or hands it to another function whose parameter
// is the same kind. One that begins a transaction on it, closes it or keeps
// it needs the database, and stays a *orm.DB. So does one that compares it
// with nil: a nil *orm.DB handed over as an orm.Executor is an interface
// that is not nil, and the comparison would quietly change its answer.

// paramOf returns the parameter of the function a call calls that its i-th
// argument is for, when the call names a function or a method declared in
// source.
func paramOf(info *types.Info, call *ast.CallExpr, i int) *types.Var {
	fun := ast.Unparen(call.Fun)
	switch x := fun.(type) {
	case *ast.IndexExpr:
		fun = x.X
	case *ast.IndexListExpr:
		fun = x.X
	}
	var id *ast.Ident
	switch x := fun.(type) {
	case *ast.Ident:
		id = x
	case *ast.SelectorExpr:
		id = x.Sel
	}
	if id == nil {
		return nil
	}
	fn, ok := info.Uses[id].(*types.Func)
	if !ok {
		return nil
	}
	params := fn.Origin().Signature().Params()
	if fn.Origin().Signature().Variadic() && i >= params.Len()-1 {
		return nil
	}
	if i >= params.Len() {
		return nil
	}
	return params.At(i)
}

// units returns every set of files of the application that is checked as
// one package, with its facts.
func (p *program) units() []*unit {
	var out []*unit
	for _, k := range p.own {
		out = append(out, &unit{files: k.files, info: k.info, types: k.types})
		out = append(out, k.tests...)
	}
	return out
}

// ownMethods returns the names of the methods the application declares.
func ownMethods(p *program) map[string]bool {
	out := map[string]bool{}
	for _, u := range p.units() {
		for _, f := range u.files {
			for _, d := range f.Decls {
				if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv != nil {
					out[fn.Name.Name] = true
				}
			}
		}
	}
	return out
}

// handleParams returns the parameters, by where they are declared, of the
// application's functions that take the generated client only to query on
// it.
func handleParams(p *program, s *schema) map[token.Pos]bool {
	isClient := func(t types.Type) bool {
		ptr, ok := types.Unalias(t).(*types.Pointer)
		if !ok {
			return false
		}
		named, ok := types.Unalias(ptr.Elem()).(*types.Named)
		return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == s.root && named.Obj().Name() == "Client"
	}
	type candidate struct {
		bad  bool
		deps []token.Pos
		fn   token.Pos // the function it is a parameter of
	}
	cands := map[token.Pos]*candidate{}
	funcs := map[token.Pos][]token.Pos{} // function -> its candidate parameters
	// The candidates: named *Client parameters of declared functions. (A
	// function literal takes the type its context gives it.)
	for _, u := range p.units() {
		if u.info == nil {
			continue
		}
		for _, f := range u.files {
			if ast.IsGenerated(f) {
				continue
			}
			for _, d := range f.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || fn.Type.Params == nil {
					continue
				}
				for _, field := range fn.Type.Params.List {
					for _, name := range field.Names {
						v, ok := u.info.Defs[name].(*types.Var)
						if !ok || !isClient(v.Type()) || name.Name == "_" {
							continue
						}
						if cands[name.Pos()] == nil {
							cands[name.Pos()] = &candidate{fn: fn.Name.Pos()}
							funcs[fn.Name.Pos()] = append(funcs[fn.Name.Pos()], name.Pos())
						}
					}
				}
			}
		}
	}
	if len(cands) == 0 {
		return nil
	}
	// How each is used, and how each function is used.
	for _, u := range p.units() {
		if u.info == nil {
			continue
		}
		for _, f := range u.files {
			var stack []ast.Node
			ast.Inspect(f, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				stack = append(stack, n)
				id, ok := n.(*ast.Ident)
				if !ok {
					return true
				}
				obj := u.info.Uses[id]
				if obj == nil {
					return true
				}
				up := func(k int) ast.Node {
					if len(stack) <= k {
						return nil
					}
					return stack[len(stack)-1-k]
				}
				// A function with candidate parameters used other than by
				// calling it (kept as a value, handed over): its type is
				// part of what takes it.
				if fn, isFunc := obj.(*types.Func); isFunc {
					if params := funcs[fn.Origin().Pos()]; len(params) > 0 {
						callee := ast.Node(id)
						k := 1
						if sel, ok := up(1).(*ast.SelectorExpr); ok && sel.Sel == id {
							callee, k = sel, 2
						}
						if call, ok := up(k).(*ast.CallExpr); !ok || ast.Node(call.Fun) != callee {
							for _, at := range params {
								cands[at].bad = true
							}
						}
					}
					return true
				}
				c := cands[obj.Pos()]
				if c == nil {
					return true
				}
				switch parent := up(1).(type) {
				case *ast.SelectorExpr:
					// c.User: an entity's client, to query on.
					sel := u.info.Selections[parent]
					if parent.X == ast.Expr(id) && sel != nil && sel.Kind() == types.FieldVal &&
						sel.Obj().Pkg() != nil && sel.Obj().Pkg().Path() == s.root && s.entities[sel.Obj().Name()] != nil {
						return true
					}
				case *ast.CallExpr:
					for i, a := range parent.Args {
						if a != ast.Expr(id) {
							continue
						}
						if v := paramOf(u.info, parent, i); v != nil && cands[v.Pos()] != nil {
							c.deps = append(c.deps, v.Pos())
							return true
						}
					}
				}
				c.bad = true
				return true
			})
		}
	}
	// A method that some interface could be asking for keeps its type: a
	// parameter of a method is a candidate only when the method's name is
	// its type's own business, which the text cannot tell. Exported methods
	// are left alone.
	for _, u := range p.units() {
		for _, f := range u.files {
			for _, d := range f.Decls {
				if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv != nil && fn.Name.IsExported() {
					for _, at := range funcs[fn.Name.Pos()] {
						cands[at].bad = true
					}
				}
			}
		}
	}
	// What is handed to a parameter that needs the database needs it too;
	// and the parameters of one function change together or not at all
	// (there is no reason for the second, but one rule is easier to read
	// in the result than two).
	for changed := true; changed; {
		changed = false
		for _, c := range cands {
			if c.bad {
				continue
			}
			for _, dep := range c.deps {
				if cands[dep].bad {
					c.bad, changed = true, true
					break
				}
			}
		}
	}
	out := map[token.Pos]bool{}
	for at, c := range cands {
		if !c.bad {
			out[at] = true
		}
	}
	return out
}
