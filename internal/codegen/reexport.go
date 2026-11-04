package codegen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"slices"
	"strconv"
	"strings"
)

// In split mode the generated package re-exports every exported name of the
// Go files of package entity that velox-gen and its plugins write:
//
//   - a type is an alias, T = entity.T, and its methods come with it; a
//     generic type is a generic alias, T[P any] = entity.T[P];
//   - a const is C = entity.C, which keeps its type;
//   - a var is V = entity.V, a copy made when the program starts:
//     velox-gen's and velox-graphql's variables do not change after it;
//   - a function is a function that calls entity's, with its signature; a
//     generic one passes its type parameters on. A function whose signature
//     names an unexported type is var F = entity.F instead, as no function
//     of gen can name that type.
//
// The re-exports of many files go in one: velox-gen's in velox.go, each
// plugin's in <plugin>_velox.go. Its types are one type declaration, its
// consts one const declaration and its vars one var declaration, in the
// order of the files; a declaration whose doc comment documents a group,
// such as an enum's values, stays a declaration of its own, after them.
// The functions follow. The re-exports keep the doc comments of what they
// re-export.

// Reexport returns the file name of package pkg that re-exports the exported
// names of srcs, Go files of package entity at entityPath; ok is false when
// they export nothing.
func Reexport(name string, srcs []File, pkg, entityPath string) (f File, ok bool, err error) {
	imports, decls, names, err := reexports(srcs)
	if err != nil || len(names) == 0 {
		return File{}, false, err
	}
	var w writer
	w.WriteString(header)
	w.p("package %s", pkg)
	w.p("")
	imports[EntityDir] = entityPath
	writeImports(&w, imports)
	w.WriteString(strings.TrimPrefix(decls, "\n"))
	data, err := formatted(name, w.String())
	if err != nil {
		return File{}, false, err
	}
	return File{Name: name, Data: data}, true, nil
}

// ReexportedNames returns the exported names that Reexport re-exports from
// src: the names a file of the user's in the generated package must not
// declare.
func ReexportedNames(name string, src []byte) ([]string, error) {
	_, _, names, err := reexports([]File{{Name: name, Data: src}})
	return names, err
}

// reexports returns the declarations that re-export the exported names of
// srcs, each after a blank line, the imports they need by name, and the
// names.
func reexports(srcs []File) (imports map[string]string, decls string, names []string, err error) {
	r := &reexporter{imports: map[string]string{}}
	for _, src := range srcs {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, src.Name, src.Data, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, "", nil, err
		}
		r.fset, r.file = fset, f
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				switch d.Tok {
				case token.TYPE:
					for _, sp := range d.Specs {
						r.typeSpec(d, sp.(*ast.TypeSpec))
					}
				case token.CONST:
					r.values(d, &r.consts)
				case token.VAR:
					r.values(d, &r.vars)
				}
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.IsExported() {
					r.function(d)
				}
			}
		}
		if r.err != nil {
			return nil, "", nil, r.err
		}
	}
	if _, ok := r.imports[EntityDir]; ok {
		return nil, "", nil, fmt.Errorf("an import named %s, which the re-exports use for package %[1]s", EntityDir)
	}
	return r.imports, r.decls(), r.names, nil
}

// reSpec is one spec of a re-export, X = entity.X, with its doc comment.
type reSpec struct {
	doc  []string
	line string
}

// reBlock is the re-export of one declaration: its doc comment and specs.
type reBlock struct {
	doc   []string
	specs []reSpec
}

// reexporter collects the re-exports of files of package entity.
type reexporter struct {
	fset                *token.FileSet
	file                *ast.File // the file being read
	types, consts, vars []reBlock
	funcs               strings.Builder
	imports             map[string]string // the imports the re-exports need, by name
	names               []string
	err                 error
}

// p writes a line of the functions.
func (r *reexporter) p(format string, args ...any) {
	fmt.Fprintf(&r.funcs, format, args...)
	r.funcs.WriteByte('\n')
}

