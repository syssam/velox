package main

import (
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"strconv"
	"strings"

	"github.com/syssam/velox/contrib/graphql/gqlinput"
	"github.com/syssam/velox/orm"
)

const ormPath = "github.com/syssam/velox/orm"

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
	// made are the calls that were functions on a selector and are
	// predicates now (the library's AndPredicates): not to be wrapped.
	made map[*ast.CallExpr]bool
	// wrapped are the calls that became the argument of another call
	// (velox.Must, velox.Check) or a call with other arguments.
	wrapped map[*ast.CallExpr]bool
	// handles are the parameters of the application's functions that take
	// a client only to query on it (by where they are declared): their type
	// becomes velox.Executor, so that a transaction can be passed for them.
	handles map[token.Pos]bool
	// ownMethods are the names of the methods the application declares,
	// and ownPkgs the import paths of its packages.
	ownMethods map[string]bool
	ownPkgs    map[string]bool
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

// velox is the library's root package, which has the database handle, the
// errors and the types of a paged list.
func (r *rewriter) velox(pos token.Pos) string { return r.ref(r.lib, "velox", pos) }

// app names, for the application, a name of the library's root package: the
// handle, an error, Must. The generated root package gives most of them under
// its own name (ormgen.RootNames), so that a file which starts its queries
// there needs one import; that is the name written wherever the file can
// import the generated root. A package the schema imports cannot, and names
// the library.
// engine returns a name of package orm as the file writes it: under the
// generated root package's name where that package forwards it and the file
// can import it, so that an application has no import of package orm, and as
// package orm's otherwise.
func (r *rewriter) engine(name string, pos token.Pos) string {
	forward := name
	switch name {
	case "Query":
		forward = "EntityQuery"
	case "Mutation":
		forward = "EntityMutation"
	}
	if engineForwarded[forward] && r.s.gives(forward) && r.reachesRoot() {
		return r.root(pos) + "." + forward
	}
	return r.orm(pos) + "." + name
}

// engineForwarded are the names of package orm the generated root package
// forwards (internal/ormgen/reexport.go).
var engineForwarded = map[string]bool{
	"Agg": true, "EntityQuery": true, "EntityMutation": true, "Predicate": true,
	"ValueColumn": true, "BoolColumn": true, "EnumColumn": true, "OrderedColumn": true, "NumberColumn": true, "StringColumn": true,
	"Count": true, "Sum": true, "Min": true, "Max": true, "Mean": true,
	"And": true, "Or": true, "Not": true, "IsZero": true, "Compile": true, "Apply": true,
	"SkipLocked": true, "NoWait": true,
}

func (r *rewriter) app(name string, pos token.Pos) string {
	if r.s.gives(name) && r.reachesRoot() {
		return r.root(pos) + "." + name
	}
	return r.velox(pos) + "." + name
}

// libraryNames moves the references a file has to the library's root package
// over to the generated root package, for the names that package gives: the
// two are called velox, and a file that names both must import one under
// another name. Where the library held the name and the generated package
// does not have it yet, the generated package takes it, and what is left of
// the library (a name the generated package does not give) is written with
// the library's other name.
func (r *rewriter) libraryNames() {
	if !r.reachesRoot() {
		return
	}
	var given, others []*ast.SelectorExpr
	ast.Inspect(r.file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if pn, ok := r.info.Uses[id].(*types.PkgName); !ok || pn.Imported().Path() != r.lib {
			return true
		}
		if r.s.gives(sel.Sel.Name) {
			given = append(given, sel)
		} else {
			others = append(others, sel)
		}
		return true
	})
	if len(given) == 0 && len(others) == 0 {
		return
	}
	// The library keeps its name in a file where the generated package
	// already has another, or where the two are not named alike.
	local := r.names[r.lib]
	if _, has := r.names[r.s.root]; !has && local == rootName(r.s.to) {
		delete(r.names, r.lib)
		if r.gone == nil {
			r.gone = map[string]bool{}
		}
		r.gone[r.lib] = true
		r.root(r.file.Pos()) // takes the name
		for _, sel := range others {
			r.replace(sel.X.Pos(), sel.X.End(), r.velox(sel.Pos()))
		}
	}
	for _, sel := range given {
		r.replace(sel.X.Pos(), sel.X.End(), r.root(sel.Pos()))
		r.count("library name of the generated package")
	}
}

// reachesRoot reports whether the file can import the generated root
// package: it does already, or its package is not one the root package
// imports (the schema and what the schema is written with would close a
// cycle).
func (r *rewriter) reachesRoot() bool {
	if _, ok := r.names[r.s.root]; ok {
		return true
	}
	if r.pkg == nil || len(r.s.below) == 0 {
		return false
	}
	path := r.pkg.Path()
	return path != r.s.root && !r.s.below[path]
}

// gqlinput is the runtime of the GraphQL layer of the new generated code:
// the direction of an order input, the depth of a where input.
func (r *rewriter) gqlinput(pos token.Pos) string {
	return r.ref(r.lib+"/contrib/graphql/gqlinput", "gqlinput", pos)
}

// paging names a type of a paged list, or of an order input, where it is
// now: the cursor and the page info in the library's root package, the
// direction of an order with the GraphQL layer.
func (r *rewriter) paging(name string, pos token.Pos) string {
	if strings.HasPrefix(name, "OrderDirection") {
		return r.gqlinput(pos) + "." + name
	}
	return r.app(name, pos)
}
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

// embeddedPath spells the embedded fields a selection went through:
// s.db.Holiday, where s.db is a struct that embeds the client, selected
// Holiday through the field Client, and the handle is s.db.Client.
func embeddedPath(sel *types.Selection) string {
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
			// AddParentIDs is the edge Parents, also next to an edge
			// called Parent: the ids of several are of a to-many edge.
			if edge := e.singular[m]; edge != "" && s == "IDs" {
				return p, edge, s, false, true
			}
			if e.edges[m] {
				return p, m, s, false, true
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
	r.libraryNames()
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
			case *ast.IndexExpr:
				r.columnType(top, top.X, []ast.Expr{top.Index})
			case *ast.IndexListExpr:
				r.columnType(top, top.X, top.Indices)
			case *ast.UnaryExpr:
				r.funcConstraint(top)
			case *ast.BinaryExpr:
				r.compareNil(top)
			case *ast.ReturnStmt:
				r.results(top, stack)
			case *ast.AssignStmt:
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
			case *ast.TypeAssertExpr:
				if top.Type != nil {
					r.assertion(top.Type)
					r.mutationAsserted(top.X, top.Type)
				}
			case *ast.TypeSwitchStmt:
				subject := switched(top)
				for _, c := range top.Body.List {
					if clause, ok := c.(*ast.CaseClause); ok {
						for _, e := range clause.List {
							r.assertion(e)
							if subject != nil {
								r.mutationAsserted(subject, e)
							}
						}
					}
				}
			case *ast.BasicLit:
				// "velox: ...": the text of an error, matched by its words.
				// The errors are the same types from package orm, but not
				// every word inside one is the same.
				if top.Kind == token.STRING && strings.HasPrefix(strings.Trim(top.Value, "`\""), "velox: ") {
					r.note(top.Pos(), "the text of an error is matched here: package orm returns the same errors, and one of a missing row reads the same (\"velox: user not found\"), but the words inside a validation error are its own; test the error itself (velox.IsNotFound, errors.Is) instead of its words")
				}
			case *ast.InterfaceType:
				r.queryMethods(top)
			case *ast.SwitchStmt:
				if top.Tag != nil && r.predicateOf(r.info.TypeOf(top.Tag)) != "" {
					r.note(top.Pos(), "a switch on a predicate: it is data now and is not compared; orm.IsZero(p) says whether it is no condition")
				}
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
	if r.failure(sel, obj) || r.queryContext(sel, obj) {
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
				r.replace(sel.Pos(), sel.End(), r.app("Tx", sel.Pos()))
				embedded()
				r.count("type Tx")
			case "Client":
				if star, ok := parent(stack, 1).(*ast.StarExpr); ok && r.handleParam(stack) {
					// A parameter that is only queried on: a transaction
					// is passed for it as well as a database.
					r.replace(star.Pos(), sel.End(), r.app("Executor", sel.Pos()))
					r.count("handle parameter")
					break
				}
				r.replace(sel.Pos(), sel.End(), r.app("DB", sel.Pos()))
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
			if !r.repeated(sel, obj) {
				r.note(sel.Pos(), "%s of the generated root package: rewrite by hand", name)
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
			r.replace(sel.Pos(), sel.End(), r.engine("Mutation", sel.Pos()))
		case "Client":
			// A client was a handle bound to an entity; what is passed
			// around now is the handle.
			from := sel.Pos()
			if star, ok := parent(stack, 1).(*ast.StarExpr); ok {
				from = star.Pos()
			}
			r.replace(from, sel.End(), r.app("Executor", sel.Pos()))
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
			} else if ent != nil && kind == "OrderField" {
				// The fields a list is ordered by are of the entity's own
				// package now.
				r.replace(sel.Pos(), sel.End(), r.leaf(ent, sel.Pos())+".OrderField")
				r.count("type OrderField")
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
				r.replace(sel.Pos(), sel.End(), r.paging(name, sel.Pos()))
				r.count("type " + name)
			}
			return
		}
		if name == "OrderDirectionAsc" || name == "OrderDirectionDesc" {
			r.replace(sel.Pos(), sel.End(), r.paging(name, sel.Pos()))
			r.count("type OrderDirection")
			return
		}
		if strings.HasPrefix(name, "With") && (strings.HasSuffix(name, "Order") || strings.HasSuffix(name, "Filter")) {
			r.note(sel.Pos(), "%s: an order or a filter is a call on the query now, .OrderBy(o.Term()) and .Where(w.P())", name)
		}
	case "intercept":
		r.intercept(sel, name)
	case "privacy":
		// The generated package repeated the library's.
		r.replace(sel.X.Pos(), sel.X.End(), r.ref(r.lib+"/privacy", "privacy", sel.Pos()))
		r.count("privacy")
	default:
		if obj.Pkg().Path() == r.lib+"/dialect/sql" && name == "PredicateFunc" {
			r.replace(sel.Pos(), sel.End(), r.engine("Predicate", sel.Pos()))
			r.count("predicate constraint")
			return
		}
		if obj.Pkg().Path() == r.lib+"/contrib/graphql/gqlrelay" {
			switch name {
			case "Cursor", "PageInfo", "OrderDirection", "OrderDirectionAsc", "OrderDirectionDesc":
				r.replace(sel.Pos(), sel.End(), r.paging(name, sel.Pos()))
				r.count("gqlrelay")
			default:
				r.note(sel.Pos(), "gqlrelay.%s: rewrite by hand", name)
			}
			return
		}
		r.foreign(sel, obj, t.where)
	}
}

