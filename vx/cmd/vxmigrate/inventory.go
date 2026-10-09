package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"io"
	"sort"
	"strings"
)

// schema is what the generated code says of the entities: read from its
// types, since that is what the application is written against.
type schema struct {
	// to is the import path of the root package vx generates: root, unless
	// the new code goes somewhere else.
	to       string
	root     string                 // import path of the generated root package
	entities map[string]*entityInfo // by Go name
	byLeaf   map[string]*entityInfo // by the import path of the entity's own package
}

type entityInfo struct {
	name   string
	fields map[string]bool // Go names of the struct's fields
	edges  map[string]bool // Go names of the edges
	// singular maps the name a to-many edge has in AddTagIDs and
	// RemoveTagIDs to the edge, Tags: read from the builders, since the
	// rules that made one from the other are the old generator's.
	singular map[string]string
}

func readSchema(p *program, root string) (*schema, error) {
	s := &schema{root: root, entities: map[string]*entityInfo{}, byLeaf: map[string]*entityInfo{}}
	ent := p.pkgs[root+"/entity"]
	if ent == nil || ent.types == nil {
		return nil, fmt.Errorf("%s/entity was not found", root)
	}
	scope := ent.types.Scope()
	for _, name := range scope.Names() {
		edges, ok := scope.Lookup(name + "Edges").(*types.TypeName)
		tn, isType := scope.Lookup(name).(*types.TypeName)
		if !ok || !isType {
			continue
		}
		st, isStruct := tn.Type().Underlying().(*types.Struct)
		es, isStruct2 := edges.Type().Underlying().(*types.Struct)
		if !isStruct || !isStruct2 {
			continue
		}
		e := &entityInfo{name: name, fields: map[string]bool{}, edges: map[string]bool{}}
		for i := range st.NumFields() {
			if f := st.Field(i); f.Exported() && f.Name() != "Edges" {
				e.fields[f.Name()] = true
			}
		}
		for i := range es.NumFields() {
			if f := es.Field(i); f.Exported() {
				e.edges[f.Name()] = true
			}
		}
		s.entities[name] = e
		s.byLeaf[root+"/"+strings.ToLower(name)] = e
		e.singular = singulars(p, root, e)
	}
	if len(s.entities) == 0 {
		return nil, fmt.Errorf("no entity was found in %s/entity", root)
	}
	return s, nil
}

// singulars reads the names of e's to-many edges in the id setters of its
// create builder.
func singulars(p *program, root string, e *entityInfo) map[string]string {
	out := map[string]string{}
	client := p.pkgs[root+"/client/"+strings.ToLower(e.name)]
	if client == nil || client.types == nil {
		return out
	}
	create, ok := client.types.Scope().Lookup(e.name + "Create").(*types.TypeName)
	if !ok {
		return out
	}
	methods := types.NewMethodSet(types.NewPointer(create.Type()))
	for i := range methods.Len() {
		mid, ok := strings.CutSuffix(strings.TrimPrefix(methods.At(i).Obj().Name(), "Add"), "IDs")
		if !ok || !strings.HasPrefix(methods.At(i).Obj().Name(), "Add") || mid == "" || e.edges[mid] {
			continue
		}
		// The edge that shares the longest start with it: Tag and Tags,
		// Category and Categories, Child and Children.
		best, shared := "", 0
		for edge := range e.edges {
			n := 0
			for n < len(edge) && n < len(mid) && edge[n] == mid[n] {
				n++
			}
			if n > shared || (n == shared && n > 0 && edge < best) {
				best, shared = edge, n
			}
		}
		if best != "" && shared*2 >= len(mid) {
			out[mid] = best
		}
	}
	return out
}

// use is one kind of reference to the old API.
type use struct{ where, on, name string }

