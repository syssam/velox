package main

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"
)

const ormPath = "github.com/syssam/velox/vx/orm"

// rewriter applies the rules to one file.
type rewriter struct {
	*fileEdits
	s   *schema
	lib string
	// done counts the references rewritten, by rule.
	done map[string]int
	// libScope is the scope of the velox module's root package: the
	// generated root package repeated some of its names.
	libScope *types.Scope
	// sets are the calls rewritten to Set(...), so that the setter that
	// follows one in a chain joins it: Set(a, b), not Set(a).Set(b).
	sets map[*ast.CallExpr]bool
}

// What the old API's packages are, seen from a reference into one.
type target struct {
	where string      // "root", "<entity>", "client/<entity>", "entity", "query", "filter", ... or a library path
	ent   *entityInfo // the entity, when the reference is to something of one
	kind  string      // of a generated type: Client, Create, UpdateOne, Querier, ...
}

func (r *rewriter) targetOf(obj types.Object, recv string) (target, bool) {
	if obj == nil || obj.Pkg() == nil {
		return target{}, false
	}
	path := obj.Pkg().Path()
	generated := path == r.s.root || strings.HasPrefix(path, r.s.root+"/")
	if !generated && path != r.lib && !strings.HasPrefix(path, r.lib+"/") {
		return target{}, false
	}
	t := target{where: r.s.where(path)}
	if generated {
		t.ent, t.kind = r.s.kindOf(recv)
		if t.ent == nil {
			t.ent = r.s.byLeaf[path]
		}
	} else {
		t.kind = recv
	}
	return t, true
}

func (r *rewriter) root(pos token.Pos) string { return r.ref(r.s.to, rootName(r.s.to), pos) }
func (r *rewriter) orm(pos token.Pos) string  { return r.ref(ormPath, "orm", pos) }
func (r *rewriter) entity(pos token.Pos) string {
	return r.ref(r.s.to+"/entity", "entity", pos)
}
func (r *rewriter) predicate(pos token.Pos) string {
	return r.ref(r.s.to+"/predicate", "predicate", pos)
}
func (r *rewriter) leaf(e *entityInfo, pos token.Pos) string {
	name := strings.ToLower(e.name)
	return r.ref(r.s.to+"/"+name, name, pos)
}

func rootName(path string) string { return path[strings.LastIndex(path, "/")+1:] }

// through spells the embedded fields a selection went through:
// s.db.Holiday, where s.db is a struct that embeds the client, selected
// Holiday through the field Client, and the handle is s.db.Client.
func through(sel *types.Selection) string {
	if sel == nil {
		return ""
	}
	var out string
	t := sel.Recv()
	path := sel.Index()
	for _, i := range path[:len(path)-1] {
		st, ok := deref(t).Underlying().(*types.Struct)
		if !ok {
			return ""
		}
		out += "." + st.Field(i).Name()
		t = st.Field(i).Type()
	}
	return out
}

// split takes a generated member's name apart: SetNillableOwnerID is
// ("SetNillable", "Owner", "ID") with Owner an edge, or ("SetNillable",
// "OwnerID", "") when the entity has a field of that name, which wins.
func split(e *entityInfo, name string, prefixes, suffixes []string) (pre, mid, suf string, field, edge bool) {
	if e == nil {
		return "", "", "", false, false
	}
	for _, p := range prefixes {
		rest, ok := strings.CutPrefix(name, p)
		if !ok || rest == "" {
			continue
		}
		if e.fields[rest] {
			return p, rest, "", true, false
		}
		for _, s := range suffixes {
			m, ok := strings.CutSuffix(rest, s)
			if !ok || m == "" {
				continue
			}
			if e.edges[m] {
				return p, m, s, false, true
			}
			if edge := e.singular[m]; edge != "" && s == "IDs" {
				return p, edge, s, false, true
			}
		}
	}
	return "", "", "", false, false
}

var (
	setterPrefixes = []string{"SetNillable", "Set", "Add", "Clear", "Remove", "Append"}
	setterSuffixes = []string{"IDs", "ID", ""}
)

// file rewrites one file and reports whether anything changed.
func (r *rewriter) run() {
	var stack []ast.Node
	ast.Inspect(r.file, func(n ast.Node) bool {
		if n == nil {
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			switch top := top.(type) {
			case *ast.CallExpr:
				r.call(top, stack)
				r.arguments(top)
			case *ast.SelectorExpr:
				r.selector(top, stack)
			case *ast.BinaryExpr:
				r.compareNil(top)
			case *ast.ReturnStmt:
				r.results(top, stack)
			case *ast.AssignStmt:
				r.whereInput(top)
				if len(top.Lhs) == len(top.Rhs) {
					for i, rhs := range top.Rhs {
						r.into(rhs, r.info.TypeOf(top.Lhs[i]))
					}
				}
			case *ast.ValueSpec:
				if top.Type != nil {
					for _, v := range top.Values {
						r.into(v, r.info.TypeOf(top.Type))
					}
				}
			case *ast.CompositeLit:
				r.elements(top)
			}
			return true
		}
		stack = append(stack, n)
		return true
	})
}

func parent(stack []ast.Node, up int) ast.Node {
	if len(stack) < up {
		return nil
	}
	return stack[len(stack)-up]
}

// calledBy reports whether n is the function of the call that is its parent.
func calledBy(n ast.Expr, stack []ast.Node) *ast.CallExpr {
	if c, ok := parent(stack, 1).(*ast.CallExpr); ok && c.Fun == n {
		return c
	}
	return nil
}

// receiverOf reports whether n is the receiver of a method call, x.M(...),
// and returns M and the call.
func receiverOf(n ast.Expr, stack []ast.Node) (string, *ast.CallExpr) {
	sel, ok := parent(stack, 1).(*ast.SelectorExpr)
	if !ok || sel.X != n {
		return "", nil
	}
	if c, ok := parent(stack, 2).(*ast.CallExpr); ok && c.Fun == sel {
		return sel.Sel.Name, c
	}
	return sel.Sel.Name, nil
}

func (r *rewriter) count(rule string) { r.done[rule]++ }

