package ormgen

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	vgen "github.com/syssam/velox/compiler/gen"
)

// Names are the names of the per-entity types in package entity.
type Names struct {
	Meta, Edges                               string
	Query, Create, CreateBulk, Update, Delete string
	ptr, edge, link                           string
}

func (g *Generator) names(e *Entity) Names { return namesOf(e.Name) }

// reservedNames are the identifiers of package entity an entity named n
// always gets.
func reservedNames(n string) []string {
	x := namesOf(n)
	return []string{x.Meta, x.Edges, n + "Edge", n + "Connection", x.Query, x.Create, x.CreateBulk, x.Update, x.Delete}
}

func namesOf(n string) Names {
	return Names{
		Meta: n + "Meta", Edges: n + "Edges",
		Query: n + "Query", Create: n + "Create", CreateBulk: n + "CreateBulk", Update: n + "Update", Delete: n + "Delete",
		ptr: "ptr" + n, edge: "edge" + n, link: "link" + n,
	}
}

func relConst(r vgen.Rel) string {
	switch r {
	case vgen.O2O:
		return "orm.O2O"
	case vgen.O2M:
		return "orm.O2M"
	case vgen.M2O:
		return "orm.M2O"
	default:
		return "orm.M2M"
	}
}

// versionGate is the line of a generated package that fails to compile
// against a package orm of another generation, with a name that says why.
const versionGate = "// The package orm this code was generated for: if this line does not compile,\n// generate the code again with the velox-gen of the velox module in go.mod.\nconst _ = orm.GeneratedCodeIsVersion1\n"

func (g *Generator) model() (map[string][]byte, error) {
	im := g.Imports()
	im.use(ormPath, "orm")
	// The errors an edge accessor returns are package velox's.
	veloxPkg := im.use(veloxPath, "velox")
	im.use("github.com/syssam/velox/schema/field", "schemafield")
	im.use(g.cfg.Package+"/predicate", "predicate")
	slotsPkg := im.use(g.cfg.Package+"/internal/slots", "slots")
	var b bytes.Buffer
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	// Field types from other packages first, then the per-entity packages (the
	// enum types of the fields live there), under names that no variable of the
	// generated functions can shadow.
	for _, e := range g.ents {
		for _, f := range e.Fields {
			ref, err := goTypeOf(f.F)
			if err != nil {
				return nil, err
			}
			if ref.PkgPath != "" && f.Enum == "" {
				if got := im.use(ref.PkgPath, ref.PkgName); got != ref.PkgName && ref.PkgName != "" {
					return nil, fmt.Errorf("velox: %s.%s: two packages named %q are used as field types", e.Name, f.F.Name, ref.PkgName)
				}
			}
		}
	}
	for _, e := range g.ents {
		_, types, _, _ := g.enumNames(e)
		if len(types) > 0 || len(e.Edges) > 0 {
			// Only the entities whose enum types or edge descriptions the
			// package entity uses are imported.
			e.alias = im.use(g.cfg.Package+"/"+e.Pkg, LocalName(e.Pkg))
		}
		for f, t := range types {
			f.Enum = t
			f.Base = e.alias + "." + t
		}
		for _, x := range e.Edges {
			x.Target.linked = true
		}
	}

	for _, e := range g.ents {
		w("\n%s%s\n\n", fileMarker, fileName(e))
		g.entityStructs(w, e, veloxPkg)
		g.entityScans(w, e)
		if err := g.entityMeta(w, e); err != nil {
			return nil, err
		}
		g.entityBuilders(w, e)
		if err := g.entityQueries(w, e); err != nil {
			return nil, err
		}
		g.entityInits(w, e, slotsPkg)
	}

	var out bytes.Buffer
	out.WriteString(header)
	out.WriteString("// Package entity holds every entity, the names of its builders and its runtime\n// description: a file per entity.\npackage entity\n\n")
	out.WriteString(im.block())
	out.WriteString("\n" + versionGate + "\n")
	out.Write(b.Bytes())
	return split(out.Bytes(), "entity")
}