var builderSuffixes = []string{"CreateBulk", "Create", "UpdateOne", "Update", "DeleteOne", "Delete", "Mutation", "Filter", "UpsertBulk", "UpsertOne", "Upsert", "Client",
	"Query", "Select", "GroupBy", "WhereInput", "Connection", "Edges", "Edge", "OrderField", "Order", "PaginateOption", "Querier", "Paginatable", "Selector", "CollectMeta"}

var memberPrefixes = []string{"SetNillable", "Set", "Add", "Clear", "Remove", "Append", "WithNamed", "With", "Query", "Old", "Reset", "Where", "Has", "By", "Field", "Default", "UpdateDefault", "Edge"}
var memberSuffixes = []string{"IDs", "ID", "Cleared", "Field", "Count", "With", "Validator", "Table", "Column", "PrimaryKey", "InverseTable", ""}

// kindOf splits the name of a generated type into its entity and what it is
// of that entity: HolidayUpdateOne is (Holiday, UpdateOne).
func (s *schema) kindOf(name string) (*entityInfo, string) {
	if e := s.entities[name]; e != nil {
		return e, "Entity"
	}
	for _, suf := range builderSuffixes {
		if base, ok := strings.CutSuffix(name, suf); ok && s.entities[base] != nil {
			return s.entities[base], suf
		}
	}
	return nil, name
}

// member replaces the field or edge in a generated member's name:
// SetHolidayDate is Set<F>, AddItemIDs is Add<E>IDs.
func member(e *entityInfo, name string) string {
	if e == nil {
		return name
	}
	if e.fields[name] {
		return "<F>"
	}
	if e.edges[name] {
		return "<E>"
	}
	for _, pre := range memberPrefixes {
		rest, ok := strings.CutPrefix(name, pre)
		if !ok || rest == "" {
			continue
		}
		for _, suf := range memberSuffixes {
			mid, ok := strings.CutSuffix(rest, suf)
			if !ok || mid == "" {
				continue
			}
			switch {
			case e.fields[mid]:
				return pre + "<F>" + suf
			case e.edges[mid]:
				return pre + "<E>" + suf
			}
		}
	}
	for _, suf := range memberSuffixes[:len(memberSuffixes)-1] {
		if mid, ok := strings.CutSuffix(name, suf); ok {
			switch {
			case e.fields[mid]:
				return "<F>" + suf
			case e.edges[mid]:
				return "<E>" + suf
			}
		}
	}
	return name
}

func (s *schema) where(path string) string {
	switch rest, ok := strings.CutPrefix(path, s.root); {
	case !ok:
		return path
	case rest == "":
		return "root"
	case s.byLeaf[path] != nil:
		return "<entity>"
	case strings.HasPrefix(rest, "/client/"):
		return "client/<entity>"
	default:
		return strings.TrimPrefix(rest, "/")
	}
}

