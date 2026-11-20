package codegen

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"maps"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/syssam/velox/internal/codegen/loadjson"
)

// NoSchemas is the error of a schema package, at importPath, that declares
// no schema.
func NoSchemas(importPath string) error {
	return fmt.Errorf("no schemas in %s: a schema is an exported struct type that implements schema.Interface, as one that embeds schema.Schema does", importPath)
}

// The discovery rule, which ent's follows: a schema is an exported,
// non-generic, defined struct type of the schema package that implements
// schema.Interface, by value or through a pointer, whether it declares the
// methods or has them from an embedded struct at any depth. velox refuses,
// at the type's line, what looks like a schema and is not one:
//
//   - a type that embeds schema.Schema, at any depth, but does not implement
//     schema.Interface, such as one with a method of another signature;
//   - an alias, a generic type or a type that is not a struct, when it
//     implements schema.Interface or embeds schema.Schema;
//   - schema.Schema embedded through a pointer, whose nil methods panic;
//   - a schema that embeds another schema: both would be entities. A shared
//     base is an unexported struct, which is never a schema;
//   - an unexported struct that embeds schema.Schema and that no type of the
//     package embeds, which is likely a schema with a lower-case name.
//
// Only the files of the package's build are read: a _test.go file is not, and
// a file that only some builds include is refused (see inspect).

// candSpec is what candidates records of one type declaration.
type candSpec struct {
	ts     *ast.TypeSpec
	pos    token.Position
	schema bool     // embeds schema.Schema directly
	embeds []string // the package's types it embeds, by name
	named  bool     // a defined type of another named type: type Release Item
}

// candDecls is the schema package's type declarations, by name, and the
// names of the types with a method named Type.
type candDecls struct {
	specs      map[string]*candSpec
	order      []string
	typeMethod map[string]bool
}

