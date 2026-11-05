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

// MixinBound is a mixin of an entity that declares behaviour: a Policy,
// Hooks or Interceptors method, which the generated code binds for the
// entity.
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
}

// MixinBehaviour finds the Policy, Hooks and Interceptors methods of the
// mixins g's entities list, and returns, per entity name, the mixins that
// declare any, in Mixin order. A mixin's methods are read from the source of
// its package, which go list, run in the module that holds dir, finds; the
// schema package is the one pattern names. The source gives the methods'
// positions, which the errors name. A method a mixin has through an
// embedded struct counts.
//
// A mixin's behaviour method takes nothing and returns velox.Policy,
// []velox.Hook or []velox.Interceptor, as an entity's does:
//
//	func (InOrg) Policy() velox.Policy
//
// Another shape is a *FileError at its line, a generic method or the typed
// shape of an older velox among them, and so is a behaviour method on a
// mixin that is itself a generic type, whose instance velox-gen could not
// name. A mixin with behaviour listed twice by one entity is an *Error.
// CheckBehaviour checks the columns the methods' bodies name.
func MixinBehaviour(dir, pattern string, g *Graph, env []string) (map[string][]MixinBound, error) {
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
		return nil, nil
	}
	if slices.Contains(paths, g.Package) {
		pkg, err := list(dir, pattern, env)
		if err != nil {
			return nil, err
		}
		r.dirs[g.Package] = pkg.Dir
		paths = slices.DeleteFunc(paths, func(p string) bool { return p == g.Package })
	}
	if len(paths) > 0 {
		dirs, err := listDirs(dir, paths, env)
		if err != nil {
			return nil, err
		}
		maps.Copy(r.dirs, dirs)
	}
	out := map[string][]MixinBound{}
	for _, e := range g.Entities {
		seen := map[string]bool{}
		for i, m := range e.Mixins {
			if m.Name == "" {
				continue
			}
			found, err := r.methods(m.PkgPath, m.Name)
			if err != nil {
				return nil, err
			}
			if len(found) == 0 {
				continue
			}
			key := m.PkgPath + "." + m.Name
			if seen[key] {
				return nil, &Error{Schema: e.Name, Reason: fmt.Sprintf("Mixin()[%d] is %s again, which declares behaviour: list a mixin once", i, m.Name)}
			}
			seen[key] = true
			mb := MixinBound{Index: i, PkgPath: m.PkgPath, PkgName: r.pkgs[m.PkgPath].name, Name: m.Name, NonZero: m.NonZero, Methods: map[string]bool{}}
			for _, k := range behaviourKinds {
				fm, ok := found[k]
				if !ok {
					continue
				}
				mb.Methods[k] = fm.ptr
			}
			out[e.Name] = append(out[e.Name], mb)
		}
	}
	return out, nil
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
	ptr bool // a pointer receiver
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
			if err := mixinMethodShape(p, m, name); err != nil {
				return err
			}
			found[k] = foundMethod{ptr: recvPtr(m.fd)}
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

// mixinMethodShape checks that m, a behaviour method of mixin name, takes
// nothing and returns the behaviour of its kind.
func mixinMethodShape(p *mixinPkg, m *mixinMethodDecl, name string) error {
	k := m.fd.Name.Name
	line := p.fset.Position(m.fd.Pos()).Line
	if m.fd.Type.TypeParams != nil {
		return &FileError{Path: m.path, Line: line, Reason: notGeneric(name, k)}
	}
	if !behaviourSignature(m.fd.Type, k, localName(m.file, veloxPkg)) {
		return &FileError{Path: m.path, Line: line, Reason: fmt.Sprintf("%s.%s must take nothing and return %s; a mixin's method serves every entity that lists it: func (%[1]s) %[2]s() %[3]s", name, k, behaviourResult(k))}
	}
	return nil
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

// exprString writes x as Go source.
func exprString(x ast.Expr) string {
	var b strings.Builder
	_ = printer.Fprint(&b, token.NewFileSet(), x)
	return b.String()
}

// noBehaviour are velox's packages whose types a mixin embeds, which
// declare no behaviour methods: velox-gen does not read them.
var noBehaviour = map[string]bool{loadjson.SchemaPath: true, loadjson.SchemaPath + "/mixin": true}

// guessName is the name a package at import path p is conventionally
// declared with: its last element, without a major version element or suffix
// (example.com/x/v2 and gopkg.in/x.v2 are x) or a go- prefix.
func guessName(p string) string {
	elems := strings.Split(p, "/")
	last := elems[len(elems)-1]
	if len(elems) > 1 && majorVersion(last) {
		last = elems[len(elems)-2]
	}
	if i := strings.LastIndex(last, ".v"); i > 0 && majorVersion(last[i+1:]) {
		last = last[:i]
	}
	last = strings.TrimPrefix(last, "go-")
	return strings.ReplaceAll(last, "-", "_")
}

// majorVersion reports whether s is v followed by digits.
func majorVersion(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	_, err := strconv.Atoi(s[1:])
	return err == nil
}

// recvType is the name of the type of d's receiver, generic or not: T for
// (T), (*T), (T[P]) and (*T[P]); "" for another shape.
func recvType(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) != 1 {
		return ""
	}
	t := d.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	switch x := t.(type) {
	case *ast.IndexExpr:
		t = x.X
	case *ast.IndexListExpr:
		t = x.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}
