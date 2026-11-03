package codegen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// prune returns the schema package's files without the entities' behaviour,
// for the loader to compile in their place. Hooks and Policy are typed with
// the generated package, which the schema package then imports: without them,
// loading the schema does not need gen to compile, or to exist. A stale gen
// would otherwise block the generation that refreshes it.
//
// It keeps what the schemas' structure reaches: the schema types and the
// package-level declarations they use, the methods of every type kept other
// than Hooks and Policy, and the init functions. It leaves out the rest, and
// the imports the rest alone uses. A declaration it leaves out is replaced by
// blanks with the same line breaks, so positions the compiler or the DSL
// reports are where the user wrote them. names gives the package name of an
// import path the file does not name; an import path missing from it is taken
// to be named by its last element, as go's convention has it.
//
// The result holds only the files it changed, by name; nil when the package
// declares no Hooks or Policy method.
func prune(files map[string][]byte, schemas []string, names map[string]string) (map[string][]byte, error) {
	type decl struct {
		file string
		node ast.Node // *ast.FuncDecl, or a *ast.GenDecl's spec
	}
	fset := token.NewFileSet()
	parsed := map[string]*ast.File{}
	byName := map[string][]*decl{}  // package-level name → its declarations
	methods := map[string][]*decl{} // receiver type → its methods, but Hooks and Policy
	var all []*decl
	byNode := map[ast.Node]*decl{}
	var roots []*decl
	behaviour := false
	for _, name := range slices.Sorted(maps.Keys(files)) {
		f, err := parser.ParseFile(fset, name, files[name], parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		parsed[name] = f
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				x := &decl{file: name, node: d}
				all = append(all, x)
				byNode[d] = x
				if d.Recv != nil {
					if slices.Contains(behaviourMethods, d.Name.Name) {
						behaviour = true
						continue
					}
					if t := recvType(d); t != "" {
						methods[t] = append(methods[t], x)
					}
					continue
				}
				if d.Name.Name == "init" {
					roots = append(roots, x)
					continue
				}
				byName[d.Name.Name] = append(byName[d.Name.Name], x)
			case *ast.GenDecl:
				if d.Tok == token.IMPORT {
					continue
				}
				for _, sp := range d.Specs {
					x := &decl{file: name, node: sp}
					all = append(all, x)
					byNode[sp] = x
					switch sp := sp.(type) {
					case *ast.TypeSpec:
						byName[sp.Name.Name] = append(byName[sp.Name.Name], x)
						if slices.Contains(schemas, sp.Name.Name) {
							roots = append(roots, x)
						}
					case *ast.ValueSpec:
						for _, id := range sp.Names {
							if id.Name != "_" {
								byName[id.Name] = append(byName[id.Name], x)
							}
						}
					}
				}
			}
		}
	}
	if !behaviour {
		return nil, nil
	}
	kept := map[*decl]bool{}
	var keep func(x *decl)
	keep = func(x *decl) {
		if kept[x] {
			return
		}
		kept[x] = true
		if ts, ok := x.node.(*ast.TypeSpec); ok {
			for _, m := range methods[ts.Name.Name] {
				keep(m)
			}
		}
		ast.Inspect(x.node, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.SelectorExpr:
				// x.Sel names a field, a method or another package's name,
				// never one of this package's.
				ast.Inspect(n.X, func(n ast.Node) bool {
					if id, ok := n.(*ast.Ident); ok {
						for _, y := range byName[id.Name] {
							keep(y)
						}
					}
					return true
				})
				return false
			case *ast.Ident:
				for _, y := range byName[n.Name] {
					keep(y)
				}
			}
			return true
		})
	}
	for _, r := range roots {
		keep(r)
	}

	out := map[string][]byte{}
	for name, f := range parsed {
		src := slices.Clone(files[name])
		changed := false
		blank := func(from, to token.Pos) {
			changed = true
			tf := fset.File(from)
			for i := tf.Offset(from); i < tf.Offset(to); i++ {
				if src[i] != '\n' {
					src[i] = ' '
				}
			}
		}
		used := map[string]string{} // the names the kept declarations qualify identifiers with
		for _, x := range all {
			if x.file != name {
				continue
			}
			if !kept[x] {
				continue
			}
			ast.Inspect(x.node, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok {
						used[id.Name] = id.Name
					}
				}
				return true
			})
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if !kept[byNode[d]] {
					blank(d.Pos(), d.End())
				}
			case *ast.GenDecl:
				var drop []ast.Spec
				for _, sp := range d.Specs {
					if d.Tok == token.IMPORT {
						if !importUsed(sp.(*ast.ImportSpec), used, names) {
							drop = append(drop, sp)
						}
					} else if !kept[byNode[sp]] {
						drop = append(drop, sp)
					}
				}
				switch {
				case len(drop) == 0:
				case len(drop) == len(d.Specs):
					blank(d.Pos(), d.End())
				default:
					for _, sp := range drop {
						blank(sp.Pos(), sp.End())
					}
				}
			}
		}
		if changed {
			out[name] = src
		}
	}
	return out, nil
}

// importUsed reports whether the file's kept declarations use im, by the
// names they qualify identifiers with. A blank or dot import counts as used.
func importUsed(im *ast.ImportSpec, used, names map[string]string) bool {
	p, err := strconv.Unquote(im.Path.Value)
	if err != nil {
		return true
	}
	if im.Name != nil {
		if n := im.Name.Name; n == "_" || n == "." {
			return true
		}
		return used[im.Name.Name] != ""
	}
	if n := names[p]; n != "" {
		return used[n] != ""
	}
	return used[guessName(p)] != ""
}

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
