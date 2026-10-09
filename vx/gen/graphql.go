package gen

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	vgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/contrib/graphql/graphqlgen"
)

// GraphQL asks Generate for the GraphQL layer of the schema as well: the
// schema files, and the Go types a GraphQL engine binds them to.
//
// The schema text is velox's, unchanged: what a client of an application
// sees does not depend on which generator wrote the Go. The Go is derived
// from that text, type by type and field by field, so the two cannot
// disagree: an order input and its field enum in package entity, a where
// input per entity in package filter, and on each entity a method per edge
// that the engine calls to resolve it.
type GraphQL struct {
	// Dir is the directory the .graphql files are written to.
	Dir string
	// Schema holds velox's schema options: the name of the root file
	// (SchemaFilename), how the schema is split over files, where inputs.
	// The package paths and what Generate itself decides are filled in.
	Schema graphqlgen.Config
}

// gqlWhere is one field of a where input.
type gqlWhere struct {
	Name   string // in the schema, and the json tag
	GoName string
	GoType string // as written in package filter
	Plan   string // its orm.WhereField
}

// How an edge appears as a field of its entity's GraphQL type.
const (
	gqlOne        = iota + 1 // author: User
	gqlList                  // tags: [Tag!]!
	gqlConnection            // posts(after, first, before, last, ...): PostConnection!
)

type gqlEdge struct {
	X      *edg
	Method string
	Kind   int
	Order  int // 0: no orderBy argument, 1: one order, 2: a list of them
	Where  bool
}

type gqlOrder struct{ Value, Const, Column string }

// gqlNeighbor is a has...With field of a where input: its place in the plan,
// and the entity whose where input its elements are.
type gqlNeighbor struct {
	At     int
	Target *ent
}