// selector handles a reference that is not itself called: a qualified name
// (a type, a constant, a column) or a field.
func (r *rewriter) selector(sel *ast.SelectorExpr, stack []ast.Node) {
	if calledBy(sel, stack) != nil {
		return // call handles it
	}
	if selection := r.info.Selections[sel]; selection != nil {
		r.member(sel, selection, stack)
		return
	}
	obj := r.info.Uses[sel.Sel]
	t, ok := r.targetOf(obj, "")
	if !ok {
		return
	}
	name := obj.Name()
	switch t.where {
	case "<entity>":
		r.leafName(sel, obj, t.ent, stack)
	case "root":
		switch obj.(type) {
		case *types.TypeName:
			// Embedded in a struct, the field was called Client (or Tx):
			// it keeps the name, so that x.Client and Client: c still mean it.
			embedded := func() {
				at := ast.Node(sel)
				up := 1
				if star, ok := parent(stack, 1).(*ast.StarExpr); ok {
					at, up = star, 2
				}
				if f, ok := parent(stack, up).(*ast.Field); ok && len(f.Names) == 0 {
					if _, inStruct := parent(stack, up+2).(*ast.StructType); inStruct {
						r.open(at.Pos(), name+" ")
					}
				}
			}
			switch name {
			case "Tx":
				r.replace(sel.Pos(), sel.End(), r.orm(sel.Pos())+".Tx")
				embedded()
				r.count("type Tx")
			case "Client":
				r.replace(sel.Pos(), sel.End(), r.orm(sel.Pos())+".DB")
				embedded()
				r.count("type Client")
			case "Noder":
				r.replace(sel.Pos(), sel.End(), r.root(sel.Pos())+".Noder")
				r.count("type Noder")
			default:
				if !r.repeated(sel, obj) {
					r.note(sel.Pos(), "the type %s of the generated root package has no counterpart", name)
				}
			}
		case *types.Const:
			if base, ok := strings.CutPrefix(name, "Type"); ok && r.s.entities[base] != nil {
				r.replace(sel.Pos(), sel.End(), r.leaf(r.s.entities[base], sel.Pos())+".ModelName")
				r.count("Type<Entity>")
			} else if !r.repeated(sel, obj) {
				r.note(sel.Pos(), "the constant %s of the generated root package has no counterpart", name)
			}
		default:
			switch name {
			case "IsNotFound", "IsConstraintError", "IsNotLoaded":
				r.replace(sel.Pos(), sel.End(), r.orm(sel.Pos())+"."+name)
				r.count(name)
			default:
				if !r.repeated(sel, obj) {
					r.note(sel.Pos(), "%s of the generated root package: rewrite by hand", name)
				}
			}
		}
	case "client/<entity>", "query":
		if _, ok := obj.(*types.TypeName); !ok {
			r.note(sel.Pos(), "%s: rewrite by hand", name)
			return
		}
		ent, kind := r.s.kindOf(name)
		if ent == nil {
			r.note(sel.Pos(), "the type %s has no counterpart", name)
			return
		}
		switch kind {
		case "Create", "CreateBulk", "Update", "Delete", "Query":
			r.replace(sel.Pos(), sel.End(), r.entity(sel.Pos())+"."+ent.name+kind)
		case "UpdateOne":
			r.replace(sel.Pos(), sel.End(), r.entity(sel.Pos())+"."+ent.name+"Update")
		case "Mutation":
			r.replace(sel.Pos(), sel.End(), r.orm(sel.Pos())+".Mutation")
		case "Client":
			// A client was a handle bound to an entity; what is passed
			// around now is the handle.
			from := sel.Pos()
			if star, ok := parent(stack, 1).(*ast.StarExpr); ok {
				from = star.Pos()
			}
			r.replace(from, sel.End(), r.orm(sel.Pos())+".Executor")
		default:
			r.note(sel.Pos(), "the type %s has no counterpart", name)
			return
		}
		r.count("builder type")
	case "entity":
		if _, ok := obj.(*types.TypeName); ok {
			if ent, kind := r.s.kindOf(name); ent != nil && kind == "Querier" {
				r.replace(sel.Pos(), sel.End(), "*"+r.entity(sel.Pos())+"."+ent.name+"Query")
				r.count("type Querier")
			} else if ent != nil && kind == "PaginateOption" {
				// An option was applied by Paginate; it is a function of
				// the query now, applied before it (orm.Apply).
				r.replace(sel.Pos(), sel.End(), "func(*"+r.entity(sel.Pos())+"."+ent.name+"Query)")
				r.count("pagination option")
			} else if ent != nil && kind == "Paginatable" {
				if assert, ok := parent(stack, 1).(*ast.TypeAssertExpr); ok && assert.Type == ast.Expr(sel) {
					// q.(entity.UserPaginatable): every query paginates.
					r.replace(assert.X.End(), assert.End(), "")
					if as, ok := parent(stack, 2).(*ast.AssignStmt); ok && len(as.Lhs) == 2 && len(as.Rhs) == 1 {
						r.insert(assert.End(), ", true")
					}
					r.count("Paginatable")
				}
			} else if name == "Cursor" || name == "PageInfo" || name == "OrderDirection" {
				r.replace(sel.Pos(), sel.End(), r.orm(sel.Pos())+"."+name)
				r.count("type " + name)
			}
			return
		}
		if name == "OrderDirectionAsc" || name == "OrderDirectionDesc" {
			r.replace(sel.Pos(), sel.End(), r.orm(sel.Pos())+"."+name)
			r.count("type OrderDirection")
			return
		}
		if strings.HasPrefix(name, "With") && (strings.HasSuffix(name, "Order") || strings.HasSuffix(name, "Filter")) {
			r.note(sel.Pos(), "%s: an order or a filter is a call on the query now, .OrderBy(o.Term()) and .Where(w.P())", name)
		}
	case "intercept":
		switch name {
		case "Func":
			r.replace(sel.Pos(), sel.End(), r.orm(sel.Pos())+".InterceptFunc")
		case "TraverseFunc":
			r.replace(sel.Pos(), sel.End(), r.orm(sel.Pos())+".TraverseFunc")
		case "Query":
			r.replace(sel.Pos(), sel.End(), "*"+r.orm(sel.Pos())+".Query")
		default:
			r.note(sel.Pos(), "intercept.%s: an interceptor is handed *orm.Query, whatever the entity", name)
			return
		}
		r.count("intercept")
	case "privacy":
		// The generated package repeated the library's.
		r.replace(sel.X.Pos(), sel.X.End(), r.ref(r.lib+"/privacy", "privacy", sel.Pos()))
		r.count("privacy")
	default:
		if obj.Pkg().Path() == r.lib+"/contrib/graphql/gqlrelay" {
			switch name {
			case "Cursor", "PageInfo", "OrderDirection", "OrderDirectionAsc", "OrderDirectionDesc":
				r.replace(sel.Pos(), sel.End(), r.orm(sel.Pos())+"."+name)
				r.count("gqlrelay")
			default:
				r.note(sel.Pos(), "gqlrelay.%s: rewrite by hand", name)
			}
		}
	}
}

// leafName handles a name of an entity's own package.
func (r *rewriter) leafName(sel *ast.SelectorExpr, obj types.Object, e *entityInfo, stack []ast.Node) {
	name := obj.Name()
	rename := func(to, rule string) {
		r.replace(sel.Sel.Pos(), sel.Sel.End(), to)
		r.count(rule)
	}
	switch obj.(type) {
	case *types.Var:
		if f, ok := strings.CutSuffix(name, "Field"); ok && e.fields[f] {
			rename(f, "column")
			return
		}
		switch name {
		case "And", "Or", "Not":
			return
		}
		r.note(sel.Pos(), "%s.%s: rewrite by hand", strings.ToLower(e.name), name)
	case *types.TypeName:
		if e.fields[name] {
			rename(name+"Enum", "enum type")
		}
	case *types.Const:
		if f, ok := strings.CutPrefix(name, "Field"); ok && e.fields[f] {
			// SkipDefault(pkg.FieldX) takes the constant with it.
			if c, ok := parent(stack, 1).(*ast.CallExpr); ok {
				if s, ok := c.Fun.(*ast.SelectorExpr); ok && (s.Sel.Name == "SkipDefault" || s.Sel.Name == "Asc" || s.Sel.Name == "Desc") {
					return
				}
			}
			rename("Column"+f, "column name")
		}
	}
}