// candidates returns, from the schema package's files, the types that may be
// schemas, sorted: those of a struct with an embedded field, the aliases,
// the defined types of another named type, and those with a method named
// Type: a package with none has no schemas. It also refuses an unexported
// struct that embeds schema.Schema, directly or through other unexported
// structs of the package, when no type of the package embeds it.
func candidates(files map[string][]byte) ([]string, error) {
	d, err := readCandDecls(files)
	if err != nil {
		return nil, err
	}
	embedded := map[string]bool{}
	for _, s := range d.specs {
		for _, e := range s.embeds {
			embedded[e] = true
		}
	}
	var out []string
	var errs []error
	for _, n := range d.order {
		s := d.specs[n]
		if !ast.IsExported(n) {
			if d.reaches(n, map[string]bool{}) && !embedded[n] {
				errs = append(errs, &Error{Pos: posString(s.pos), Reason: fmt.Sprintf("type %s embeds schema.Schema and is unexported: a schema must be exported, and a shared base is embedded by one", n)})
			}
			continue
		}
		// A defined type of another named type has its underlying struct's
		// promoted methods, so it may be a schema too: classify decides.
		if s.ts.Assign.IsValid() || len(s.embeds) > 0 || d.typeMethod[n] || s.named {
			out = append(out, n)
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	sort.Strings(out)
	return out, nil
}

// readCandDecls parses the files, in name order, and returns their type
// declarations and the receivers of their methods named Type.
func readCandDecls(files map[string][]byte) (*candDecls, error) {
	d := &candDecls{specs: map[string]*candSpec{}, typeMethod: map[string]bool{}}
	fset := token.NewFileSet()
	for _, fname := range slices.Sorted(maps.Keys(files)) {
		f, err := parser.ParseFile(fset, fname, files[fname], parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		d.addFile(fset, f)
	}
	return d, nil
}

// addFile records the type declarations of f and its methods named Type.
func (d *candDecls) addFile(fset *token.FileSet, f *ast.File) {
	local := schemaName(f)
	for _, decl := range f.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			if decl.Name.Name != "Type" {
				continue
			}
			if t := recvType(decl); t != "" {
				d.typeMethod[t] = true
			}
		case *ast.GenDecl:
			if decl.Tok != token.TYPE {
				continue
			}
			for _, sp := range decl.Specs {
				ts := sp.(*ast.TypeSpec)
				if _, dup := d.specs[ts.Name.Name]; !dup {
					d.order = append(d.order, ts.Name.Name)
				}
				d.specs[ts.Name.Name] = newCandSpec(ts, fset.Position(ts.Pos()), local)
			}
		}
	}
}

// newCandSpec returns the candSpec of ts, whose file names the schema
// package local.
func newCandSpec(ts *ast.TypeSpec, pos token.Position, local string) *candSpec {
	s := &candSpec{ts: ts, pos: pos}
	switch ts.Type.(type) {
	case *ast.Ident, *ast.SelectorExpr, *ast.IndexExpr, *ast.IndexListExpr, *ast.ParenExpr:
		s.named = true
	}
	if st, ok := ts.Type.(*ast.StructType); ok {
		s.schema = embedsSchema(st, local, false) || embedsSchema(st, local, true)
		s.embeds = embeddedNames(st)
	}
	return s
}

// embeddedNames returns the name of each embedded field of st, without a
// pointer or type arguments; one not of a local type is "".
func embeddedNames(st *ast.StructType) []string {
	var names []string
	for _, fld := range st.Fields.List {
		if len(fld.Names) != 0 {
			continue
		}
		t := fld.Type
		if star, ok := t.(*ast.StarExpr); ok {
			t = star.X
		}
		if ix, ok := t.(*ast.IndexExpr); ok {
			t = ix.X
		}
		if id, ok := t.(*ast.Ident); ok {
			names = append(names, id.Name)
		} else {
			names = append(names, "")
		}
	}
	return names
}

// reaches reports whether the type n embeds schema.Schema, directly or
// through the package's unexported structs.
func (d *candDecls) reaches(n string, seen map[string]bool) bool {
	s := d.specs[n]
	if s == nil || seen[n] {
		return false
	}
	seen[n] = true
	if s.schema {
		return true
	}
	for _, e := range s.embeds {
		if e != "" && !ast.IsExported(e) && d.reaches(e, seen) {
			return true
		}
	}
	return false
}

// compileError is the compiler's message as go list reports it, without the
// "# package" line before it.
func compileError(msg string) string {
	msg = strings.TrimSpace(msg)
	if strings.HasPrefix(msg, "# ") {
		if _, rest, ok := strings.Cut(msg, "\n"); ok {
			msg = rest
		}
	}
	return msg
}

// posString is p as file:line.
func posString(p token.Position) string {
	if !p.IsValid() {
		return ""
	}
	return p.Filename + ":" + strconv.Itoa(p.Line)
}

// exports runs go list -export for the package at importPath and its
// dependencies, and returns the export data file of each package by import
// path. A package that does not compile is an error that names it, and the
// chain of imports that reaches it from the schema package.
func exports(dir, importPath string, env []string) (map[string]string, error) {
	out, stderr, err := run(dir, env, "list", "-e", "-export", "-deps", "-json=ImportPath,Export,Imports,Error", importPath)
	if err != nil {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	type listedExport struct {
		ImportPath, Export string
		Imports            []string
		Error              *struct{ Err string }
	}
	pkgs := map[string]*listedExport{}
	exp := map[string]string{}
	dec := json.NewDecoder(strings.NewReader(out))
	for {
		var p listedExport
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("reading go list output: %w", err)
		}
		pkgs[p.ImportPath] = &p
		if p.Export != "" {
			exp[p.ImportPath] = p.Export
		}
	}
	root := pkgs[importPath]
	if root == nil {
		return nil, fmt.Errorf("go list did not list %s", importPath)
	}
	if root.Error != nil {
		return nil, errors.New(compileError(root.Error.Err))
	}
	// The first package that does not compile, by the shortest chain of
	// imports from the schema package.
	prev := map[string]string{importPath: ""}
	queue := []string{importPath}
	for len(queue) > 0 {
		p := pkgs[queue[0]]
		queue = queue[1:]
		if p == nil {
			continue
		}
		if p.Error != nil && p.ImportPath != importPath {
			var chain []string
			for q := p.ImportPath; q != ""; q = prev[q] {
				chain = append([]string{q}, chain...)
			}
			return nil, fmt.Errorf("%s, which does not compile:\n%s\n"+
				"The loader leaves out the Hooks and Policy methods of the schema package, and what only they use, "+
				"but not the packages its schemas' other methods reach: those must compile before velox-gen runs. "+
				"Move what needs the generated package out of what Fields, Edges, Indexes, Mixin and Annotations reach",
				strings.Join(chain, " imports "), compileError(p.Error.Err))
		}
		for _, q := range p.Imports {
			if _, seen := prev[q]; !seen {
				prev[q] = p.ImportPath
				queue = append(queue, q)
			}
		}
	}
	if _, ok := exp[importPath]; !ok {
		return nil, fmt.Errorf("go list wrote no export data for %s", importPath)
	}
	return exp, nil
}

// classify reads the schema package from its export data and returns its
// schemas, sorted, by the discovery rule above. Every refusal is an *Error
// at the type's line, joined.
func classify(importPath string, exp map[string]string) ([]string, error) {
	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		f, ok := exp[path]
		if !ok {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		return io.NopCloser(bytes.NewReader(b)), nil
	})
	if _, ok := exp[loadjson.SchemaPath]; !ok {
		return nil, nil // the package does not use velox's schema package
	}
	sp, err := imp.Import(loadjson.SchemaPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", loadjson.SchemaPath, err)
	}
	pkg, err := imp.Import(importPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", importPath, err)
	}
	iface := sp.Scope().Lookup("Interface").Type().Underlying().(*types.Interface)
	base := sp.Scope().Lookup("Schema").Type()
	qual := func(p *types.Package) string {
		if p == pkg {
			return ""
		}
		return p.Name()
	}

	type found struct {
		tn  *types.TypeName
		err string
	}
	var schemas []*types.TypeName
	var refused []found
	for _, n := range pkg.Scope().Names() {
		tn, ok := pkg.Scope().Lookup(n).(*types.TypeName)
		if !ok || types.IsInterface(tn.Type()) {
			continue
		}
		t := tn.Type()
		emb, ptr := embedsBase(t, base)
		var impl bool
		if isGeneric(t) {
			// Implements is unspecified for a generic type: declaring Type
			// is what makes one look like a schema.
			impl = declaresType(t)
		} else {
			impl = types.Implements(t, iface) || types.Implements(types.NewPointer(t), iface)
		}
		if !impl && !emb {
			continue
		}
		if !tn.Exported() {
			continue // a base, or refused by candidates
		}
		var why string
		switch {
		case tn.IsAlias():
			why = fmt.Sprintf("%s is an alias of %s: a schema is a type of its own, declared type %s struct{ ... }", n, types.TypeString(types.Unalias(t), qual), n)
		case isGeneric(t):
			why = fmt.Sprintf("%s is generic: a schema has no type parameters, since velox-gen makes one entity of it", n)
		case !isStruct(t):
			why = fmt.Sprintf("the type of %s is %s: a schema is a struct type", n, types.TypeString(t.Underlying(), qual))
		case ptr:
			why = fmt.Sprintf("%s embeds schema.Schema through a pointer: embed it by value, since a nil pointer's methods panic", n)
		case !impl:
			why = fmt.Sprintf("%s embeds schema.Schema but does not implement schema.Interface: %s", n, missing(t, iface, qual))
		}
		if why != "" {
			refused = append(refused, found{tn, why})
			continue
		}
		schemas = append(schemas, tn)
	}
	// A schema that embeds another is refused: both would be entities.
	for _, s := range schemas {
		if other := embeddedSchema(s.Type(), schemas); other != nil {
			refused = append(refused, found{s, fmt.Sprintf("%s embeds %s, which is a schema itself, so both would be entities: make %s a shared base by leaving it unexported (type %s struct{ schema.Schema }), or share fields with a mixin",
				s.Name(), other.Name(), other.Name(), lowerFirst(other.Name()))})
		}
	}
	if len(refused) > 0 {
		slices.SortStableFunc(refused, func(a, b found) int { return cmp.Compare(a.tn.Pos(), b.tn.Pos()) })
		errs := make([]error, len(refused))
		for i, r := range refused {
			errs[i] = &Error{Pos: posString(fset.Position(r.tn.Pos())), Reason: r.err}
		}
		return nil, errors.Join(errs...)
	}
	names := make([]string, len(schemas))
	for i, s := range schemas {
		names[i] = s.Name()
	}
	sort.Strings(names)
	return names, nil
}