// intercept rewrites a name of the generated package intercept.
func (r *rewriter) intercept(sel *ast.SelectorExpr, name string) {
	switch name {
	case "Func":
		r.replace(sel.Pos(), sel.End(), r.orm(sel.Pos())+".InterceptFunc")
	case "TraverseFunc":
		r.replace(sel.Pos(), sel.End(), r.orm(sel.Pos())+".TraverseFunc")
	case "Query":
		r.replace(sel.Pos(), sel.End(), "*"+r.engine("Query", sel.Pos()))
	default:
		r.note(sel.Pos(), "intercept.%s: an interceptor is handed *orm.Query, whatever the entity", name)
		return
	}
	r.count("intercept")
}

// foreign reports a reference to a package of the old generated tree that
// the new one does not have, or to a name of one that it has that no rule
// knows: left as it is, it would be found by the build, without a word on
// what to do.
func (r *rewriter) foreign(sel *ast.SelectorExpr, obj types.Object, where string) {
	path := obj.Pkg().Path()
	if path != r.s.root && !strings.HasPrefix(path, r.s.root+"/") {
		return
	}
	switch where {
	case "filter":
		switch name := obj.Name(); {
		case strings.HasPrefix(name, "ErrEmpty"):
			r.note(sel.Pos(), "%s: an input with no condition is not an error now: P() returns the zero predicate, which is no condition", name)
		case name == "ErrFilterDepthExceeded":
			// The same failure, met where the query runs: P returns no
			// error, and a predicate that fails the query with this one.
			r.replace(sel.Pos(), sel.End(), r.gqlinput(sel.Pos())+".ErrWhereDepth")
			r.count("where depth")
		case name == "DefaultMaxFilterDepth":
			// The depth was an option of the generator; it is the runtime's
			// constant now.
			if c, ok := obj.(*types.Const); ok && c.Val().ExactString() == strconv.Itoa(gqlinput.MaxWhereDepth) {
				r.replace(sel.Pos(), sel.End(), r.gqlinput(sel.Pos())+".MaxWhereDepth")
				r.count("where depth")
			} else {
				r.note(sel.Pos(), "%s: a where input nests %d deep now (gqlinput.MaxWhereDepth), whatever the generator was told", name, gqlinput.MaxWhereDepth)
			}
		}
	case "predicate", "migrate", "entity", "root", "<entity>", "client/<entity>", "query", "intercept", "privacy":
	default:
		// Once for the run: an application may refer to such a package in
		// a thousand places (a test opener kept in the generated tree).
		rule := "no counterpart: package " + where
		if r.done[rule] == 0 {
			r.note(sel.Pos(), "%s.%s: the package %s of the generated tree has no counterpart; this is the first reference to it, the summary of the run counts them", where[strings.LastIndex(where, "/")+1:], obj.Name(), where)
		}
		r.count(rule)
	}
}

// queryContext reports the query context of the old runtime: what an
// interceptor read from its context (the operation, the type, the limit).
// Package orm puts none there, so the read would compile, return nil and
// leave a rule that guarded on it doing nothing.
func (r *rewriter) queryContext(sel *ast.SelectorExpr, obj types.Object) bool {
	if obj.Pkg() == nil {
		return false
	}
	if path := obj.Pkg().Path(); path != r.lib && path != r.s.root {
		return false
	}
	switch obj.Name() {
	case "QueryFromContext", "NewQueryContext", "QueryContext":
		r.note(sel.Pos(), "%s: package orm puts no QueryContext in the context, so this reads nil. An interceptor is handed the query itself, a *orm.Query (Type, Limit, Offset, WhereP): read it there", obj.Name())
		return true
	}
	return false
}

// errorNames are the errors of the library's root package, and what tests
// and makes them: package orm returns these very errors, so code that tests
// one keeps holding for the new code as it is.
var errorNames = map[string]bool{
	"ErrNotFound": true, "ErrNotSingular": true,
	"IsNotFound": true, "IsNotSingular": true, "IsNotLoaded": true, "IsConstraintError": true, "IsValidationError": true,
	"NotFoundError": true, "NotSingularError": true, "NotLoadedError": true, "ConstraintError": true, "ValidationError": true,
	"NewNotFoundError": true, "NewNotSingularError": true, "NewNotLoadedError": true, "NewConstraintError": true, "NewValidationError": true,
	"MaskNotFound": true,
}

// failure moves the name of an error that the generated root package, or
// package runtime, repeated from the library to the library's own. One
// already written with the library's name is right as it is.
func (r *rewriter) failure(sel *ast.SelectorExpr, obj types.Object) bool {
	if obj.Pkg() == nil || !errorNames[obj.Name()] {
		return false
	}
	switch obj.Pkg().Path() {
	case r.lib:
		return true
	case r.s.root, r.lib + "/runtime":
		r.replace(sel.Pos(), sel.End(), r.app(obj.Name(), sel.Pos()))
		r.count("error of the library")
		return true
	}
	return false
}

// queryMethods renames, in an interface the application declares for
// queries (a constraint, a seam for tests), the method whose name changed:
// Exist is Exists. Only when no type of the application has a method Exist,
// which such an interface could be meant for instead.
func (r *rewriter) queryMethods(it *ast.InterfaceType) {
	if it.Methods == nil || r.ownMethods["Exist"] {
		return
	}
	for _, m := range it.Methods.List {
		if len(m.Names) != 1 || m.Names[0].Name != "Exist" {
			continue
		}
		sig, ok := r.info.TypeOf(m.Type).(*types.Signature)
		if !ok || sig.Params().Len() != 1 || sig.Results().Len() != 2 ||
			types.TypeString(sig.Params().At(0).Type(), nil) != "context.Context" ||
			types.TypeString(sig.Results().At(0).Type(), nil) != "bool" || types.TypeString(sig.Results().At(1).Type(), nil) != "error" {
			continue
		}
		r.replace(m.Names[0].Pos(), m.Names[0].End(), "Exists")
		r.count("Exists")
	}
}

// ownInterfaceCall renames a call through an interface of the application's
// whose method was renamed with the query's (see queryMethods): q.Exist(ctx)
// on a value of a type parameter constrained by it.
func (r *rewriter) ownInterfaceCall(sel *ast.SelectorExpr, obj types.Object) {
	fn, ok := obj.(*types.Func)
	if !ok || fn.Name() != "Exist" || r.ownMethods["Exist"] || fn.Pkg() == nil {
		return
	}
	recv := fn.Signature().Recv()
	if recv == nil || !types.IsInterface(recv.Type()) {
		return
	}
	if !r.ownPkgs[fn.Pkg().Path()] {
		return // an interface of a library's
	}
	r.replace(sel.Sel.Pos(), sel.Sel.End(), "Exists")
	r.count("Exists")
}

// assertion reports a type assertion, or a case of a type switch, to the
// query or mutation type of one entity. It goes by the type asserted, not
// by how it is written: through an alias it is the same assertion.
func (r *rewriter) assertion(e ast.Expr) {
	t := r.info.TypeOf(e)
	if t == nil {
		return
	}
	if ptr, ok := types.Unalias(t).(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := types.Unalias(t).(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return
	}
	path := named.Obj().Pkg().Path()
	if path != r.s.root && !strings.HasPrefix(path, r.s.root+"/") {
		return
	}
	ent, kind := r.s.kindOf(named.Obj().Name())
	if ent == nil {
		return
	}
	switch where := r.s.where(path); {
	case where == "client/<entity>" && kind == "Mutation":
		r.note(e.Pos(), "a value is asserted to the mutation type of %s: every entity's mutation is *orm.Mutation now, so this holds for all of them; test m.Type() == %q as well", ent.name, ent.name)
	case (where == "query" && kind == "Query") || (where == "entity" && kind == "Querier"):
		r.note(e.Pos(), "a value is asserted to the query type of %s: an interceptor or a rule is handed *orm.Query whatever the entity, so this never holds there; test q.Type() == %q", ent.name, ent.name)
	}
}

// switched returns x of `switch v := x.(type)` or `switch x.(type)`.
func switched(s *ast.TypeSwitchStmt) ast.Expr {
	var e ast.Expr
	switch a := s.Assign.(type) {
	case *ast.ExprStmt:
		e = a.X
	case *ast.AssignStmt:
		if len(a.Rhs) == 1 {
			e = a.Rhs[0]
		}
	}
	if ta, ok := e.(*ast.TypeAssertExpr); ok {
		return ta.X
	}
	return nil
}

// mutationAsserted reports a mutation asserted to an interface with a method
// no mutation has now. velox generated a mutation type per entity with a
// method per field (SetOwnerID, Where(...predicate.X), ...), and a hook
// shared by many entities asserts the mutation to the ones it needs. Every
// mutation is *orm.Mutation here: the assertion compiles and never holds,
// and what the hook did for the entities that had the methods it does for
// none.
func (r *rewriter) mutationAsserted(x, typ ast.Expr) {
	if !r.isMutation(r.info.TypeOf(x), 0) {
		return
	}
	t := r.info.TypeOf(typ)
	if t == nil {
		return
	}
	iface, ok := t.Underlying().(*types.Interface)
	if !ok {
		return
	}
	have := reflect.TypeFor[*orm.Mutation]()
	var missing []string
	for i := range iface.NumMethods() {
		if _, ok := have.MethodByName(iface.Method(i).Name()); !ok {
			missing = append(missing, iface.Method(i).Name())
		}
	}
	if len(missing) == 0 {
		return
	}
	r.note(typ.Pos(), "a mutation is asserted to an interface with %s: velox generated that for each entity and no mutation has it now, so this never holds; every mutation is *orm.Mutation (Field and SetField by name, WhereP, PredicatesFuncs)", strings.Join(missing, ", "))
}

// isMutation reports whether t is the library's Mutation, or an interface
// that embeds it.
func (r *rewriter) isMutation(t types.Type, depth int) bool {
	if t == nil || depth > 4 {
		return false
	}
	if named, ok := types.Unalias(t).(*types.Named); ok && named.Obj().Pkg() != nil &&
		named.Obj().Pkg().Path() == r.lib && named.Obj().Name() == "Mutation" {
		return true
	}
	iface, ok := t.Underlying().(*types.Interface)
	if !ok {
		return false
	}
	for i := range iface.NumEmbeddeds() {
		if r.isMutation(iface.EmbeddedType(i), depth+1) {
			return true
		}
	}
	return false
}

// emptyList reports an Or (or a Not) of a list that may be empty: velox's
// matched every row then, and the runtime's matches none.
func (r *rewriter) emptyList(call *ast.CallExpr, name string) {
	if call.Ellipsis.IsValid() || len(call.Args) == 0 {
		r.note(call.Pos(), "%s of a list that may be empty: velox's matched every row then, this one matches none; check the list, or skip the condition when it is empty", name)
	}
}

// handleParam reports whether the type being visited, *Client, is that of
// parameters every one of which the analysis found to be only queried on.
func (r *rewriter) handleParam(stack []ast.Node) bool {
	f, ok := parent(stack, 2).(*ast.Field)
	if !ok || len(f.Names) == 0 {
		return false
	}
	for _, name := range f.Names {
		if !r.handles[name.Pos()] {
			return false
		}
	}
	return true
}

// toHandle reports whether e is an argument for a parameter that takes
// velox.Executor now.
func (r *rewriter) toHandle(e ast.Expr, stack []ast.Node) bool {
	c, ok := parent(stack, 1).(*ast.CallExpr)
	if !ok {
		return false
	}
	for i, a := range c.Args {
		if a == e {
			v := paramOf(r.info, c, i)
			return v != nil && r.handles[v.Pos()]
		}
	}
	return false
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
			// Passed on as a value rather than asked for a predicate: as the
			// plain column type of its level, which is what the parameter
			// it is passed to has become (columnType).
			if up, ok := parent(stack, 1).(*ast.SelectorExpr); !ok || up.X != ast.Expr(sel) {
				r.insert(sel.End(), ".As"+columnLevel(obj.Type())+"()")
			}
			return
		}
		if f, ok := strings.CutPrefix(name, "Default"); ok && e.fields[f] {
			// The default of an enum field: a function now.
			r.insert(sel.End(), "()")
			r.count("enum default")
			return
		}
		switch name {
		case "Or", "Not":
			// As a value: whoever calls it may do so with nothing.
			r.note(sel.Pos(), "%s as a value: called with no predicate velox's matched every row, this one matches none", name)
			return
		case "And":
			return
		}
		r.note(sel.Pos(), "%s.%s: rewrite by hand", strings.ToLower(e.name), name)
	case *types.TypeName:
		if e.fields[name] {
			rename(name+"Enum", "enum type")
		}
	case *types.Const:
		if f, ok := strings.CutPrefix(name, "Field"); ok && e.fields[f] {
			// Also inside SkipDefault(pkg.FieldX) and velox.Asc(pkg.FieldX):
			// the rules for those replace the call with this in it when
			// they apply, and when they do not (sql.Asc, an Order they
			// leave to a person) the name must still be one that exists.
			rename("Column"+f, "column name")
		}
	}
}