// member handles a field or a method value selected from a value.
func (r *rewriter) member(sel *ast.SelectorExpr, selection *types.Selection, stack []ast.Node) {
	obj := selection.Obj()
	t, ok := r.targetOf(obj, declaring(selection))
	if !ok {
		return
	}
	if _, isField := obj.(*types.Var); isField {
		// client.Holiday, or tx.Holiday: consumed by the call of a method on it.
		if t.where == "root" && r.s.entities[obj.Name()] != nil {
			if _, call := receiverOf(sel, stack); call != nil {
				return
			}
			if m, _ := receiverOf(sel, stack); m == "Get" {
				return // the method value below
			}
			// client.Holiday as a value: the handle it held.
			r.replace(sel.X.End(), sel.End(), through(selection))
			r.count("client as a value")
		}
		return
	}
	// A method value.
	if t.where == "client/<entity>" && t.kind == "Client" && obj.Name() == "Get" {
		inner, ok := sel.X.(*ast.SelectorExpr)
		sig, isFunc := obj.Type().(*types.Signature)
		if ok && isFunc && sig.Params().Len() == 2 {
			id := types.TypeString(sig.Params().At(1).Type(), func(p *types.Package) string { return r.ref(p.Path(), p.Name(), sel.Pos()) })
			ctx := r.ref("context", "context", sel.Pos())
			r.replace(sel.Pos(), sel.End(), "func(ctx "+ctx+".Context, id "+id+") (*"+r.entity(sel.Pos())+"."+t.ent.name+", error) {\n\treturn "+
				r.root(sel.Pos())+"."+t.ent.name+".Get(ctx, "+r.render(inner.X)+through(r.info.Selections[inner])+", id)\n}")
			r.count("Get as a value")
			return
		}
	}
	if t.where == "filter" && obj.Name() == "Filter" {
		return // an argument of With<Entity>Filter: Paginate handles it
	}
	if t.where == "client/<entity>" && (t.kind == "Create" || t.kind == "Update" || t.kind == "UpdateOne") && r.setterValue(sel, t, obj) {
		return
	}
	if t.where == "root" || t.where == "client/<entity>" || t.where == "entity" || t.where == "query" {
		r.note(sel.Pos(), "the method %s is used as a value: rewrite by hand", obj.Name())
	}
}

// call handles a call of something of the old API.
func (r *rewriter) call(call *ast.CallExpr, stack []ast.Node) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	selection := r.info.Selections[sel]
	if selection == nil {
		r.function(call, sel, stack)
		return
	}
	obj := selection.Obj()
	t, ok := r.targetOf(obj, declaring(selection))
	if !ok {
		return
	}
	name := obj.Name()
	switch {
	case t.where == "client/<entity>" && t.kind == "Client":
		r.client(call, sel, t, name, stack)
	case t.where == "client/<entity>" && (t.kind == "Create" || t.kind == "Update" || t.kind == "UpdateOne" || t.kind == "CreateBulk" || t.kind == "Delete" || t.kind == "DeleteOne"):
		r.builder(call, sel, t, name, stack)
	case (t.where == "entity" && (t.kind == "Querier" || t.kind == "Paginatable")) || (t.where == "query" && t.kind == "Query"):
		r.query(call, sel, t, name, stack)
	case t.where == "entity" && t.kind == "Selector":
		// What Select returned: the query itself now, after Columns.
		switch name {
		case "AllX", "OnlyX", "FirstX":
			r.replace(sel.Sel.Pos(), sel.Sel.End(), strings.TrimSuffix(name, "X"))
			r.open(call.Pos(), r.orm(call.Pos())+".Must(")
			r.insert(call.End(), ")")
			r.count(name)
		}
	case t.where == "root" && (t.kind == "Client" || t.kind == "Tx"):
		// A method promoted from an embedded client: the field is named now.
		if via := through(selection); via != "" && name != "Client" {
			r.insert(sel.X.End(), via)
		}
		switch name {
		case "Tx":
			r.replace(sel.Sel.Pos(), sel.Sel.End(), "Begin")
			r.count("Begin")
		case "Commit", "Rollback", "Close", "Use", "Intercept":
		case "Client":
			r.replace(call.Pos(), call.End(), r.render(sel.X))
			r.count("tx.Client()")
		default:
			r.note(call.Pos(), "%s.%s: rewrite by hand", t.kind, name)
		}
	case t.where == "entity" && t.kind == "Entity":
		switch {
		case name == "Unwrap":
			r.replace(call.Pos(), call.End(), r.render(sel.X))
			r.count("Unwrap")
		case strings.HasPrefix(name, "Query") && t.ent.edges[strings.TrimPrefix(name, "Query")]:
			// An entity remembered what it was read on; it is told now.
			switch handle, ok := r.handleOf(sel.X, call.Pos(), stack, 0); {
			case len(call.Args) != 0:
			case ok:
				r.open(call.Lparen+1, handle)
				r.count("edge query")
			case r.contextAt(call.Pos()) != "":
				// The entity came from somewhere else (a parameter). The
				// handle of the context is what its edges are resolved on
				// for a GraphQL engine too; whether every caller's context
				// carries one is for a person to check.
				r.open(call.Lparen+1, r.orm(call.Pos())+".ExecutorFrom("+r.contextAt(call.Pos())+")")
				r.note(call.Pos(), "%s is given the handle of the context (orm.ExecutorFrom): check that every caller's context carries one (orm.WithExecutor), or pass the handle", name)
				r.count("edge query on the context's handle")
			default:
				r.note(call.Pos(), "%s takes the handle to query on now: e.%s(handle)", name, name)
			}
		}
	case t.where == "<entity>":
		// A method of an enum type.
		switch name {
		case "String":
			star := ""
			if _, isPtr := types.Unalias(r.info.TypeOf(sel.X)).(*types.Pointer); isPtr {
				star = "*"
			}
			r.replace(call.Pos(), call.End(), "string("+star+r.render(sel.X)+")")
			r.count("enum String")
		case "IsValid":
			recv := declaring(selection)
			if t.ent != nil && t.ent.fields[recv] {
				r.replace(call.Pos(), call.End(), r.ref("slices", "slices", call.Pos())+".Contains("+r.leaf(t.ent, call.Pos())+"."+recv+"Values(), "+r.render(sel.X)+")")
				r.count("enum IsValid")
			} else {
				r.note(call.Pos(), "IsValid of %s: rewrite by hand", recv)
			}
		}
	case t.where == "filter" && name == "Filter":
		r.note(call.Pos(), "a where input is turned into a predicate with P(): q.Where(w.P())")
	case obj.Pkg().Path() == r.lib+"/dialect/sql" && strings.HasSuffix(t.kind, "Field"):
		// A predicate of a column: the names that differ.
		switch name {
		case "IsNull":
			r.replace(sel.Sel.Pos(), sel.Sel.End(), "IsNil")
			r.count("predicate name")
		case "NotNull":
			r.replace(sel.Sel.Pos(), sel.Sel.End(), "NotNil")
			r.count("predicate name")
		case "EQ", "NEQ", "In", "NotIn", "GT", "GTE", "LT", "LTE", "Contains", "HasPrefix", "HasSuffix", "EqualFold", "ContainsFold", "IsNil", "NotNil":
		default:
			r.note(call.Pos(), "the predicate %s: check that the column has it", name)
		}
	}
}