// classify names the reference a selector makes to the old API, or reports
// that it makes none.
func (s *schema) classify(info *types.Info, sel *ast.SelectorExpr, lib string) (use, bool) {
	relevant := func(obj types.Object) bool {
		if obj == nil || obj.Pkg() == nil {
			return false
		}
		path := obj.Pkg().Path()
		return path == s.root || strings.HasPrefix(path, s.root+"/") || path == lib || strings.HasPrefix(path, lib+"/")
	}
	if selection := info.Selections[sel]; selection != nil {
		obj := selection.Obj()
		if !relevant(obj) {
			return use{}, false
		}
		recv := declaring(selection)
		var e *entityInfo
		on := recv
		if strings.HasPrefix(obj.Pkg().Path(), s.root) {
			e, on = s.kindOf(recv)
			if leaf := s.byLeaf[obj.Pkg().Path()]; leaf != nil && e == nil {
				e = leaf
			}
		}
		kind := "."
		if _, isMethod := obj.(*types.Func); isMethod {
			kind = "()"
		}
		return use{s.where(obj.Pkg().Path()), on, member(e, obj.Name()) + kind}, true
	}
	obj := info.Uses[sel.Sel]
	if !relevant(obj) {
		return use{}, false
	}
	// A qualified name, pkg.Name.
	e := s.byLeaf[obj.Pkg().Path()]
	name := obj.Name()
	var what string
	switch o := obj.(type) {
	case *types.Func:
		what = "func"
	case *types.Var:
		what = "var"
	case *types.Const:
		what = "const"
		if named, ok := o.Type().(*types.Named); ok && e != nil && named.Obj().Pkg() == obj.Pkg() {
			// A value of an enum field.
			return use{"<entity>", "const", "<enum value>"}, true
		}
	case *types.TypeName:
		what = "type"
	}
	if strings.HasPrefix(obj.Pkg().Path(), s.root) {
		if e != nil {
			name = member(e, name)
		} else if ent, kind := s.kindOf(name); ent != nil {
			name = "<Entity>" + strings.TrimPrefix(kind, "Entity")
		} else if base, ok := strings.CutPrefix(name, "Type"); ok && s.entities[base] != nil {
			name = "Type<Entity>"
		} else {
			for _, pre := range []string{"With", "New", "Build"} {
				if rest, ok := strings.CutPrefix(name, pre); ok {
					if ent, kind := s.kindOf(rest); ent != nil {
						name = pre + "<Entity>" + strings.TrimPrefix(kind, "Entity")
					} else {
						for _, suf := range []string{"Order", "Filter", "Predicate"} {
							if base, ok := strings.CutSuffix(rest, suf); ok && s.entities[base] != nil {
								name = pre + "<Entity>" + suf
							}
						}
					}
				}
			}
		}
	}
	return use{s.where(obj.Pkg().Path()), what, name}, true
}

// declaring names the type a selected field or method is declared on.
func declaring(sel *types.Selection) string {
	if fn, ok := sel.Obj().(*types.Func); ok {
		if recv := fn.Signature().Recv(); recv != nil {
			return typeName(recv.Type())
		}
	}
	t := sel.Recv()
	path := sel.Index()
	for _, i := range path[:len(path)-1] {
		st, ok := deref(t).Underlying().(*types.Struct)
		if !ok {
			break
		}
		t = st.Field(i).Type()
	}
	return typeName(t)
}

func deref(t types.Type) types.Type {
	if p, ok := types.Unalias(t).(*types.Pointer); ok {
		return p.Elem()
	}
	return t
}

func typeName(t types.Type) string {
	switch t := types.Unalias(deref(t)).(type) {
	case *types.Named:
		return t.Obj().Name()
	case *types.Interface:
		return "interface"
	}
	return "?"
}

// inventory counts the references the application makes to the old API.
func inventory(p *program, s *schema, lib string, w io.Writer) {
	type count struct{ code, test int }
	counts := map[use]*count{}
	visit := func(files []*ast.File, info *types.Info, test bool) {
		for _, f := range files {
			if ast.IsGenerated(f) {
				// Another generator's output (a GraphQL engine's bindings):
				// regenerated, not rewritten.
				continue
			}
			ast.Inspect(f, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if u, ok := s.classify(info, sel, lib); ok {
					c := counts[u]
					if c == nil {
						c = &count{}
						counts[u] = c
					}
					if test {
						c.test++
					} else {
						c.code++
					}
				}
				return true
			})
		}
	}
	for _, k := range p.own {
		visit(k.files, k.info, false)
		for _, t := range k.tests {
			visit(t.files, t.info, true)
		}
	}
	uses := make([]use, 0, len(counts))
	for u := range counts {
		uses = append(uses, u)
	}
	sort.Slice(uses, func(i, j int) bool {
		a, b := uses[i], uses[j]
		if a.where != b.where {
			return a.where < b.where
		}
		if a.on != b.on {
			return a.on < b.on
		}
		ca, cb := counts[a], counts[b]
		if ca.code+ca.test != cb.code+cb.test {
			return ca.code+ca.test > cb.code+cb.test
		}
		return a.name < b.name
	})
	for _, u := range uses {
		c := counts[u]
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\n", u.where, u.on, u.name, c.code, c.test)
	}
}