// embedsBase reports whether t is a struct that embeds base, at any depth,
// and whether a pointer is on the way.
func embedsBase(t, base types.Type) (found, ptr bool) {
	seen := map[types.Type]bool{}
	var walk func(t types.Type, viaPtr bool) bool
	walk = func(t types.Type, viaPtr bool) bool {
		st, ok := types.Unalias(t).Underlying().(*types.Struct)
		if !ok || seen[t] {
			return false
		}
		seen[t] = true
		for f := range st.Fields() {
			if !f.Embedded() {
				continue
			}
			ft, p := types.Unalias(f.Type()), viaPtr
			if pt, ok := ft.(*types.Pointer); ok {
				ft, p = types.Unalias(pt.Elem()), true
			}
			if types.Identical(ft, base) {
				found, ptr = true, p
				return true
			}
			if walk(ft, p) {
				return true
			}
		}
		return false
	}
	walk(t, false)
	return found, ptr
}

// embeddedSchema returns the first of schemas that t embeds, at any depth,
// or nil.
func embeddedSchema(t types.Type, schemas []*types.TypeName) *types.TypeName {
	seen := map[types.Type]bool{}
	var walk func(t types.Type) *types.TypeName
	walk = func(t types.Type) *types.TypeName {
		st, ok := t.Underlying().(*types.Struct)
		if !ok || seen[t] {
			return nil
		}
		seen[t] = true
		for f := range st.Fields() {
			if !f.Embedded() {
				continue
			}
			ft := types.Unalias(f.Type())
			if pt, ok := ft.(*types.Pointer); ok {
				ft = types.Unalias(pt.Elem())
			}
			if n, ok := ft.(*types.Named); ok && slices.Contains(schemas, n.Obj()) {
				return n.Obj()
			}
			if s := walk(ft); s != nil {
				return s
			}
		}
		return nil
	}
	return walk(t)
}