// function handles a call of a package-level function.
func (r *rewriter) function(call *ast.CallExpr, sel *ast.SelectorExpr, stack []ast.Node) {
	obj := r.info.Uses[sel.Sel]
	t, ok := r.targetOf(obj, "")
	if !ok {
		return
	}
	name := obj.Name()
	switch t.where {
	case "<entity>":
		e := t.ent
		if edge, ok := strings.CutSuffix(strings.TrimPrefix(name, "Has"), "With"); ok && strings.HasPrefix(name, "Has") && e.edges[edge] {
			r.replace(sel.Sel.Pos(), sel.Sel.End(), edge+".HasWith")
			r.count("Has<E>With")
			return
		}
		if edge, ok := strings.CutPrefix(name, "Has"); ok && e.edges[edge] {
			r.replace(sel.Sel.Pos(), sel.Sel.End(), edge+".Has")
			r.count("Has<E>")
			return
		}
		if f, ok := strings.CutPrefix(name, "By"); ok && e.fields[f] {
			if !r.inOrder(stack) {
				r.note(call.Pos(), "%s outside Order: an order term is %s.%s.Asc() or .Desc()", name, strings.ToLower(e.name), f)
			}
			return
		}
		switch name {
		case "And", "Or", "Not":
			return
		}
		if f, ok := strings.CutSuffix(name, "Values"); ok && e.fields[f] {
			return
		}
		if _, isType := obj.(*types.TypeName); isType && e.fields[name] {
			// A conversion to an enum type.
			r.replace(sel.Sel.Pos(), sel.Sel.End(), name+"Enum")
			r.count("enum type")
			return
		}
		r.note(call.Pos(), "%s.%s: rewrite by hand", strings.ToLower(e.name), name)
	case "root":
		switch name {
		case "IsNotFound", "IsConstraintError", "IsNotLoaded":
			r.replace(sel.Pos(), sel.End(), r.orm(sel.Pos())+"."+name)
			r.count(name)
		case "Asc", "Desc":
			if !r.inOrder(stack) {
				r.note(call.Pos(), "%s outside Order: an order term is <entity>.<Column>.Asc() or .Desc()", name)
			}
		case "As", "Sum", "Count", "Max", "Min", "Mean":
			// The aggregates of a grouped read; the read around them is
			// rewritten by hand (see the note on GroupBy and Aggregate).
			r.replace(sel.Pos(), sel.End(), r.orm(sel.Pos())+"."+name)
			r.count("aggregate")
		default:
			if !r.repeated(sel, obj) {
				r.note(call.Pos(), "%s of the generated root package: rewrite by hand", name)
			}
		}
	case "privacy":
		r.replace(sel.X.Pos(), sel.X.End(), r.ref(r.lib+"/privacy", "privacy", sel.Pos()))
		r.count("privacy")
	case "entity":
		if strings.HasPrefix(name, "With") && (strings.HasSuffix(name, "Order") || strings.HasSuffix(name, "Filter")) {
			if c, ok := parent(stack, 1).(*ast.CallExpr); ok {
				if s, ok := c.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "Paginate" {
					if _, inline := r.inlineOption(call, false); inline {
						return // Paginate puts it on the query
					}
				}
			}
			r.option(call, name, obj, stack)
		}
	}
}

// inOrder reports whether the innermost enclosing call is Order of a query.
func (r *rewriter) inOrder(stack []ast.Node) bool {
	c, ok := parent(stack, 1).(*ast.CallExpr)
	if !ok {
		return false
	}
	s, ok := c.Fun.(*ast.SelectorExpr)
	return ok && s.Sel.Name == "Order"
}

// client handles a method of an entity's client: where a query or a mutation
// starts. The handle, which the old client held, becomes an argument.
func (r *rewriter) client(call *ast.CallExpr, sel *ast.SelectorExpr, t target, name string, stack []ast.Node) {
	// <ex>.<Entity>.<name>(...), or <client>.<name>(...) with a client held
	// in a variable, which is the handle now.
	ex, via := sel.X, ""
	if holder, ok := sel.X.(*ast.SelectorExpr); ok {
		if fieldSel := r.info.Selections[holder]; fieldSel != nil && fieldSel.Kind() == types.FieldVal && r.isRootClient(fieldSel) {
			ex, via = holder.X, through(fieldSel)
		}
	}
	start := r.root(call.Pos()) + "." + t.ent.name + "."
	comma := ""
	if len(call.Args) > 0 {
		comma = ", "
	}
	// <ex>.<Entity>.<name>(args) -> <root>.<Entity>.<to>(<ex>, args)
	prepend := func(to string) {
		r.open(ex.Pos(), start+to+"(")
		r.replace(ex.End(), call.Lparen+1, via+comma)
		r.count(name)
	}
	switch name {
	case "Query", "Create", "Update", "Delete", "CreateBulk":
		prepend(name)
	case "UpdateOneID":
		prepend("UpdateByID")
	case "UpdateOne":
		if len(call.Args) == 1 {
			r.insert(call.Args[0].End(), ".ID")
			prepend("UpdateByID")
		}
	case "Get", "GetX":
		if len(call.Args) != 2 {
			return
		}
		text := start + "Get(" + r.render(call.Args[0]) + ", " + r.render(ex) + via + ", " + r.render(call.Args[1]) + ")"
		if name == "GetX" {
			text = r.orm(call.Pos()) + ".Must(" + text + ")"
		}
		r.replace(call.Pos(), call.End(), text)
		r.count(name)
	case "DeleteOneID", "DeleteOne":
		if m, outer := receiverOf(call, stack); outer != nil && (m == "Exec" || m == "ExecX") {
			return // the Exec that follows rewrites the pair
		}
		r.note(call.Pos(), "%s that is not run at once: DeleteByID(ctx, handle, id) runs it", name)
	default:
		r.note(call.Pos(), "%s.%s: rewrite by hand", t.ent.name, name)
	}
}

// setterOf says which method of a column or an edge a generated setter is:
// SetName is the column's Set, AddTagIDs the edge's Add, SetOwner the edge's
// Set of the entity's id (viaID).
func setterOf(pre, suf string, field, edge bool) (method string, viaID, ok bool) {
	switch {
	case field && suf == "" && (pre == "Set" || pre == "SetNillable" || pre == "Clear" || pre == "Add"):
		return pre, false, true
	case edge && pre == "Set" && suf == "ID":
		return "Set", false, true
	case edge && pre == "Set" && suf == "":
		return "Set", true, true
	case edge && (pre == "Add" || pre == "Remove") && suf == "IDs":
		return pre, false, true
	case edge && pre == "Clear" && suf == "":
		return "Clear", false, true
	}
	return "", false, false
}

// typeText writes a type of the old code as the new code spells it.
func (r *rewriter) typeText(t types.Type, pos token.Pos) string {
	switch t := t.(type) {
	case *types.Pointer:
		return "*" + r.typeText(t.Elem(), pos)
	case *types.Slice:
		return "[]" + r.typeText(t.Elem(), pos)
	case *types.Named:
		if pkg := t.Obj().Pkg(); pkg != nil {
			if e := r.s.byLeaf[pkg.Path()]; e != nil && e.fields[t.Obj().Name()] {
				return r.leaf(e, pos) + "." + t.Obj().Name() + "Enum"
			}
		}
	}
	return types.TypeString(t, func(p *types.Package) string {
		path := p.Path()
		if to, ok := r.s.moveTo(path); ok {
			path = to
		}
		return r.ref(path, p.Name(), pos)
	})
}

