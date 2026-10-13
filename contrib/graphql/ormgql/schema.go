package ormgql

import (
	"bytes"
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	vgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/contrib/graphql/graphqlgen"
	"github.com/syssam/velox/internal/ormgen"
	"github.com/syssam/velox/schema/field"
)

// gqlWhere is one field of a where input.
type gqlWhere struct {
	Name   string // in the schema, and the json tag
	GoName string
	GoType string // as written in package filter
	Plan   string // its gqlinput.WhereField
}

// How an edge appears as a field of its entity's GraphQL type.
const (
	gqlOne        = iota + 1 // author: User
	gqlList                  // tags: [Tag!]!
	gqlConnection            // posts(after, first, before, last, ...): PostConnection!
)

type gqlEdge struct {
	X *ormgen.Edge
	// Method is the edge's name in Go, or the name graphql.InterfaceField
	// gives it in the schema.
	Method string
	// Name is the field's name in the schema, where graphql.InterfaceField
	// gives the edge another than its own.
	Name  string
	Kind  int
	Order int // 0: no orderBy argument, 1: one order, 2: a list of them
	Where bool
}

type gqlOrder struct{ Value, Const, Column string }

// gqlNeighbor is a has...With field of a where input: its place in the plan,
// and the entity whose where input its elements are.
type gqlNeighbor struct {
	At     int
	Target *ormgen.Entity
}

// gqlMark is a method without a body by which an engine tells that an
// entity is of an interface or a union: IsPrincipal.
type gqlMark struct{ Method, Doc string }

// gqlPoly is a field several edges resolve, typed as an interface their
// targets implement: the neighbor on the first edge that has one.
type gqlPoly struct {
	Name      string // of the field in the schema
	Method    string
	Interface string
	Edges     []*ormgen.Edge
}

// gqlEnt is what the schema says of one entity.
type gqlEnt struct {
	Neighbors []gqlNeighbor
	Node      bool
	// Typed says the entity has a type in the schema, and not only a where
	// input.
	Typed    bool
	HasWhere bool
	Where    []gqlWhere
	// Enums are the enum fields the where input filters on: the values of
	// each by name in the schema, in the schema's order.
	Enums    [][]graphqlgen.EnumValue
	HasOrder bool
	// OrderType and the Const of each Order are names of the entity's own
	// package.
	OrderType string
	Order     []gqlOrder
	Edges     []gqlEdge
	// Bytes are the nillable bytes fields the schema reads through an
	// accessor.
	Bytes []*ormgen.Field
	Marks []gqlMark
	Polys []gqlPoly
	// members are the fields and methods of the entity's struct, those of
	// the layer among them.
	members map[string]bool
}

// mark gives the entity the method IsName.
func (q *gqlEnt) mark(e *ormgen.Entity, name, doc string) error {
	m := "Is" + name
	if q.members[m] {
		return fmt.Errorf("velox: graphql: %s needs a method %s, and has a field or method of that name", e.Name, m)
	}
	q.members[m] = true
	q.Marks = append(q.Marks, gqlMark{Method: m, Doc: doc})
	return nil
}

func normalized(s string) string { return strings.ToLower(strings.ReplaceAll(s, "_", "")) }

var veloxRuntimeRx = regexp.MustCompile(`@goModel\(model: "(github\.com/syssam/velox/contrib/graphql/[^"]+)"\)`)

var goModelRx = regexp.MustCompile(`@goModel\(model: "([^"]+)"\)`)

// goModel returns the Go type a definition is bound to, as package path and
// name, or "", "".
func goModel(d *ast.Definition) (path, name string) {
	dir := d.Directives.ForName("goModel")
	if dir == nil {
		return "", ""
	}
	arg := dir.Arguments.ForName("model")
	if arg == nil || arg.Value == nil {
		return "", ""
	}
	i := strings.LastIndex(arg.Value.Raw, ".")
	if i < 0 {
		return "", ""
	}
	return arg.Value.Raw[:i], arg.Value.Raw[i+1:]
}