// member handles a field or a method value selected from a value.
func (r *rewriter) member(sel *ast.SelectorExpr, selection *types.Selection, stack []ast.Node) {
	obj := selection.Obj()
	// o.Field.Column: the column an order input's field stands for.
	if v, ok := obj.(*types.Var); ok && v.IsField() && v.Name() == "Column" && v.Pkg() != nil && v.Pkg().Path() == r.s.root+"/entity" {
		if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "Field" {
			if ent, kind := r.s.kindOf(typeName(r.info.TypeOf(sel.X))); ent != nil && kind == "OrderField" {
				r.replace(inner.X.End(), sel.End(), ".Term().Col")
				r.count("order field")
				return
			}
		}
	}
	// x.RuntimeConfig().Driver: the driver under a client or a transaction.
	if v, ok := obj.(*types.Var); ok && v.IsField() && v.Name() == "Driver" && v.Pkg() != nil && v.Pkg().Path() == r.lib+"/runtime" {
		if inner, ok := sel.X.(*ast.CallExpr); ok {
			if m, ok := inner.Fun.(*ast.SelectorExpr); ok && m.Sel.Name == "RuntimeConfig" && len(inner.Args) == 0 {
				r.replace(m.X.End(), sel.End(), ".Driver()")
				r.count("Driver")
				return
			}
		}
	}
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
			r.replace(sel.X.End(), sel.End(), embeddedPath(selection))
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
			r.late(inner.X, stack, "the handle of Get")
			handle := r.render(inner.X) + embeddedPath(r.info.Selections[inner])
			pc, pid := fresh("ctx", handle, ctx), fresh("id", handle, ctx)
			r.replace(sel.Pos(), sel.End(), "func("+pc+" "+ctx+".Context, "+pid+" "+id+") (*"+r.entity(sel.Pos())+"."+t.ent.name+", error) {\n\treturn "+
				r.root(sel.Pos())+"."+t.ent.name+".Get("+pc+", "+handle+", "+pid+")\n}")
			r.count("Get as a value")
			return
		}
	}
	if t.where == "client/<entity>" && t.kind == "Client" && obj.Name() == "Query" {
		// c.User.Query handed to something that calls it: a function that
		// starts the query on the handle the client held.
		if inner, ok := sel.X.(*ast.SelectorExpr); ok {
			if fs := r.info.Selections[inner]; fs != nil && fs.Kind() == types.FieldVal && r.isRootClient(fs) {
				r.late(inner.X, stack, "the handle of Query")
				handle := r.render(inner.X) + embeddedPath(fs)
				r.replace(sel.Pos(), sel.End(), "func() *"+r.entity(sel.Pos())+"."+t.ent.name+"Query { return "+
					r.root(sel.Pos())+"."+t.ent.name+".Query("+handle+") }")
				r.count("Query as a value")
				return
			}
		}
	}
	if t.where == "filter" && obj.Name() == "Filter" {
		if c, ok := parent(stack, 1).(*ast.CallExpr); ok {
			if fn, ok := c.Fun.(*ast.SelectorExpr); ok && strings.HasPrefix(fn.Sel.Name, "With") && strings.HasSuffix(fn.Sel.Name, "Filter") {
				return // an argument of With<Entity>Filter: Paginate handles it
			}
		}
		// Anywhere else it was a function that returned the predicate and
		// an error, and what takes it still expects that.
		if t.ent != nil && selection.Kind() != types.MethodExpr {
			r.late(sel.X, stack, "the input of Filter")
			r.replace(sel.Pos(), sel.End(), "func() ("+r.predicate(sel.Pos())+"."+t.ent.name+", error) { return "+r.render(sel.X)+".P(), nil }")
			r.count("Filter as a value")
		}
		return
	}
	if t.where == "filter" && obj.Name() == "P" && t.ent != nil {
		// P as a value, for something that calls it: it returned an error
		// too, and what calls it still expects one.
		pred := r.predicate(sel.Pos()) + "." + t.ent.name
		if selection.Kind() == types.MethodExpr {
			input := "*" + r.ref(r.s.to+"/filter", "filter", sel.Pos()) + "." + t.ent.name + "WhereInput"
			r.replace(sel.Pos(), sel.End(), "func(w "+input+") ("+pred+", error) { return w.P(), nil }")
		} else {
			r.late(sel.X, stack, "the input of P")
			r.replace(sel.Pos(), sel.End(), "func() ("+pred+", error) { return "+r.render(sel.X)+".P(), nil }")
		}
		r.count("P as a value")
		return
	}
	if t.where == "client/<entity>" && (t.kind == "Create" || t.kind == "Update" || t.kind == "UpdateOne") && r.setterValue(sel, t, obj, stack) {
		return
	}
	if t.where == "root" || t.where == "client/<entity>" || t.where == "entity" || t.where == "query" {
		r.note(sel.Pos(), "the method %s is used as a value: rewrite by hand", obj.Name())
	}
}

// call handles a call of something of the old API.
func (r *rewriter) call(call *ast.CallExpr, stack []ast.Node) {
	r.dispatch(call, stack)
	if !r.wrapped[call] {
		return
	}
	// defer q.ExecX(ctx) ran ExecX when the function returned. As the
	// argument of velox.Must it would run where the defer statement is.
	switch p := parent(stack, 1).(type) {
	case *ast.DeferStmt:
		if p.Call != call {
			return
		}
	case *ast.GoStmt:
		if p.Call != call {
			return
		}
	default:
		return
	}
	r.open(call.Pos(), "func() { ")
	r.insert(call.End(), " }()")
	r.note(call.Pos(), "a deferred (or go) call is inside a function now, so that it still runs later: its arguments are evaluated then too, not at the statement")
	r.count("deferred call")
}