// setterValue rewrites a setter used as a value, b.SetName passed to a
// function that calls it, to a function that makes the assignment.
func (r *rewriter) setterValue(sel *ast.SelectorExpr, t target, obj types.Object) bool {
	pre, mid, suf, field, edge := split(t.ent, obj.Name(), setterPrefixes, setterSuffixes)
	if !field && !edge {
		return false
	}
	method, viaID, ok := setterOf(pre, suf, field, edge)
	sig, isFunc := obj.Type().(*types.Signature)
	if !ok || !isFunc || sig.Params().Len() > 1 {
		return false
	}
	kind := t.kind
	if kind == "UpdateOne" {
		kind = "Update"
	}
	params, arg := "", ""
	if sig.Params().Len() == 1 {
		pt := sig.Params().At(0).Type()
		arg = "v"
		slice, isSlice := pt.(*types.Slice)
		switch {
		case sig.Variadic() && isSlice:
			params = "v ..." + r.typeText(slice.Elem(), sel.Pos())
			arg = "v..."
		case viaID:
			// SetOwner took the entity; the edge takes its id.
			params = "v " + r.typeText(pt, sel.Pos())
			arg = "v.ID"
		default:
			params = "v " + r.typeText(pt, sel.Pos())
		}
	}
	builder := "*" + r.entity(sel.Pos()) + "." + t.ent.name + kind
	recv := ""
	if s := r.info.Selections[sel]; s != nil && s.Kind() == types.MethodExpr {
		// (*UserUpdateOne).SetName: the builder is the first argument.
		recv = "b"
		if params != "" {
			params = ", " + params
		}
		params = "b " + builder + params
	} else {
		recv = r.render(sel.X)
	}
	r.replace(sel.Pos(), sel.End(), "func("+params+") "+builder+" {\n\treturn "+
		recv+".Set("+r.leaf(t.ent, sel.Pos())+"."+mid+"."+method+"("+arg+"))\n}")
	r.count("setter as a value")
	return true
}

// handleOf finds what an entity was read on, by looking at where the
// variable that holds it got its value in the functions around: a query or
// a mutation that starts at a client names the handle. It answers only when
// that handle is the same thing at the place it is needed.
func (r *rewriter) handleOf(e ast.Expr, at token.Pos, stack []ast.Node, depth int) (string, bool) {
	if depth > 4 {
		return "", false
	}
	switch e := e.(type) {
	case *ast.ParenExpr:
		return r.handleOf(e.X, at, stack, depth)
	case *ast.CallExpr:
		// A chain: walk to where it starts.
		sel, ok := e.Fun.(*ast.SelectorExpr)
		if !ok {
			return "", false
		}
		if selection := r.info.Selections[sel]; selection != nil {
			if t, ok := r.targetOf(selection.Obj(), declaring(selection)); ok && t.where == "client/<entity>" && t.kind == "Client" {
				ex, via := sel.X, ""
				if holder, ok := sel.X.(*ast.SelectorExpr); ok {
					if fs := r.info.Selections[holder]; fs != nil && fs.Kind() == types.FieldVal && r.isRootClient(fs) {
						ex, via = holder.X, through(fs)
					}
				}
				if !r.sameAt(ex, at) {
					return "", false
				}
				return string(r.src[r.off(ex.Pos()):r.off(ex.End())]) + via, true
			}
		}
		return r.handleOf(sel.X, at, stack, depth)
	case *ast.SelectorExpr:
		// parent.Edges.Items: read with its parent.
		return r.handleOf(e.X, at, stack, depth)
	case *ast.IndexExpr:
		return r.handleOf(e.X, at, stack, depth)
	case *ast.Ident:
		obj := r.info.Uses[e]
		if obj == nil {
			return "", false
		}
		// The assignment to it that comes last before the place.
		var from ast.Expr
		for i := len(stack) - 1; i >= 0; i-- {
			var body *ast.BlockStmt
			switch fn := stack[i].(type) {
			case *ast.FuncDecl:
				body = fn.Body
			case *ast.FuncLit:
				body = fn.Body
			}
			if body == nil {
				continue
			}
			ast.Inspect(body, func(n ast.Node) bool {
				if n == nil || n.Pos() >= at {
					return false
				}
				switch st := n.(type) {
				case *ast.AssignStmt:
					for j, lhs := range st.Lhs {
						id, ok := lhs.(*ast.Ident)
						if !ok || (r.info.Defs[id] != obj && r.info.Uses[id] != obj) {
							continue
						}
						if len(st.Rhs) == len(st.Lhs) {
							from = st.Rhs[j]
						} else if len(st.Rhs) == 1 && j == 0 {
							from = st.Rhs[0] // v, err := call
						}
					}
				case *ast.RangeStmt:
					if id, ok := st.Value.(*ast.Ident); ok && r.info.Defs[id] == obj {
						from = st.X
					}
				}
				return true
			})
			if from != nil {
				break
			}
		}
		if from == nil {
			return "", false
		}
		return r.handleOf(from, at, stack, depth+1)
	}
	return "", false
}

// contextAt names the context.Context variable called ctx that is visible
// at pos, or returns "".
func (r *rewriter) contextAt(pos token.Pos) string {
	scope := r.pkg.Scope().Innermost(pos)
	if scope == nil {
		return ""
	}
	_, obj := scope.LookupParent("ctx", pos)
	v, ok := obj.(*types.Var)
	if !ok {
		return ""
	}
	named, ok := types.Unalias(v.Type()).(*types.Named)
	if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != "context" || named.Obj().Name() != "Context" {
		return ""
	}
	return "ctx"
}

// sameAt reports whether the names e starts with mean at pos what they
// mean where e is written.
func (r *rewriter) sameAt(e ast.Expr, pos token.Pos) bool {
	for {
		switch x := e.(type) {
		case *ast.SelectorExpr:
			e = x.X
			continue
		case *ast.Ident:
			obj := r.info.Uses[x]
			scope := r.pkg.Scope().Innermost(pos)
			if obj == nil || scope == nil {
				return false
			}
			_, found := scope.LookupParent(x.Name, pos)
			return found == obj
		}
		return false
	}
}

// isRootClient reports whether sel selects an entity's client from the
// generated root client or transaction.
func (r *rewriter) isRootClient(sel *types.Selection) bool {
	obj := sel.Obj()
	if obj.Pkg() == nil || obj.Pkg().Path() != r.s.root {
		return false
	}
	return r.s.entities[obj.Name()] != nil
}

