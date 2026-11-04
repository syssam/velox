package codegen

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/syssam/velox/internal/codegen/loadjson"
)

// MixinBound is a mixin of an entity that declares behaviour: a generic
// Policy, Hooks or Interceptors method, which the generated code
// instantiates for the entity.
type MixinBound struct {
	Index   int    // the mixin's position in the schema's Mixin method
	PkgPath string // the import path of the mixin's package
	PkgName string // that package's name
	Name    string // the mixin's type name
	// NonZero says the value Mixin returns is not the zero value of its
	// type: the generated code reads it with schema.MixinAt, rather than
	// write a new one.
	NonZero bool
	// Methods are the kinds the mixin declares ("Policy", "Hooks",
	// "Interceptors"), each with whether its receiver is a pointer.
	Methods map[string]bool
	// Fields are the velox.Field calls of its methods' bodies.
	Fields []MixinField
}

// MixinField is a velox.Field[E, V](name) call in a mixin's method.
type MixinField struct {
	Pos  string // file:line of the call
	Call string // the call as written, for errors: velox.Field[E, int64]("org_id")
	Name string // the column's name: the literal's value
	// Type is V, with each package named by its import path, such as
	// "*time.Time" → "*time.Time" or "uuid.UUID" →
	// "github.com/google/uuid.UUID"; "" when velox-gen cannot read it.
	Type string
}

// MixinBehaviour finds the Policy, Hooks and Interceptors methods of the
// mixins g's entities list, and returns, per entity name, the mixins that
// declare any, in Mixin order. A mixin's methods are read from the source of
// its package, which go list, run in the module that holds dir, finds; the
// schema package is the one pattern names: generic methods are in no method set, so neither the
// loader nor reflect sees them. A method a mixin has through an embedded
// struct counts.
//
// A mixin's behaviour method has one type parameter and no other
// parameter, and returns velox.Policy[E], []velox.Hook[E] or
// []velox.Interceptor[E]:
//
//	func (InOrg) Policy[E any]() velox.Policy[E]
//
// Another shape is a *FileError at its line, and so is a behaviour method
// on a mixin that is itself a generic type, whose instance velox-gen could
// not name. A mixin with behaviour listed twice by one entity is an *Error.
//
// Each velox.Field call in a method's body whose name is a string literal
// is checked against every entity that lists the mixin (see Lower); a
// call whose name is not a literal is left to the check velox makes when it
// builds the metadata, and is a warning, with its line.
func MixinBehaviour(dir, pattern string, g *Graph, env []string) (map[string][]MixinBound, []string, error) {
	r := &mixinReader{dir: dir, env: env, dirs: map[string]string{}, pkgs: map[string]*mixinPkg{}}
	var paths []string
	for _, e := range g.Entities {
		for _, m := range e.Mixins {
			if m.PkgPath != "" && !noBehaviour[m.PkgPath] && !slices.Contains(paths, m.PkgPath) {
				paths = append(paths, m.PkgPath)
			}
		}
	}
	if len(paths) == 0 {
		return nil, nil, nil
	}
	if slices.Contains(paths, g.Package) {
		pkg, err := list(dir, pattern, env)
		if err != nil {
			return nil, nil, err
		}
		r.dirs[g.Package] = pkg.Dir
		paths = slices.DeleteFunc(paths, func(p string) bool { return p == g.Package })
	}
	if len(paths) > 0 {
		dirs, err := listDirs(dir, paths, env)
		if err != nil {
			return nil, nil, err
		}
		maps.Copy(r.dirs, dirs)
	}
	out := map[string][]MixinBound{}
	var warnings []string
	warned := map[string]bool{}
	for _, e := range g.Entities {
		seen := map[string]bool{}
		for i, m := range e.Mixins {
			if m.Name == "" {
				continue
			}
			found, err := r.methods(m.PkgPath, m.Name)
			if err != nil {
				return nil, nil, err
			}
			if len(found) == 0 {
				continue
			}
			key := m.PkgPath + "." + m.Name
			if seen[key] {
				return nil, nil, &Error{Schema: e.Name, Reason: fmt.Sprintf("Mixin()[%d] is %s again, which declares behaviour: list a mixin once", i, m.Name)}
			}
			seen[key] = true
			mb := MixinBound{Index: i, PkgPath: m.PkgPath, PkgName: r.pkgs[m.PkgPath].name, Name: m.Name, NonZero: m.NonZero, Methods: map[string]bool{}}
			for _, k := range behaviourKinds {
				fm, ok := found[k]
				if !ok {
					continue
				}
				mb.Methods[k] = fm.ptr
				for _, c := range fm.fields {
					if c.literal {
						mb.Fields = append(mb.Fields, c.MixinField)
					} else if !warned[c.Pos] {
						warned[c.Pos] = true
						warnings = append(warnings, fmt.Sprintf("%s: %s names its column with a value velox-gen cannot read; velox checks it when it builds each entity's metadata", c.Pos, c.Call))
					}
				}
			}
			out[e.Name] = append(out[e.Name], mb)
		}
	}
	return out, warnings, nil
}

