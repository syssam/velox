package main

import (
	"bytes"
	"fmt"
	"go/format"
	"go/types"
	"sort"
	"strconv"
	"strings"
)

// imports chooses the names a generated file gives the packages it imports,
// avoiding the names the entity's package already declares.
type imports struct {
	self  *types.Package
	names map[string]string // import path → the name the file uses
	decl  map[string]string // import path → the package's own name
	used  map[string]bool
}

func newImports(self *types.Package) *imports {
	return &imports{self: self, names: map[string]string{}, decl: map[string]string{}, used: map[string]bool{}}
}

// name returns the name the file uses for the package at path, whose own
// name is pkgName, and imports it.
func (im *imports) name(path, pkgName string) string {
	if n, ok := im.names[path]; ok {
		return n
	}
	n := pkgName
	for i := 2; im.used[n] || im.self.Scope().Lookup(n) != nil; i++ {
		n = pkgName + strconv.Itoa(i)
	}
	im.names[path], im.decl[path], im.used[n] = n, pkgName, true
	return n
}

// qualifier is the types.Qualifier of the generated file.
func (im *imports) qualifier(p *types.Package) string {
	if p.Path() == im.self.Path() {
		return ""
	}
	return im.name(p.Path(), p.Name())
}

// write writes the import block: the standard library, then the rest.
func (im *imports) write(b *bytes.Buffer) {
	var std, other []string
	for p := range im.names {
		if strings.Contains(strings.Split(p, "/")[0], ".") {
			other = append(other, p)
		} else {
			std = append(std, p)
		}
	}
	sort.Strings(std)
	sort.Strings(other)
	b.WriteString("import (\n")
	for i, group := range [][]string{std, other} {
		if i == 1 && len(std) > 0 && len(other) > 0 {
			b.WriteString("\n")
		}
		for _, p := range group {
			if im.names[p] != im.decl[p] {
				fmt.Fprintf(b, "\t%s %q\n", im.names[p], p)
			} else {
				fmt.Fprintf(b, "\t%q\n", p)
			}
		}
	}
	b.WriteString(")\n")
}

func quoted(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = strconv.Quote(s)
	}
	return strings.Join(q, ", ")
}

// render returns the source of an entity's generated file (design §2).
func render(e *entity) ([]byte, error) {
	im := newImports(e.pkg.Types)
	// The file's own declarations come first: no import may take their names.
	im.used[e.handles], im.used[e.meta], im.used[e.shape] = true, true, true
	vx := im.name(veloxPath, "velox")
	E := e.name
	handle := func(c column) string {
		return fmt.Sprintf("%s.%s[%s, %s]", vx, c.handle, E, types.TypeString(c.value, im.qualifier))
	}
	fields := make([]string, len(e.columns))
	for i, c := range e.columns {
		fields[i] = c.field
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, "// Fails to compile if this file and the velox library disagree.\nconst _ = %s.GeneratedCodeIsVersion1\n\n", vx)
	fmt.Fprintf(&b, "// %s holds the typed handles of %s's columns and relations.\nvar %s = struct {\n", e.handles, E, e.handles)
	for _, c := range e.columns {
		fmt.Fprintf(&b, "\t%s %s\n", c.field, handle(c))
	}
	b.WriteString("}{\n")
	for i, c := range e.columns {
		fmt.Fprintf(&b, "\t%s: %s{I: %d},\n", c.field, handle(c), i)
	}
	b.WriteString("}\n\n")
	fmt.Fprintf(&b, "var %s %s.MetaOnce[%s]\n\n", e.meta, vx, E)
	fmt.Fprintf(&b, "// VeloxMeta is called by velox, not by applications.\nfunc (*%s) VeloxMeta() *%s.Meta[%s] { return %s.Get(%s) }\n\n", E, vx, E, e.meta, e.shape)
	fmt.Fprintf(&b, "func %s() %s.Shape[%s] {\n\treturn %s.Shape[%s]{\n\t\tColumns: []string{%s},\n", e.shape, vx, E, vx, E, quoted(fields))
	if len(e.relations) > 0 {
		fmt.Fprintf(&b, "\t\tRelations: []string{%s},\n", quoted(e.relations))
	}
	fmt.Fprintf(&b, "\t\tAddr: func(x *%s, i int) any {\n\t\t\tswitch i {\n", E)
	for i, c := range e.columns {
		fmt.Fprintf(&b, "\t\t\tcase %d:\n\t\t\t\treturn &x.%s\n", i, c.field)
	}
	b.WriteString("\t\t\t}\n\t\t\treturn nil\n\t\t},\n\t}\n}\n")

	// The header and imports go last: rendering the body chose the imports.
	var out bytes.Buffer
	fmt.Fprintf(&out, "%s\n\npackage %s\n\n", header, e.pkg.Types.Name())
	im.write(&out)
	out.WriteString("\n")
	out.Write(b.Bytes())
	return format.Source(out.Bytes())
}