// builder handles a method of a create, update or delete builder.
func (r *rewriter) builder(call *ast.CallExpr, sel *ast.SelectorExpr, t target, name string, stack []ast.Node) {
	e := t.ent
	leaf := func() string { return r.leaf(e, call.Pos()) }
	// b.<name>(args) -> b.Set(<leaf>.<column>.<method>(args))
	assign := func(column, method string) {
		inner := leaf() + "." + column + "." + method + "("
		if len(call.Args) == 0 {
			inner += ")"
		} else {
			r.insert(call.Rparen, ")")
		}
		r.sets[call] = true
		r.count("Set")
		// After another setter: one Set with both.
		if prev, ok := sel.X.(*ast.CallExpr); ok && r.sets[prev] {
			gap := string(r.src[r.off(prev.Rparen)+1 : r.off(sel.Sel.Pos())])
			if !strings.Contains(gap, "/") {
				sep := ", "
				if strings.Contains(gap, "\n") {
					sep = ",\n"
				}
				// Not replace: what was inserted at the earlier call's
				// closing parenthesis (the end of its own setter) stays.
				r.add(r.off(prev.Rparen), r.off(call.Lparen)+1, sep+inner)
				// The closing parenthesis of the earlier Set went with the
				// range; this call's own closes the joined one.
				return
			}
		}
		r.replace(sel.Sel.Pos(), sel.Sel.End(), "Set")
		r.open(call.Lparen+1, inner)
	}
	wrap := func(fn string) {
		r.open(call.Pos(), r.orm(call.Pos())+"."+fn+"(")
		r.insert(call.End(), ")")
	}
	rename := func(to string) { r.replace(sel.Sel.Pos(), sel.Sel.End(), to) }

	if pre, mid, suf, field, edge := split(e, name, setterPrefixes, setterSuffixes); field || edge {
		method, viaID, ok := setterOf(pre, suf, field, edge)
		if !ok || (viaID && len(call.Args) != 1) {
			r.note(call.Pos(), "%s.%s: rewrite by hand", t.kind, name)
			return
		}
		if viaID {
			r.insert(call.Args[0].End(), ".ID")
		}
		assign(mid, method)
		return
	}
	switch name {
	case "Where":
		// Its arguments are looked at as any call's are: see arguments.
	case "SkipDefault":
		var keeps []string
		for _, a := range call.Args {
			f, isName := "", false
			if as, ok := a.(*ast.SelectorExpr); ok {
				f, isName = strings.CutPrefix(as.Sel.Name, "Field")
			}
			if !isName || !e.fields[f] {
				r.note(call.Pos(), "SkipDefault of a name that is not a constant: Set(%s.<Column>.Keep())", strings.ToLower(e.name))
				return
			}
			keeps = append(keeps, leaf()+"."+f+".Keep()")
		}
		r.replace(sel.Sel.Pos(), call.End(), "Set("+strings.Join(keeps, ", ")+")")
		r.count("Keep")
	case "Save", "SaveX":
		switch t.kind {
		case "UpdateOne":
			rename("SaveOne")
		case "Update":
			rename("Exec") // the count of rows
		default:
			if name == "SaveX" {
				rename("Save")
			}
		}
		if name == "SaveX" {
			wrap("Must")
		}
		r.count(name)
	case "Exec", "ExecX":
		switch t.kind {
		case "DeleteOne":
			r.deleteOne(call, sel, t, name)
			return
		case "UpdateOne":
			// It reported a missing row and returned no count.
			rename("Exec")
			wrap("One")
			if name == "ExecX" {
				wrap("Check")
			}
		case "Create", "CreateBulk":
			if name == "ExecX" {
				rename("Exec")
				wrap("Check")
			}
		case "Update":
			// It returned an error; Exec returns the count too.
			if name == "ExecX" {
				rename("Exec")
				wrap("Must")
			} else if !r.dropCount(call, stack) {
				r.note(call.Pos(), "Exec of a bulk update returns the number of rows too: _, err := ...Exec(ctx)")
			}
		case "Delete":
			if name == "ExecX" {
				rename("Exec")
				wrap("Must")
			}
		}
		r.count(name)
	default:
		r.note(call.Pos(), "%s.%s: rewrite by hand", t.kind, name)
	}
}

// dropCount makes `err := call` and `err = call` take the count the call now
// returns before the error, and reports whether it could.
func (r *rewriter) dropCount(call *ast.CallExpr, stack []ast.Node) bool {
	switch p := parent(stack, 1).(type) {
	case *ast.AssignStmt:
		if len(p.Lhs) == 1 && len(p.Rhs) == 1 && p.Rhs[0] == call {
			r.open(p.Lhs[0].Pos(), "_, ")
			return true
		}
	case *ast.ExprStmt:
		return true
	}
	return false
}

// deleteOne rewrites <ex>.<Entity>.DeleteOneID(id).Exec(ctx).
func (r *rewriter) deleteOne(call *ast.CallExpr, sel *ast.SelectorExpr, t target, name string) {
	inner, ok := sel.X.(*ast.CallExpr)
	if !ok || len(inner.Args) != 1 || len(call.Args) != 1 {
		r.note(call.Pos(), "a delete of one row built in steps: DeleteByID(ctx, handle, id) runs it")
		return
	}
	innerSel, ok := inner.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	ex, via := innerSel.X, ""
	if holder, ok := innerSel.X.(*ast.SelectorExpr); ok {
		if fs := r.info.Selections[holder]; fs != nil && fs.Kind() == types.FieldVal && r.isRootClient(fs) {
			ex, via = holder.X, through(fs)
		}
	}
	id := r.render(inner.Args[0])
	if innerSel.Sel.Name == "DeleteOne" {
		id += ".ID"
	}
	text := r.root(call.Pos()) + "." + t.ent.name + ".DeleteByID(" + r.render(call.Args[0]) + ", " + r.render(ex) + via + ", " + id + ")"
	if name == "ExecX" {
		text = r.orm(call.Pos()) + ".Check(" + text + ")"
	}
	r.replace(call.Pos(), call.End(), text)
	r.count("DeleteByID")
}

// query handles a method of a query.
func (r *rewriter) query(call *ast.CallExpr, sel *ast.SelectorExpr, t target, name string, stack []ast.Node) {
	e := t.ent
	rename := func(to string) { r.replace(sel.Sel.Pos(), sel.Sel.End(), to) }
	must := func() {
		r.open(call.Pos(), r.orm(call.Pos())+".Must(")
		r.insert(call.End(), ")")
	}
	switch name {
	case "Where", "All", "Only", "First", "Count", "IDs", "Limit", "Offset", "ForUpdate", "ForShare", "Clone":
	case "AllX", "OnlyX", "FirstX", "CountX", "IDsX":
		rename(strings.TrimSuffix(name, "X"))
		must()
		r.count(name)
	case "Exist":
		rename("Exists")
		r.count("Exists")
	case "ExistX":
		rename("Exists")
		must()
		r.count("Exists")
	case "Unique":
		if len(call.Args) == 1 && types.ExprString(call.Args[0]) == "true" {
			r.replace(sel.Sel.Pos(), call.End(), "Distinct()")
			r.count("Distinct")
		} else {
			r.note(call.Pos(), "Unique(false): rows are not made distinct unless Distinct() is called")
		}
	case "Order":
		r.order(call, sel, e)
	case "Paginate":
		r.paginate(call, sel)
	case "GroupBy", "Aggregate":
		// A grouped read starts at Select, which may name no column:
		// q.Select().GroupBy("status").Aggregate(orm.Count()).Scan(ctx, &rows)
		r.replace(sel.Sel.Pos(), sel.Sel.End(), "Select()."+name)
		r.count("grouped read")
	case "Select":
		// Columns scanned into something of the caller's are read the same
		// way; rows read as entities with only some columns are not.
		switch next, _ := receiverOf(call, stack); next {
		case "Scan", "Int", "GroupBy", "Aggregate":
		case "All", "Only", "First", "AllX", "OnlyX", "FirstX":
			// Entities with only some of their columns read.
			r.replace(sel.Sel.Pos(), sel.Sel.End(), "Columns")
			r.count("Columns")
		default:
			r.note(call.Pos(), "Select followed by %s: read the columns with Select(...).Scan(ctx, &rows)", next)
		}
	default:
		if edge, ok := strings.CutPrefix(name, "With"); ok && e.edges[edge] {
			if len(call.Args) > 0 && !r.loadOption(call) {
				r.note(call.Pos(), "%s with options: the option is a query of the neighbors now, %s(<root>.<Target>.Query(nil).Where(...))", name, name)
			}
			return
		}
		r.note(call.Pos(), "%s of a query: rewrite by hand", name)
	}
}