// listDirs returns the directories of the packages at paths, by go list run
// in dir.
func listDirs(dir string, paths []string, env []string) (map[string]string, error) {
	out, stderr, err := run(dir, env, append([]string{"list", "-e", "-find", "-json=ImportPath,Dir,Error"}, paths...)...)
	if err != nil {
		return nil, fmt.Errorf("go list %s: %w\n%s", strings.Join(paths, " "), err, strings.TrimSpace(stderr))
	}
	dirs := map[string]string{}
	dec := json.NewDecoder(strings.NewReader(out))
	for dec.More() {
		var p struct {
			ImportPath, Dir string
			Error           *struct{ Err string }
		}
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("reading go list output: %w", err)
		}
		if p.Error != nil && p.Dir == "" {
			return nil, fmt.Errorf("finding mixin package %s: %s", p.ImportPath, p.Error.Err)
		}
		dirs[p.ImportPath] = p.Dir
	}
	return dirs, nil
}

// mixinReader reads the source of the packages that declare mixins.
type mixinReader struct {
	dir  string               // where go list runs
	env  []string             // its environment
	dirs map[string]string    // import path → directory
	pkgs map[string]*mixinPkg // import path → its parsed files
}

// mixinPkg is a parsed package.
type mixinPkg struct {
	path, name string
	fset       *token.FileSet
	types      map[string]*mixinType // by name
	methods    map[string][]*mixinMethodDecl
}

type mixinType struct {
	spec *ast.TypeSpec
	file *ast.File
	path string // the file's path
}

type mixinMethodDecl struct {
	fd   *ast.FuncDecl
	file *ast.File
	path string
}

// foundMethod is a behaviour method a mixin has.
type foundMethod struct {
	ptr    bool // a pointer receiver
	fields []fieldCall
}

type fieldCall struct {
	MixinField
	literal bool
}

