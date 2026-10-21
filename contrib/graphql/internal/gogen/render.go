// Package gogen writes the Go code velox.graphqls binds to: the Go types
// gqlgen's autobind finds in the generated package, named after the SDL types
// they bind to, and the methods and functions their fields resolve through.
// It writes gql_<label>.go for each entity with a GraphQL type (its types,
// its edge methods, its root connection function), and gql_node.go with
// Node and Nodes.
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
	gqlimplPath  = "github.com/syssam/velox2/contrib/graphql/veloxgql/gqlimpl"
)

// Render returns the Go files of m's GraphQL types, for the generated
// package outPkg at the import path outPath, without the generated-file
// header, which velox-gen adds. The output depends only on its arguments.
//
// It fails on a model sdl.Build refuses, an import of the application's Go
// types it cannot name, and a Go name it writes that velox-gen also declares
// in the package. Which types are Nodes, the Go types of where predicates
// and which fields bind to getters are sdl.Build's to decide.
func Render(m *plugin.Model, outPath, outPkg string) ([]plugin.File, error) {
	if outPkg == "" {
		return nil, fmt.Errorf("velox-graphql: the request names no generated package")
	}
	s, err := sdl.Build(m)
	if err != nil {
		return nil, err
	}
	r := &renderer{m: m, outPath: outPath, outPkg: outPkg, veloxEnums: map[string]bool{}, names: map[string]string{}, types: map[string]*sdl.Type{}}
	for _, t := range s.Types {
		r.types[t.Name] = t
	}
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
	veloxEnums map[string]bool      // the enum types velox-gen declares
	names      map[string]string    // Go name in the package → what declares it
	types      map[string]*sdl.Type // the GraphQL types by name
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
func (f *file) gqlimpl() string  { return f.use(gqlimplPath, "gqlimpl") }

// finish is the file's Go source, gofmt'd: the package clause, the imports
// in two groups (the standard library, then the rest), the version guard
// and the body.
func (f *file) finish(name, pkg string) (plugin.File, error) {
	guard := f.veloxgql() // the guard imports it, whatever the body uses
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
	// Generated code and veloxgql fail to compile together when they
	// disagree on the version, as the core's files do.
	fmt.Fprintf(&src, "const _ = %s.GeneratedCodeIsVersion1\n\n", guard)
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
	for _, g := range t.Getters {
		if err := r.writeGetter(f, t, g); err != nil {
			return plugin.File{}, err
		}
	}
	for _, en := range t.Enums {
		if err := r.writeEnum(f, t, en); err != nil {
			return plugin.File{}, err
		}
	}
	if err := r.writeEdges(f, t); err != nil {
		return plugin.File{}, err
	}
	if err := r.writeConnection(f, t); err != nil {
		return plugin.File{}, err
	}
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

func (r *renderer) writeNode(f *file, t *sdl.Type) error {
	e := t.Entity
	k := e.Fields[e.PrimaryKey[0]]
	v := "x." + k.GoName
	if elem := cmp.Or(k.ElemType, t.Key); elem != t.Key {
		if err := r.converts(e, k, t.Key); err != nil {
			return err
		}
		v = t.Key + "(" + v + ")"
	}
	f.doc("", fmt.Sprintf("IsNode marks %s as a GraphQL Node: gqlgen binds the interface Node to veloxgql.Noder.", e.Name))
	f.p("func (*%s) IsNode() {}", e.Name)
	f.p("")
	f.doc("", fmt.Sprintf("IDGQL is the global ID of the %s, the GraphQL field id: the type %s and the key %s, encoded.", e.Name, t.Name, k.Name))
	f.p("func (x *%s) IDGQL() string { return %s.EncodeID(%q, %s) }", e.Name, f.veloxgql(), t.Name, v)
	f.p("")
	return nil
}

// writeGetter writes g, which returns its field as the Go type the field's
// scalar binds to.
func (r *renderer) writeGetter(f *file, t *sdl.Type, g sdl.Getter) error {
	e := t.Entity
	fd := e.Fields[g.Field]
	ret, err := r.qualify(f, e, fd, g.Returns, g.Pkg)
	if err != nil {
		return err
	}
	if !g.JSON() {
		if err := r.converts(e, fd, g.Returns); err != nil {
			return err
		}
	}
	if g.JSON() {
		f.doc("", fmt.Sprintf("%s is the field %s as JSON, the GraphQL scalar it is: gqlgen binds the field to it.", g.Method, fd.Name))
		f.p("func (x *%s) %s() (%s, error) {", e.Name, g.Method, ret)
		f.p("	b, err := %s.Marshal(x.%s)", f.use("encoding/json", "json"), fd.GoName)
		f.p("	return %s(b), err", ret)
		f.p("}")
		f.p("")
		return nil
	}
	f.doc("", fmt.Sprintf("%s is the field %s as a %s, the Go type its GraphQL scalar binds to: gqlgen binds the field to it.", g.Method, fd.Name, ret))
	if !fd.Pointer {
		f.p("func (x *%s) %s() %s { return %s(x.%s) }", e.Name, g.Method, ret, ret, fd.GoName)
		f.p("")
		return nil
	}
	f.p("func (x *%s) %s() *%s {", e.Name, g.Method, ret)
	f.p("	if x.%s == nil {", fd.GoName)
	f.p("		return nil")
	f.p("	}")
	f.p("	v := %s(*x.%s)", ret, fd.GoName)
	f.p("	return &v")
	f.p("}")
	f.p("")
	return nil
}

// locals are the names the generated code declares inside its functions,
// and the packages and builtins it names: an imported package of the
// application's named one of them is imported under another name, so that
// none shadows the other.
var locals = map[string]bool{
	"ctx": true, "id": true, "ids": true, "typ": true, "err": true, "k": true, "db": true, "x": true,
	"rows": true, "by": true, "i": true, "j": true, "out": true, "c": true, "n": true, "e": true,
	"s": true, "b": true, "w": true, "v": true, "o": true, "f": true,
	"context": true, "json": true, "errors": true, "fmt": true, "io": true, "velox": true, "veloxgql": true, "gqlimpl": true,
	"any": true, "bool": true, "string": true, "int": true, "len": true, "make": true, "append": true,
	"nil": true, "error": true, "true": true, "false": true,
}

// qualify is the Go type typ of the package at pkg, as the generated file
// names it: typ as it is when it names no package, unqualified when pkg is
// the generated package, else with pkg imported. The qualifier is typ's own,
// the package's name, unless it is one of locals, when the import is named
// <qualifier>pkg. With no pkg, the qualifier names one of the field's
// imports, as for a join entity's field, which holds another entity's ID.
func (r *renderer) qualify(f *file, e *plugin.Entity, fd *plugin.Field, typ, pkg string) (string, error) {
	q, name, ok := strings.Cut(typ, ".")
	switch {
	case !ok:
		return typ, nil
	case strings.ContainsAny(q, "[]*"):
		return "", fmt.Errorf("schema %s, field %s: velox-graphql cannot name the Go type %s", e.Name, fd.Name, typ)
	}
	if pkg == "" {
		for _, imp := range fd.Imports {
			if path.Base(imp) == q {
				pkg = imp
			}
		}
	}
	switch pkg {
	case "":
		return "", fmt.Errorf("schema %s, field %s: the model gives no package for the Go type %s", e.Name, fd.Name, typ)
	case r.outPath:
		return name, nil
	}
	if prev, ok := f.imports[pkg]; ok {
		return prev + "." + name, nil
	}
	if locals[q] && pkg != veloxPath && pkg != veloxgqlPath && pkg != gqlimplPath && !slices.Contains([]string{"context", "encoding/json", "errors", "fmt", "io"}, pkg) {
		q += "pkg"
	}
	return f.use(pkg, q) + "." + name, nil
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
	json, io := f.use("encoding/json", "json"), f.use("io", "io")
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
		f.p("\treturn %s.InputErrorf(%q)", f.gqlimpl(), strings.ReplaceAll(typ+" must be "+orList(fd.EnumValues), "%", "%%"))
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
	f.p("\t\treturn %s.InputErrorf(%q)", f.gqlimpl(), strings.ReplaceAll(typ+" must be "+orList(en.Values), "%", "%%"))
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
	f.p("\t\treturn %s.InputErrorf(%q)", f.gqlimpl(), strings.ReplaceAll(n+"OrderField must be "+orList(values), "%", "%%"))
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
		if w.GoName == "Pred" {
			return fmt.Errorf("schema %s: the field %s of %s is the Go field Pred, which is the input's method; rename it with graphql.FieldName", e.Name, w.Name, in)
		}
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
				f.doc("\t", fmt.Sprintf("The edge %s: whether it has a node, or one that matches.", named(e.Edges[w.Edge].Name, w.On)))
			case w.Edge >= 0:
				f.doc("\t", fmt.Sprintf("Predicates on the edge %s, by the ID of its node.", named(e.Edges[w.Edge].Name, w.On)))
			case w.IDs():
				f.p("\t// Predicates on the ID.")
			default:
				f.doc("\t", fmt.Sprintf("Predicates on the field %s.", named(e.Fields[w.Field].Name, w.On)))
			}
		}
		f.p("\t%s %s", w.GoName, typ)
	}
	f.p("}")
	return r.writePred(f, t)
}

