package codegen

import (
	"fmt"
	"go/parser"
	"strconv"
)

// mixinSources adds to each entity's sources the behaviour of its mixins
// (Config.Mixins), before its schema's own, and checks the columns their
// velox.Field calls name. The entity's own file binds them, and imports
// their packages.
func (l *lowerer) mixinSources() {
	for _, ge := range l.g.Entities {
		mbs := l.cfg.Mixins[ge.Name]
		if len(mbs) == 0 {
			continue
		}
		s := l.state[ge.Name]
		for _, mb := range mbs {
			l.checkMixinFields(s, mb)
			q := l.mixinName(mb.PkgPath, mb.PkgName)
			imports := map[string]string{}
			if q != "sch" {
				imports[q] = mb.PkgPath
			}
			typ := q + "." + mb.Name
			name := mb.Name
			if q != "sch" {
				name = mb.PkgName + "." + mb.Name
			}
			for _, k := range behaviourKinds {
				ptr, ok := mb.Methods[k]
				if !ok {
					continue
				}
				recv := typ + "{}"
				switch {
				case mb.NonZero:
					schemaValue := "sch." + ge.Name + "{}"
					if ge.Pointer {
						schemaValue = "&" + schemaValue
					}
					recv = fmt.Sprintf("schema.MixinAt[%s](%s, %d)", typ, schemaValue, mb.Index)
					imports["schema"] = schemaPkg
				case ptr:
					recv = "(&" + recv + ")"
				}
				*s.out.sources(k) = append(*s.out.sources(k), OutSource{Name: name, Expr: recv + "." + k, Imports: imports})
			}
			s.sch = s.sch || q == "sch" || mb.NonZero
			for n, p := range imports {
				s.paths[n] = p
				if n != "schema" && n != mb.PkgName {
					if s.out.ImportAs == nil {
						s.out.ImportAs = map[string]string{}
					}
					s.out.ImportAs[p] = n
				}
			}
		}
	}
}

// mixinName returns the name the generated code gives the package at path,
// named pkgName, that declares a mixin: sch for the schema package, else
// pkgName, or pkgName and a number when another package the generated code
// uses has that name.
func (l *lowerer) mixinName(path, pkgName string) string {
	if path == l.out.SchemaPath {
		return "sch"
	}
	if n, ok := l.mixinNames[path]; ok {
		return n
	}
	taken := func(n string) bool {
		if n == l.out.Package || n == "schema" || n == "sch" {
			return true
		}
		if p, ok := l.owned[n]; ok && p != path {
			return true
		}
		for p, m := range l.mixinNames {
			if m == n && p != path {
				return true
			}
		}
		for _, st := range l.state {
			if p, ok := st.paths[n]; ok && p != path {
				return true
			}
		}
		return false
	}
	n := pkgName
	for i := 2; taken(n); i++ {
		n = pkgName + strconv.Itoa(i)
	}
	if l.mixinNames == nil {
		l.mixinNames = map[string]string{}
	}
	l.mixinNames[path] = n
	return n
}

// checkMixinFields refuses each velox.Field call of mixin mb that names a
// column s's entity does not have, or gives it another Go type, at the
// call's line.
func (l *lowerer) checkMixinFields(s *entState, mb MixinBound) {
	local := l.out.GenPath
	if local == "" {
		local = l.out.Package
	}
	for _, c := range mb.Fields {
		var f *OutField
		for _, of := range s.out.Fields {
			if of.Column == c.Name {
				f = of
				break
			}
		}
		if f == nil {
			l.errs = append(l.errs, &Error{Pos: c.Pos, Schema: s.name, Reason: fmt.Sprintf("%s: %s, which lists %s, has no column %q", c.Call, s.name, mb.Name, c.Name)})
			continue
		}
		if c.Type == "" {
			continue
		}
		x, err := parser.ParseExpr(f.GoType)
		if err != nil {
			continue
		}
		qual := func(n string) string {
			if p, ok := s.paths[n]; ok {
				return p
			}
			return l.owned[n]
		}
		if got := canonicalType(x, qual, local); got != c.Type {
			l.errs = append(l.errs, &Error{Pos: c.Pos, Schema: s.name, Reason: fmt.Sprintf("%s: %s's column %q is of type %s", c.Call, s.name, c.Name, f.GoType)})
		}
	}
}