// graphql generates the schema text and reads from it what Go each entity
// needs. It returns the schema files by name.
func (ext *extension) read(graph *vgen.Graph) (map[string]string, error) {
	cfg := ext.cfg.Schema
	cfg.ORMPackage, cfg.OutDir = ext.g.Package(), ext.g.Target()
	cfg.SchemaGenerator, cfg.WithoutGQLGen = true, true
	cfg.RelayConnection, cfg.Ordering, cfg.RelaySpec = true, true, true
	// Inputs of mutations are the application's own: it moves their fields
	// to a builder with Set or Fill.
	cfg.Mutations = false
	sdlGen := graphqlgen.NewGenerator(graph, cfg)
	files, err := sdlGen.SchemaFiles()
	if err != nil {
		return nil, fmt.Errorf("velox: graphql schema: %w", err)
	}
	ext.scalars = sdlGen.JSONScalars()
	// The interfaces package entity declares: the ones the schema defines,
	// and the ones a field of several edges is typed as (gqlEdges).
	ext.ifaces = map[string]bool{}
	defined, err := sdlGen.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("velox: graphql schema: %w", err)
	}
	for _, i := range defined {
		ext.ifaces[i.Name] = true
	}

	// The Go types of this tree, where the schema names velox's.
	enumModel := map[string]string{} // velox's enum type -> this generator's
	for _, e := range ext.g.Entities() {
		n, types := ext.g.LeafNames(e)
		for f, t := range types {
			enumModel[ext.g.Package()+"/"+strings.ToLower(e.Name)+"."+f.F.StructField()] = ext.g.Package() + "/" + e.Pkg + "." + t
		}
		enumModel[ext.g.Package()+"/entity."+e.Name+"OrderField"] = ext.g.Package() + "/" + e.Pkg + "." + n.Ext[orderTypeKey]
	}
	const relay = "github.com/syssam/velox/contrib/graphql/gqlrelay."
	const bytesScalar = "github.com/syssam/velox/contrib/graphql/gqlgenrelay.Bytes"
	for name, src := range files {
		src = goModelRx.ReplaceAllStringFunc(src, func(m string) string {
			model := goModelRx.FindStringSubmatch(m)[1]
			switch {
			case model == relay+"Cursor":
				model = ormgen.VeloxPath + ".Cursor"
			case model == relay+"PageInfo":
				model = ormgen.VeloxPath + ".PageInfo"
			case model == bytesScalar:
				// velox binds the scalar of a bytes field to a type of its
				// gqlgen runtime. Which type marshals []byte is the
				// engine's business here: the scalar is left unbound, as
				// Time is.
				return ""
			case enumModel[model] != "":
				model = enumModel[model]
			}
			return `@goModel(model: "` + model + `")`
		})
		src = strings.Replace(src, "scalar Bytes \n", "scalar Bytes\n", 1)
		src = strings.Replace(src, "enum OrderDirection {", `enum OrderDirection @goModel(model: "`+gqlinputPath+`.OrderDirection") {`, 1)
		// A binding to the runtime of the other generator's GraphQL layer
		// (gqlrelay, gqlgenrelay): gqlinput is this one's.
		if m := veloxRuntimeRx.FindStringSubmatch(strings.ReplaceAll(src, gqlinputPath+".", "")); m != nil {
			return nil, fmt.Errorf("velox: graphql schema: %s binds a type of velox's GraphQL runtime, %s, which this generator has no counterpart of yet", name, m[1])
		}
		files[name] = src
	}

	// Read the schema back.
	defs := map[string]*ast.Definition{}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		doc, perr := parser.ParseSchema(&ast.Source{Name: name, Input: files[name]})
		if perr != nil {
			return nil, fmt.Errorf("velox: graphql schema: %s: %w", name, perr)
		}
		for _, d := range doc.Definitions {
			if defs[d.Name] != nil {
				// The schema is invalid for any engine: two things of the
				// schema got one name (an enum field called order_field
				// and the entity's OrderField enum).
				return nil, fmt.Errorf("velox: graphql schema: %s is defined twice (the second time in %s)", d.Name, name)
			}
			defs[d.Name] = d
		}
	}
	ext.ents = map[*ormgen.Entity]*gqlEnt{}
	typeOf := map[string]*ormgen.Entity{} // GraphQL object type -> entity
	entityPkg := ext.g.Package() + "/entity"
	for _, d := range defs {
		if path, name := goModel(d); d.Kind == ast.Object && path == entityPkg && ext.g.Entity(name) != nil {
			if d.Name != name {
				return nil, fmt.Errorf("velox: graphql: entity %s is the GraphQL type %s: a renamed type (graphql.Type on the entity) is not supported yet", name, d.Name)
			}
			typeOf[d.Name] = ext.g.Entity(name)
		}
	}
	// The packages of the fields' Go types get their names in package
	// filter before any entity's package does: an entity called Time must
	// not take "time" from time.Time.
	for _, e := range ext.g.Entities() {
		for _, f := range e.Fields {
			if ref, err := ormgen.GoTypeOf(f.F); err == nil && ref.PkgPath != "" && f.Enum == "" {
				if got := ext.filterIm.Use(ref.PkgPath, ref.PkgName); got != ref.PkgName && ref.PkgName != "" {
					return nil, fmt.Errorf("velox: graphql: %s.%s: two packages named %q are used as field types", e.Name, f.F.Name, ref.PkgName)
				}
			}
		}
	}
	for _, e := range ext.g.Entities() {
		// An entity may have a where input without a type of its own: it
		// is hidden from the schema and still filtered through.
		d, wd := defs[e.Name], defs[e.Name+"WhereInput"]
		if typeOf[e.Name] != e {
			d = nil
		}
		if wd != nil && wd.Kind != ast.InputObject {
			wd = nil
		}
		if d == nil && wd == nil {
			continue
		}
		q := &gqlEnt{HasWhere: wd != nil}
		ext.ents[e] = q
		if d == nil {
			continue
		}
		q.Typed = true
		q.Node = contains(d.Interfaces, "Node")
		if q.Node {
			for _, f := range e.Fields {
				if f.GoName == "IsNode" {
					return nil, fmt.Errorf("velox: graphql: %s has a field %q, and as a node of the schema a method IsNode", e.Name, f.F.Name)
				}
			}
		}
		if err := ext.gqlEdges(e, q, d, typeOf, sdlGen); err != nil {
			return nil, err
		}
		for _, u := range sdlGen.Unions(e.T) {
			if err := q.mark(e, u, fmt.Sprintf("marks %s as a member of the union %s of the GraphQL schema.", e.Name, u)); err != nil {
				return nil, err
			}
		}
		if od := defs[e.Name+"OrderField"]; od != nil && od.Kind == ast.Enum {
			if err := ext.gqlOrder(e, q, od, sdlGen); err != nil {
				return nil, err
			}
		}
	}
	// The interfaces after every entity's fields are read: a field of several
	// edges names one.
	ifaces := make([]string, 0, len(ext.ifaces))
	for name := range ext.ifaces {
		ifaces = append(ifaces, name)
	}
	sort.Strings(ifaces)
	for _, name := range ifaces {
		if !token.IsExported(name) {
			return nil, fmt.Errorf("velox: graphql: the interface %s would be a type of package entity that nothing outside it can name; give it a name that starts with a capital", name)
		}
		if !ext.g.Reserve(name) {
			return nil, fmt.Errorf("velox: graphql: the interface %s collides with another generated name of package entity", name)
		}
		for _, t := range sdlGen.Implementors(name) {
			e := ext.g.Entity(t.Name)
			if q := ext.ents[e]; e != nil && q != nil && q.Typed {
				if err := q.mark(e, name, fmt.Sprintf("marks %s as implementing the interface %s of the GraphQL schema.", e.Name, name)); err != nil {
					return nil, err
				}
			}
		}
	}
	// Where inputs after every entity is known to have one or not: a
	// has...With field names its target's.
	for _, e := range ext.g.Entities() {
		if q := ext.ents[e]; q != nil && q.HasWhere {
			if err := ext.gqlWhere(e, q, defs[e.Name+"WhereInput"], sdlGen); err != nil {
				return nil, err
			}
		}
	}
	if err := ext.gqlBindings(defs, typeOf); err != nil {
		return nil, err
	}
	return files, nil
}