// whereType is the Go type of the where-input field w: an input for not and
// has<Edge>With, else w.GoType, a pointer for one value and a slice for a
// list.
func (r *renderer) whereType(f *file, t *sdl.Type, w sdl.WhereField) (string, error) {
	switch w.Kind {
	case sdl.WhereNot:
		return "*" + t.Name + "WhereInput", nil
	case sdl.WhereAnd, sdl.WhereOr:
		return "[]*" + t.Name + "WhereInput", nil
	case sdl.WhereHasWith:
		return "[]*" + w.Target + "WhereInput", nil
	}
	var fd *plugin.Field
	if w.Field >= 0 {
		fd = t.Entity.Fields[w.Field]
	} else {
		fd = &plugin.Field{Name: w.On}
	}
	elem, err := r.qualify(f, t.Entity, fd, w.GoType, w.GoPkg)
	if err != nil {
		return "", err
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
		k := t.Entity.Fields[t.Entity.PrimaryKey[0]]
		elem := cmp.Or(k.ElemType, t.Key)
		nd := node{t: t, base: t.Key, conv: "k"}
		var err error
		if nd.key, err = r.qualify(f, t.Entity, k, elem, k.PkgPath); err != nil {
			return plugin.File{}, err
		}
		if elem != t.Key {
			nd.conv = nd.key + "(k)"
		}
		nodes = append(nodes, nd)
	}
	noType := func(fn, indent string) {
		f.p("%sreturn nil, %s.InputErrorf(\"%s: %%w: the ID is of no Node type\", %s.ErrWrongIDType)", indent, f.gqlimpl(), fn, vgql)
	}

	if len(nodes) == 0 {
		f.p("// Node is the root field node. No type of the schema is a Node, so every")
		f.p("// ID fails: with veloxgql.ErrWrongIDType, or with veloxgql.ErrBadID when")
		f.p("// it is malformed.")
		f.p("func Node(_ %s.Context, id string) (%s.Noder, error) {", ctx, vgql)
		f.p("\tif _, err := %s.IDType(id); err != nil {", vgql)
		f.p("\t\treturn nil, err")
		f.p("\t}")
		noType("Node", "\t")
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
		noType("Node", "\t")
		f.p("}")
	}

	f.p("")
	tooMany := func() {
		f.p("\tif most := %s.LimitsFrom(ctx).MaxPage; len(ids) > most {", vgql)
		f.p("\t\treturn nil, %s.InputErrorf(\"Nodes: %%w: %%d IDs, and the most is %%d\", %s.ErrPageTooLarge, len(ids), most)", f.gqlimpl(), vgql)
		f.p("\t}")
	}
	if len(nodes) == 0 {
		f.p("// Nodes is the root field nodes. No type of the schema is a Node, so an")
		f.p("// ID fails as in Node, and so do more IDs than the MaxPage of")
		f.p("// veloxgql.LimitsFrom(ctx), with veloxgql.ErrPageTooLarge.")
		f.p("func Nodes(ctx %s.Context, ids []string) ([]%s.Noder, error) {", ctx, vgql)
		tooMany()
		f.p("\tfor _, id := range ids {")
		f.p("\t\tif _, err := %s.IDType(id); err != nil {", vgql)
		f.p("\t\t\treturn nil, err")
		f.p("\t\t}")
		noType("Nodes", "\t\t")
		f.p("\t}")
		f.p("\treturn []%s.Noder{}, nil", vgql)
		f.p("}")
		return f.finish("gql_node.go", r.outPkg)
	}
	f.p("// Nodes returns the nodes with the global IDs ids, in their order, with nil")
	f.p("// for an ID no row has. It decodes every ID first, and then looks up each")
	f.p("// type's nodes with one query, through the DB veloxgql.DBFrom reads from")
	f.p("// ctx; the query reads at most one row for each of its IDs, whatever the")
	f.p("// DB's WithMaxRows. It fails as Node does on an ID, before any query runs,")
	f.p("// and with veloxgql.ErrPageTooLarge on more IDs than the MaxPage of")
	f.p("// veloxgql.LimitsFrom(ctx).")
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
	noType("Nodes", "\t\t\t")
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
		f.p("\t\trows, err := db.Query[%s]().Where(%s.%s.In(keys%s...)).Unbounded().MaxRows(len(keys%[4]s)).All(ctx)", e.Name, e.Handles, pk, nd.t.Name)
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

// named is a schema element's name, with its GraphQL name when that differs:
// "name (displayName)".
func named(name, gql string) string {
	if gql == "" || gql == name {
		return name
	}
	return name + " (" + gql + ")"
}
