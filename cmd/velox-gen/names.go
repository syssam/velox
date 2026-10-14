package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
	"unicode"

	"golang.org/x/tools/go/packages"
)

// plural names an entity's handles variable: Invoice → Invoices, Category →
// Categories, Box → Boxes, Day → Days.
func plural(s string) string {
	switch {
	case len(s) > 1 && s[len(s)-1] == 'y' && !strings.ContainsRune("aeiouAEIOU", rune(s[len(s)-2])):
		return s[:len(s)-1] + "ies"
	case strings.HasSuffix(s, "s"), strings.HasSuffix(s, "x"), strings.HasSuffix(s, "z"),
		strings.HasSuffix(s, "ch"), strings.HasSuffix(s, "sh"):
		return s + "es"
	}
	return s + "s"
}

// lowerCamel lowers an identifier's leading capitals: Invoice → invoice,
// ID → id, URLRecord → urlRecord.
func lowerCamel(s string) string {
	r := []rune(s)
	n := 0
	for n < len(r) && unicode.IsUpper(r[n]) {
		n++
	}
	if n > 1 && n < len(r) {
		n-- // the last capital starts the next word: URLRecord
	}
	for i := range n {
		r[i] = unicode.ToLower(r[i])
	}
	return string(r)
}

// snake is the snake_case of an identifier, for file names. It is the
// library's rule (names.go in package velox), which this module cannot import.
func snake(s string) string {
	lower := func(c byte) bool { return 'a' <= c && c <= 'z' || '0' <= c && c <= '9' }
	upper := func(c byte) bool { return 'A' <= c && c <= 'Z' }
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		if upper(c) {
			if i > 0 && (lower(s[i-1]) || upper(s[i-1]) && i+1 < len(s) && lower(s[i+1])) {
				b.WriteByte('_')
			}
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String()
}

// handleDirective returns the name a //velox:handle Name comment on the
// entity's type declaration sets, or "".
func handleDirective(pkg *packages.Package, name string) string {
	for _, f := range pkg.Syntax {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, s := range gd.Specs {
				ts := s.(*ast.TypeSpec)
				if ts.Name.Name != name {
					continue
				}
				for _, doc := range []*ast.CommentGroup{ts.Doc, gd.Doc} {
					if doc == nil {
						continue
					}
					for _, c := range doc.List {
						if v, ok := strings.CutPrefix(c.Text, "//velox:handle "); ok {
							return strings.TrimSpace(v)
						}
					}
				}
			}
		}
	}
	return ""
}

// name gives a package's entities their identifiers and file, and reports any
// that collide with each other or with the package's own declarations.
// Declarations in generated files do not count: they are about to be rewritten.
func name(pkg *packages.Package, ents []*entity) []problem {
	var probs []problem
	taken := map[string]string{} // identifier or file → the entity that has it
	claim := func(e *entity, what, id string) {
		if other, ok := taken[id]; ok {
			probs = append(probs, problem{e.pos, fmt.Sprintf("%s: its %s %s is also %s's; rename one of them", e.name, what, id, other)})
			return
		}
		taken[id] = e.name
		if obj := pkg.Types.Scope().Lookup(id); obj != nil {
			if at := pkg.Fset.Position(obj.Pos()); !isGenerated(at.Filename) {
				probs = append(probs, problem{e.pos, fmt.Sprintf("%s: its %s %s is already declared at %s; set another with //velox:handle Name", e.name, what, id, at)})
			}
		}
	}
	for _, e := range ents {
		e.handles = plural(e.name)
		if d := handleDirective(pkg, e.name); d != "" {
			if !token.IsIdentifier(d) {
				probs = append(probs, problem{e.pos, fmt.Sprintf("%s: //velox:handle %q is not an identifier", e.name, d)})
				continue
			}
			e.handles = d
		}
		e.meta = lowerCamel(e.name) + "Meta"
		e.shape = lowerCamel(e.name) + "Shape"
		e.out = filepath.Join(filepath.Dir(e.file), snake(e.name)+"_velox.go")
		claim(e, "handles variable", e.handles)
		claim(e, "metadata variable", e.meta)
		claim(e, "shape function", e.shape)
		claim(e, "file", e.out)
	}
	return probs
}