// pkg parses the package at path, once.
func (r *mixinReader) pkg(path string) (*mixinPkg, error) {
	if p, ok := r.pkgs[path]; ok {
		return p, nil
	}
	dir, ok := r.dirs[path]
	if !ok {
		dirs, err := listDirs(r.dir, []string{path}, r.env)
		if err != nil {
			return nil, err
		}
		dir = dirs[path]
		r.dirs[path] = dir
	}
	p := &mixinPkg{path: path, types: map[string]*mixinType{}, methods: map[string][]*mixinMethodDecl{}}
	err := userFiles(dir, 0, func(fset *token.FileSet, fp string, f *ast.File) error {
		p.fset, p.name = fset, f.Name.Name
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				for _, sp := range d.Specs {
					if ts, ok := sp.(*ast.TypeSpec); ok {
						p.types[ts.Name.Name] = &mixinType{spec: ts, file: f, path: fp}
					}
				}
			case *ast.FuncDecl:
				if d.Recv == nil || !slices.Contains(behaviourMethods, d.Name.Name) {
					continue
				}
				if t := recvType(d); t != "" {
					p.methods[t] = append(p.methods[t], &mixinMethodDecl{fd: d, file: f, path: fp})
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if p.name == "" {
		p.name = guessName(path)
	}
	r.pkgs[path] = p
	return p, nil
}

// methods returns the behaviour methods of the type name of the package at
// path, its own and those it has through embedded structs, by kind; a
// method of a shape velox-gen does not bind is a *FileError.
func (r *mixinReader) methods(path, name string) (map[string]foundMethod, error) {
	found := map[string]foundMethod{}
	depth := map[string]int{}
	var walk func(path, name string, d int, seen map[string]bool) error
	walk = func(path, name string, d int, seen map[string]bool) error {
		key := path + "." + name
		if seen[key] || noBehaviour[path] {
			return nil
		}
		seen[key] = true
		p, err := r.pkg(path)
		if err != nil {
			return err
		}
		t := p.types[name]
		for _, m := range p.methods[name] {
			k := m.fd.Name.Name
			if old, ok := depth[k]; ok && old <= d {
				continue
			}
			pos := p.fset.Position(m.fd.Pos())
			if t != nil && t.spec.TypeParams != nil {
				return &FileError{Path: m.path, Line: pos.Line, Reason: fmt.Sprintf("%s.%s: %s is a generic type, whose instance velox-gen cannot name; declare the behaviour on a mixin that is not generic", name, k, name)}
			}
			calls, err := mixinMethodShape(p, m, name)
			if err != nil {
				return err
			}
			found[k] = foundMethod{ptr: recvPtr(m.fd), fields: calls}
			depth[k] = d
		}
		if t == nil {
			return nil
		}
		st, ok := t.spec.Type.(*ast.StructType)
		if !ok {
			return nil
		}
		for _, f := range st.Fields.List {
			if len(f.Names) != 0 {
				continue
			}
			ft := f.Type
			if s, ok := ft.(*ast.StarExpr); ok {
				ft = s.X
			}
			switch x := ft.(type) {
			case *ast.Ident:
				if err := walk(path, x.Name, d+1, seen); err != nil {
					return err
				}
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok {
					if ip := importPath(t.file, id.Name, r); ip != "" {
						if err := walk(ip, x.Sel.Name, d+1, seen); err != nil {
							return err
						}
					}
				}
			}
		}
		return nil
	}
	if err := walk(path, name, 0, map[string]bool{}); err != nil {
		return nil, err
	}
	return found, nil
}

// recvPtr reports whether fd's receiver is a pointer.
func recvPtr(fd *ast.FuncDecl) bool {
	if fd.Recv == nil || len(fd.Recv.List) != 1 {
		return false
	}
	_, ok := fd.Recv.List[0].Type.(*ast.StarExpr)
	return ok
}

// importPath returns the import path f names local by, "" when it names
// none. A package imported without a name is found by its package name,
// which r reads, or its path's last element.
func importPath(f *ast.File, local string, r *mixinReader) string {
	for _, im := range f.Imports {
		p, _ := strconv.Unquote(im.Path.Value) // the parser checked the literal
		if im.Name != nil {
			if im.Name.Name == local {
				return p
			}
			continue
		}
		if guessName(p) == local {
			return p
		}
		if r != nil {
			if pk, err := r.pkg(p); err == nil && pk.name == local {
				return p
			}
		}
	}
	return ""
}

// mixinMethodShape checks that m, a behaviour method of mixin name, has one
// type parameter, constrained by any, takes nothing and returns the
// behaviour of its type parameter, and returns the velox.Field calls of its
// body.
func mixinMethodShape(p *mixinPkg, m *mixinMethodDecl, name string) ([]fieldCall, error) {
	k := m.fd.Name.Name
	want := mixinResult(k)
	fail := func(why string) error {
		return &FileError{Path: m.path, Line: p.fset.Position(m.fd.Pos()).Line, Reason: fmt.Sprintf("%s.%s %s; a mixin serves every entity that lists it, so its method is generic over the entity: func (%[1]s) %[2]s[E any]() %[4]s", name, k, why, want)}
	}
	t := m.fd.Type
	if t.TypeParams == nil {
		return nil, fail("is not generic")
	}
	if len(t.TypeParams.List) != 1 || len(t.TypeParams.List[0].Names) != 1 || !isAny(t.TypeParams.List[0].Type) {
		return nil, fail("must have one type parameter, constrained by any")
	}
	e := t.TypeParams.List[0].Names[0].Name
	if len(t.Params.List) != 0 {
		return nil, fail("must take nothing")
	}
	velox := localName(m.file, veloxPkg)
	if t.Results == nil || len(t.Results.List) != 1 || len(t.Results.List[0].Names) > 1 || !behaviourOf(t.Results.List[0].Type, k, velox, e) {
		return nil, fail("must return " + strings.ReplaceAll(want, "[E]", "["+e+"]"))
	}
	var calls []fieldCall
	if m.fd.Body == nil {
		return nil, nil
	}
	ast.Inspect(m.fd.Body, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		ix, ok := c.Fun.(*ast.IndexListExpr)
		if !ok || len(ix.Indices) != 2 || len(c.Args) != 1 {
			return true
		}
		sel, ok := ix.X.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Field" {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); !ok || id.Name != velox {
			return true
		}
		var fc fieldCall
		fc.Pos = fmt.Sprintf("%s:%d", m.path, p.fset.Position(c.Pos()).Line)
		fc.Call = exprString(c)
		fc.Type = canonicalType(ix.Indices[1], func(n string) string { return importPath(m.file, n, nil) }, p.path)
		if lit, ok := c.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s, err := strconv.Unquote(lit.Value); err == nil {
				fc.Name, fc.literal = s, true
			}
		}
		calls = append(calls, fc)
		return true
	})
	return calls, nil
}