// loadOption rewrites the option of an eager load when it is a function
// that only calls methods of the query it is given,
//
//	WithItems(func(q *query.ItemQuery) { q.Where(...).Limit(3) })
//
// to the query itself, WithItems(<root>.Item.Query(nil).Where(...).Limit(3)).
func (r *rewriter) loadOption(call *ast.CallExpr) bool {
	if len(call.Args) != 1 {
		return false
	}
	fn, ok := call.Args[0].(*ast.FuncLit)
	if !ok || len(fn.Type.Params.List) != 1 || len(fn.Type.Params.List[0].Names) != 1 || len(fn.Body.List) != 1 {
		return false
	}
	stmt, ok := fn.Body.List[0].(*ast.ExprStmt)
	if !ok {
		return false
	}
	param := r.info.Defs[fn.Type.Params.List[0].Names[0]]
	// The chain must start at the parameter.
	var first *ast.Ident
	for e := stmt.X; first == nil; {
		switch x := e.(type) {
		case *ast.CallExpr:
			e = x.Fun
		case *ast.SelectorExpr:
			e = x.X
		case *ast.Ident:
			first = x
		default:
			return false
		}
	}
	if param == nil || r.info.Uses[first] != param {
		return false
	}
	target, kind := r.s.kindOf(typeName(param.Type()))
	if target == nil || (kind != "Query" && kind != "Querier") {
		return false
	}
	r.replace(first.Pos(), first.End(), r.root(call.Pos())+"."+target.name+".Query(nil)")
	text := r.render(stmt.X)
	r.replace(fn.Pos(), fn.End(), text)
	r.count("load option")
	return true
}

// order rewrites Order(pkg.ByX(sql.OrderDesc()), velox.Asc(pkg.FieldY)) to
// OrderBy(pkg.X.Desc(), pkg.Y.Asc()).
func (r *rewriter) order(call *ast.CallExpr, sel *ast.SelectorExpr, e *entityInfo) {
	var terms []string
	for _, a := range call.Args {
		c, ok := a.(*ast.CallExpr)
		if !ok {
			r.note(call.Pos(), "Order of something that is not a call: OrderBy takes %s.<Column>.Asc() or .Desc()", strings.ToLower(e.name))
			return
		}
		fn, ok := c.Fun.(*ast.SelectorExpr)
		if !ok {
			r.note(call.Pos(), "Order: rewrite by hand")
			return
		}
		obj := r.info.Uses[fn.Sel]
		t, known := r.targetOf(obj, "")
		switch {
		case known && t.where == "<entity>" && strings.HasPrefix(fn.Sel.Name, "By") && t.ent.fields[strings.TrimPrefix(fn.Sel.Name, "By")]:
			dir := "Asc"
			for _, opt := range c.Args {
				option := ""
				if oc, ok := opt.(*ast.CallExpr); ok {
					if os, ok := oc.Fun.(*ast.SelectorExpr); ok {
						option = os.Sel.Name
					}
				}
				switch option {
				case "OrderDesc":
					dir = "Desc"
				case "OrderAsc":
				default:
					r.note(call.Pos(), "Order with an option other than a direction: rewrite by hand")
					return
				}
			}
			terms = append(terms, r.render(fn.X)+"."+strings.TrimPrefix(fn.Sel.Name, "By")+"."+dir+"()")
		case known && t.where == "root" && (fn.Sel.Name == "Asc" || fn.Sel.Name == "Desc"):
			for _, col := range c.Args {
				cs, ok := col.(*ast.SelectorExpr)
				f, isName := "", false
				if ok {
					f, isName = strings.CutPrefix(cs.Sel.Name, "Field")
				}
				if !isName || !e.fields[f] {
					r.note(call.Pos(), "Order by a name that is not a constant of the entity: OrderBy takes %s.<Column>.Asc() or .Desc()", strings.ToLower(e.name))
					return
				}
				terms = append(terms, r.render(cs.X)+"."+f+"."+fn.Sel.Name+"()")
			}
		default:
			r.note(call.Pos(), "Order: rewrite by hand")
			return
		}
	}
	r.replace(sel.Sel.Pos(), call.End(), "OrderBy("+strings.Join(terms, ", ")+")")
	r.count("OrderBy")
}

// paginate moves the order and the filter out of Paginate's options and
// onto the query.
func (r *rewriter) paginate(call *ast.CallExpr, sel *ast.SelectorExpr) {
	if len(call.Args) < 5 {
		return
	}
	var chain string
	var applied []string
	for i, opt := range call.Args[5:] {
		if c, ok := opt.(*ast.CallExpr); ok {
			if text, inline := r.inlineOption(c, true); inline {
				chain += text
				continue
			}
		}
		// An option held in a variable or a slice: a function of the query
		// by now, whatever made it.
		text := r.render(opt)
		if call.Ellipsis.IsValid() && i == len(call.Args[5:])-1 {
			text += "..."
		}
		applied = append(applied, text)
	}
	if len(call.Args) == 5 {
		return
	}
	recv := r.render(sel.X) + chain
	if len(applied) > 0 {
		recv = r.orm(call.Pos()) + ".Apply(" + recv + ", " + strings.Join(applied, ", ") + ")"
	}
	r.replace(sel.X.Pos(), sel.X.End(), recv)
	r.replace(call.Args[4].End(), call.Rparen, "")
	r.count("Paginate")
}

// inlineOption says whether c is an option Paginate can put on the query
// itself, entity.WithUserOrder(o) or entity.WithUserFilter(w.Filter), and
// returns the calls that do (taking the arguments' text when take is set).
func (r *rewriter) inlineOption(c *ast.CallExpr, take bool) (string, bool) {
	fn, ok := c.Fun.(*ast.SelectorExpr)
	if !ok || len(c.Args) != 1 || !strings.HasPrefix(fn.Sel.Name, "With") {
		return "", false
	}
	t, known := r.targetOf(r.info.Uses[fn.Sel], "")
	if !known || t.where != "entity" {
		return "", false
	}
	sig, _ := r.info.TypeOf(c.Fun).(*types.Signature)
	switch {
	case strings.HasSuffix(fn.Sel.Name, "Order"):
		// The form that takes a list of orders returns an error too, and
		// is not an argument of Paginate.
		if sig == nil || sig.Results().Len() != 1 {
			return "", false
		}
		if !take {
			return "", true
		}
		return ".OrderBy(" + r.render(c.Args[0]) + ".Term())", true
	case strings.HasSuffix(fn.Sel.Name, "Filter"):
		w, ok := c.Args[0].(*ast.SelectorExpr)
		if !ok || w.Sel.Name != "Filter" {
			return "", false
		}
		if !take {
			return "", true
		}
		return ".Where(" + r.render(w.X) + ".P())", true
	}
	return "", false
}

// option rewrites entity.WithUserOrder(o) and entity.WithUserFilter(f) where
// the option is kept (in a variable, in a slice): to a function of the
// query, which orm.Apply calls.
func (r *rewriter) option(call *ast.CallExpr, name string, obj types.Object, stack []ast.Node) {
	sig, ok := obj.Type().(*types.Signature)
	if !ok || len(call.Args) != 1 || sig.Params().Len() != 1 {
		r.note(call.Pos(), "%s: an order or a filter is a call on the query now, .OrderBy(o.Term()) and .Where(w.P())", name)
		return
	}
	base := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(name, "With"), "Order"), "Filter")
	ent := r.s.entities[base]
	if ent == nil {
		r.note(call.Pos(), "%s: an order or a filter is a call on the query now, .OrderBy(o.Term()) and .Where(w.P())", name)
		return
	}
	head := "func(q *" + r.entity(call.Pos()) + "." + ent.name + "Query) {"
	var text string
	switch {
	case strings.HasSuffix(name, "Order"):
		if _, many := sig.Params().At(0).Type().(*types.Slice); many {
			text = head + "\n\tfor _, o := range " + r.render(call.Args[0]) + " {\n\t\tq.OrderBy(o.Term())\n\t}\n}"
		} else {
			text = head + " q.OrderBy(" + r.render(call.Args[0]) + ".Term()) }"
		}
	default:
		if w, ok := call.Args[0].(*ast.SelectorExpr); ok && w.Sel.Name == "Filter" {
			text = head + " q.Where(" + r.render(w.X) + ".P()) }"
			break
		}
		// A filter of the application's own: a function that returns the
		// predicate or an error, which fails the query as it did.
		text = head + "\n\tp, err := (" + r.render(call.Args[0]) + ")()\n\tif err != nil {\n\t\tp = " +
			r.predicate(call.Pos()) + "." + ent.name + "(" + r.orm(call.Pos()) + ".Invalid(err))\n\t}\n\tq.Where(p)\n}"
	}
	if sig.Results().Len() == 2 {
		// The option and an error that is always nil now.
		as, ok := parent(stack, 1).(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 2 || len(as.Rhs) != 1 {
			r.note(call.Pos(), "%s returned an error: it is a function of the query now and returns none", name)
			return
		}
		text += ", error(nil)"
	}
	r.replace(call.Pos(), call.End(), text)
	r.count("pagination option")
}