// entityStructs writes the struct of an entity, the struct of its loaded
// edges, and the accessor of each edge that says whether it was loaded.
func (g *Generator) entityStructs(w func(format string, a ...any), e *Entity, veloxPkg string) {
	n := g.names(e)
	// Entity struct.
	w("// %s is the %s entity.\ntype %s struct {\n", e.Name, e.Name, e.Name)
	for _, f := range e.Fields {
		if f.Hidden {
			w("\t%s %s\n", f.GoName, f.goType())
			continue
		}
		// velox's tags: a sensitive field never reaches JSON, a tag
		// declared in the schema is used as it is, and otherwise the
		// key is the field's name.
		tag := fmt.Sprintf(`json:"%s,omitempty"`, f.JSON)
		switch {
		case f.F.Sensitive():
			tag = `json:"-"`
		case f.F.StructTag != "" && f.JSON == f.F.Name:
			tag = f.F.StructTag
		}
		w("\t%s %s `%s`\n", f.GoName, f.goType(), tag)
	}
	w("\t// Edges holds the edges loaded with the query; see the OrErr methods.\n\tEdges %s `json:\"edges\"`\n}\n\n", n.Edges)

	// Edges struct.
	w("// %s holds the loaded edges of a %s.\ntype %s struct {\n", n.Edges, e.Name, n.Edges)
	for _, x := range e.Edges {
		tag := fmt.Sprintf(`json:"%s,omitempty"`, x.E.Name)
		if x.E.StructTag != "" {
			tag = x.E.StructTag
		}
		if x.E.Unique {
			w("\t%s *%s `%s`\n", x.GoName, x.Target.Name, tag)
		} else {
			w("\t%s []*%s `%s`\n", x.GoName, x.Target.Name, tag)
		}
	}
	w("\tloaded [%d]bool\n}\n\n", max(len(e.Edges), 1))
	for _, x := range e.Edges {
		if x.E.Unique {
			w("// %[1]sOrErr returns the %[2]s edge: an error when it was not loaded (velox.IsNotLoaded), velox.ErrNotFound when it was loaded and is empty.\nfunc (e *%[3]s) %[1]sOrErr() (*%[4]s, error) {\n\tif !e.loaded[%[5]d] {\n\t\treturn nil, %[6]s.NewNotLoadedError(%[2]q)\n\t}\n\tif e.%[1]s == nil {\n\t\treturn nil, %[7]s.NotFound()\n\t}\n\treturn e.%[1]s, nil\n}\n\n",
				x.GoName, x.E.Name, n.Edges, x.Target.Name, x.Index, veloxPkg, g.names(x.Target).Meta)
		} else {
			w("// %[1]sOrErr returns the %[2]s edge, or an error when it was not loaded (velox.IsNotLoaded).\nfunc (e *%[3]s) %[1]sOrErr() ([]*%[4]s, error) {\n\tif !e.loaded[%[5]d] {\n\t\treturn nil, %[6]s.NewNotLoadedError(%[2]q)\n\t}\n\treturn e.%[1]s, nil\n}\n\n",
				x.GoName, x.E.Name, n.Edges, x.Target.Name, x.Index, veloxPkg)
		}
	}
}

// entityScans writes the three functions the engine reaches an entity's
// struct through, the only code per field: the address of the field of a
// column (Ptr), of the field of an edge (Edge), and what stores the neighbors
// an eager load read (Link).
func (g *Generator) entityScans(w func(format string, a ...any), e *Entity) {
	n := g.names(e)
	// The address of each column's field: the only per-field code.
	w("// %[1]s returns the address of the field of a %[2]s that holds its i-th column:\n// what a row is scanned into, and read from. It is the Ptr of %[3]s.\nfunc %[1]s(v any, i int) any {\n\te := v.(*%[2]s)\n\tswitch i {\n", n.ptr, e.Name, n.Meta)
	for i, f := range e.Fields {
		w("\tcase %d:\n\t\treturn &e.%s\n", i, f.GoName)
	}
	w("\t}\n\treturn nil\n}\n\n")
	if len(e.Edges) > 0 {
		w("// %[1]s marks the i-th edge of a %[2]s as loaded and returns the address of the\n// field that holds its neighbors. It is the Edge of %[3]s.\nfunc %[1]s(v any, i int) any {\n\te := v.(*%[2]s)\n\te.Edges.loaded[i] = true\n\tswitch i {\n", n.edge, e.Name, n.Meta)
		for _, x := range e.Edges {
			w("\tcase %d:\n\t\treturn &e.Edges.%s\n", x.Index, x.GoName)
		}
		w("\t}\n\treturn nil\n}\n\n")
	}
	if e.linked {
		w("// %[1]s stores the neighbors an eager load read into dst, the address of the\n// field of another entity's edge to %[2]s: all of them, or the first where the\n// edge has one. It is the Link of %[3]s.\nfunc %[1]s(dst any, es []any) {\n\tswitch d := dst.(type) {\n\tcase *[]*%[2]s:\n\t\ts := make([]*%[2]s, len(es))\n\t\tfor i, e := range es {\n\t\t\ts[i] = e.(*%[2]s)\n\t\t}\n\t\t*d = s\n\tcase **%[2]s:\n\t\t*d = nil\n\t\tif len(es) > 0 {\n\t\t\t*d = es[0].(*%[2]s)\n\t\t}\n\t}\n}\n\n", n.link, e.Name, n.Meta)
	}
}