func (r *rewriter) dispatch(call *ast.CallExpr, stack []ast.Node) {
	if r.combinator(call) {
		return
	}
	if r.nilAsserted(call) {
		return
	}
	if tv, ok := r.info.Types[call.Fun]; ok && !tv.IsType() && r.predicateOf(tv.Type) != "" {
		r.note(call.Pos(), "a predicate is called as a function on a selector: it is data now (a function on a selector is made one with orm.Raw)")
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	selection := r.info.Selections[sel]
	if selection == nil {
		r.ownInterfaceCall(sel, r.info.Uses[sel.Sel])
		r.function(call, sel, stack)
		return
	}
	obj := selection.Obj()
	r.ownInterfaceCall(sel, obj)
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
			r.open(call.Pos(), r.app("Must", call.Pos())+"(")
			r.insert(call.End(), ")")
			r.wrapped[call] = true
			r.count(name)
		}
	case t.where == "root" && (t.kind == "Client" || t.kind == "Tx"):
		// A method promoted from an embedded client: the field is named now.
		if via := embeddedPath(selection); via != "" && name != "Client" {
			r.insert(sel.X.End(), via)
		}
		switch name {
		case "Tx":
			r.replace(sel.Sel.Pos(), sel.Sel.End(), "Begin")
			r.count("Begin")
		case "Commit", "Rollback", "Close", "Use", "Intercept":
		case "RuntimeConfig":
			// x.RuntimeConfig().Driver is x.Driver(): see member. Anything
			// else done with the configuration is for a person.
			if m, _ := receiverOf(call, stack); m != "Driver" {
				r.note(call.Pos(), "%s.RuntimeConfig: there is no runtime configuration to pass on; what took it takes the handle (velox.Executor)", t.kind)
			}
		case "Client":
			r.replace(call.Pos(), call.End(), r.render(sel.X))
			r.count("tx.Client()")
			if m, _ := receiverOf(call, stack); m == "" && !r.toHandle(call, stack) {
				// Handed on rather than queried on at once.
				r.note(call.Pos(), "tx.Client() is the transaction itself now: what it is given to must take velox.Executor (a transaction is not a *velox.DB)")
			}
		default:
			r.note(call.Pos(), "%s.%s: rewrite by hand", t.kind, name)
		}
	case t.where == "entity" && t.kind == "Entity":
		switch {
		case name == "Unwrap":
			text := r.render(sel.X)
			switch parent(stack, 1).(type) {
			case *ast.ExprStmt:
				text = "_ = " + text // a statement of its own: nothing is left to do
			case *ast.DeferStmt, *ast.GoStmt:
				text = "func() {}()" // nothing is left to do later either
			}
			r.replace(call.Pos(), call.End(), text)
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
				r.open(call.Lparen+1, r.app("ExecutorFrom", call.Pos())+"("+r.contextAt(call.Pos())+")")
				r.note(call.Pos(), "%s is given the handle of the context (velox.ExecutorFrom): check that every caller's context carries one (velox.WithExecutor), or pass the handle", name)
				r.count("edge query on the context's handle")
			default:
				r.note(call.Pos(), "%s takes the handle to query on now: e.%s(handle)", name, name)
			}
		case t.ent.edges[name]:
			// The method a GraphQL engine resolves the edge with.
			r.note(call.Pos(), "%s.%s reads the edge on the handle of the context now: the context must carry one (velox.WithExecutor), or it returns velox.ErrNoExecutor", t.ent.name, name)
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
				star := ""
				if _, isPtr := types.Unalias(r.info.TypeOf(sel.X)).(*types.Pointer); isPtr {
					star = "*" // the method was called through the pointer
				}
				r.replace(call.Pos(), call.End(), r.ref("slices", "slices", call.Pos())+".Contains("+r.leaf(t.ent, call.Pos())+"."+recv+"Values(), "+star+r.render(sel.X)+")")
				r.count("enum IsValid")
			} else {
				r.note(call.Pos(), "IsValid of %s: rewrite by hand", recv)
			}
		}
	case t.where == "migrate" && name == "Create" && len(call.Args) >= 1:
		// client.Schema.Create(ctx, opts...): the tables are made by the
		// generated migrate package, on a handle.
		holder, ok := sel.X.(*ast.SelectorExpr)
		if !ok || holder.Sel.Name != "Schema" {
			break
		}
		args := []string{r.render(call.Args[0]), r.render(holder.X) + embeddedPath(r.info.Selections[holder])}
		for i, a := range call.Args[1:] {
			text := r.render(a)
			if call.Ellipsis.IsValid() && i == len(call.Args)-2 {
				text += "..."
			}
			args = append(args, text)
		}
		r.replace(call.Pos(), call.End(), r.ref(r.s.to+"/migrate", "migrate", call.Pos())+".Create("+strings.Join(args, ", ")+")")
		r.count("migrate.Create")
	case t.where == "filter" && name == "P":
		r.whereInput(call, stack)
	case t.where == "filter" && name == "Filter":
		// w.Filter() returned what w.P() did.
		r.replace(sel.Sel.Pos(), sel.Sel.End(), "P")
		r.whereInput(call, stack)
	case t.where == "filter" && name == "AddPredicates":
		if _, alone := parent(stack, 1).(*ast.ExprStmt); !alone {
			r.note(call.Pos(), "AddPredicates: the conditions the application adds are the field Predicates of the input")
			break
		}
		var args []string
		for i, a := range call.Args {
			text := r.render(a)
			if call.Ellipsis.IsValid() && i == len(call.Args)-1 {
				text += "..."
			}
			args = append(args, text)
		}
		recv := r.render(sel.X)
		r.replace(call.Pos(), call.End(), recv+".Predicates = append("+recv+".Predicates, "+strings.Join(args, ", ")+")")
		r.count("Predicates")
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
	if r.failure(sel, obj) || r.queryContext(sel, obj) {
		return
	}
	name := obj.Name()
	switch t.where {
	case "intercept":
		r.intercept(sel, name)
	case "client/<entity>", "query":
		if _, isType := obj.(*types.TypeName); !isType {
			r.note(call.Pos(), "%s of a package the new tree does not have: an entity's client is <root>.<Entity>, and takes the handle with each call", name)
		}
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
		case "Or", "Not":
			r.emptyList(call, name)
			return
		case "And":
			return
		}
		if f, ok := strings.CutSuffix(name, "Values"); ok && e.fields[f] {
			return
		}
		if f, ok := strings.CutSuffix(name, "Validator"); ok && e.fields[f] && len(call.Args) == 1 {
			// The validator of an enum field said whether a value is one
			// of the field's: <F>Validator(v) != nil is "it is not".
			if bin, ok := parent(stack, 1).(*ast.BinaryExpr); ok && (bin.Op == token.EQL || bin.Op == token.NEQ) {
				other := bin.Y
				if ast.Expr(call) == bin.Y {
					other = bin.X
				}
				if id, isNil := other.(*ast.Ident); isNil && id.Name == "nil" {
					text := r.ref("slices", "slices", call.Pos()) + ".Contains(" + r.render(sel.X) + "." + f + "Values(), " + r.render(call.Args[0]) + ")"
					if bin.Op == token.NEQ {
						text = "!" + text
					}
					r.replace(bin.Pos(), bin.End(), text)
					r.count("enum validator")
					return
				}
			}
			r.note(call.Pos(), "%s: whether a value is one of the field's is slices.Contains(%s.%sValues(), v)", name, strings.ToLower(e.name), f)
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
		case "Asc", "Desc":
			if r.inOrder(stack) {
				break
			}
			// As a value it was a function on a selector, and stays one:
			// what takes it (a variable, a list, Order) is unchanged, and
			// Order makes an order term of it.
			sqlPkg := r.ref(r.lib+"/dialect/sql", "sql", call.Pos())
			var cols []string
			for _, a := range call.Args {
				cols = append(cols, r.render(a))
			}
			sv := fresh("s", cols...)
			for i, c := range cols {
				cols[i] = sqlPkg + "." + name + "(" + sv + ".C(" + c + "))"
			}
			r.replace(call.Pos(), call.End(), "func("+sv+" *"+sqlPkg+".Selector) { "+sv+".OrderBy("+strings.Join(cols, ", ")+") }")
			r.count("order function")
		case "NewContext", "NewTxContext":
			r.replace(sel.Pos(), sel.End(), r.app("WithExecutor", sel.Pos()))
			r.count("WithExecutor")
		case "Open":
			if len(call.Args) == 2 {
				r.replace(sel.Pos(), sel.End(), r.app("Open", sel.Pos()))
				r.count("Open")
			} else {
				r.note(call.Pos(), "Open with options: velox.Open(dialect, dsn) takes none; hooks and interceptors are registered on the handle (Use, Intercept)")
			}
		case "FromContext":
			r.replace(sel.Pos(), sel.End(), r.app("ExecutorFrom", sel.Pos()))
			r.note(call.Pos(), "FromContext is velox.ExecutorFrom: the handle the context carries, a transaction too (velox kept a client and a transaction under separate keys), as a velox.Executor")
			r.count("ExecutorFrom")
		case "TxFromContext":
			r.replace(sel.Pos(), sel.End(), r.app("TxFrom", sel.Pos()))
			r.count("TxFrom")
		case "As":
			// As(aggregate, name) is the aggregate's method:
			// orm.Count().As("n").
			if len(call.Args) != 2 {
				r.note(call.Pos(), "As of an aggregate: it is the aggregate's method now, orm.Count().As(name)")
				return
			}
			r.replace(call.Pos(), call.Args[0].Pos(), "")
			r.replace(call.Args[0].End(), call.Args[1].Pos(), ".As(")
			r.count("aggregate")
		case "Sum", "Count", "Max", "Min", "Mean":
			// The aggregates of a grouped read; the read around them is
			// rewritten by hand (see the note on GroupBy and Aggregate).
			r.replace(sel.Pos(), sel.End(), r.engine(name, sel.Pos()))
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
					if _, _, inline := r.inlineOption(call, false); inline {
						return // Paginate puts it on the query
					}
				}
			}
			r.option(call, name, obj, stack)
		}
	default:
		r.foreign(sel, obj, t.where)
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
			ex, via = holder.X, embeddedPath(fieldSel)
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
			r.idOf(call.Args[0])
			prepend("UpdateByID")
		}
	case "Get", "GetX":
		if len(call.Args) != 2 {
			return
		}
		text := start + "Get(" + r.render(call.Args[0]) + ", " + r.render(ex) + via + ", " + r.render(call.Args[1]) + ")"
		if name == "GetX" {
			text = r.app("Must", call.Pos()) + "(" + text + ")"
			r.wrapped[call] = true
		}
		r.replace(call.Pos(), call.End(), text)
		r.count(name)
	case "DeleteOneID", "DeleteOne":
		if m, outer := receiverOf(call, stack); outer != nil && (m == "Exec" || m == "ExecX") {
			return // the Exec that follows rewrites the pair
		}
		r.note(call.Pos(), "%s that is not run at once: it is DeleteByID(handle, id), with Where for a guard, ended by ExecOne(ctx)", name)
	default:
		// c.Order.QueryItems(o): the items of o, queried on the handle the
		// client held.
		if edge, ok := strings.CutPrefix(name, "Query"); ok && t.ent.edges[edge] && len(call.Args) == 1 {
			text := r.operand(call.Args[0]) + "." + name + "(" + r.render(ex) + via + ")"
			r.replace(call.Pos(), call.End(), text)
			r.count("edge query from a client")
			break
		}
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
func (r *rewriter) setterValue(sel *ast.SelectorExpr, t target, obj types.Object, stack []ast.Node) bool {
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
	builder := "*" + r.entity(sel.Pos()) + "." + t.ent.name + kind
	column := r.leaf(t.ent, sel.Pos()) + "." + mid
	methodExpr := false
	if s := r.info.Selections[sel]; s != nil && s.Kind() == types.MethodExpr {
		methodExpr = true
	}
	recv := ""
	if !methodExpr {
		// b.SetName as a value held b; the function reads it when called.
		r.late(sel.X, stack, "the builder of "+obj.Name())
		recv = r.render(sel.X)
	}
	// Names for the parameters that nothing written inside the function uses.
	v := fresh("v", recv, column, builder)
	params, arg := "", ""
	if sig.Params().Len() == 1 {
		pt := sig.Params().At(0).Type()
		arg = v
		slice, isSlice := pt.(*types.Slice)
		switch {
		case sig.Variadic() && isSlice:
			params = v + " ..." + r.typeText(slice.Elem(), sel.Pos())
			arg = v + "..."
		case viaID:
			// SetOwner took the entity; the edge takes its id.
			params = v + " " + r.typeText(pt, sel.Pos())
			arg = v + ".ID"
		default:
			params = v + " " + r.typeText(pt, sel.Pos())
		}
	}
	if methodExpr {
		// (*UserUpdateOne).SetName: the builder is the first argument.
		recv = fresh("b", column, builder, params)
		if params != "" {
			params = ", " + params
		}
		params = recv + " " + builder + params
	}
	r.replace(sel.Pos(), sel.End(), "func("+params+") "+builder+" {\n\treturn "+
		recv+".Set("+column+"."+method+"("+arg+"))\n}")
	r.count("setter as a value")
	return true
}

