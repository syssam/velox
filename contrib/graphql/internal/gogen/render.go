// Package gogen writes the Go code velox.graphqls binds to: the Go types
// gqlgen's autobind finds in the generated package, named after the SDL types
// they bind to. It writes gql_<label>.go for each entity with a GraphQL
// type, and gql_node.go with Node and Nodes.
//
// Every name comes from sdl.Build, which the SDL is rendered from too, so the
// Go and the SDL cannot drift.
package gogen

import (
	"bytes"
	"cmp"
	"fmt"
	"go/format"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/syssam/velox2/contrib/graphql/internal/sdl"
	"github.com/syssam/velox2/plugin"
)

// The import paths of the packages the generated code uses.
const (
	veloxPath    = "github.com/syssam/velox2"
	veloxgqlPath = "github.com/syssam/velox2/contrib/graphql/veloxgql"
)

// Render returns the Go files of m's GraphQL types, for the generated
// package outPkg at the import path outPath, without the generated-file
// header, which velox-gen adds. The output depends only on its arguments.
//
// It fails on a model sdl.Build refuses, a Node whose key has no ID (a key
// that is not an integer, a string, a UUID or an enum), an import of the
// application's Go types it cannot name, and a Go name it writes that
// velox-gen also declares in the package.
func Render(m *plugin.Model, outPath, outPkg string) ([]plugin.File, error) {
	if outPkg == "" {
		return nil, fmt.Errorf("velox-graphql: the request names no generated package")
	}
	s, err := sdl.Build(m)
	if err != nil {
		return nil, err
	}
	r := &renderer{m: m, outPath: outPath, outPkg: outPkg, veloxEnums: map[string]bool{}, names: map[string]string{}}
	for _, en := range m.Enums {
		r.veloxEnums[en.Type] = true
	}
	r.reserve()
	files := make([]plugin.File, 0, len(s.Types)+1)
	for _, t := range s.Types {
		e := t.Entity
		if e.Label == "" || e.Handles == "" {
			return nil, fmt.Errorf("schema %s: the model gives it no label or no handles, which velox-gen names its Go after", e.Name)
		}
		if e.Label == "node" {
			return nil, fmt.Errorf("schema %s: its Go file would be gql_node.go, which holds Node and Nodes; rename the schema", e.Name)
		}
		f, err := r.typeFile(t)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	f, err := r.nodeFile(s)
	if err != nil {
		return nil, err
	}
	files = append(files, f)
	slices.SortFunc(files, func(a, b plugin.File) int { return cmp.Compare(a.Path, b.Path) })
	return files, nil
}

// pkgNames are the names of the packages the generated code imports whose
// name is not their path's last element, which need no import name.
var pkgNames = map[string]string{veloxPath: "velox"}

type renderer struct {
	m          *plugin.Model
	outPath    string
	outPkg     string
	veloxEnums map[string]bool   // the enum types velox-gen declares
	names      map[string]string // Go name in the package → what declares it
}

// reserve records the Go names velox-gen declares in the package that a name
// the plugin writes could take: the entities' types, the handles variables
// and the enum types.
func (r *renderer) reserve() {
	for _, e := range r.m.Entities {
		r.names[e.Name] = "velox-gen's type of schema " + e.Name
		r.names[e.Handles] = "velox-gen's handles of schema " + e.Name
	}
	for _, en := range r.m.Enums {
		r.names[en.Type] = "velox-gen's enum type " + en.Type
	}
}

// declare records that the plugin declares the Go name, for what.
func (r *renderer) declare(name, what string) error {
	if prev, ok := r.names[name]; ok {
		return fmt.Errorf("%s: its Go name %s is also %s in the generated package", what, name, prev)
	}
	r.names[name] = what
	return nil
}

// file is a Go file being written: its body and the imports the body uses.
type file struct {
	bytes.Buffer
	imports map[string]string // import path → the name the body uses
}

func newFile() *file { return &file{imports: map[string]string{}} }

func (f *file) p(format string, args ...any) {
	_, _ = fmt.Fprintf(f, format, args...) // a bytes.Buffer does not fail
	f.WriteByte('\n')
}

// doc writes text as a comment at indent (tabs), wrapped to 80 columns.
func (f *file) doc(indent, text string) {
	width := 80 - 4*len(indent) - len("// ")
	line := ""
	for _, w := range strings.Fields(text) {
		if line != "" && len(line)+1+len(w) > width {
			f.p("%s// %s", indent, line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += w
	}
	if line != "" {
		f.p("%s// %s", indent, line)
	}
}

// use records that the body uses the package at path by name, and returns
// name.
func (f *file) use(path, name string) string {
	f.imports[path] = name
	return name
}

func (f *file) velox() string    { return f.use(veloxPath, "velox") }
func (f *file) veloxgql() string { return f.use(veloxgqlPath, "veloxgql") }

// finish is the file's Go source, gofmt'd: the package clause, the imports
// in two groups (the standard library, then the rest) and the body.
func (f *file) finish(name, pkg string) (plugin.File, error) {
	var src bytes.Buffer
	fmt.Fprintf(&src, "package %s\n\n", pkg)
	byName := map[string]string{}
	var std, other []string
	for _, p := range slices.Sorted(maps.Keys(f.imports)) {
		if prev, ok := byName[f.imports[p]]; ok {
			return plugin.File{}, fmt.Errorf("velox-graphql: %s would import both %s and %s as %s: an application's Go type is of a package named as one velox-graphql imports", name, prev, p, f.imports[p])
		}
		byName[f.imports[p]] = p
		spec := strconv.Quote(p)
		if n := f.imports[p]; n != path.Base(p) && n != pkgNames[p] {
			spec = n + " " + spec
		}
		if strings.Contains(strings.Split(p, "/")[0], ".") {
			other = append(other, spec)
		} else {
			std = append(std, spec)
		}
	}
	if len(std)+len(other) > 0 {
		src.WriteString("import (\n")
		for _, s := range std {
			src.WriteString("\t" + s + "\n")
		}
		if len(std) > 0 && len(other) > 0 {
			src.WriteString("\n")
		}
		for _, s := range other {
			src.WriteString("\t" + s + "\n")
		}
		src.WriteString(")\n\n")
	}
	src.Write(f.Bytes())
	out, err := format.Source(src.Bytes())
	if err != nil {
		return plugin.File{}, fmt.Errorf("velox-graphql wrote Go that does not parse, which is a bug: %s: %w\n%s", name, err, src.Bytes())
	}
	return plugin.File{Path: name, Content: string(out)}, nil
}

// typeFile is gql_<label>.go, t's Go types.
func (r *renderer) typeFile(t *sdl.Type) (plugin.File, error) {
	e := t.Entity
	f := newFile()
	what := "schema " + e.Name
	n := t.Name

	if n != e.Name {
		if err := r.declare(n, what); err != nil {
			return plugin.File{}, err
		}
		f.doc("", fmt.Sprintf("%s is %s under its GraphQL type name, so that gqlgen's autobind finds the Go type of the GraphQL type %[1]s.", n, e.Name))
		f.p("type %s = %s", n, e.Name)
		f.p("")
	}
	if t.Node {
		if err := r.writeNode(f, t); err != nil {
			return plugin.File{}, err
		}
	}
	for _, en := range t.Enums {
		if err := r.writeEnum(f, t, en); err != nil {
			return plugin.File{}, err
		}
	}
	for _, name := range []string{n + "Connection", n + "Edge"} {
		if err := r.declare(name, what); err != nil {
			return plugin.File{}, err
		}
	}
	f.doc("", fmt.Sprintf("%sConnection is the GraphQL type %[1]sConnection, a connection to a list of %[1]s. It holds the query of the whole list; gqlgen calls its methods for the fields a query selects, and each runs only then.", n))
	f.p("type %sConnection struct {", n)
	f.p("\tquery %s.Query[%s] // the whole list, before paging", f.velox(), e.Name)
	f.p("}")
	f.p("")
	f.doc("", fmt.Sprintf("TotalCount is the number of %s nodes in the whole connection, not only on this page. It counts the rows when gqlgen asks for it, which is only when the field totalCount is selected.", n))
	f.p("func (c *%sConnection) TotalCount(ctx %s.Context) (int, error) {", n, f.use("context", "context"))
	f.p("\tn, err := c.query.Count(ctx)")
	f.p("\treturn int(n), err")
	f.p("}")
	f.p("")
	f.doc("", fmt.Sprintf("%sEdge is the GraphQL type %[1]sEdge, an edge in a connection to a list of %[1]s.", n))
	f.p("type %sEdge struct {", n)
	f.p("\t// Node is the %s at the end of the edge.", n)
	f.p("\tNode *%s", e.Name)
	f.p("\t// Cursor is the cursor of the edge, for paging.")
	f.p("\tCursor %s.Cursor", f.veloxgql())
	f.p("}")
	if t.Order != nil {
		if err := r.writeOrder(f, t); err != nil {
			return plugin.File{}, err
		}
	}
	if t.Where != nil {
		if err := r.writeWhere(f, t); err != nil {
			return plugin.File{}, err
		}
	}
	return f.finish("gql_"+e.Label+".go", r.outPkg)
}

// keyBase is the Go type of a Node's key that veloxgql encodes and decodes:
// the field's own type, or for a type of the application's the builtin type
// it is made of, which the key converts to and from.
func keyBase(e *plugin.Entity) (*plugin.Field, string, error) {
	k := e.Fields[e.PrimaryKey[0]]
	switch k.Type {
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return k, k.Type, nil
	case "string", "enum":
		return k, "string", nil
	case "uuid":
		return k, "[16]byte", nil
	}
	return nil, "", fmt.Errorf("schema %s: its key, of type %s, cannot be a GraphQL ID, which veloxgql encodes for an integer, a string or a UUID; leave the schema out of GraphQL with graphql.Skip()", e.Name, k.Type)
}

func (r *renderer) writeNode(f *file, t *sdl.Type) error {
	e := t.Entity
	k, base, err := keyBase(e)
	if err != nil {
		return err
	}
	v := "x." + k.GoName
	if k.ElemType != base {
		v = base + "(" + v + ")"
	}
	f.doc("", fmt.Sprintf("IsNode marks %s as a GraphQL Node: gqlgen binds the interface Node to veloxgql.Noder.", e.Name))
	f.p("func (*%s) IsNode() {}", e.Name)
	f.p("")
	f.doc("", fmt.Sprintf("IDGQL is the global ID of the %s, the GraphQL field id: of the type %s and the key %s.", e.Name, t.Name, k.Name))
	f.p("func (x *%s) IDGQL() string { return %s.EncodeID(%q, %s) }", e.Name, f.veloxgql(), t.Name, v)
	f.p("")
	return nil
}

// goType is the Go type of the field's values, as the generated package
// names it: ElemType, with its package imported, or unqualified when it is
// the generated package's own.
func (r *renderer) goType(f *file, e *plugin.Entity, fd *plugin.Field) (string, error) {
	q, name, ok := strings.Cut(fd.ElemType, ".")
	switch {
	case !ok:
		return fd.ElemType, nil
	case strings.ContainsAny(q, "[]*"):
		return "", fmt.Errorf("schema %s, field %s: velox-graphql cannot name the Go type %s", e.Name, fd.Name, fd.ElemType)
	}
	p := fd.PkgPath
	if p == "" {
		// An older model: the entity's import whose last element is the
		// qualifier.
		for _, imp := range e.Imports {
			if path.Base(imp) == q {
				p = imp
			}
		}
	}
	switch p {
	case "":
		return "", fmt.Errorf("schema %s, field %s: the model gives no package for the Go type %s", e.Name, fd.Name, fd.ElemType)
	case r.outPath:
		return name, nil
	}
	if prev, ok := f.imports[p]; ok && prev != q {
		return "", fmt.Errorf("schema %s, field %s: the package %s is named both %s and %s", e.Name, fd.Name, p, prev, q)
	}
	return f.use(p, q) + "." + name, nil
}

// writeEnum writes the marshalers of velox-gen's Go enum type of the field
// en: gqlgen binds the GraphQL enum, or the String, to the Go type, and they
// turn its values into the stored ones and back. An enum of the
// application's own Go type brings its own.
func (r *renderer) writeEnum(f *file, t *sdl.Type, en sdl.Enum) error {
	fd := t.Entity.Fields[en.Field]
	typ := fd.Enum
	if !r.veloxEnums[typ] {
		return nil
	}
	if err := r.declare(typ+".MarshalGQL", "schema "+t.Entity.Name+", field "+fd.Name); err != nil {
		return err
	}
	json, errs, io := f.use("encoding/json", "json"), f.use("errors", "errors"), f.use("io", "io")
	if en.Name == "" {
		// A String: graphql.Skip(SkipEnumField) shows the stored values.
		f.doc("", fmt.Sprintf("MarshalGQL writes e as a GraphQL String, its stored value: the field %s of %s is a String, as graphql.Skip(graphql.SkipEnumField) asks.", fd.Name, t.Entity.Name))
		f.p("func (e %s) MarshalGQL(w %s.Writer) {", typ, io)
		f.p("\tb, _ := %s.Marshal(string(e)) // never fails for a string", json)
		f.p("\t_, _ = w.Write(b)")
		f.p("}")
		f.p("")
		f.p("// UnmarshalGQL sets e from v, a String that is one of its stored values.")
		f.p("func (e *%s) UnmarshalGQL(v any) error {", typ)
		f.p("\ts, _ := v.(string)")
		f.p("\tswitch s {")
		f.p("\tcase %s:", quoteList(fd.EnumValues))
		f.p("\t\t*e = %s(s)", typ)
		f.p("\t\treturn nil")
		f.p("\t}")
		f.p("\treturn %s.New(%q)", errs, typ+" must be "+orList(fd.EnumValues))
		f.p("}")
		f.p("")
		return nil
	}
	f.p("// MarshalGQL writes e as its value of the GraphQL enum %s.", en.Name)
	f.p("func (e %s) MarshalGQL(w %s.Writer) {", typ, io)
	f.p("\tvar s string")
	f.p("\tswitch e {")
	for i, v := range fd.EnumValues {
		f.p("\tcase %q:", v)
		f.p("\t\ts = %q", en.Values[i])
	}
	f.p("\tdefault:")
	f.p("\t\ts = string(e) // not a value of the enum")
	f.p("\t}")
	f.p("\tb, _ := %s.Marshal(s) // never fails for a string", json)
	f.p("\t_, _ = w.Write(b)")
	f.p("}")
	f.p("")
	f.p("// UnmarshalGQL sets e from v, a value of the GraphQL enum %s.", en.Name)
	f.p("func (e *%s) UnmarshalGQL(v any) error {", typ)
	f.p("\ts, _ := v.(string)")
	f.p("\tswitch s {")
	for i, v := range fd.EnumValues {
		f.p("\tcase %q:", en.Values[i])
		f.p("\t\t*e = %q", v)
	}
	f.p("\tdefault:")
	f.p("\t\treturn %s.New(%q)", errs, typ+" must be "+orList(en.Values))
	f.p("\t}")
	f.p("\treturn nil")
	f.p("}")
	f.p("")
	return nil
}

func (r *renderer) writeOrder(f *file, t *sdl.Type) error {
	n, e := t.Name, t.Entity
	what := "schema " + e.Name
	for _, name := range []string{n + "Order", n + "OrderField"} {
		if err := r.declare(name, what); err != nil {
			return err
		}
	}
	values := make([]string, 0, len(t.Order))
	for _, v := range t.Order {
		values = append(values, v.Value)
	}
	f.p("")
	f.doc("", fmt.Sprintf("%sOrder is the GraphQL input %[1]sOrder, an order of connections to %[1]s.", n))
	f.p("type %sOrder struct {", n)
	f.p("\t// Direction is the direction of the order, ASC unless the input says.")
	f.p("\tDirection %s.OrderDirection", f.veloxgql())
	f.p("\t// Field is the field to order by.")
	f.p("\tField %sOrderField", n)
	f.p("}")
	f.p("")
	f.doc("", fmt.Sprintf("%sOrderField is the GraphQL enum %[1]sOrderField, the fields connections to %[1]s can be ordered by.", n))
	f.p("type %sOrderField string", n)
	f.p("")
	f.p("// The values of %sOrderField.", n)
	f.p("const (")
	for _, v := range t.Order {
		fd := e.Fields[v.Field]
		c := n + "OrderField" + fd.GoName
		if err := r.declare(c, what+", field "+fd.Name); err != nil {
			return err
		}
		f.p("\t// %s orders by the field %s.", c, fd.Name)
		f.p("\t%s %sOrderField = %q", c, n, v.Value)
	}
	f.p(")")
	f.p("")
	f.p("// IsValid reports whether o is a value of %sOrderField.", n)
	f.p("func (o %sOrderField) IsValid() bool {", n)
	f.p("\tswitch o {")
	f.p("\tcase %s:", quoteList(values))
	f.p("\t\treturn true")
	f.p("\t}")
	f.p("\treturn false")
	f.p("}")
	f.p("")
	f.p("// String returns o's value.")
	f.p("func (o %sOrderField) String() string { return string(o) }", n)
	f.p("")
	f.p("// MarshalGQL writes o as a value of the GraphQL enum %sOrderField.", n)
	f.p("func (o %sOrderField) MarshalGQL(w %s.Writer) {", n, f.use("io", "io"))
	f.p("\tb, _ := %s.Marshal(string(o)) // never fails for a string", f.use("encoding/json", "json"))
	f.p("\t_, _ = w.Write(b)")
	f.p("}")
	f.p("")
	f.p("// UnmarshalGQL sets o from v, a value of the GraphQL enum %sOrderField.", n)
	f.p("func (o *%sOrderField) UnmarshalGQL(v any) error {", n)
	f.p("\ts, _ := v.(string)")
	f.p("\tif !%sOrderField(s).IsValid() {", n)
	f.p("\t\treturn %s.New(%q)", f.use("errors", "errors"), n+"OrderField must be "+orList(values))
	f.p("\t}")
	f.p("\t*o = %sOrderField(s)", n)
	f.p("\treturn nil")
	f.p("}")
	return nil
}

func (r *renderer) writeWhere(f *file, t *sdl.Type) error {
	n, e := t.Name, t.Entity
	in := n + "WhereInput"
	if err := r.declare(in, "schema "+e.Name); err != nil {
		return err
	}
	f.p("")
	f.doc("", fmt.Sprintf("%s is the GraphQL input %[1]s, a filter of %s nodes: a node matches when every predicate given holds.", in, n))
	f.p("type %s struct {", in)
	seen := map[string]string{} // as gqlgen matches names → the Go name
	group := ""
	for _, w := range t.Where {
		key := strings.ToLower(strings.ReplaceAll(w.GoName, "_", ""))
		if prev, ok := seen[key]; ok {
			return fmt.Errorf("schema %s: the fields %s and %s of %s are one Go field to gqlgen, which ignores case and underscores; rename one with graphql.FieldName", e.Name, prev, w.GoName, in)
		}
		seen[key] = w.GoName
		typ, err := r.whereType(f, t, w)
		if err != nil {
			return err
		}
		var g string
		switch w.Kind {
		case sdl.WhereNot, sdl.WhereAnd, sdl.WhereOr:
			g = "logic"
		case sdl.WhereHas, sdl.WhereHasWith:
			g = "edge " + strconv.Itoa(w.Edge)
		default:
			g = "field " + strconv.Itoa(w.Field)
		}
		if g != group {
			if group != "" {
				f.p("")
			}
			group = g
			switch {
			case g == "logic":
				f.doc("\t", "Not, And and Or combine filters: a node matches when it does not match Not, matches each of And, and matches one of Or.")
			case w.Kind == sdl.WhereHas || w.Kind == sdl.WhereHasWith:
				f.p("\t// The edge %s: whether it has a node, or one that matches.", e.Edges[w.Edge].Name)
			case w.Edge >= 0:
				f.p("\t// Predicates on the edge %s, by the ID of its node.", e.Edges[w.Edge].Name)
			case w.IDs():
				f.p("\t// Predicates on the ID.")
			default:
				f.p("\t// Predicates on the field %s.", e.Fields[w.Field].Name)
			}
		}
		f.p("\t%s %s", w.GoName, typ)
	}
	f.p("}")
	return nil
}

// whereType is the Go type of the where-input field w: the GraphQL type's
// Go type, a pointer for one value and a slice for a list. A GraphQL ID is
// a string; Time, UUID and Uint64 are veloxgql's; an Int, a Float, a String
// or a Boolean is the field's builtin type (int8, float32, ...); an enum is
// the field's Go enum type.
func (r *renderer) whereType(f *file, t *sdl.Type, w sdl.WhereField) (string, error) {
	switch w.Kind {
	case sdl.WhereNot:
		return "*" + t.Name + "WhereInput", nil
	case sdl.WhereAnd, sdl.WhereOr:
		return "[]*" + t.Name + "WhereInput", nil
	case sdl.WhereHas:
		return "*bool", nil
	case sdl.WhereHasWith:
		return "[]*" + w.Target + "WhereInput", nil
	}
	if w.Type == "Boolean" {
		return "*bool", nil // IsNil, NotNil, and a bool field's predicates
	}
	elem := "string"
	if !w.IDs() {
		fd := t.Entity.Fields[w.Field]
		switch fd.Type {
		case "string":
		case "time":
			elem = f.veloxgql() + ".Time"
		case "uuid":
			elem = f.veloxgql() + ".UUID"
		case "uint", "uint64":
			elem = f.veloxgql() + ".Uint64"
		case "int", "int8", "int16", "int32", "int64", "uint8", "uint16", "uint32", "float32", "float64":
			elem = fd.Type
		case "enum":
			if r.veloxEnums[fd.Enum] {
				elem = fd.Enum
				break
			}
			var err error
			if elem, err = r.goType(f, t.Entity, fd); err != nil {
				return "", err
			}
		default:
			return "", fmt.Errorf("schema %s, field %s: a where predicate on a field of type %s, which velox-graphql has no Go type for", t.Entity.Name, fd.Name, fd.Type)
		}
	}
	if strings.HasPrefix(w.Type, "[") {
		return "[]" + elem, nil
	}
	return "*" + elem, nil
}

// nodeFile is gql_node.go: Node and Nodes, which look up the nodes of the
// Node types by their IDs.
func (r *renderer) nodeFile(s *sdl.Surface) (plugin.File, error) {
	for _, name := range []string{"Node", "Nodes"} {
		if err := r.declare(name, "velox-graphql's "+name); err != nil {
			return plugin.File{}, err
		}
	}
	f := newFile()
	ctx, vgql := f.use("context", "context"), f.veloxgql()
	type node struct {
		t         *sdl.Type
		key, base string // the key's Go type, and veloxgql's
		conv      string // the expression of the key from k, the decoded base
	}
	var nodes []node
	for _, t := range s.Types {
		if !t.Node {
			continue
		}
		k, base, err := keyBase(t.Entity)
		if err != nil {
			return plugin.File{}, err
		}
		nd := node{t: t, base: base, conv: "k"}
		if nd.key, err = r.goType(f, t.Entity, k); err != nil {
			return plugin.File{}, err
		}
		if k.ElemType != base {
			nd.conv = nd.key + "(k)"
		}
		nodes = append(nodes, nd)
	}
	noType := func(indent string) {
		f.p("%sreturn nil, %s.Errorf(\"node: %%w: the ID is of no Node type\", %s.ErrWrongIDType)", indent, f.use("fmt", "fmt"), vgql)
	}

	if len(nodes) == 0 {
		f.p("// Node is the root field node. No type of the schema is a Node, so every")
		f.p("// ID fails: with veloxgql.ErrWrongIDType, or with veloxgql.ErrBadID when")
		f.p("// it is malformed.")
		f.p("func Node(_ %s.Context, id string) (%s.Noder, error) {", ctx, vgql)
		f.p("\tif _, err := %s.IDType(id); err != nil {", vgql)
		f.p("\t\treturn nil, err")
		f.p("\t}")
		noType("\t")
		f.p("}")
	} else {
		f.p("// Node returns the node with the global ID id, or nil when no row has its")
		f.p("// key. It reads the ID's type, decodes the key as that type's, and looks")
		f.p("// the row up with one query on that entity, through the DB veloxgql.DBFrom")
		f.p("// reads from ctx. An ID of no Node type fails with veloxgql.ErrWrongIDType")
		f.p("// and a malformed one with veloxgql.ErrBadID; neither runs a query.")
		f.p("func Node(ctx %s.Context, id string) (%s.Noder, error) {", ctx, vgql)
		f.p("\ttyp, err := %s.IDType(id)", vgql)
		f.p("\tif err != nil {")
		f.p("\t\treturn nil, err")
		f.p("\t}")
		f.p("\tswitch typ {")
		for _, nd := range nodes {
			e := nd.t.Entity
			f.p("\tcase %q:", nd.t.Name)
			f.p("\t\tk, err := %s.DecodeID[%s](id, %q)", vgql, nd.base, nd.t.Name)
			f.p("\t\tif err != nil {")
			f.p("\t\t\treturn nil, err")
			f.p("\t\t}")
			f.p("\t\tdb, err := %s.DBFrom(ctx)", vgql)
			f.p("\t\tif err != nil {")
			f.p("\t\t\treturn nil, err")
			f.p("\t\t}")
			f.p("\t\tx, err := db.Query[%s]().Where(%s.%s.EQ(%s)).One(ctx)", e.Name, e.Handles, e.Fields[e.PrimaryKey[0]].GoName, nd.conv)
			f.p("\t\tswitch {")
			f.p("\t\tcase %s.Is(err, %s.ErrNotFound):", f.use("errors", "errors"), f.velox())
			f.p("\t\t\treturn nil, nil")
			f.p("\t\tcase err != nil:")
			f.p("\t\t\treturn nil, err")
			f.p("\t\t}")
			f.p("\t\treturn x, nil")
		}
		f.p("\t}")
		noType("\t")
		f.p("}")
	}

	f.p("")
	tooMany := func() {
		f.p("\tif len(ids) > %s.MaxPage {", vgql)
		f.p("\t\treturn nil, %s.Errorf(\"nodes: %%w: %%d IDs, and the most is %%d\", %s.ErrPageTooLarge, len(ids), %[2]s.MaxPage)", f.use("fmt", "fmt"), vgql)
		f.p("\t}")
	}
	if len(nodes) == 0 {
		f.p("// Nodes is the root field nodes. No type of the schema is a Node, so an")
		f.p("// ID fails as in Node, and so do more than veloxgql.MaxPage IDs, with")
		f.p("// veloxgql.ErrPageTooLarge.")
		f.p("func Nodes(_ %s.Context, ids []string) ([]%s.Noder, error) {", ctx, vgql)
		tooMany()
		f.p("\tfor _, id := range ids {")
		f.p("\t\tif _, err := %s.IDType(id); err != nil {", vgql)
		f.p("\t\t\treturn nil, err")
		f.p("\t\t}")
		noType("\t\t")
		f.p("\t}")
		f.p("\treturn []%s.Noder{}, nil", vgql)
		f.p("}")
		return f.finish("gql_node.go", r.outPkg)
	}
	f.p("// Nodes returns the nodes with the global IDs ids, in their order, with nil")
	f.p("// for an ID no row has. It decodes every ID first, and then looks up each")
	f.p("// type's nodes with one query, through the DB veloxgql.DBFrom reads from")
	f.p("// ctx. It fails as Node does on an ID, before any query runs, and with")
	f.p("// veloxgql.ErrPageTooLarge on more than veloxgql.MaxPage IDs.")
	f.p("func Nodes(ctx %s.Context, ids []string) ([]%s.Noder, error) {", ctx, vgql)
	tooMany()
	f.p("\t// The keys of each type, and the indexes in ids of their IDs.")
	f.p("\tvar (")
	for _, nd := range nodes {
		f.p("\t\tkeys%s []%s", nd.t.Name, nd.key)
		f.p("\t\tat%s   []int", nd.t.Name)
	}
	f.p("\t)")
	f.p("\tfor i, id := range ids {")
	f.p("\t\ttyp, err := %s.IDType(id)", vgql)
	f.p("\t\tif err != nil {")
	f.p("\t\t\treturn nil, err")
	f.p("\t\t}")
	f.p("\t\tswitch typ {")
	for _, nd := range nodes {
		f.p("\t\tcase %q:", nd.t.Name)
		f.p("\t\t\tk, err := %s.DecodeID[%s](id, %q)", vgql, nd.base, nd.t.Name)
		f.p("\t\t\tif err != nil {")
		f.p("\t\t\t\treturn nil, err")
		f.p("\t\t\t}")
		f.p("\t\t\tkeys%s = append(keys%[1]s, %s)", nd.t.Name, nd.conv)
		f.p("\t\t\tat%s = append(at%[1]s, i)", nd.t.Name)
	}
	f.p("\t\tdefault:")
	noType("\t\t\t")
	f.p("\t\t}")
	f.p("\t}")
	f.p("\tout := make([]%s.Noder, len(ids))", vgql)
	f.p("\tif len(ids) == 0 {")
	f.p("\t\treturn out, nil")
	f.p("\t}")
	f.p("\tdb, err := %s.DBFrom(ctx)", vgql)
	f.p("\tif err != nil {")
	f.p("\t\treturn nil, err")
	f.p("\t}")
	for _, nd := range nodes {
		e := nd.t.Entity
		pk := e.Fields[e.PrimaryKey[0]].GoName
		f.p("\tif len(keys%s) > 0 {", nd.t.Name)
		f.p("\t\trows, err := db.Query[%s]().Where(%s.%s.In(keys%s...)).All(ctx)", e.Name, e.Handles, pk, nd.t.Name)
		f.p("\t\tif err != nil {")
		f.p("\t\t\treturn nil, err")
		f.p("\t\t}")
		f.p("\t\tby := make(map[%s]*%s, len(rows))", nd.key, e.Name)
		f.p("\t\tfor i := range rows {")
		f.p("\t\t\tby[rows[i].%s] = &rows[i]", pk)
		f.p("\t\t}")
		f.p("\t\tfor j, k := range keys%s {", nd.t.Name)
		f.p("\t\t\tif x := by[k]; x != nil {")
		f.p("\t\t\t\tout[at%s[j]] = x", nd.t.Name)
		f.p("\t\t\t}")
		f.p("\t\t}")
		f.p("\t}")
	}
	f.p("\treturn out, nil")
	f.p("}")
	return f.finish("gql_node.go", r.outPkg)
}

// quoteList is vs as Go string literals, separated by commas.
func quoteList(vs []string) string {
	q := make([]string, len(vs))
	for i, v := range vs {
		q[i] = strconv.Quote(v)
	}
	return strings.Join(q, ", ")
}

// orList is vs in English, as "A, B or C".
func orList(vs []string) string {
	if len(vs) < 2 {
		return strings.Join(vs, "")
	}
	return strings.Join(vs[:len(vs)-1], ", ") + " or " + vs[len(vs)-1]
}