// entityMeta writes the runtime description of an entity, and its String
// method when it has a sensitive field.
func (g *Generator) entityMeta(w func(format string, a ...any), e *Entity) error {
	n := g.names(e)
	// Meta.
	var cols []string
	for _, f := range e.Fields {
		cols = append(cols, strconv.Quote(f.Col))
	}
	w("// %s is the runtime description of %s. Use it through its address.\nvar %s = orm.Meta{\n\tName: %q,\n\tTable: %q,\n\tColumns: []string{%s},\n\tNew: func() any { return new(%s) },\n\tPtr: %s,\n",
		n.Meta, e.Name, n.Meta, e.Name, e.T.Table(), strings.Join(cols, ", "), e.Name, n.ptr)
	if hasRuntime(e) {
		w("\tRuntime: true,\n\tPackage: %q,\n", g.cfg.Package)
	}
	if len(e.Edges) > 0 {
		w("\tEdge: %s,\n", n.edge)
	}
	if e.linked {
		w("\tLink: %s,\n", n.link)
	}
	sensitive := false
	w("\tFields: []*orm.FieldMeta{\n")
	for _, f := range e.Fields {
		if f.Hidden {
			continue
		}
		w("\t\t{Name: %q, Column: %q, Type: schemafield.%s", f.F.Name, f.Col, f.F.Type.Type.ConstName())
		if f.Ptr {
			w(", Nillable: true")
		}
		// The id of a new row is the database's to assign when it is a
		// number; any other id is the caller's or a default's.
		if f.F.Optional || (f.IsID && f.F.Type.Numeric()) {
			w(", Optional: true")
		}
		if !f.Null {
			w(", NotNull: true")
		}
		if f.F.Immutable && !f.IsID {
			w(", Immutable: true")
		}
		if f.F.Sensitive() {
			w(", Sensitive: true")
			sensitive = true
		}
		if f.F.IsEnum() {
			var vs []string
			for _, en := range f.F.Enums {
				vs = append(vs, strconv.Quote(en.Value))
			}
			w(", Enums: []string{%s}", strings.Join(vs, ", "))
		}
		w("},\n")
	}
	w("\t},\n}\n\n")
	if sensitive {
		for _, f := range e.Fields {
			if !f.Hidden && f.GoName == "String" {
				return fmt.Errorf("velox: %s has a field %q and, with a sensitive field, a method String that keeps the secret out of what is printed: rename the field", e.Name, f.F.Name)
			}
		}
		w("// String renders the %[1]s without its sensitive fields, so that printing\n// one does not put a secret in a log.\nfunc (e *%[1]s) String() string { return orm.Describe(&%[2]s, e) }\n\n", e.Name, n.Meta)
	}
	return nil
}