// gqlEnt is what the schema says of one entity.
type gqlEnt struct {
	Neighbors []gqlNeighbor
	Node      bool
	HasWhere  bool
	Where     []gqlWhere
	HasOrder  bool
	// OrderType and the Const of each Order are names of the entity's own
	// package.
	OrderType string
	Order     []gqlOrder
	Edges     []gqlEdge
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
func (g *generator) graphql(graph *vgen.Graph) (map[string]string, error) {
	cfg := g.cfg.GraphQL.Schema
	cfg.ORMPackage, cfg.OutDir = g.cfg.Package, g.cfg.Target
	cfg.SchemaGenerator, cfg.WithoutGQLGen = true, true
	cfg.RelayConnection, cfg.Ordering, cfg.RelaySpec = true, true, true
	// Inputs of mutations are the application's own: it moves their fields
	// to a builder with Set or Fill.
	cfg.Mutations = false
	sdlGen := graphqlgen.NewGenerator(graph, cfg)
	files, err := sdlGen.SchemaFiles()
	if err != nil {
		return nil, fmt.Errorf("vx: graphql schema: %w", err)
	}

	// The Go types of this tree, where the schema names velox's.
	enumModel := map[string]string{} // velox's enum type -> this generator's
	for _, e := range g.ents {
		n, types, _, _ := g.enumNames(e)
		for f, t := range types {
			enumModel[g.cfg.Package+"/"+strings.ToLower(e.Name)+"."+f.F.StructField()] = g.cfg.Package + "/" + e.Pkg + "." + t
		}
		enumModel[g.cfg.Package+"/entity."+e.Name+"OrderField"] = g.cfg.Package + "/" + e.Pkg + "." + n.orderType
	}
	const relay = "github.com/syssam/velox/contrib/graphql/gqlrelay."
	const bytesScalar = "github.com/syssam/velox/contrib/graphql/gqlgenrelay.Bytes"
	for name, src := range files {
		src = goModelRx.ReplaceAllStringFunc(src, func(m string) string {
			model := goModelRx.FindStringSubmatch(m)[1]
			switch {
			case model == relay+"Cursor":
				model = ormPath + ".Cursor"
			case model == relay+"PageInfo":
				model = ormPath + ".PageInfo"
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
		src = strings.Replace(src, "enum OrderDirection {", `enum OrderDirection @goModel(model: "`+ormPath+`.OrderDirection") {`, 1)
		if m := veloxRuntimeRx.FindStringSubmatch(src); m != nil {
			return nil, fmt.Errorf("vx: graphql schema: %s binds a type of velox's GraphQL runtime, %s, which this generator has no counterpart of yet", name, m[1])
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
			return nil, fmt.Errorf("vx: graphql schema: %s: %w", name, perr)
		}
		for _, d := range doc.Definitions {
			if defs[d.Name] != nil {
				// The schema is invalid for any engine: two things of the
				// schema got one name (an enum field called order_field
				// and the entity's OrderField enum).
				return nil, fmt.Errorf("vx: graphql schema: %s is defined twice (the second time in %s)", d.Name, name)
			}
			defs[d.Name] = d
		}
	}
	g.gql = map[*ent]*gqlEnt{}
	typeOf := map[string]*ent{} // GraphQL object type -> entity
	entityPkg := g.cfg.Package + "/entity"
	for _, d := range defs {
		if path, name := goModel(d); d.Kind == ast.Object && path == entityPkg && g.by[name] != nil {
			if d.Name != name {
				return nil, fmt.Errorf("vx: graphql: entity %s is the GraphQL type %s: a renamed type (graphql.Type on the entity) is not supported yet", name, d.Name)
			}
			typeOf[d.Name] = g.by[name]
		}
	}
	// The packages of the fields' Go types get their names in package
	// filter before any entity's package does: an entity called Time must
	// not take "time" from time.Time.
	for _, e := range g.ents {
		for _, f := range e.Fields {
			if ref, err := goTypeOf(f.F); err == nil && ref.PkgPath != "" && f.Enum == "" {
				if got := g.filterIm.use(ref.PkgPath, ref.PkgName); got != ref.PkgName && ref.PkgName != "" {
					return nil, fmt.Errorf("vx: graphql: %s.%s: two packages named %q are used as field types", e.Name, f.F.Name, ref.PkgName)
				}
			}
		}
	}
	for _, e := range g.ents {
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
		g.gql[e] = q
		if d == nil {
			continue
		}
		q.Node = contains(d.Interfaces, "Node")
		if q.Node {
			for _, f := range e.Fields {
				if f.GoName == "IsNode" {
					return nil, fmt.Errorf("vx: graphql: %s has a field %q, and as a node of the schema a method IsNode", e.Name, f.F.Name)
				}
			}
		}
		if err := g.gqlEdges(e, q, d, typeOf); err != nil {
			return nil, err
		}
		if od := defs[e.Name+"OrderField"]; od != nil && od.Kind == ast.Enum {
			if err := g.gqlOrder(e, q, od, sdlGen); err != nil {
				return nil, err
			}
		}
	}
	// Where inputs after every entity is known to have one or not: a
	// has...With field names its target's.
	for _, e := range g.ents {
		if q := g.gql[e]; q != nil && q.HasWhere {
			if err := g.gqlWhere(e, q, defs[e.Name+"WhereInput"], sdlGen); err != nil {
				return nil, err
			}
		}
	}
	if err := g.gqlBindings(defs, typeOf); err != nil {
		return nil, err
	}
	return files, nil
}

// gqlBindings checks that the schema binds nothing of this tree that the
// generator does not write. The schema text is velox's, and velox generates
// Go this generator does not have (an interface for graphql.InterfaceField,
// a scalar type for a typed JSON field, an OrNil accessor for nillable
// bytes): left alone, the schema would name them, and the first to find out
// would be an engine's build.
func (g *generator) gqlBindings(defs map[string]*ast.Definition, typeOf map[string]*ent) error {
	pkg := g.cfg.Package
	known := map[string]bool{}
	nodes := false
	for _, e := range g.ents {
		known[pkg+"/entity."+e.Name] = true
		known[pkg+"/entity."+e.Name+"Connection"] = true
		known[pkg+"/entity."+e.Name+"Edge"] = true
		n, types, _, _ := g.enumNames(e)
		for _, t := range types {
			known[pkg+"/"+e.Pkg+"."+t] = true
		}
		if q := g.gql[e]; q != nil {
			nodes = nodes || q.Node
			if q.HasOrder {
				known[pkg+"/entity."+e.Name+"Order"] = true
				known[pkg+"/"+e.Pkg+"."+n.orderType] = true
			}
			if q.HasWhere {
				known[pkg+"/filter."+e.Name+"WhereInput"] = true
			}
		}
	}
	if nodes {
		known[pkg+".Noder"] = true
		for _, name := range []string{"Noder", "NodeType", "NodeTypes"} {
			if g.by[name] != nil {
				return fmt.Errorf("vx: graphql: the entity %s has the name of what the root package declares for the nodes of the schema", name)
			}
		}
	}
	// The packages this generator writes. A package of the application's
	// own under the same directory (the Go type of an enum field) is none
	// of its business.
	written := map[string]bool{pkg: true, pkg + "/entity": true, pkg + "/filter": true, pkg + "/predicate": true}
	for _, e := range g.ents {
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
				return fmt.Errorf("vx: graphql: the schema binds %s to %s.%s, which this generator does not write: what it comes from in the schema package is not supported yet", d.Name, path, typ)
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
		if q := g.gql[e]; q != nil {
			for _, x := range q.Edges {
				members[x.Method] = true
			}
		}
		for _, f := range d.Fields {
			dir := f.Directives.ForName("goField")
			if dir == nil {
				continue
			}
			if arg := dir.Arguments.ForName("name"); arg != nil && arg.Value != nil && !members[arg.Value.Raw] {
				return fmt.Errorf("vx: graphql: the schema binds %s.%s to %s.%s, which this generator does not write: not supported yet", d.Name, f.Name, e.Name, arg.Value.Raw)
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

// gqlEdges reads which edges of e the schema exposes, and how.
func (g *generator) gqlEdges(e *ent, q *gqlEnt, d *ast.Definition, typeOf map[string]*ent) error {
	edges := map[string]*edg{}
	for _, x := range e.Edges {
		edges[normalized(x.E.Name)] = x
	}
	members := map[string]bool{"Edges": true, "IsNode": true}
	for _, f := range e.Fields {
		if f.F.Sensitive() {
			members["String"] = true // the entity has the method then
		}
	}
	for _, f := range e.Fields {
		members[f.GoName] = true
	}
	for _, x := range e.Edges {
		members["Query"+x.GoName] = true
	}
	for _, f := range d.Fields {
		x := edges[normalized(f.Name)]
		if x == nil {
			continue // a column, or a field the application resolves itself
		}
		if dir := f.Directives.ForName("goField"); dir != nil {
			if arg := dir.Arguments.ForName("forceResolver"); arg != nil && arg.Value != nil && arg.Value.Raw == "true" {
				continue
			}
		}
		ge := gqlEdge{X: x, Method: x.GoName}
		target := f.Type.Name()
		switch {
		case len(f.Arguments) > 0:
			if !strings.HasSuffix(target, "Connection") || typeOf[strings.TrimSuffix(target, "Connection")] != x.Target {
				return fmt.Errorf("vx: graphql: %s.%s has arguments and is not a connection of %s", e.Name, f.Name, x.Target.Name)
			}
			ge.Kind = gqlConnection
			want := []string{"after", "first", "before", "last"}
			for i, a := range f.Arguments {
				switch {
				case i < len(want):
					if a.Name != want[i] {
						return fmt.Errorf("vx: graphql: %s.%s: argument %d is %s, not %s", e.Name, f.Name, i+1, a.Name, want[i])
					}
				case a.Name == "orderBy" && !ge.Where:
					ge.Order = 1
					if a.Type.Elem != nil {
						ge.Order = 2
					}
				case a.Name == "where":
					ge.Where = true
				default:
					return fmt.Errorf("vx: graphql: %s.%s: unexpected argument %s", e.Name, f.Name, a.Name)
				}
			}
		case typeOf[target] != x.Target:
			continue
		case f.Type.Elem != nil:
			ge.Kind = gqlList
		default:
			ge.Kind = gqlOne
		}
		if members[ge.Method] {
			return fmt.Errorf("vx: graphql: %s needs a method %s to resolve the edge %q, and has a field or method of that name", e.Name, ge.Method, x.E.Name)
		}
		members[ge.Method] = true
		q.Edges = append(q.Edges, ge)
	}
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
func (g *generator) gqlOrder(e *ent, q *gqlEnt, d *ast.Definition, sdl *graphqlgen.Generator) error {
	if g.taken[e.Name+"Order"] {
		return fmt.Errorf("vx: graphql: the order type %sOrder collides with another generated name", e.Name)
	}
	g.taken[e.Name+"Order"] = true
	// The names the entity's package has given out so far: these come after.
	n, _, _, _ := g.enumNames(e)
	q.OrderType = n.orderType
	columns := map[string]string{}
	for _, of := range sdl.OrderFields(e.T) {
		if of.Field == nil {
			return fmt.Errorf("vx: graphql: %s orders by %s, the number of neighbors on an edge, which is not supported yet", e.Name, of.Name)
		}
		columns[of.Name] = of.Field.StorageKey()
	}
	var order []gqlOrder
	for _, v := range d.EnumValues {
		col, ok := columns[v.Name]
		if !ok {
			return fmt.Errorf("vx: graphql: %sOrderField has the value %s, which no field of %s is ordered by", e.Name, v.Name, e.Name)
		}
		order = append(order, gqlOrder{Value: v.Name, Const: n.unique("OrderField"+vgen.Pascal(strings.ToLower(v.Name)), "Value"), Column: col})
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
func (g *generator) gqlWhere(e *ent, q *gqlEnt, d *ast.Definition, sdl *graphqlgen.Generator) error {
	fields := map[*vgen.Field]*fld{}
	for _, f := range e.Fields {
		fields[f.F] = f
	}
	if e.ID != nil {
		fields[e.ID.F] = e.ID
	}
	edges := map[*vgen.Edge]*edg{}
	for _, x := range e.Edges {
		edges[x.E] = x
	}
	described := map[string]graphqlgen.WhereField{}
	for _, w := range sdl.WhereFields(e.T) {
		described[w.Name] = w
	}
	members := map[string]bool{"Predicates": true, "Not": true, "Or": true, "And": true}
	for i, f := range d.Fields {
		if i < 3 {
			if want := [...]string{"not", "and", "or"}[i]; f.Name != want {
				return fmt.Errorf("vx: graphql: %sWhereInput: field %d is %s, not %s", e.Name, i+1, f.Name, want)
			}
			continue
		}
		src, ok := described[f.Name]
		if !ok {
			return fmt.Errorf("vx: graphql: %sWhereInput.%s: the schema has this field and its generator does not say what it filters on", e.Name, f.Name)
		}
		w := gqlWhere{Name: f.Name}
		switch {
		case src.Edge != nil:
			x := edges[src.Edge]
			if x == nil {
				return fmt.Errorf("vx: graphql: %sWhereInput.%s filters on the edge %q, which %s does not have here", e.Name, f.Name, src.Edge.Name, e.Name)
			}
			if !src.With {
				w.GoName, w.GoType = "Has"+x.GoName, "*bool"
				w.Plan = fmt.Sprintf("{Op: orm.OpHasEdge, Edge: %sEdges[%d]}", lowerFirst(e.Name), x.Index)
				break
			}
			if t := g.gql[x.Target]; t == nil || !t.HasWhere {
				return fmt.Errorf("vx: graphql: %sWhereInput.%s filters %s, which has no where input", e.Name, f.Name, x.Target.Name)
			}
			w.GoName, w.GoType = "Has"+x.GoName+"With", "[]*"+x.Target.Name+"WhereInput"
			w.Plan = fmt.Sprintf("{Op: orm.OpHasEdge, Edge: %sEdges[%d], With: true}", lowerFirst(e.Name), x.Index)
			q.Neighbors = append(q.Neighbors, gqlNeighbor{At: len(q.Where), Target: x.Target})
		default:
			c := fields[src.Field]
			op, known := whereOps[src.Op]
			if c == nil || c.Hidden {
				return fmt.Errorf("vx: graphql: %sWhereInput.%s filters on the field %q, which has no column of its own here", e.Name, f.Name, src.Field.Name)
			}
			if !known {
				return fmt.Errorf("vx: graphql: %sWhereInput.%s: this kind of filter (%s) is not supported yet", e.Name, f.Name, src.Op)
			}
			w.GoName = c.GoName
			if src.Op != "EQ" {
				w.GoName += src.Op
			}
			switch op.shape {
			case '1':
				w.GoType = "*" + g.filterType(c)
			case 'n':
				w.GoType = "[]" + g.filterType(c)
			default:
				w.GoType = "bool"
			}
			w.Plan = fmt.Sprintf("{Col: %q, Op: %s}", c.Col, op.op)
		}
		if members[w.GoName] {
			return fmt.Errorf("vx: graphql: %sWhereInput: two fields would be called %s", e.Name, w.GoName)
		}
		members[w.GoName] = true
		q.Where = append(q.Where, w)
	}
	return nil
}

func lowerFirst(s string) string { return strings.ToLower(s[:1]) + s[1:] }

// filterType is the Go type of a column as package filter writes it; the
// package it comes from is imported under filterImports.
func (g *generator) filterType(c *fld) string {
	if c.Enum != "" {
		// The type's name is the same everywhere; the package's alias is
		// this package's own.
		owner := g.enumOwner[c]
		return g.filterIm.use(g.cfg.Package+"/"+owner.Pkg, g.filterAlias(owner)) + "." + g.enumType[c]
	}
	ref, err := goTypeOf(c.F)
	if err != nil {
		return "any"
	}
	if ref.PkgPath != "" {
		g.filterIm.use(ref.PkgPath, ref.PkgName)
	}
	return ref.Expr
}

// filterAlias is the name package filter imports an entity's package under:
// its own, unless a name of the generated code already has it.
func (g *generator) filterAlias(e *ent) string {
	switch e.Pkg {
	case "orm", "predicate", "filter", "i", "p":
		return e.Pkg + "pkg"
	}
	return e.Pkg
}

// filters generates package filter: a where input per entity that has one in
// the schema.
//
// A where input is a struct with a field per filter the schema offers,
// because that is what a GraphQL engine fills in: it binds an input object
// field by field. What the fields mean is data (an orm.WhereField each), and
// one function of the runtime turns any of them into a predicate, so the
// package holds no code per field. As a type that only held a predicate it
// would have cost the 312 services of the benchmark 5 to 8% less to compile,
// and every application that reads or sets a filter in Go its code.
func (g *generator) filters() (map[string][]byte, error) {
	// The where fields were typed while the schema was read: the imports
	// their types need are registered already.
	im := g.filterIm
	im.use(ormPath, "orm")
	im.use(g.cfg.Package+"/predicate", "predicate")
	var b bytes.Buffer
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	inputs := 0
	for _, e := range g.ents {
		q := g.gql[e]
		if q == nil || !q.HasWhere {
			continue
		}
		inputs++
		w("\n%s%s\n\n", fileMarker, fileName(e))
		w("// %[1]sWhereInput is the filter a request gives for a list of %[1]s: the where\n// argument of a GraphQL connection. A field that is nil, or false for a flag,\n// was not given.\ntype %[1]sWhereInput struct {\n", e.Name)
		w("\t// Predicates are conditions the application adds in Go.\n\tPredicates []%s `json:\"-\"`\n", e.pred())
		w("\tNot *%[1]sWhereInput `json:\"not,omitempty\"`\n\tOr []*%[1]sWhereInput `json:\"or,omitempty\"`\n\tAnd []*%[1]sWhereInput `json:\"and,omitempty\"`\n\n", e.Name)
		hasEdge := false
		for _, f := range q.Where {
			w("\t%s %s `json:\"%s,omitempty\"`\n", f.GoName, f.GoType, f.Name)
			hasEdge = hasEdge || strings.Contains(f.Plan, "Edges[")
		}
		w("}\n\n")
		if hasEdge {
			w("var %sEdges = %s.EdgeMetas()\n\n", lowerFirst(e.Name), im.use(g.cfg.Package+"/"+e.Pkg, g.filterAlias(e)))
		}
		w("// %sWhere says what each field of %sWhereInput means, in the order of the struct.\nvar %sWhere = []orm.WhereField{\n", lowerFirst(e.Name), e.Name, lowerFirst(e.Name))
		for _, f := range q.Where {
			w("\t%s, // %s\n", f.Plan, f.Name)
		}
		w("}\n\n")
		if len(q.Neighbors) > 0 {
			// After every plan exists: two inputs may filter through each
			// other, which an initializer could not say.
			w("func init() {\n")
			for _, n := range q.Neighbors {
				w("\t%sWhere[%d].Neighbor = %sWhere // %s\n", lowerFirst(e.Name), n.At, lowerFirst(n.Target.Name), q.Where[n.At].Name)
			}
			w("}\n\n")
		}
		w("// P returns the condition the input describes: none for a nil or an empty\n// input, and one that fails the query for an input that cannot be a condition\n// (an empty input nested in not, and, or; nesting deeper than orm.MaxWhereDepth).\n//\n//go:noinline\nfunc (i *%[1]sWhereInput) P() %[2]s { return %[2]s(orm.Where(i, %[3]sWhere)) }\n\n", e.Name, e.pred(), lowerFirst(e.Name))
	}
	if inputs == 0 {
		return nil, nil
	}
	var out bytes.Buffer
	out.WriteString(header)
	out.WriteString("// Package filter holds the where inputs of the GraphQL schema: the filter a request\n// gives for a list of an entity, and its conversion to a predicate.\npackage filter\n\n")
	out.WriteString(im.block())
	out.WriteString("\n")
	out.Write(b.Bytes())
	return split(out.Bytes(), "filter")
}

// writeSchema writes the schema files, and removes the ones an earlier
// generation wrote for entities that are gone.
func (g *generator) writeSchema(files map[string]string) error {
	dir := g.cfg.GraphQL.Dir
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