// decls writes the type, const and var declarations, then the functions.
func (r *reexporter) decls() string {
	var b strings.Builder
	line := func(s string) {
		b.WriteString(s)
		b.WriteByte('\n')
	}
	// decl writes one declaration of the blocks.
	decl := func(kw string, blocks []reBlock) {
		n := 0
		for _, bl := range blocks {
			n += len(bl.specs)
		}
		if n == 0 {
			return
		}
		line("")
		if n == 1 {
			bl := blocks[0]
			for _, l := range slices.Concat(bl.doc, bl.specs[0].doc) {
				line(l)
			}
			line(kw + " " + bl.specs[0].line)
			return
		}
		if len(blocks) == 1 {
			for _, l := range blocks[0].doc {
				line(l)
			}
		}
		line(kw + " (")
		for i, bl := range blocks {
			if i > 0 {
				line("")
			}
			for _, s := range bl.specs {
				for _, l := range s.doc {
					line(l)
				}
				line(s.line)
			}
		}
		line(")")
	}
	// A block with a doc comment of its own, such as an enum's values, is
	// its own declaration, which the comment documents; the others are one.
	for _, g := range []struct {
		kw     string
		blocks []reBlock
	}{{"type", r.types}, {"const", r.consts}, {"var", r.vars}} {
		var grouped []reBlock
		for _, bl := range g.blocks {
			if len(bl.doc) == 0 {
				grouped = append(grouped, bl)
			}
		}
		decl(g.kw, grouped)
		for _, bl := range g.blocks {
			if len(bl.doc) > 0 {
				decl(g.kw, []reBlock{bl})
			}
		}
	}
	b.WriteString(r.funcs.String())
	return b.String()
}

// docLines returns the doc comment cg as lines, or else
// "// <name> is entity.<name>.", or nothing when name is "".
func docLines(cg *ast.CommentGroup, name string) []string {
	text := ""
	if cg != nil {
		text = cg.Text()
	}
	if strings.TrimSpace(text) == "" {
		if name == "" {
			return nil
		}
		text = name + " is " + EntityDir + "." + name + "."
	}
	var ls []string
	for l := range strings.SplitSeq(strings.TrimRight(text, "\n"), "\n") {
		ls = append(ls, "//"+strings.TrimRight(" "+l, " "))
	}
	return ls
}

// doc writes the doc comment cg, or else "// <name> is entity.<name>.",
// before a function.
func (r *reexporter) doc(cg *ast.CommentGroup, name string) {
	for _, l := range docLines(cg, name) {
		r.p("%s", l)
	}
}

// node prints n as the source has it, and records the imports its package
// names need.
func (r *reexporter) node(n ast.Node) string {
	ast.Inspect(n, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok {
			r.qualifier(id.Name)
		}
		return false
	})
	var b bytes.Buffer
	if err := printer.Fprint(&b, r.fset, n); err != nil && r.err == nil {
		r.err = err
	}
	return b.String()
}

// qualifier records the import of the file that name stands for. Two files
// that import different packages by one name are an error.
func (r *reexporter) qualifier(name string) {
	for _, im := range r.file.Imports {
		p, err := strconv.Unquote(im.Path.Value)
		if err != nil {
			continue
		}
		if (im.Name != nil && im.Name.Name == name) || (im.Name == nil && guessName(p) == name) {
			if old, ok := r.imports[name]; ok && old != p && r.err == nil {
				r.err = fmt.Errorf("%s: imports %s as %s, which another file re-exported here imports for %s", r.fset.Position(r.file.Pos()).Filename, p, name, old)
			}
			r.imports[name] = p
			return
		}
	}
	if r.err == nil {
		r.err = fmt.Errorf("%s: no import named %s", r.fset.Position(r.file.Pos()).Filename, name)
	}
}

// typeParams returns the type parameter list of fl, as [P C, Q D], and the
// arguments that pass them on, as [P, Q]; two empty strings for nil.
func (r *reexporter) typeParams(fl *ast.FieldList) (params, args string) {
	if fl == nil || len(fl.List) == 0 {
		return "", ""
	}
	var ps, as []string
	for _, fld := range fl.List {
		var ns []string
		for _, n := range fld.Names {
			ns = append(ns, n.Name)
		}
		ps = append(ps, strings.Join(ns, ", ")+" "+r.node(fld.Type))
		as = append(as, ns...)
	}
	return "[" + strings.Join(ps, ", ") + "]", "[" + strings.Join(as, ", ") + "]"
}

func (r *reexporter) typeSpec(d *ast.GenDecl, ts *ast.TypeSpec) {
	if !ts.Name.IsExported() {
		return
	}
	n := ts.Name.Name
	doc := ts.Doc
	if doc == nil && len(d.Specs) == 1 {
		doc = d.Doc
	}
	params, args := r.typeParams(ts.TypeParams)
	r.types = append(r.types, reBlock{specs: []reSpec{{docLines(doc, n), fmt.Sprintf("%s%s = %s.%[1]s%[4]s", n, params, EntityDir, args)}}})
	r.names = append(r.names, n)
}