// fresh returns name, with underscores added until none of the texts that
// will be written inside the function it is a parameter of uses it: a
// builder held in a variable v must not become the parameter v.
func fresh(name string, texts ...string) string {
	for {
		used := false
		for _, t := range texts {
			used = used || hasWord(t, name)
		}
		if !used {
			return name
		}
		name += "_"
	}
}

// hasWord reports whether text has word as a whole identifier.
func hasWord(text, word string) bool {
	part := func(c byte) bool {
		return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
	}
	for i := 0; ; {
		j := strings.Index(text[i:], word)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(word)
		if (start == 0 || !part(text[start-1])) && (end == len(text) || !part(text[end])) {
			return true
		}
		i = start + 1
	}
}

// late reports an expression that a method value held when it was made and
// that the function in its place reads when it is called, unless it is
// plainly the same then: a variable of the function given its value once,
// or fields selected from one.
func (r *rewriter) late(e ast.Expr, stack []ast.Node, what string) {
	plain := true
	for x := e; plain; {
		switch y := x.(type) {
		case *ast.ParenExpr:
			x = y.X
			continue
		case *ast.SelectorExpr:
			x = y.X
			continue
		case *ast.Ident:
		default:
			plain = false
		}
		break
	}
	if plain && r.unchanged(e, stack) {
		return
	}
	// set(v, b.SetName, b.ClearName): handed to a call as it is made, from
	// a variable. It is read when the call uses it, a statement at most
	// away from where it was; only a function that keeps it for later
	// could see another value, and that is not what such helpers do.
	if c, ok := parent(stack, 1).(*ast.CallExpr); ok && plain {
		for _, a := range c.Args {
			if sel, ok := a.(*ast.SelectorExpr); ok && sel.X == e {
				return
			}
		}
	}
	r.note(e.Pos(), "%s was taken when the method value was made; the function in its place evaluates it at each call: take it into a variable first if it can differ by then", what)
}

// writes counts the places in the function around that give the variable a
// value, and returns what the last of them gives it. A variable whose
// address is taken, and an entity Unwrap is called on, count as given a
// value more than once.
func (r *rewriter) writes(obj types.Object, stack []ast.Node) (n int, from ast.Expr) {
	n, from, _ = r.writesThrough(obj, stack)
	return n, from
}

// writesThrough is writes, with the number of assignments to something
// reached through the variable (an element of it, a field) apart: those
// change what its elements are, not what it is.
func (r *rewriter) writesThrough(obj types.Object, stack []ast.Node) (n int, from ast.Expr, through int) {
	var body *ast.BlockStmt
	for _, node := range stack {
		switch fn := node.(type) {
		case *ast.FuncDecl:
			body = fn.Body
		case *ast.FuncLit:
			body = fn.Body
		}
		if body != nil {
			break
		}
	}
	if body == nil {
		return 2, nil, 0
	}
	is := func(e ast.Expr) bool {
		id, ok := e.(*ast.Ident)
		return ok && (r.info.Defs[id] == obj || r.info.Uses[id] == obj)
	}
	// reached reports whether e is reached through the variable: u.Edges.X,
	// us[0], *u.
	reached := func(e ast.Expr) bool {
		for depth := 0; ; depth++ {
			switch x := e.(type) {
			case *ast.ParenExpr:
				e = x.X
			case *ast.SelectorExpr:
				e = x.X
			case *ast.IndexExpr:
				e = x.X
			case *ast.StarExpr:
				e = x.X
			case *ast.SliceExpr:
				e = x.X
			default:
				return depth > 0 && is(e)
			}
		}
	}
	// A parameter has the value its caller gave it: that is one.
	for _, node := range stack {
		var ft *ast.FuncType
		var recv *ast.FieldList
		switch fn := node.(type) {
		case *ast.FuncDecl:
			ft, recv = fn.Type, fn.Recv
		case *ast.FuncLit:
			ft = fn.Type
		default:
			continue
		}
		for _, list := range []*ast.FieldList{recv, ft.Params, ft.Results} {
			if list == nil {
				continue
			}
			for _, f := range list.List {
				for _, name := range f.Names {
					if r.info.Defs[name] == obj {
						n++
					}
				}
			}
		}
	}
	ast.Inspect(body, func(node ast.Node) bool {
		switch st := node.(type) {
		case *ast.CallExpr:
			// An entity's Unwrap changes the entity it is called on.
			if m, ok := st.Fun.(*ast.SelectorExpr); ok && m.Sel.Name == "Unwrap" && (is(m.X) || reached(m.X)) {
				n += 2
			}
		case *ast.AssignStmt:
			for j, lhs := range st.Lhs {
				if reached(lhs) {
					through++ // an element or a field of it
					continue
				}
				if !is(lhs) {
					continue
				}
				n++
				switch {
				case len(st.Rhs) == len(st.Lhs):
					from = st.Rhs[j]
				case len(st.Rhs) == 1 && j == 0:
					from = st.Rhs[0] // v, err := call
				default:
					from = nil
				}
			}
		case *ast.RangeStmt:
			if st.Key != nil && is(st.Key) {
				n, from = n+1, nil
			}
			if st.Value != nil && is(st.Value) {
				n, from = n+1, st.X
			}
		case *ast.ValueSpec:
			for j, id := range st.Names {
				if r.info.Defs[id] != obj || len(st.Values) == 0 {
					continue
				}
				n++
				switch {
				case len(st.Values) == len(st.Names):
					from = st.Values[j]
				case j == 0:
					from = st.Values[0]
				default:
					from = nil
				}
			}
		case *ast.IncDecStmt:
			if is(st.X) {
				n++
			}
		case *ast.UnaryExpr:
			if st.Op == token.AND && is(st.X) {
				n += 2
			}
		}
		return true
	})
	return n, from, through
}

// unchanged reports whether the variable e starts with is given a value
// once in the function around: a parameter by its caller, a local where it
// is declared or first assigned. What
// is not a variable of the function (a field of the receiver, a variable of
// the package) is taken to hold what it held.
func (r *rewriter) unchanged(e ast.Expr, stack []ast.Node) bool {
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
			continue
		case *ast.SelectorExpr:
			e = x.X
			continue
		case *ast.Ident:
			v, ok := r.info.Uses[x].(*types.Var)
			if !ok {
				_, isPkg := r.info.Uses[x].(*types.PkgName)
				return isPkg
			}
			if v.IsField() || v.Parent() == nil || v.Parent() == r.pkg.Scope() || v.Pkg() != r.pkg {
				return true
			}
			n, _ := r.writes(v, stack)
			return n <= 1
		}
		return false
	}
}

// handleOf finds what an entity was read on, by looking at where the
// variable that holds it got its value in the functions around: a query or
// a mutation that starts at a client names the handle. It answers only when
// the text settles that this handle is the one, at the place it is needed.
func (r *rewriter) handleOf(e ast.Expr, at token.Pos, stack []ast.Node, depth int) (string, bool) {
	return (&handleTrace{r: r, at: at, stack: stack}).of(e, false, depth)
}

// handleTrace follows an entity back to the query that read it.
type handleTrace struct {
	r     *rewriter
	at    token.Pos
	stack []ast.Node
	// scopes are where the variables on the way are declared: the handle's
	// variable must be declared around all of them. A handle declared
	// further in (the variable of a loop, the parameter of a function
	// literal) is another handle each time round, and the entity kept
	// outside is not read on the one of this round.
	scopes []*types.Scope
}

func (h *handleTrace) of(e ast.Expr, via bool, depth int) (string, bool) {
	r := h.r
	if depth > 4 {
		return "", false
	}
	switch e := e.(type) {
	case *ast.ParenExpr:
		return h.of(e.X, via, depth)
	case *ast.CallExpr:
		// A chain: walk to where it starts.
		sel, ok := e.Fun.(*ast.SelectorExpr)
		if !ok {
			return "", false
		}
		if selection := r.info.Selections[sel]; selection != nil {
			if t, ok := r.targetOf(selection.Obj(), declaring(selection)); ok && t.where == "client/<entity>" && t.kind == "Client" {
				ex, through := sel.X, ""
				if holder, ok := sel.X.(*ast.SelectorExpr); ok {
					if fs := r.info.Selections[holder]; fs != nil && fs.Kind() == types.FieldVal && r.isRootClient(fs) {
						ex, through = holder.X, embeddedPath(fs)
					}
				}
				if !r.sameAt(ex, h.at) || !r.unchanged(ex, h.stack) || !h.around(ex) {
					return "", false
				}
				return string(r.src[r.off(ex.Pos()):r.off(ex.End())]) + through, true
			}
		}
		if sel.Sel.Name == "Unwrap" {
			// Read in a transaction and taken out of it: what its edges
			// are read on is the client behind, which is not written here.
			return "", false
		}
		return h.of(sel.X, via, depth)
	case *ast.SelectorExpr:
		// parent.Edges.Items: read with its parent.
		return h.of(e.X, true, depth)
	case *ast.IndexExpr:
		return h.of(e.X, true, depth)
	case *ast.Ident:
		// A variable of the function that is given its value in one
		// place: with two (a reassignment, the branches of an if) which of
		// them reached here is not something the text says.
		obj, ok := r.info.Uses[e].(*types.Var)
		if !ok || obj.IsField() || obj.Parent() == r.pkg.Scope() {
			return "", false
		}
		// ... and that place is before this one: an assignment further
		// down reaches here only around a loop, after the value that was
		// used the first time. When what is queried is an element or a
		// field of the variable (us[0], u.Edges.Posts[0]), nothing is
		// written through it either.
		n, from, through := r.writesThrough(obj, h.stack)
		if n != 1 || from == nil || from.End() > h.at || (via && through > 0) {
			return "", false
		}
		h.scopes = append(h.scopes, obj.Parent())
		return h.of(from, false, depth+1)
	}
	return "", false
}