// isAny reports whether x is any or interface{}.
func isAny(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.Ident:
		return x.Name == "any"
	case *ast.InterfaceType:
		return len(x.Methods.List) == 0
	}
	return false
}

// localName is the name f gives the package at path, "" when f does not
// import it by a usable name.
func localName(f *ast.File, path string) string {
	for _, im := range f.Imports {
		if p, _ := strconv.Unquote(im.Path.Value); p == path {
			if im.Name == nil {
				return guessName(path)
			}
			if im.Name.Name == "." || im.Name.Name == "_" {
				return ""
			}
			return im.Name.Name
		}
	}
	return ""
}

// behaviourOf reports whether x is velox.Policy[e], []velox.Hook[e] or
// []velox.Interceptor[e], as kind k says, velox being velox's name in the
// file.
func behaviourOf(x ast.Expr, k, velox, e string) bool {
	name := "Policy"
	if k != "Policy" {
		at, ok := x.(*ast.ArrayType)
		if !ok || at.Len != nil {
			return false
		}
		x, name = at.Elt, strings.TrimSuffix(k, "s")
	}
	ix, ok := x.(*ast.IndexExpr)
	if !ok {
		return false
	}
	sel, ok := ix.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	if id, ok := sel.X.(*ast.Ident); !ok || id.Name != velox || velox == "" {
		return false
	}
	id, ok := ix.Index.(*ast.Ident)
	return ok && id.Name == e
}

// predeclared are Go's predeclared type names.
var predeclared = map[string]bool{"bool": true, "byte": true, "complex64": true, "complex128": true, "error": true,
	"float32": true, "float64": true, "int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"rune": true, "string": true, "uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"uintptr": true, "any": true}

// canonicalType writes the type x with each named type qualified by its
// package's import path, such as "*time.Time" or "[]example.com/x.Tag":
// qual gives the import path of a package name, and local the path of a
// type x names without one. It returns "" for a type velox-gen does not
// read, such as a func or a struct.
func canonicalType(x ast.Expr, qual func(string) string, local string) string {
	switch x := x.(type) {
	case *ast.Ident:
		switch {
		case x.Name == "byte":
			return "uint8"
		case x.Name == "rune":
			return "int32"
		case predeclared[x.Name]:
			return x.Name
		}
		return local + "." + x.Name
	case *ast.StarExpr:
		if s := canonicalType(x.X, qual, local); s != "" {
			return "*" + s
		}
	case *ast.ArrayType:
		if x.Len == nil {
			if s := canonicalType(x.Elt, qual, local); s != "" {
				return "[]" + s
			}
		}
	case *ast.MapType:
		k, v := canonicalType(x.Key, qual, local), canonicalType(x.Value, qual, local)
		if k != "" && v != "" {
			return "map[" + k + "]" + v
		}
	case *ast.InterfaceType:
		if len(x.Methods.List) == 0 {
			return "any"
		}
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok {
			if p := qual(id.Name); p != "" {
				return p + "." + x.Sel.Name
			}
		}
	}
	return ""
}

// exprString writes x as Go source.
func exprString(x ast.Expr) string {
	var b strings.Builder
	_ = printer.Fprint(&b, token.NewFileSet(), x)
	return b.String()
}

// noBehaviour are velox's packages whose types a mixin embeds, which
// declare no behaviour methods: velox-gen does not read them.
var noBehaviour = map[string]bool{loadjson.SchemaPath: true, loadjson.SchemaPath + "/mixin": true}