// repeated rewrites a name the generated root package repeated from the
// library (Mutation, OpCreate, Hook, MutateFunc ...) to the library's, and
// reports whether it was one.
func (r *rewriter) repeated(sel *ast.SelectorExpr, obj types.Object) bool {
	if r.libScope == nil {
		return false
	}
	orig := r.libScope.Lookup(obj.Name())
	if orig == nil || !orig.Exported() {
		return false
	}
	switch obj.(type) {
	case *types.TypeName:
		if _, ok := orig.(*types.TypeName); !ok || !types.Identical(types.Unalias(obj.Type()), types.Unalias(orig.Type())) {
			return false
		}
	case *types.Const:
		if _, ok := orig.(*types.Const); !ok || !types.Identical(obj.Type(), orig.Type()) {
			return false
		}
	default:
		if !types.Identical(obj.Type(), orig.Type()) {
			return false
		}
	}
	r.replace(sel.X.Pos(), sel.X.End(), r.ref(r.lib, "velox", sel.Pos()))
	r.count("library name")
	return true
}

// predicateOf names the entity whose predicate type t is, or "".
func (r *rewriter) predicateOf(t types.Type) string {
	named, ok := types.Unalias(t).(*types.Named)
	if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != r.s.root+"/predicate" {
		return ""
	}
	if _, isFunc := named.Underlying().(*types.Signature); !isFunc {
		return ""
	}
	if r.s.entities[named.Obj().Name()] == nil {
		return ""
	}
	return named.Obj().Name()
}

// into wraps an expression that is a function on a selector and goes where
// a predicate is wanted. A predicate was such a function, so the two were
// assignable; a predicate is data now, and orm.Raw makes one of a function.
func (r *rewriter) into(e ast.Expr, target types.Type) {
	if target == nil {
		return
	}
	name := r.predicateOf(target)
	if name == "" {
		return
	}
	tv, ok := r.info.Types[e]
	if !ok {
		return
	}
	if id, isIdent := e.(*ast.Ident); isIdent && id.Name == "nil" && tv.IsNil() {
		r.replace(e.Pos(), e.End(), r.predicate(e.Pos())+"."+name+"{}")
		r.count("zero predicate")
		return
	}
	if _, isFunc := types.Unalias(tv.Type).(*types.Signature); !isFunc {
		return
	}
	pred := r.predicate(e.Pos())
	r.open(e.Pos(), pred+"."+name+"("+r.orm(e.Pos())+".Raw(")
	r.insert(e.End(), "))")
	r.count("Raw predicate")
}

// arguments looks at what a call passes where it takes predicates.
func (r *rewriter) arguments(call *ast.CallExpr) {
	tv, ok := r.info.Types[call.Fun]
	if !ok {
		return
	}
	if tv.IsType() {
		// A conversion, predicate.User(f).
		if len(call.Args) == 1 && r.predicateOf(tv.Type) != "" {
			if at, known := r.info.Types[call.Args[0]]; known && !at.IsNil() {
				if _, isFunc := types.Unalias(at.Type).(*types.Signature); isFunc {
					r.open(call.Args[0].Pos(), r.orm(call.Pos())+".Raw(")
					r.insert(call.Args[0].End(), ")")
					r.count("Raw predicate")
				}
			}
		}
		return
	}
	sig, ok := types.Unalias(tv.Type).Underlying().(*types.Signature)
	if !ok || call.Ellipsis.IsValid() {
		return
	}
	for i, a := range call.Args {
		var param types.Type
		switch n := sig.Params().Len(); {
		case sig.Variadic() && i >= n-1:
			if slice, ok := sig.Params().At(n - 1).Type().(*types.Slice); ok {
				param = slice.Elem()
			}
		case i < n:
			param = sig.Params().At(i).Type()
		}
		r.into(a, param)
	}
}

// results looks at what a function returns where it returns predicates.
func (r *rewriter) results(ret *ast.ReturnStmt, stack []ast.Node) {
	var sig *types.Signature
	for i := len(stack) - 1; i >= 0 && sig == nil; i-- {
		switch fn := stack[i].(type) {
		case *ast.FuncLit:
			sig, _ = r.info.TypeOf(fn).(*types.Signature)
		case *ast.FuncDecl:
			if obj := r.info.Defs[fn.Name]; obj != nil {
				sig, _ = obj.Type().(*types.Signature)
			}
		}
	}
	if sig == nil || sig.Results().Len() != len(ret.Results) {
		return
	}
	for i, e := range ret.Results {
		r.into(e, sig.Results().At(i).Type())
	}
}

// compareNil rewrites p == nil and p != nil of a predicate.
func (r *rewriter) compareNil(bin *ast.BinaryExpr) {
	if bin.Op != token.EQL && bin.Op != token.NEQ {
		return
	}
	isNil := func(e ast.Expr) bool {
		id, ok := e.(*ast.Ident)
		return ok && id.Name == "nil" && r.info.Types[e].IsNil()
	}
	other := bin.X
	switch {
	case isNil(bin.Y):
	case isNil(bin.X):
		other = bin.Y
	default:
		return
	}
	if r.predicateOf(r.info.TypeOf(other)) == "" {
		return
	}
	text := r.orm(bin.Pos()) + ".P(" + r.render(other) + ").IsZero()"
	if bin.Op == token.NEQ {
		text = "!" + text
	}
	r.replace(bin.Pos(), bin.End(), text)
	r.count("zero predicate")
}

// whereInput keeps `p, err := where.P()` compiling: P returned an error for
// an input that cannot be a condition, and returns a predicate that fails
// the query now.
func (r *rewriter) whereInput(as *ast.AssignStmt) {
	if len(as.Lhs) != 2 || len(as.Rhs) != 1 {
		return
	}
	call, ok := as.Rhs[0].(*ast.CallExpr)
	if !ok {
		return
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	selection := r.info.Selections[sel]
	if !ok || selection == nil || sel.Sel.Name != "P" {
		return
	}
	if t, ok := r.targetOf(selection.Obj(), declaring(selection)); !ok || t.where != "filter" {
		return
	}
	r.insert(call.End(), ", error(nil)")
	r.count("P without an error")
}

// elements looks at the elements of a slice or array of predicates.
func (r *rewriter) elements(lit *ast.CompositeLit) {
	var elem types.Type
	switch t := types.Unalias(r.info.TypeOf(lit)).Underlying().(type) {
	case *types.Slice:
		elem = t.Elem()
	case *types.Array:
		elem = t.Elem()
	default:
		return
	}
	for _, e := range lit.Elts {
		r.into(e, elem)
	}
}