// around reports whether the variable the handle starts with is declared
// around every variable on the way from the entity. What is not a variable
// of the function (a field of the receiver, a variable of the package) is.
func (h *handleTrace) around(ex ast.Expr) bool {
	for {
		switch x := ex.(type) {
		case *ast.ParenExpr:
			ex = x.X
			continue
		case *ast.SelectorExpr:
			ex = x.X
			continue
		case *ast.Ident:
			v, ok := h.r.info.Uses[x].(*types.Var)
			if !ok || v.IsField() || v.Parent() == nil || v.Parent() == h.r.pkg.Scope() || v.Pkg() != h.r.pkg {
				return true
			}
			for _, inner := range h.scopes {
				enclosed := false
				for sc := inner; sc != nil; sc = sc.Parent() {
					if sc == v.Parent() {
						enclosed = true
						break
					}
				}
				if !enclosed {
					return false
				}
			}
			return true
		}
		return false
	}
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
		r.open(call.Pos(), r.app(fn, call.Pos())+"(")
		r.insert(call.End(), ")")
		r.wrapped[call] = true
	}
	rename := func(to string) { r.replace(sel.Sel.Pos(), sel.Sel.End(), to) }

	if pre, mid, suf, field, edge := split(e, name, setterPrefixes, setterSuffixes); field || edge {
		method, viaID, ok := setterOf(pre, suf, field, edge)
		if !ok || (viaID && len(call.Args) != 1) {
			r.note(call.Pos(), "%s.%s: rewrite by hand", t.kind, name)
			return
		}
		if viaID {
			r.idOf(call.Args[0])
		}
		assign(mid, method)
		return
	}
	switch name {
	case "Where":
		// Its arguments are looked at as any call's are: see arguments.
	case "BatchSize":
		// The same call, with the same meaning.
		if t.kind != "CreateBulk" {
			r.note(call.Pos(), "%s.%s: rewrite by hand", t.kind, name)
		}
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
			// It reported a missing row and returned no count: ExecOne.
			rename("ExecOne")
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

// idOf makes an expression that is an entity the entity's id: u is u.ID, &u
// is u.ID too, and anything that does not end in a name is parenthesized.
func (r *rewriter) idOf(arg ast.Expr) {
	switch a := arg.(type) {
	case *ast.UnaryExpr:
		if a.Op == token.AND {
			r.replace(a.Pos(), a.X.Pos(), "")
			r.insert(a.X.End(), ".ID")
			return
		}
	case *ast.Ident, *ast.SelectorExpr, *ast.CallExpr, *ast.IndexExpr, *ast.ParenExpr:
		r.insert(arg.End(), ".ID")
		return
	}
	r.open(arg.Pos(), "(")
	r.insert(arg.End(), ").ID")
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
		r.note(call.Pos(), "a delete of one row built in steps: it is DeleteByID(handle, id), with Where for a guard, ended by ExecOne(ctx)")
		return
	}
	innerSel, ok := inner.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	ex, via := innerSel.X, ""
	if holder, ok := innerSel.X.(*ast.SelectorExpr); ok {
		if fs := r.info.Selections[holder]; fs != nil && fs.Kind() == types.FieldVal && r.isRootClient(fs) {
			ex, via = holder.X, embeddedPath(fs)
		}
	}
	if innerSel.Sel.Name == "DeleteOne" {
		r.idOf(inner.Args[0])
	}
	id := r.render(inner.Args[0])
	text := r.root(call.Pos()) + "." + t.ent.name + ".DeleteByID(" + r.render(ex) + via + ", " + id + ").ExecOne(" + r.render(call.Args[0]) + ")"
	if name == "ExecX" {
		text = r.app("Check", call.Pos()) + "(" + text + ")"
		r.wrapped[call] = true
	}
	r.replace(call.Pos(), call.End(), text)
	r.count("DeleteByID")
}

