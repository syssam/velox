package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
)

// A file is rewritten as text: each rule replaces, inserts or deletes a
// range of the source, and what the rules leave alone is not touched, so
// comments and layout stay where they were. A rule that moves an expression
// (the handle of db.X.Get(ctx, handle, id)) takes the expression's text with
// the changes already made inside it.
type edit struct {
	start, end int
	text       string
	seq        int
	// opens marks an insertion that opens something around what follows it
	// (a wrapping call's name and parenthesis): of several at one place the
	// one made last is the outermost and goes first. The others close or
	// continue what precedes them and go in the order they were made.
	opens bool
}

// fileEdits collects the changes to one file.
type fileEdits struct {
	path    string
	src     []byte
	edits   []edit
	seq     int
	imports map[string]string // import path -> local name, to add
	notes   []string          // what a person has to look at

	// For choosing the name a package is referred to by.
	file  *ast.File
	info  *types.Info
	pkg   *types.Package
	fset  *token.FileSet
	names map[string]string // import path -> the name the file already imports it under
	// declared holds every name the file declares that is not a package.
	declared map[string]bool
	// rename maps the import paths of the old generated tree the file has
	// to where the same package is in the new one, and moved says whether
	// any differs.
	rename map[string]string
	moved  bool
}

func (f *fileEdits) off(p token.Pos) int { return f.fset.Position(p).Offset }

func (f *fileEdits) add(start, end int, text string) {
	f.seq++
	f.edits = append(f.edits, edit{start: start, end: end, text: text, seq: f.seq})
}

// insert adds text after what precedes at: a closing parenthesis, a further
// call of a chain.
func (f *fileEdits) insert(at token.Pos, text string) { f.add(f.off(at), f.off(at), text) }

// open adds text before what follows at: the start of a call that wraps it.
func (f *fileEdits) open(at token.Pos, text string) {
	f.seq++
	f.edits = append(f.edits, edit{start: f.off(at), end: f.off(at), text: text, seq: f.seq, opens: true})
}

// replace puts text in place of a range, dropping the changes made inside
// it: the range's old contents are gone.
func (f *fileEdits) replace(from, to token.Pos, text string) {
	start, end := f.off(from), f.off(to)
	f.drop(start, end)
	f.add(start, end, text)
}

func (f *fileEdits) drop(start, end int) {
	kept := f.edits[:0]
	for _, e := range f.edits {
		if e.start >= start && e.end <= end {
			continue
		}
		kept = append(kept, e)
	}
	f.edits = kept
}

// render returns the source of n with the changes made inside it, and takes
// those changes out of the file: the caller puts the text somewhere else.
func (f *fileEdits) render(n ast.Node) string {
	start, end := f.off(n.Pos()), f.off(n.End())
	var inside []edit
	kept := f.edits[:0]
	for _, e := range f.edits {
		if e.start >= start && e.end <= end {
			inside = append(inside, e)
		} else {
			kept = append(kept, e)
		}
	}
	f.edits = kept
	return string(apply(f.src[start:end], start, inside))
}

// apply makes the edits to src, whose first byte is at offset base.
func apply(src []byte, base int, edits []edit) []byte {
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].start != edits[j].start {
			return edits[i].start < edits[j].start
		}
		// At one place: what closes the text before it, then what opens
		// around the text after it (outermost first), then a replacement
		// that starts there.
		rank := func(e edit) int {
			switch {
			case e.start != e.end:
				return 2
			case e.opens:
				return 1
			}
			return 0
		}
		if ri, rj := rank(edits[i]), rank(edits[j]); ri != rj {
			return ri < rj
		}
		if edits[i].opens {
			return edits[i].seq > edits[j].seq
		}
		return edits[i].seq < edits[j].seq
	})
	var out bytes.Buffer
	at := base
	for _, e := range edits {
		if e.start < at {
			// Overlapping changes: the later rule loses. It is reported by
			// the caller's build of the result, not silently merged.
			continue
		}
		out.Write(src[at-base : e.start-base])
		out.WriteString(e.text)
		at = e.end
	}
	out.Write(src[at-base:])
	return out.Bytes()
}

func (f *fileEdits) note(pos token.Pos, msg string, args ...any) {
	p := f.fset.Position(pos)
	f.notes = append(f.notes, fmt.Sprintf("%s:%d: %s", p.Filename, p.Line, fmt.Sprintf(msg, args...)))
}

