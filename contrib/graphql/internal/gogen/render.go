package gogen

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/syssam/velox/contrib/graphql/internal/english"
	"github.com/syssam/velox/contrib/graphql/internal/sdl"
	"github.com/syssam/velox/plugin"
)

// The import paths of the packages the generated code uses.
const (
	veloxPath    = "github.com/syssam/velox"
	veloxgqlPath = "github.com/syssam/velox/contrib/graphql/veloxgql"
	gqlgraphPath = "github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	untypedPath  = "github.com/syssam/velox/untyped"
)

// Render returns the Go files of m's GraphQL types, for the generated
// package outPkg at the import path outPath, without the generated-file
// header, which velox-gen adds. The output depends only on its arguments.
//
// It fails on a model sdl.Build refuses, an import of the application's Go
// types it cannot name, and a Go name it writes that velox-gen also declares
// in the package. Which types have an ID, the Go types of where predicates
// and which fields bind to getters are sdl.Build's to decide.
func Render(m *plugin.Model, outPath, outPkg string) ([]plugin.File, error) {
	return RenderConfig(m, outPath, outPkg, sdl.Config{})
}

// RenderConfig is Render under the options cfg, which must be those the SDL
// is rendered under.
func RenderConfig(m *plugin.Model, outPath, outPkg string, cfg sdl.Config) ([]plugin.File, error) {
	if outPkg == "" {
		return nil, fmt.Errorf("velox-graphql: the request names no generated package")
	}
	s, err := sdl.BuildConfig(m, cfg)
	if err != nil {
		return nil, err
	}
	r := newRenderer(m, outPath, outPkg)
	r.veloxe, r.graphqlGo = cfg.Veloxe, cfg.GraphQLGo
	for _, t := range s.Types {
		r.types[t.Name] = t
		r.typeOf[t.Entity.Name] = t
	}
	files := make([]plugin.File, 0, len(s.Types))
	for _, t := range s.Types {
		e := t.Entity
		if e.Label == "" || e.Handles == "" {
			return nil, fmt.Errorf("schema %s: the model gives it no label or no handles, which velox-gen names its Go after", e.Name)
		}
		f, err := r.typeFile(t)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	if r.veloxe {
		f, ok, err := r.nodeFile(s.Types)
		if err != nil {
			return nil, err
		}
		if ok {
			files = append(files, f)
		}
	}
	slices.SortFunc(files, func(a, b plugin.File) int { return cmp.Compare(a.Path, b.Path) })
	return files, nil
}

// RenderEnums returns, for an application that writes its own SDL, only
// the GraphQL marshalers of velox-gen's enum types: <label>_gql.go for each
// entity that sdl.Enums gives enums. A marshaler writes the value
// sdl.Enums computes, the stored one upper-cased unless graphql.EnumValues
// renames it, and reads it back; gqlgen's autobind finds them on the type.
func RenderEnums(m *plugin.Model, outPath, outPkg string) ([]plugin.File, error) {
	if outPkg == "" {
		return nil, fmt.Errorf("velox-graphql: the request names no generated package")
	}
	ees, err := sdl.Enums(m)
	if err != nil {
		return nil, err
	}
	r := newRenderer(m, outPath, outPkg)
	files := make([]plugin.File, 0, len(ees))
	for _, ee := range ees {
		e := ee.Entity
		if e.Label == "" {
			return nil, fmt.Errorf("schema %s: the model gives it no label, which velox-gen names its Go after", e.Name)
		}
		f := newFile()
		for _, en := range ee.Enums {
			if err := r.writeEnum(f, e, en); err != nil {
				return nil, err
			}
		}
		file, err := f.finish(e.Label+"_gql.go", outPkg)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	slices.SortFunc(files, func(a, b plugin.File) int { return cmp.Compare(a.Path, b.Path) })
	return files, nil
}

// newRenderer returns the renderer of m with its entities and velox-gen's
// enum types indexed and velox-gen's Go names reserved.
func newRenderer(m *plugin.Model, outPath, outPkg string) *renderer {
	r := &renderer{m: m, outPath: outPath, outPkg: outPkg, veloxEnums: map[string]*plugin.Enum{}, names: map[string]string{}, types: map[string]*sdl.Type{}, typeOf: map[string]*sdl.Type{}, ents: map[string]*plugin.Entity{}}
	for _, e := range m.Entities {
		r.ents[e.Name] = e
	}
	for _, en := range m.Enums {
		r.veloxEnums[en.Type] = en
	}
	r.reserve()
	return r
}

// pkgNames are the names of the packages the generated code imports whose
// name is not their path's last element, which need no import name.
var pkgNames = map[string]string{veloxPath: "velox"}

type renderer struct {
	m          *plugin.Model
	outPath    string
	outPkg     string
	veloxEnums map[string]*plugin.Enum // the enum types velox-gen declares
	names      map[string]string       // Go name in the package → what declares it
	types      map[string]*sdl.Type    // the GraphQL types by name
	typeOf     map[string]*sdl.Type    // the GraphQL types by their entity's name
	ents       map[string]*plugin.Entity
	veloxe     bool // sdl.Config.Veloxe: veloxe's where semantics and Node
	graphqlGo  bool // sdl.Config.GraphQLGo: inputs graphql-go binds by name
}

// reserve records the Go names velox-gen declares in the package that a name
// the plugin writes could take: the entities' types, the handles variables
// and the enum types.
func (r *renderer) reserve() {
	for _, e := range r.m.Entities {
		r.names[e.Name] = "velox-gen's type of schema " + e.Name
		r.names[e.Handles] = "velox-gen's handles of schema " + e.Name
		// A method of the plugin's on a row may not take a name its struct
		// holds, or a method velox-gen writes. The relations are in the
		// struct's Edges field, which is there when the entity has one.
		for _, f := range e.Fields {
			r.names[e.Name+"."+f.GoName] = "velox-gen's field " + f.Name + " of schema " + e.Name
		}
		if len(e.Edges) > 0 {
			r.names[e.Name+".Edges"] = "velox-gen's field Edges of schema " + e.Name
		}
		for _, m := range []string{"String", "GoString"} {
			r.names[e.Name+"."+m] = "velox-gen's method " + m + " of schema " + e.Name
		}
		// velox-gen's unexported names, which postGQL could be.
		if e.Label != "" {
			for _, s := range []string{"Descriptor", "Handles", "Meta"} {
				r.names[labelCamel(e.Label)+s] = "velox-gen's " + labelCamel(e.Label) + s
			}
			for _, f := range e.Fields {
				r.names[colConst(e, f)] = "velox-gen's column constant of field " + f.Name + " of schema " + e.Name
			}
			for _, ed := range e.Edges {
				r.names[edgeConst(e, ed)] = "velox-gen's relation constant of edge " + ed.Name + " of schema " + e.Name
			}
		}
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

// typeFile is <label>_gql.go, t's Go types.
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
	for _, g := range t.Getters {
		if err := r.writeGetter(f, t, g); err != nil {
			return plugin.File{}, err
		}
	}
	for _, en := range t.Enums {
		if err := r.writeEnum(f, t.Entity, en); err != nil {
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
	if err := r.writeMutations(f, t); err != nil {
		return plugin.File{}, err
	}
	return f.finish(e.Label+"_gql.go", r.outPkg)
}

// writeGetter writes g, which returns its field as the Go type the field's
// scalar binds to.
func (r *renderer) writeGetter(f *file, t *sdl.Type, g sdl.Getter) error {
	e := t.Entity
	if err := r.declare(e.Name+"."+g.Method, "schema "+e.Name+", the getter of field "+e.Fields[g.Field].Name); err != nil {
		return err
	}
	fd, x := e.Fields[g.Field], recv(e)
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
		f.p("func (%s *%s) %s() (%s, error) {", x, e.Name, g.Method, ret)
		f.p("	b, err := %s.Marshal(%s.%s)", f.use("encoding/json", "json"), x, fd.GoName)
		f.p("	return %s(b), err", ret)
		f.p("}")
		f.p("")
		return nil
	}
	f.doc("", fmt.Sprintf("%s is the field %s as %s %s, the Go type its GraphQL scalar binds to: gqlgen binds the field to it.", g.Method, fd.Name, english.Article(ret), ret))
	if fd.Type == "bytes" && !fd.Nullable {
		// Its column is NOT NULL, so nil is an empty blob, which gqlgen would
		// refuse in a Bytes! as a null.
		f.p("func (%s *%s) %s() %s {", x, e.Name, g.Method, ret)
		f.p("	if %s.%s == nil {", x, fd.GoName)
		f.p("		return %s{}", ret)
		f.p("	}")
		f.p("	return %s(%s.%s)", ret, x, fd.GoName)
		f.p("}")
		f.p("")
		return nil
	}
	if !fd.Pointer {
		f.p("func (%s *%s) %s() %s { return %s(%s.%s) }", x, e.Name, g.Method, ret, ret, x, fd.GoName)
		f.p("")
		return nil
	}
	f.p("func (%s *%s) %s() *%s {", x, e.Name, g.Method, ret)
	f.p("	if %s.%s == nil {", x, fd.GoName)
	f.p("		return nil")
	f.p("	}")
	f.p("	v := %s(*%s.%s)", ret, x, fd.GoName)
	f.p("	return &v")
	f.p("}")
	f.p("")
	return nil
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
	if (locals[q] || indexedLocal(q)) && pkg != veloxPath && pkg != veloxgqlPath && pkg != gqlgraphPath && pkg != untypedPath && !slices.Contains([]string{"context", "encoding/json", "errors", "fmt", "io"}, pkg) {
		q += "pkg"
	}
	return f.use(pkg, q) + "." + name, nil
}