// query handles a method of a query.
func (r *rewriter) query(call *ast.CallExpr, sel *ast.SelectorExpr, t target, name string, stack []ast.Node) {
	e := t.ent
	rename := func(to string) { r.replace(sel.Sel.Pos(), sel.Sel.End(), to) }
	must := func() {
		r.open(call.Pos(), r.app("Must", call.Pos())+"(")
		r.insert(call.End(), ")")
		r.wrapped[call] = true
	}
	switch name {
	case "ForUpdate", "ForShare":
		// sql.WithLockAction(sql.SkipLocked) is orm.SkipLocked.
		for _, a := range call.Args {
			opt, ok := a.(*ast.CallExpr)
			if ok {
				if fn, isSel := opt.Fun.(*ast.SelectorExpr); isSel && fn.Sel.Name == "WithLockAction" && len(opt.Args) == 1 {
					if v, isSel := opt.Args[0].(*ast.SelectorExpr); isSel && (v.Sel.Name == "SkipLocked" || v.Sel.Name == "NoWait") {
						r.replace(a.Pos(), a.End(), r.engine(v.Sel.Name, a.Pos()))
						r.count("lock option")
						continue
					}
				}
			}
			r.note(a.Pos(), "a lock option: %s takes orm.SkipLocked or orm.NoWait", name)
		}
	case "Where", "All", "Only", "First", "Count", "IDs", "Limit", "Offset", "Clone":
	case "AllX", "OnlyX", "FirstX", "CountX", "IDsX":
		rename(strings.TrimSuffix(name, "X"))
		must()
		r.count(name)
	case "FirstID", "FirstIDX":
		// The id of the first row: the first of the ids of a query of one.
		rename("Limit(1).IDs")
		r.open(call.Pos(), r.app("First", call.Pos())+"(")
		r.insert(call.End(), ")")
		r.wrapped[call] = true
		if name == "FirstIDX" {
			must()
		}
		r.count("FirstID")
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
		case "":
			// q.Select(...) on its own, as in the option of an eager load:
			// it narrowed the query it was called on.
			if _, alone := parent(stack, 1).(*ast.ExprStmt); alone {
				r.replace(sel.Sel.Pos(), sel.Sel.End(), "Columns")
				r.count("Columns")
				break
			}
			r.note(call.Pos(), "Select whose result is kept: read the columns with Select(...).Scan(ctx, &rows), or entities with some columns with Columns(...)")
		default:
			r.note(call.Pos(), "Select followed by %s: read the columns with Select(...).Scan(ctx, &rows)", next)
		}
	default:
		if edge, ok := strings.CutPrefix(name, "With"); ok && e.edges[edge] {
			// An eager load is the query's With, given the edge of the
			// entity's package: WithPosts() is With(user.Posts), and what
			// described the load goes to the edge's Load.
			desc := r.leaf(e, call.Pos()) + "." + edge
			r.count("With")
			if len(call.Args) == 0 {
				r.replace(sel.Sel.Pos(), call.End(), "With("+desc+")")
				return
			}
			if !r.loadOption(call) && !r.applyOption(call) {
				r.note(call.Pos(), "%s with options: the option is a query of the neighbors now, With(%s.Load(<root>.<Target>.Query(nil).Where(...)))", name, desc)
			}
			r.replace(sel.Sel.Pos(), call.Lparen+1, "With("+desc+".Load(")
			r.insert(call.Rparen, ")")
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
// to the query itself, <root>.Item.Query(nil).Where(...).Limit(3): what the
// edge's Load takes.
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
	for e := stmt.X; e != nil; {
		c, ok := e.(*ast.CallExpr)
		if !ok {
			break
		}
		m, ok := c.Fun.(*ast.SelectorExpr)
		if !ok {
			break
		}
		if m.Sel.Name == "Limit" || m.Sel.Name == "Offset" {
			r.note(c.Pos(), "%s in the option of an eager load was over all the neighbors read; on the query given to the load it is for each parent", m.Sel.Name)
		}
		e = m.X
	}
	r.replace(first.Pos(), first.End(), r.root(call.Pos())+"."+target.name+".Query(nil)")
	text := r.render(stmt.X)
	r.replace(fn.Pos(), fn.End(), text)
	r.count("load option")
	return true
}

// applyOption rewrites the options of an eager load that are functions of
// more than one statement, or kept in variables, to the query they shape:
//
//	WithItems(opt)    orm.Apply(<root>.Item.Query(nil), opt)
func (r *rewriter) applyOption(call *ast.CallExpr) bool {
	var target *entityInfo
	for _, a := range call.Args {
		sig, ok := types.Unalias(r.info.TypeOf(a)).Underlying().(*types.Signature)
		if !ok || sig.Params().Len() != 1 || sig.Results().Len() != 0 {
			return false
		}
		ent, kind := r.s.kindOf(typeName(sig.Params().At(0).Type()))
		if ent == nil || (kind != "Query" && kind != "Querier") || (target != nil && target != ent) {
			return false
		}
		target = ent
	}
	if target == nil || call.Ellipsis.IsValid() {
		return false
	}
	r.open(call.Args[0].Pos(), r.engine("Apply", call.Pos())+"("+r.root(call.Pos())+"."+target.name+".Query(nil), ")
	r.insert(call.Args[len(call.Args)-1].End(), ")")
	r.count("load option")
	return true
}

// order rewrites Order(pkg.ByX(sql.OrderDesc()), velox.Asc(pkg.FieldY)) to
// OrderBy(pkg.X.Desc(), pkg.Y.Asc()).
func (r *rewriter) order(call *ast.CallExpr, sel *ast.SelectorExpr, e *entityInfo) {
	term := r.predicate(call.Pos()) + "." + e.name + "OrderTerm"
	// raw makes an order term of a function on a selector.
	raw := func(text string) string { return term + "(" + r.orm(call.Pos()) + ".RawOrder(" + text + "))" }
	var terms []string
	for i, a := range call.Args {
		if call.Ellipsis.IsValid() && i == len(call.Args)-1 {
			// Order(fs...): the functions of a list, one after the other.
			if len(call.Args) != 1 {
				r.note(call.Pos(), "Order of a list after other terms: rewrite by hand")
				return
			}
			list := r.render(a)
			sqlPkg := r.ref(r.lib+"/dialect/sql", "sql", call.Pos())
			sv, fv := fresh("s", list), fresh("f", list)
			terms = append(terms, raw("func("+sv+" *"+sqlPkg+".Selector) {\n\tfor _, "+fv+" := range "+list+" {\n\t\t"+fv+"("+sv+")\n\t}\n}"))
			continue
		}
		var fn *ast.SelectorExpr
		c, isCall := a.(*ast.CallExpr)
		if isCall {
			fn, _ = c.Fun.(*ast.SelectorExpr)
		}
		var t target
		known := false
		if fn != nil {
			t, known = r.targetOf(r.info.Uses[fn.Sel], "")
		}
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
					dir = "" // NULLS FIRST, a function of the column: the library's own term
				}
			}
			if dir == "" {
				sqlPkg := r.ref(r.lib+"/dialect/sql", "sql", call.Pos())
				opts := ""
				for _, opt := range c.Args {
					opts += ", " + r.render(opt)
				}
				terms = append(terms, raw(sqlPkg+".OrderByField("+r.render(fn.X)+".Column"+strings.TrimPrefix(fn.Sel.Name, "By")+opts+").ToFunc()"))
				continue
			}
			terms = append(terms, r.render(fn.X)+"."+strings.TrimPrefix(fn.Sel.Name, "By")+"."+dir+"()")
		case known && t.where == "root" && (fn.Sel.Name == "Asc" || fn.Sel.Name == "Desc"):
			for _, col := range c.Args {
				cs, ok := col.(*ast.SelectorExpr)
				f, isName := "", false
				if ok {
					f, isName = strings.CutPrefix(cs.Sel.Name, "Field")
				}
				if isName && e.fields[f] {
					terms = append(terms, r.render(cs.X)+"."+f+"."+fn.Sel.Name+"()")
					continue
				}
				// A column whose name arrives at run time: the term
				// written out. A name that is no column fails the query,
				// as it did.
				desc := ""
				if fn.Sel.Name == "Desc" {
					desc = ", Desc: true"
				}
				terms = append(terms, term+"{Col: "+r.render(col)+desc+"}")
			}
		default:
			// Anything else that is a function on a selector (NULLS LAST,
			// an expression, a function kept in a variable).
			if _, isFunc := types.Unalias(r.info.TypeOf(a)).Underlying().(*types.Signature); !isFunc {
				r.note(call.Pos(), "Order of something that is not a function on a selector: OrderBy takes %s.<Column>.Asc() or .Desc()", strings.ToLower(e.name))
				return
			}
			terms = append(terms, raw(r.render(a)))
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
	// Of several orders, and of several filters, velox used the last.
	var order, filter string
	orderAt, filterAt := -1, -1
	var applied []string
	inline, repeated := 0, false
	for i, opt := range call.Args[5:] {
		if c, ok := opt.(*ast.CallExpr); ok {
			if text, kind, ok := r.inlineOption(c, true); ok {
				inline++
				switch kind {
				case "order":
					// The last one is the one used, also when it is nil:
					// no order then, whatever came before.
					repeated = repeated || orderAt >= 0
					order, orderAt = text, i
				case "filter":
					repeated = repeated || filterAt >= 0
					filter, filterAt = text, i
				}
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
	if repeated {
		r.note(call.Pos(), "Paginate was given more than one order or more than one filter: the last of each was used, and is the one kept")
	}
	if len(applied) > 0 && len(applied)+inline > 1 {
		r.note(call.Pos(), "the options of Paginate held in variables are applied to the query one after the other now: of several orders or filters velox used the last, here each adds to the ones before")
	}
	// In the order they were written, for whoever reads the result.
	chain := order + filter
	if filterAt < orderAt {
		chain = filter + order
	}
	recv := r.render(sel.X) + chain
	if len(applied) > 0 {
		recv = r.engine("Apply", call.Pos()) + "(" + recv + ", " + strings.Join(applied, ", ") + ")"
	}
	r.replace(sel.X.Pos(), sel.X.End(), recv)
	r.replace(call.Args[4].End(), call.Rparen, "")
	r.count("Paginate")
}

// inlineOption says whether c is an option Paginate can put on the query
// itself, entity.WithUserOrder(o) or entity.WithUserFilter(w.Filter), and
// returns the calls that do (taking the arguments' text when take is set).
func (r *rewriter) inlineOption(c *ast.CallExpr, take bool) (text, kind string, ok bool) {
	fn, isSel := c.Fun.(*ast.SelectorExpr)
	if !isSel || len(c.Args) != 1 || !strings.HasPrefix(fn.Sel.Name, "With") {
		return "", "", false
	}
	t, known := r.targetOf(r.info.Uses[fn.Sel], "")
	if !known || t.where != "entity" {
		return "", "", false
	}
	sig, _ := r.info.TypeOf(c.Fun).(*types.Signature)
	switch {
	case strings.HasSuffix(fn.Sel.Name, "Order"):
		// The form that takes a list of orders returns an error too, and
		// is not an argument of Paginate.
		if sig == nil || sig.Results().Len() != 1 {
			return "", "", false
		}
		if r.info.Types[ast.Unparen(c.Args[0])].IsNil() {
			return "", "order", true // no order: the default
		}
		if !take {
			return "", "order", true
		}
		return ".OrderBy(" + r.operand(c.Args[0]) + ".Term())", "order", true
	case strings.HasSuffix(fn.Sel.Name, "Filter"):
		w, isMethod := c.Args[0].(*ast.SelectorExpr)
		if !isMethod || w.Sel.Name != "Filter" {
			return "", "", false
		}
		if !take {
			return "", "filter", true
		}
		return ".Where(" + r.operand(w.X) + ".P())", "filter", true
	}
	return "", "", false
}

// operand returns the text of e where a method is called on it: in
// parentheses unless it is a name, a selection, a call or an index, so that
// &entity.PostOrder{...} is (&entity.PostOrder{...}).Term().
func (r *rewriter) operand(e ast.Expr) string {
	text := r.render(e)
	switch e.(type) {
	case *ast.Ident, *ast.SelectorExpr, *ast.CallExpr, *ast.IndexExpr, *ast.ParenExpr:
		return text
	}
	return "(" + text + ")"
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
	// The argument is written inside the function: its parameter and its
	// variables take names the argument does not use.
	arg := r.operand(call.Args[0])
	if w, ok := call.Args[0].(*ast.SelectorExpr); ok && w.Sel.Name == "Filter" && !strings.HasSuffix(name, "Order") {
		arg = r.operand(w.X)
	}
	q, o, p, e := fresh("q", arg), fresh("o", arg), fresh("p", arg), fresh("err", arg)
	head := "func(" + q + " *" + r.entity(call.Pos()) + "." + ent.name + "Query) {"
	var text string
	switch {
	case strings.HasSuffix(name, "Order") && r.info.Types[ast.Unparen(call.Args[0])].IsNil():
		text = head + " }" // no order: the default
	case strings.HasSuffix(name, "Order"):
		if _, many := sig.Params().At(0).Type().(*types.Slice); many {
			text = head + "\n\tfor _, " + o + " := range " + arg + " {\n\t\t" + q + ".OrderBy(" + o + ".Term())\n\t}\n}"
		} else {
			text = head + " " + q + ".OrderBy(" + arg + ".Term()) }"
		}
	default:
		if w, ok := call.Args[0].(*ast.SelectorExpr); ok && w.Sel.Name == "Filter" {
			text = head + " " + q + ".Where(" + arg + ".P()) }"
			break
		}
		// A filter of the application's own: a function that returns the
		// predicate or an error, which fails the query as it did.
		text = head + "\n\t" + p + ", " + e + " := (" + arg + ")()\n\tif " + e + " != nil {\n\t\t" + p + " = " +
			r.predicate(call.Pos()) + "." + ent.name + "(" + r.orm(call.Pos()) + ".Invalid(" + e + "))\n\t}\n\t" + q + ".Where(" + p + ")\n}"
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

// repeated handles a name the generated root package repeated from the
// library (Mutation, OpCreate, Hook, MutateFunc ...), and reports whether it
// was one. The new root package gives most of them too, and the name stays
// as it is written; one it does not give takes the library's.
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
	r.replace(sel.Pos(), sel.End(), r.app(obj.Name(), sel.Pos()))
	r.count("library name")
	return true
}

// predicateText spells the predicate type t is, a named one
// (predicate.User) or a type parameter that stands for one, or returns "".
func (r *rewriter) predicateText(t types.Type, pos token.Pos) string {
	if t == nil {
		return ""
	}
	if name := r.predicateOf(t); name != "" {
		return r.predicate(pos) + "." + name
	}
	if tp, ok := types.Unalias(t).(*types.TypeParam); ok && r.predicateConstraint(tp.Constraint()) {
		return tp.Obj().Name()
	}
	return ""
}

// predicateConstraint reports whether a constraint admitted the predicate
// types when they were functions: sql.PredicateFunc, or ~func(*sql.Selector)
// written out.
func (r *rewriter) predicateConstraint(c types.Type) bool {
	text := types.TypeString(c, nil)
	return strings.Contains(text, r.lib+"/dialect/sql.PredicateFunc") || strings.Contains(text, "*"+r.lib+"/dialect/sql.Selector)")
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
	pred := r.predicateText(target, e.Pos())
	if pred == "" {
		// The other way: a predicate handed to what takes a function on a
		// selector (a predicate was one, so it went in as it was).
		if r.selectorFunc(target) {
			if tv, ok := r.info.Types[e]; ok && !tv.IsNil() && r.predicateOf(tv.Type) != "" {
				r.open(e.Pos(), r.engine("Compile", e.Pos())+"(")
				r.insert(e.End(), ")")
				r.count("Compile")
			}
		}
		return
	}
	tv, ok := r.info.Types[e]
	if !ok {
		return
	}
	if id, isIdent := e.(*ast.Ident); isIdent && id.Name == "nil" && tv.IsNil() {
		zero := pred + "{}"
		if _, generic := types.Unalias(target).(*types.TypeParam); generic {
			zero = "*new(" + pred + ")"
		}
		r.replace(e.Pos(), e.End(), zero)
		r.count("zero predicate")
		return
	}
	if _, isFunc := types.Unalias(tv.Type).(*types.Signature); !isFunc {
		return
	}
	if c, isCall := e.(*ast.CallExpr); isCall && r.made[c] {
		return
	}
	r.open(e.Pos(), pred+"("+r.orm(e.Pos())+".Raw(")
	r.insert(e.End(), "))")
	r.count("Raw predicate")
}

// selectorFunc reports whether t is a function on a selector that is not a
// predicate type: func(*sql.Selector), under whatever name.
func (r *rewriter) selectorFunc(t types.Type) bool {
	if r.predicateOf(t) != "" {
		return false
	}
	sig, ok := types.Unalias(t).Underlying().(*types.Signature)
	if !ok || sig.Params().Len() != 1 || sig.Results().Len() != 0 || sig.Variadic() {
		return false
	}
	ptr, ok := sig.Params().At(0).Type().(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := ptr.Elem().(*types.Named)
	return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == r.lib+"/dialect/sql" && named.Obj().Name() == "Selector"
}

// arguments looks at what a call passes where it takes predicates.
func (r *rewriter) arguments(call *ast.CallExpr) {
	tv, ok := r.info.Types[call.Fun]
	if !ok {
		return
	}
	if tv.IsType() {
		// A conversion, predicate.User(f).
		if len(call.Args) == 1 && r.predicateText(tv.Type, call.Pos()) != "" {
			if c, isCall := call.Args[0].(*ast.CallExpr); isCall && r.made[c] {
				return
			}
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

// combinator rewrites the library's predicate combinators, which took and
// returned functions on a selector: sql.AndPredicates[P](ps...) is
// orm.And(ps...), of the predicates' own type.
func (r *rewriter) combinator(call *ast.CallExpr) bool {
	fun := call.Fun
	switch f := fun.(type) {
	case *ast.IndexExpr:
		fun = f.X
	case *ast.IndexListExpr:
		fun = f.X
	}
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	obj := r.info.Uses[sel.Sel]
	if obj == nil || obj.Pkg() == nil || obj.Pkg().Path() != r.lib+"/dialect/sql" {
		return false
	}
	var to string
	switch obj.Name() {
	case "AndPredicates", "PredicateAnd":
		to = "And"
	case "OrPredicates", "PredicateOr":
		to = "Or"
	case "NotPredicates", "PredicateNot":
		to = "Not"
	default:
		return false
	}
	// Of functions that are not predicate types (the application's own
	// conditions on a selector) the library's combinator is still the one:
	// it takes and returns such functions, and orm.And takes predicates.
	if inst, ok := r.info.Instances[sel.Sel]; ok && inst.TypeArgs.Len() > 0 {
		arg := inst.TypeArgs.At(0)
		_, param := types.Unalias(arg).(*types.TypeParam)
		if r.predicateOf(arg) == "" && (!param || r.predicateText(arg, call.Pos()) == "") {
			return false
		}
	}
	if to != "And" {
		r.emptyList(call, to)
	}
	r.replace(call.Fun.Pos(), call.Fun.End(), r.engine(to, call.Pos()))
	// orm.Not takes one condition: what velox negated was the conjunction
	// of a list.
	if to == "Not" && (len(call.Args) != 1 || call.Ellipsis.IsValid()) {
		if len(call.Args) == 0 {
			r.note(call.Pos(), "Not of no predicate: orm.Not takes one condition; of none, velox's matched no row")
		} else {
			r.open(call.Args[0].Pos(), r.engine("And", call.Pos())+"(")
			r.insert(call.Rparen, ")")
		}
	}
	r.made[call] = true
	r.count("generic " + to)
	return true
}

// columnLevel returns the level of the column type of package orm that a
// column of velox's generic type t is: String, Bool, Enum, Ordered, or Value
// for a type whose values are only stored.
func columnLevel(t types.Type) string {
	name := ""
	if n, ok := types.Unalias(t).(*types.Named); ok {
		name = strings.TrimSuffix(n.Obj().Name(), "Field")
	}
	switch {
	case name == "String":
		return "String"
	case name == "Bool":
		return "Bool"
	case name == "Enum":
		return "Enum"
	case name == "Time" || name == "Other" || name == "UUID" || strings.HasPrefix(name, "Int") || strings.HasPrefix(name, "Uint") || strings.HasPrefix(name, "Float"):
		return "Ordered"
	}
	return "Value"
}

// columnType rewrites the library's generic column types where code generic
// over entities names them: sql.Int64Field[P] is *orm.OrderedColumn[int64, P].
func (r *rewriter) columnType(n, x ast.Expr, args []ast.Expr) {
	sel, ok := x.(*ast.SelectorExpr)
	if !ok {
		return
	}
	obj, isType := r.info.Uses[sel.Sel].(*types.TypeName)
	if !isType || obj.Pkg() == nil || obj.Pkg().Path() != r.lib+"/dialect/sql" || !strings.HasSuffix(obj.Name(), "Field") || len(args) == 0 {
		return
	}
	// The plain column types are forwarded by the generated root package.
	pkg := strings.TrimSuffix(r.engine("StringColumn", n.Pos()), ".StringColumn")
	p := r.render(args[0])
	// The second type argument, of the column types that have one.
	value := ""
	if len(args) == 2 {
		value = r.render(args[len(args)-1])
	}
	var text string
	switch base := strings.TrimSuffix(obj.Name(), "Field"); {
	case base == "String":
		text = "*" + pkg + ".StringColumn[string, " + p + "]"
	case base == "Bool":
		text = "*" + pkg + ".BoolColumn[bool, " + p + "]"
	case base == "Enum" && value != "":
		text = "*" + pkg + ".EnumColumn[" + value + ", " + p + "]"
	case (base == "Time" || base == "Other" || base == "UUID") && value != "":
		text = "*" + pkg + ".OrderedColumn[" + value + ", " + p + "]"
	case strings.HasPrefix(base, "Int") || strings.HasPrefix(base, "Uint") || strings.HasPrefix(base, "Float"):
		text = "*" + pkg + ".OrderedColumn[" + strings.ToLower(base) + ", " + p + "]"
	default:
		r.note(n.Pos(), "the column type sql.%s: declare what the code needs of a column with a column type of package orm (orm.ValueColumn, orm.EnumColumn, orm.OrderedColumn, ...)", obj.Name())
		return
	}
	r.replace(n.Pos(), n.End(), text)
	r.count("generic column")
}

// funcConstraint rewrites ~func(*sql.Selector) in a constraint: what
// admitted every predicate type is orm.Predicate.
func (r *rewriter) funcConstraint(u *ast.UnaryExpr) {
	if u.Op != token.TILDE {
		return
	}
	sig, ok := r.info.TypeOf(u.X).(*types.Signature)
	if !ok || sig.Params().Len() != 1 || sig.Results().Len() != 0 {
		return
	}
	ptr, ok := sig.Params().At(0).Type().(*types.Pointer)
	if !ok {
		return
	}
	named, ok := ptr.Elem().(*types.Named)
	if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != r.lib+"/dialect/sql" || named.Obj().Name() != "Selector" {
		return
	}
	r.replace(u.Pos(), u.End(), r.engine("Predicate", u.Pos()))
	r.count("predicate constraint")
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
	if r.predicateText(r.info.TypeOf(other), bin.Pos()) == "" {
		// o.Field == nil: the field of an order input was a pointer, and
		// is a name now, empty when none was asked for.
		if ptr, ok := types.Unalias(r.info.TypeOf(other)).(*types.Pointer); ok {
			if ent, kind := r.s.kindOf(typeName(ptr.Elem())); ent != nil && kind == "OrderField" {
				nilSide := bin.Y
				if isNil(bin.X) {
					nilSide = bin.X
				}
				r.replace(nilSide.Pos(), nilSide.End(), `""`)
				r.count("order field")
			}
		}
		return
	}
	text := r.engine("IsZero", bin.Pos()) + "(" + r.render(other) + ")"
	if bin.Op == token.NEQ {
		text = "!" + text
	}
	r.replace(bin.Pos(), bin.End(), text)
	r.count("zero predicate")
}

// nilAsserted rewrites testify's Nil and NotNil of a predicate. A predicate
// was a function, and nil when it was no condition; it is data now, which
// is never nil: left as it was, NotNil would pass for every predicate (and a
// test that a denied scope still narrows would assert nothing), and Nil for
// none.
func (r *rewriter) nilAsserted(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	want, ok := map[string]string{"Nil": "True", "Nilf": "Truef", "NotNil": "False", "NotNilf": "Falsef"}[sel.Sel.Name]
	if !ok {
		return false
	}
	fn, ok := r.info.Uses[sel.Sel].(*types.Func)
	if !ok || fn.Pkg() == nil || !strings.HasPrefix(fn.Pkg().Path(), "github.com/stretchr/testify/") {
		return false
	}
	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		return false
	}
	at := 0
	if sig.Recv() == nil {
		at = 1 // assert.Nil(t, object, ...); a.Nil(object, ...) on an Assertions
	}
	if len(call.Args) <= at || r.predicateText(r.info.TypeOf(call.Args[at]), call.Pos()) == "" {
		return false
	}
	arg := call.Args[at]
	r.replace(sel.Sel.Pos(), sel.Sel.End(), want)
	r.replace(arg.Pos(), arg.End(), r.engine("IsZero", arg.Pos())+"("+r.render(arg)+")")
	r.count("zero predicate")
	return true
}

// whereInput keeps a call of P compiling where its two results went: P
// returned an error for an input that cannot be a condition, and returns a
// predicate that fails the query now. A call with two results stands in an
// assignment, a declaration, a return or as the only argument of a call,
// and in each of them "w.P(), error(nil)" takes its place.
func (r *rewriter) whereInput(call *ast.CallExpr, stack []ast.Node) {
	switch parent(stack, 1).(type) {
	case *ast.ExprStmt, *ast.DeferStmt, *ast.GoStmt:
		return // its results are dropped
	}
	r.insert(call.End(), ", error(nil)")
	r.note(call.Pos(), "P of an input with no condition was an error; it is the zero predicate now, which is no condition: every row. Refuse an empty input here if the code relied on the error")
	r.count("P without an error")
}

// elements looks at the elements of a slice, an array or a map of
// predicates, and at the fields of a struct that are predicates.
func (r *rewriter) elements(lit *ast.CompositeLit) {
	value := func(e ast.Expr) ast.Expr {
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			return kv.Value
		}
		return e
	}
	lt := r.info.TypeOf(lit)
	if lt == nil {
		return
	}
	// []*rule{{cond: f}}: the literal with its type left out is a *rule.
	if ptr, ok := types.Unalias(lt).Underlying().(*types.Pointer); ok {
		lt = ptr.Elem()
	}
	switch t := types.Unalias(lt).Underlying().(type) {
	case *types.Slice:
		for _, e := range lit.Elts {
			r.into(value(e), t.Elem())
		}
	case *types.Array:
		for _, e := range lit.Elts {
			r.into(value(e), t.Elem())
		}
	case *types.Map:
		for _, e := range lit.Elts {
			r.into(value(e), t.Elem())
		}
	case *types.Struct:
		for i, e := range lit.Elts {
			if kv, ok := e.(*ast.KeyValueExpr); ok {
				if id, ok := kv.Key.(*ast.Ident); ok {
					if f, ok := r.info.Uses[id].(*types.Var); ok {
						r.into(kv.Value, f.Type())
					}
				}
			} else if i < t.NumFields() {
				r.into(e, t.Field(i).Type())
			}
		}
	}
}