// ref returns the name by which the file can refer to the package at pos,
// importing it when it does not yet, under a name nothing at pos hides.
func (f *fileEdits) ref(path, name string, pos token.Pos) string {
	scope := f.pkg.Scope().Innermost(pos)
	visible := func(local string) bool {
		if scope == nil {
			return true
		}
		_, obj := scope.LookupParent(local, pos)
		if obj == nil {
			return true
		}
		pn, ok := obj.(*types.PkgName)
		return ok && pn.Imported().Path() == path
	}
	if local, ok := f.names[path]; ok && visible(local) {
		return local
	}
	// One name per file for a package it does not import yet: a name that
	// the file declares anywhere (a variable called like the package) is
	// not it, wherever the reference is.
	if local, ok := f.imports[path]; ok {
		return local
	}
	local := name
	taken := func(local string) bool {
		if f.declared[local] {
			return true
		}
		for p, n := range f.names {
			if n == local && p != path {
				return true
			}
		}
		for p, n := range f.imports {
			if n == local && p != path {
				return true
			}
		}
		return false
	}
	for i := 0; taken(local); i++ {
		local = name + "pkg" + strings.Repeat("x", i)
	}
	f.imports[path] = local
	return local
}

// finish applies the edits, fixes the imports and formats the file.
func (f *fileEdits) finish(defaultName func(path string) string) ([]byte, error) {
	out := apply(f.src, 0, f.edits)
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, f.path, out, parser.ParseComments)
	if err != nil {
		return out, fmt.Errorf("the rewritten file does not parse: %w", err)
	}
	for _, spec := range file.Imports {
		old, _ := strconv.Unquote(spec.Path.Value)
		to, ok := f.rename[old]
		if !ok || to == old {
			continue
		}
		// What the file calls the package stays: the references the rules
		// did not touch still say it.
		if spec.Name == nil && defaultName(old) != to[strings.LastIndex(to, "/")+1:] {
			spec.Name = ast.NewIdent(defaultName(old))
		}
		spec.Path.Value = strconv.Quote(to)
	}
	for path, local := range f.imports {
		if local == defaultName(path) {
			astutil.AddImport(fset, file, path)
		} else {
			astutil.AddNamedImport(fset, file, local, path)
		}
	}
	// An import nothing refers to any more.
	used := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Obj == nil {
				used[id.Name] = true
			}
		}
		return true
	})
	type unused struct{ name, path string }
	var drop []unused
	for _, spec := range file.Imports {
		path, _ := strconv.Unquote(spec.Path.Value)
		local, name := defaultName(path), ""
		if spec.Name != nil {
			local, name = spec.Name.Name, spec.Name.Name
		}
		if local == "_" || local == "." || used[local] {
			continue
		}
		drop = append(drop, unused{name, path})
	}
	for _, u := range drop {
		if u.name != "" {
			astutil.DeleteNamedImport(fset, file, u.name, u.path)
		} else {
			astutil.DeleteImport(fset, file, u.path)
		}
	}
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, file); err != nil {
		return out, err
	}
	return buf.Bytes(), nil
}

// moveTo says where a package of the old generated tree is in the new one:
// the root, and the packages both trees have under the same name (entity,
// predicate, filter, migrate, an entity's own). The others have no
// counterpart.
func (s *schema) moveTo(path string) (string, bool) {
	sub, ok := strings.CutPrefix(path, s.root)
	if !ok {
		return "", false
	}
	switch {
	case sub == "", sub == "/entity", sub == "/predicate", sub == "/filter", sub == "/migrate", s.byLeaf[path] != nil:
		return s.to + sub, true
	}
	return "", false
}

func newFileEdits(p *program, file *ast.File, info *types.Info, pkg *types.Package, s *schema) (*fileEdits, error) {
	path := p.fset.Position(file.Pos()).Filename
	src, err := os.ReadFile(path) //nolint:gosec // a file of the application being migrated
	if err != nil {
		return nil, err
	}
	f := &fileEdits{path: path, src: src, imports: map[string]string{}, file: file, info: info, pkg: pkg, fset: p.fset, names: map[string]string{}, declared: map[string]bool{}, rename: map[string]string{}}
	ast.Inspect(file, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			if obj := info.Defs[id]; obj != nil {
				if _, isPkg := obj.(*types.PkgName); !isPkg {
					f.declared[id.Name] = true
				}
			}
		}
		return true
	})
	for _, spec := range file.Imports {
		var obj types.Object
		if spec.Name != nil {
			obj = info.Defs[spec.Name]
		} else {
			obj = info.Implicits[spec]
		}
		if pn, ok := obj.(*types.PkgName); ok && pn.Name() != "_" && pn.Name() != "." {
			path := pn.Imported().Path()
			if to, ok := s.moveTo(path); ok {
				f.rename[path] = to
				f.moved = f.moved || to != path
				path = to
			}
			f.names[path] = pn.Name()
		}
	}
	return f, nil
}