// missing says which method of schema.Interface *t lacks, or has with
// another signature. classify calls it only for a t whose pointer does not
// implement schema.Interface, so a method is missing.
func missing(t types.Type, iface *types.Interface, qual types.Qualifier) string {
	m, wrong := types.MissingMethod(types.NewPointer(t), iface, true)
	want := types.TypeString(m.Type(), qual)
	if wrong {
		got, _, _ := types.LookupFieldOrMethod(types.NewPointer(t), true, m.Pkg(), m.Name())
		if got != nil {
			return fmt.Sprintf("its method %s%s should be %s%s", m.Name(), strings.TrimPrefix(types.TypeString(got.Type(), qual), "func"), m.Name(), strings.TrimPrefix(want, "func"))
		}
	}
	return fmt.Sprintf("it has no method %s%s; a field or another method of that name hides schema.Schema's", m.Name(), strings.TrimPrefix(want, "func"))
}

// isGeneric reports whether t is a generic type.
func isGeneric(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.TypeParams().Len() > 0
}

// declaresType reports whether the named type t declares a method Type.
func declaresType(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return false
	}
	for m := range n.Methods() {
		if m.Name() == "Type" {
			return true
		}
	}
	return false
}

// isStruct reports whether t's underlying type is a struct.
func isStruct(t types.Type) bool {
	_, ok := t.Underlying().(*types.Struct)
	return ok
}

// lowerFirst is s with its first letter in lower case.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