// gqlBindings checks that the schema binds nothing of this tree that the
// generator does not write. The schema text is velox's, whose own generator
// writes the Go it names; what this one writes is read back from the text
// (an interface, the scalar of a JSON field's Go type, the accessor of a
// nillable bytes field). Whatever the text names beyond that would be found
// missing by an engine's build first: it is said here.
func (ext *extension) gqlBindings(defs map[string]*ast.Definition, typeOf map[string]*ormgen.Entity) error {
	pkg := ext.g.Package()
	known := map[string]bool{}
	nodes := false
	for _, e := range ext.g.Entities() {
		known[pkg+"/entity."+e.Name] = true
		known[pkg+"/entity."+e.Name+"Connection"] = true
		known[pkg+"/entity."+e.Name+"Edge"] = true
		n, types := ext.g.LeafNames(e)
		for _, t := range types {
			known[pkg+"/"+e.Pkg+"."+t] = true
		}
		if q := ext.ents[e]; q != nil {
			nodes = nodes || q.Node
			if q.HasOrder {
				known[pkg+"/entity."+e.Name+"Order"] = true
				known[pkg+"/"+e.Pkg+"."+n.Ext[orderTypeKey]] = true
			}
			if q.HasWhere {
				known[pkg+"/filter."+e.Name+"WhereInput"] = true
			}
		}
	}
	for name := range ext.ifaces {
		known[pkg+"/entity."+name] = true
	}
	for _, s := range ext.scalars {
		known[pkg+"."+s.Name] = true
	}
	if nodes {
		known[pkg+".Noder"] = true
		for _, name := range []string{"Noder", "NodeType", "NodeTypes"} {
			if ext.g.Entity(name) != nil {
				return fmt.Errorf("velox: graphql: the entity %s has the name of what the root package declares for the nodes of the schema", name)
			}
		}
	}
	// The packages this generator writes. A package of the application's
	// own under the same directory (the Go type of an enum field) is none
	// of its business.
	written := map[string]bool{pkg: true, pkg + "/entity": true, pkg + "/filter": true, pkg + "/predicate": true}
	for _, e := range ext.g.Entities() {
		written[pkg+"/"+e.Pkg] = true
	}
	names := make([]string, 0, len(defs))
	for name := range defs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		d := defs[name]
		if path, typ := goModel(d); written[path] {
			if !known[path+"."+typ] {
				return fmt.Errorf("velox: graphql: the schema binds %s to %s.%s, which this generator does not write: what it comes from in the schema package is not supported yet", d.Name, path, typ)
			}
		}
		e := typeOf[d.Name]
		if d.Kind != ast.Object || e == nil {
			continue
		}
		// A field bound to another name is a struct field or an edge's
		// method of that name.
		members := map[string]bool{"ID": true}
		for _, f := range e.Fields {
			if !f.Hidden {
				members[f.GoName] = true
			}
		}
		if q := ext.ents[e]; q != nil {
			for _, x := range q.Edges {
				members[x.Method] = true
			}
			for _, c := range q.Bytes {
				members[c.GoName+"OrNil"] = true
			}
		}
		for _, f := range d.Fields {
			dir := f.Directives.ForName("goField")
			if dir == nil {
				continue
			}
			if arg := dir.Arguments.ForName("name"); arg != nil && arg.Value != nil && !members[arg.Value.Raw] {
				return fmt.Errorf("velox: graphql: the schema binds %s.%s to %s.%s, which this generator does not write: not supported yet", d.Name, f.Name, e.Name, arg.Value.Raw)
			}
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// forced reports whether the schema leaves a field to a resolver of the
// application's.
func forced(f *ast.FieldDefinition) bool {
	dir := f.Directives.ForName("goField")
	if dir == nil {
		return false
	}
	arg := dir.Arguments.ForName("forceResolver")
	return arg != nil && arg.Value != nil && arg.Value.Raw == "true"
}

// gqlEdges reads which edges of e the schema exposes, and how, and what else
// of e's type is resolved by a method: an edge under another name or several
// under one (graphql.InterfaceField), and a nillable bytes field.
func (ext *extension) gqlEdges(e *ormgen.Entity, q *gqlEnt, d *ast.Definition, typeOf map[string]*ormgen.Entity, sdl *graphqlgen.Generator) error {
	edges := map[string]*ormgen.Edge{}
	byEdge := map[*vgen.Edge]*ormgen.Edge{}
	for _, x := range e.Edges {
		edges[normalized(x.E.Name)] = x
		byEdge[x.E] = x
	}
	named := map[string]graphqlgen.InterfaceField{}
	ifields, err := sdl.InterfaceFields(e.T)
	if err != nil {
		return fmt.Errorf("velox: graphql schema: %w", err)
	}
	for _, f := range ifields {
		named[f.Name] = f
	}
	members := map[string]bool{"Edges": true, "IsNode": true}
	q.members = members
	for _, f := range e.Fields {
		if f.F.Sensitive() {
			members["String"] = true // the entity has the method then
		}
	}
	bytesFields := map[string]*ormgen.Field{}
	for _, f := range e.Fields {
		members[f.GoName] = true
		if !f.Hidden && f.Ptr && f.F.Type != nil && f.F.Type.Type == field.TypeBytes {
			bytesFields[f.GoName+"OrNil"] = f
		}
	}
	for _, x := range e.Edges {
		members["Query"+x.GoName] = true
	}
	take := func(method, what string) error {
		if members[method] {
			return fmt.Errorf("velox: graphql: %s needs a method %s to resolve %s, and has a field or method of that name", e.Name, method, what)
		}
		members[method] = true
		return nil
	}
	for _, f := range d.Fields {
		x := edges[normalized(f.Name)]
		if x == nil {
			if forced(f) {
				continue
			}
			if ifc, ok := named[f.Name]; ok {
				if err := ext.gqlNamed(e, q, f, ifc, byEdge, typeOf, take); err != nil {
					return err
				}
				continue
			}
			// A nillable bytes field is a *[]byte, which the schema reads
			// through an accessor that returns the bytes or nil.
			if dir := f.Directives.ForName("goField"); dir != nil {
				if arg := dir.Arguments.ForName("name"); arg != nil && arg.Value != nil {
					if c := bytesFields[arg.Value.Raw]; c != nil {
						if err := take(arg.Value.Raw, fmt.Sprintf("the field %q", c.F.Name)); err != nil {
							return err
						}
						q.Bytes = append(q.Bytes, c)
					}
				}
			}
			continue // a column, or a field the application resolves itself
		}
		if forced(f) {
			continue
		}
		ge := gqlEdge{X: x, Method: x.GoName}
		target := f.Type.Name()
		switch {
		case len(f.Arguments) > 0:
			if !strings.HasSuffix(target, "Connection") || typeOf[strings.TrimSuffix(target, "Connection")] != x.Target {
				return fmt.Errorf("velox: graphql: %s.%s has arguments and is not a connection of %s", e.Name, f.Name, x.Target.Name)
			}
			ge.Kind = gqlConnection
			want := []string{"after", "first", "before", "last"}
			for i, a := range f.Arguments {
				switch {
				case i < len(want):
					if a.Name != want[i] {
						return fmt.Errorf("velox: graphql: %s.%s: argument %d is %s, not %s", e.Name, f.Name, i+1, a.Name, want[i])
					}
				case a.Name == "orderBy" && !ge.Where:
					ge.Order = 1
					if a.Type.Elem != nil {
						ge.Order = 2
					}
				case a.Name == "where":
					ge.Where = true
				default:
					return fmt.Errorf("velox: graphql: %s.%s: unexpected argument %s", e.Name, f.Name, a.Name)
				}
			}
		case typeOf[target] != x.Target:
			continue
		case f.Type.Elem != nil:
			ge.Kind = gqlList
		default:
			ge.Kind = gqlOne
		}
		if err := take(ge.Method, fmt.Sprintf("the edge %q", x.E.Name)); err != nil {
			return err
		}
		q.Edges = append(q.Edges, ge)
	}
	return nil
}

// gqlNamed reads a field graphql.InterfaceField puts on edges of e: one edge
// under another name, resolved as the edge is, or several under one, typed
// as an interface and resolved by the first that has a neighbor.
func (ext *extension) gqlNamed(e *ormgen.Entity, q *gqlEnt, f *ast.FieldDefinition, ifc graphqlgen.InterfaceField, byEdge map[*vgen.Edge]*ormgen.Edge, typeOf map[string]*ormgen.Entity, take func(method, what string) error) error {
	method := vgen.Pascal(ifc.Name)
	xs := make([]*ormgen.Edge, 0, len(ifc.Edges))
	for _, ge := range ifc.Edges {
		x := byEdge[ge]
		if x == nil {
			return fmt.Errorf("velox: graphql: %s.%s is the edge %q, which %s does not have here", e.Name, f.Name, ge.Name, e.Name)
		}
		xs = append(xs, x)
	}
	what := fmt.Sprintf("the field %q", f.Name)
	if ifc.Interface == "" {
		x := xs[0]
		if typeOf[f.Type.Name()] != x.Target {
			return nil // of a type that is not this tree's: the application's to resolve
		}
		ge := gqlEdge{X: x, Method: method, Name: f.Name, Kind: gqlOne}
		if f.Type.Elem != nil {
			ge.Kind = gqlList
		}
		if err := take(method, what); err != nil {
			return err
		}
		q.Edges = append(q.Edges, ge)
		return nil
	}
	if len(f.Arguments) > 0 || f.Type.Elem != nil {
		return fmt.Errorf("velox: graphql: %s.%s is a list over the edges of several types (graphql.InterfaceField on edges that are not unique), which is not supported yet", e.Name, f.Name)
	}
	if f.Type.Name() != ifc.Interface {
		return fmt.Errorf("velox: graphql: %s.%s is a %s in the schema, and the interface of its edges is %s", e.Name, f.Name, f.Type.Name(), ifc.Interface)
	}
	if err := take(method, what); err != nil {
		return err
	}
	ext.ifaces[ifc.Interface] = true
	q.Polys = append(q.Polys, gqlPoly{Name: f.Name, Method: method, Interface: ifc.Interface, Edges: xs})
	return nil
}

// gqlOrder reads the fields a list of e can be ordered by.
//
// The order input, entity.UserOrder, is declared in package entity; the type
// of its Field and that type's constants, user.OrderField and
// user.OrderFieldCreatedAt, in the entity's own package. A package pays for
// every exported name of each package it imports, whether it uses the name
// or not (the compiler indexes them all), and almost every package of an
// application imports package entity: on a 454-entity schema these were
// 12,079 constants, and a fifth of what a package that only returns an entity
// took to compile.
func (ext *extension) gqlOrder(e *ormgen.Entity, q *gqlEnt, d *ast.Definition, sdl *graphqlgen.Generator) error {
	if !ext.g.Reserve(e.Name + "Order") {
		return fmt.Errorf("velox: graphql: the order type %sOrder collides with another generated name", e.Name)
	}
	// The names the entity's package has given out so far: these come after.
	n, _ := ext.g.LeafNames(e)
	q.OrderType = n.Ext[orderTypeKey]
	columns := map[string]string{}
	for _, of := range sdl.OrderFields(e.T) {
		if of.Field == nil {
			return fmt.Errorf("velox: graphql: %s orders by %s, the number of neighbors on an edge, which is not supported yet", e.Name, of.Name)
		}
		columns[of.Name] = of.Field.StorageKey()
	}
	var order []gqlOrder
	for _, v := range d.EnumValues {
		col, ok := columns[v.Name]
		if !ok {
			return fmt.Errorf("velox: graphql: %sOrderField has the value %s, which no field of %s is ordered by", e.Name, v.Name, e.Name)
		}
		order = append(order, gqlOrder{Value: v.Name, Const: n.Unique("OrderField"+vgen.Pascal(strings.ToLower(v.Name)), "Value"), Column: col})
	}
	q.HasOrder, q.Order = true, order
	return nil
}

// whereOps maps the operators velox's schema generator names to the
// runtime's, with the shape of the Go field each takes: '1' one value, 'n' a
// list, 'b' a flag.
var whereOps = map[string]struct {
	op    string
	shape byte
}{
	"EQ": {"orm.OpEQ", '1'}, "NEQ": {"orm.OpNEQ", '1'}, "In": {"orm.OpIn", 'n'}, "NotIn": {"orm.OpNotIn", 'n'},
	"GT": {"orm.OpGT", '1'}, "GTE": {"orm.OpGTE", '1'}, "LT": {"orm.OpLT", '1'}, "LTE": {"orm.OpLTE", '1'},
	"Contains": {"orm.OpContains", '1'}, "HasPrefix": {"orm.OpHasPrefix", '1'}, "HasSuffix": {"orm.OpHasSuffix", '1'},
	"EqualFold": {"orm.OpEqualFold", '1'}, "ContainsFold": {"orm.OpContainsFold", '1'},
	"IsNil": {"orm.OpIsNil", 'b'}, "NotNil": {"orm.OpNotNil", 'b'},
}

// gqlWhere reads the fields of e's where input.
//
// What a field of the input filters on comes from the schema generator
// (WhereFields), which knows; the name alone does not say. nameIn is name
// with In, or a field called name_in; hasPosts is the edge, or a boolean
// field. Read back from names, the input of one real shape filtered a
// column the schema had not made filterable.
func (ext *extension) gqlWhere(e *ormgen.Entity, q *gqlEnt, d *ast.Definition, sdl *graphqlgen.Generator) error {
	fields := map[*vgen.Field]*ormgen.Field{}
	for _, f := range e.Fields {
		fields[f.F] = f
	}
	if e.ID != nil {
		fields[e.ID.F] = e.ID
	}
	edges := map[*vgen.Edge]*ormgen.Edge{}
	for _, x := range e.Edges {
		edges[x.E] = x
	}
	described := map[string]graphqlgen.WhereField{}
	for _, w := range sdl.WhereFields(e.T) {
		described[w.Name] = w
	}
	members := map[string]bool{"Predicates": true, "Not": true, "Or": true, "And": true}
	methods := map[string]bool{"P": true, "UnmarshalGQL": true, "MarshalGQL": true, "UnmarshalJSON": true, "MarshalJSON": true}
	enumAt := map[*ormgen.Field]int{}
	for i, f := range d.Fields {
		if i < 3 {
			if want := [...]string{"not", "and", "or"}[i]; f.Name != want {
				return fmt.Errorf("velox: graphql: %sWhereInput: field %d is %s, not %s", e.Name, i+1, f.Name, want)
			}
			continue
		}
		src, ok := described[f.Name]
		if !ok {
			return fmt.Errorf("velox: graphql: %sWhereInput.%s: the schema has this field and its generator does not say what it filters on", e.Name, f.Name)
		}
		w := gqlWhere{Name: f.Name}
		switch {
		case src.Edge != nil:
			x := edges[src.Edge]
			if x == nil {
				return fmt.Errorf("velox: graphql: %sWhereInput.%s filters on the edge %q, which %s does not have here", e.Name, f.Name, src.Edge.Name, e.Name)
			}
			if !src.With {
				w.GoName, w.GoType = "Has"+x.GoName, "*bool"
				w.Plan = fmt.Sprintf("{Op: orm.OpHasEdge, Edge: %sEdges[%d]}", lowerFirst(e.Name), x.Index)
				break
			}
			if t := ext.ents[x.Target]; t == nil || !t.HasWhere {
				return fmt.Errorf("velox: graphql: %sWhereInput.%s filters %s, which has no where input", e.Name, f.Name, x.Target.Name)
			}
			w.GoName, w.GoType = "Has"+x.GoName+"With", "[]*"+x.Target.Name+"WhereInput"
			w.Plan = fmt.Sprintf("{Op: orm.OpHasEdge, Edge: %sEdges[%d], With: true}", lowerFirst(e.Name), x.Index)
			q.Neighbors = append(q.Neighbors, gqlNeighbor{At: len(q.Where), Target: x.Target})
		default:
			c := fields[src.Field]
			op, known := whereOps[src.Op]
			if c == nil || c.Hidden {
				return fmt.Errorf("velox: graphql: %sWhereInput.%s filters on the field %q, which has no column of its own here", e.Name, f.Name, src.Field.Name)
			}
			if !known {
				return fmt.Errorf("velox: graphql: %sWhereInput.%s: this kind of filter (%s) is not supported yet", e.Name, f.Name, src.Op)
			}
			w.GoName = c.GoName
			if src.Op != "EQ" {
				w.GoName += src.Op
			}
			switch op.shape {
			case '1':
				w.GoType = "*" + ext.filterType(c)
			case 'n':
				w.GoType = "[]" + ext.filterType(c)
			default:
				w.GoType = "bool"
			}
			w.Plan = fmt.Sprintf("{Col: %q, Op: %s", c.Col, op.op)
			if src.Field.IsEnum() && op.shape != 'b' {
				// What a name of the schema is in the column: the engine
				// hands DRAFT over, and a stored filter holds it.
				at, ok := enumAt[c]
				if !ok {
					at = len(q.Enums)
					enumAt[c] = at
					values := sdl.EnumValues(src.Field)
					if err := distinctEnum(values); err != nil {
						return fmt.Errorf("velox: graphql: the enum of %s.%s: %w", e.Name, src.Field.Name, err)
					}
					q.Enums = append(q.Enums, values)
				}
				w.Plan += fmt.Sprintf(", Enum: %sEnums[%d]", lowerFirst(e.Name), at)
			}
			w.Plan += "}"
		}
		if methods[w.GoName] {
			return fmt.Errorf("velox: graphql: %sWhereInput: the field %s would be called %s, as a method of the input is", e.Name, f.Name, w.GoName)
		}
		if members[w.GoName] {
			return fmt.Errorf("velox: graphql: %sWhereInput: two fields would be called %s", e.Name, w.GoName)
		}
		members[w.GoName] = true
		q.Where = append(q.Where, w)
	}
	return nil
}

// writeSchema writes the schema files, and removes the ones an earlier
// generation wrote for entities that are gone.
func (ext *extension) writeSchema(files map[string]string) error {
	dir := ext.cfg.Dir
	for name, src := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return err
		}
		if _, err := vgen.WriteFileIfChanged(path, []byte(src), 0o644); err != nil {
			return err
		}
	}
	old, err := filepath.Glob(filepath.Join(dir, "velox_*.graphql"))
	if err != nil {
		return err
	}
	for _, path := range old {
		if _, ok := files[filepath.Base(path)]; ok {
			continue
		}
		head, err := os.ReadFile(path) //nolint:gosec // a file the glob above found in the schema directory
		if err == nil && bytes.HasPrefix(head, []byte("# Code generated by velox. DO NOT EDIT.")) {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
	}
	return nil
}