// values adds the re-export of the const or var declaration d to *to.
func (r *reexporter) values(d *ast.GenDecl, to *[]reBlock) {
	type spec struct {
		doc   *ast.CommentGroup
		names []string
	}
	var specs []spec
	count := 0
	for _, sp := range d.Specs {
		vs := sp.(*ast.ValueSpec)
		var ns []string
		for _, id := range vs.Names {
			if id.IsExported() {
				ns = append(ns, id.Name)
			}
		}
		if len(ns) > 0 {
			specs = append(specs, spec{vs.Doc, ns})
			count += len(ns)
		}
	}
	if count == 0 {
		return
	}
	reexp := func(n string) string { return fmt.Sprintf("%s = %s.%[1]s", n, EntityDir) }
	if count == 1 && specs[0].doc == nil {
		n := specs[0].names[0]
		*to = append(*to, reBlock{specs: []reSpec{{docLines(d.Doc, n), reexp(n)}}})
		r.names = append(r.names, n)
		return
	}
	b := reBlock{doc: docLines(d.Doc, "")}
	for _, s := range specs {
		for i, n := range s.names {
			rs := reSpec{line: reexp(n)}
			if i == 0 {
				rs.doc = docLines(s.doc, "")
			}
			b.specs = append(b.specs, rs)
			r.names = append(r.names, n)
		}
	}
	*to = append(*to, b)
}

func (r *reexporter) function(d *ast.FuncDecl) {
	n := d.Name.Name
	ft := d.Type
	tparams := map[string]bool{}
	if ft.TypeParams != nil {
		for _, fld := range ft.TypeParams.List {
			for _, id := range fld.Names {
				tparams[id.Name] = true
			}
		}
	}
	// A signature that names an unexported type of entity cannot be
	// written in gen.
	local := false
	ast.Inspect(ft, func(x ast.Node) bool {
		switch x := x.(type) {
		case *ast.SelectorExpr:
			return false
		case *ast.Field:
			// Visit the type only: the names are the parameters'.
			ast.Inspect(x.Type, func(y ast.Node) bool {
				switch y := y.(type) {
				case *ast.SelectorExpr:
					return false
				case *ast.Ident:
					if !y.IsExported() && !tparams[y.Name] && types.Universe.Lookup(y.Name) == nil {
						local = true
					}
				}
				return true
			})
			return false
		}
		return true
	})
	r.p("")
	r.doc(d.Doc, n)
	if local {
		if ft.TypeParams != nil {
			r.err = fmt.Errorf("%s: the generic function %s names an unexported type in its signature, which package %s cannot re-export", r.fset.Position(d.Pos()), n, EntityDir)
			return
		}
		r.p("var %s = %s.%[1]s", n, EntityDir)
		r.names = append(r.names, n)
		return
	}
	params, targs := r.typeParams(ft.TypeParams)
	taken := map[string]bool{EntityDir: true}
	for _, im := range r.file.Imports {
		if im.Name != nil {
			taken[im.Name.Name] = true
		} else if p, err := strconv.Unquote(im.Path.Value); err == nil {
			taken[guessName(p)] = true
		}
	}
	var sig, call []string
	i := 0
	for _, fld := range ft.Params.List {
		typ := r.node(fld.Type)
		_, variadic := fld.Type.(*ast.Ellipsis)
		names := fld.Names
		if len(names) == 0 {
			names = []*ast.Ident{nil}
		}
		for _, id := range names {
			pn := "p" + strconv.Itoa(i)
			if id != nil && id.Name != "_" && !taken[id.Name] && !tparams[id.Name] {
				pn = id.Name
			}
			i++
			sig = append(sig, pn+" "+typ)
			if variadic {
				call = append(call, pn+"...")
			} else {
				call = append(call, pn)
			}
		}
	}
	var results []string
	if ft.Results != nil {
		for _, fld := range ft.Results.List {
			typ := r.node(fld.Type)
			for range max(len(fld.Names), 1) {
				results = append(results, typ)
			}
		}
	}
	res := ""
	switch len(results) {
	case 0:
	case 1:
		res = " " + results[0]
	default:
		res = " (" + strings.Join(results, ", ") + ")"
	}
	r.p("func %s%s(%s)%s {", n, params, strings.Join(sig, ", "), res)
	ret := ""
	if len(results) > 0 {
		ret = "return "
	}
	r.p("%s%s.%s%s(%s)", ret, EntityDir, n, targs, strings.Join(call, ", "))
	r.p("}")
	r.names = append(r.names, n)
}