// entityBuilders writes the names of an entity's builders.
func (g *Generator) entityBuilders(w func(format string, a ...any), e *Entity) {
	n := g.names(e)
	// The builders, and a page of the entity: types of package orm under
	// the entity's names. They are not written out per entity because
	// their methods then are in this package, the one every package of an
	// application reads: see orm/builders.go for what that cost.
	w(`// The builders of %[1]s, and a page of it: what the root package's %[1]s
// returns. Their methods are those of the types of package orm.
type (
%[2]s = orm.QueryOf[*%[1]s, %[3]s, %[4]s]
%[5]s = orm.CreateOf[*%[1]s, %[3]s]
%[6]s = orm.CreateBulkOf[*%[1]s, %[3]s]
%[7]s = orm.UpdateOf[*%[1]s, %[3]s]
%[8]s = orm.DeleteOf[%[3]s]
// %[1]sEdge is a row of a page of %[1]s, with its cursor, and
// %[1]sConnection one page: what Paginate returns.
%[1]sEdge       = orm.PageEdge[*%[1]s]
%[1]sConnection = orm.Connection[*%[1]s]
)

`, e.Name, n.Query, e.pred(), e.ID.Base, n.Create, n.CreateBulk, n.Update, n.Delete)
}

// entityQueries writes, for each edge, the method that queries the neighbors
// of one row.
func (g *Generator) entityQueries(w func(format string, a ...any), e *Entity) error {
	n := g.names(e)
	// The edges of the entity: u.QueryPosts(ex) queries the neighbors of
	// one row. (Eager loading is the query's With, which takes the edge
	// of the entity's package: With(user.Posts).)
	for _, x := range e.Edges {
		t := g.names(x.Target)
		tid, err := goTypeOf(x.Target.ID.F)
		if err != nil {
			return fmt.Errorf("velox: %s: edge %q: %w", e.Name, x.E.Name, err)
		}
		w("// Query%[1]s queries the %[2]q neighbors of e, on ex.\nfunc (e *%[3]s) Query%[1]s(ex velox.Executor) *%[4]s {\n\treturn orm.Neighbors[*%[5]s, %[6]s, %[7]s](%[8]s.Edges[%[9]d], e, ex)\n}\n\n",
			x.GoName, x.E.Name, e.Name, t.Query, x.Target.Name, x.Target.pred(), tid.Expr, n.Meta, x.Index)
	}
	return nil
}

// entityInits writes what connects an entity when the program starts: its
// slot, and its edges to the entities they join.
func (g *Generator) entityInits(w func(format string, a ...any), e *Entity, slotsPkg string) {
	n := g.names(e)
	// An edge is described by data in its owner's package, which cannot
	// name package entity; package entity hands it the Metas it connects.
	if hasRuntime(e) {
		w("func init() { %s.%s.Bind(&%s) }\n\n", slotsPkg, e.Name, n.Meta)
	}
	if len(e.Edges) > 0 {
		w("func init() {\n\t%s.Edges = %s.EdgeMetas()\n", n.Meta, e.alias)
		for _, x := range e.Edges {
			w("\t%[1]s.Edges[%[2]d].Owner, %[1]s.Edges[%[2]d].Target = &%[1]s, &%[3]s\n", n.Meta, x.Index, g.names(x.Target).Meta)
			if th := x.E.Through; th != nil && g.by[th.Name] != nil {
				w("\t%s.Edges[%d].Through = &%s\n", n.Meta, x.Index, g.names(g.by[th.Name]).Meta)
			}
		}
		w("}\n\n")
	}
}

// noInline marks every function of a source //go:noinline: what an extension
// does to the methods it adds to package entity (NoInline).
//
// The compiler writes the body of each function it could inline into the
// package's export data, and every package that calls one reads it back and
// compiles it again at the call site. While the builders were generated,
// package entity was thousands of three-line wrappers around the runtime,
// called from every package of the application, and withholding their bodies
// took 17% off the compile time of 312 services on a 454-entity schema (100.6
// to 83.5 CPU-s). The builders are types of package orm now, and for what is
// left of the package itself (the accessors of the edges, Query<Edge>) it
// makes no difference: the same services, 78.4-79.8 CPU-s without it and
// 78.5-78.9 with it. So the package's own functions can be inlined again.
func noInline(src []byte) []byte {
	var out bytes.Buffer
	for _, line := range bytes.SplitAfter(src, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("func ")) && !bytes.HasPrefix(line, []byte("func init(")) {
			out.WriteString("//go:noinline\n")
		}
		out.Write(line)
	}
	return out.Bytes()
}
