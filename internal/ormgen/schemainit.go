package ormgen

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"

	vgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

// basicType is the Go type schema descriptors hold a field's default and
// validators at: the basic type of the field, not its custom Go type.
func basicType(f *vgen.Field, full string) string {
	if f.IsJSON() {
		return full
	}
	switch f.Type.Type {
	case field.TypeEnum:
		return "string"
	case field.TypeBytes:
		return "[]byte"
	case field.TypeTime:
		return "time.Time"
	case field.TypeUUID:
		return "uuid.UUID"
	case field.TypeOther:
		return full
	}
	return f.Type.Type.String()
}

// runtimeFields are the fields of e whose defaults or validators schemainit
// stitches.
func runtimeFields(e *Entity) []*Field {
	var fields []*Field
	for _, f := range e.Fields {
		if f.Hidden || (f.IsID && !e.T.ID.UserDefined) {
			continue
		}
		if f.F.Default || f.F.UpdateDefault || f.F.Validators > 0 {
			fields = append(fields, f)
		}
	}
	return fields
}

// hasRuntime reports whether the schema of e has anything only schemainit
// can provide. Package entity marks such an entity, and the runtime
// refuses to run it until schemainit has: an entity without its policy would
// answer every caller.
func hasRuntime(e *Entity) bool {
	return len(runtimeFields(e)) > 0 || len(e.T.PolicyPositions()) > 0 || len(e.T.HookPositions()) > 0
}

// scansDefault reports whether the field's Go type cannot be converted from
// its basic type and has a Scan method to parse the schema default with.
func scansDefault(f *vgen.Field) bool {
	rt := f.Type.RType
	if rt == nil {
		return false
	}
	switch rt.Kind {
	case reflect.Struct, reflect.Array, reflect.Slice, reflect.Map:
		_, ok := rt.Methods["Scan"]
		return ok
	}
	return false
}

// schemaInit generates the one package that imports the user's schema: per
// entity an init() that reads default values, update defaults and validators
// from the schema descriptors and stores them in the entity's runtime table.
// It reaches the table through the entity's slot (orm.Slot), not through
// package entity: an edit to a schema field recompiles both packages, and
// this way at the same time.
//
// It is one package on purpose. Go rebuilds every direct importer of a
// package whose compiled object changed, and a schema package's object
// changes on any edit to it; with the schema read from here, such an edit
// rebuilds this package and nothing that imports the entities.
func (g *Generator) schemaInit(graph *vgen.Graph) (map[string][]byte, error) {
	schemaPath := graph.Schema
	if schemaPath == "" {
		return nil, fmt.Errorf("velox: the graph has no schema package path")
	}
	im := g.Imports()
	im.use(ormPath, "orm")
	slotsPkg := im.use(g.cfg.Package+"/internal/slots", "slots")
	schemaPkg := im.use(schemaPath, "schemapkg")
	var b bytes.Buffer
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	for _, e := range g.ents {
		t := e.T
		fields := runtimeFields(e)
		policies, hooks := t.PolicyPositions(), t.HookPositions()
		if !hasRuntime(e) {
			continue
		}
		w("\n%s%s\n\n", fileMarker, fileName(e))
		w("// init stitches the defaults, validators, hooks and policy of the %s schema.\nfunc init() {\n\t%s.%s.Stitch(func(m *orm.Meta) {\n", e.Name, slotsPkg, e.Name)
		mixins := map[int]bool{}
		own := false
		for _, f := range fields {
			if p := f.F.Position; p != nil && p.MixedIn {
				mixins[p.MixinIndex] = true
			} else {
				own = true
			}
		}
		needMixin := len(mixins) > 0 || len(t.MixedInPolicies()) > 0 || len(t.MixedInHooks()) > 0
		if needMixin {
			w("\tmixin := %s.%s{}.Mixin()\n", schemaPkg, t.Name)
			for i := 0; i < t.NumMixin(); i++ {
				if mixins[i] {
					w("\tmixinFields%d := mixin[%d].Fields()\n", i, i)
				}
			}
		}
		if len(policies) > 0 {
			// The mixins' policies first, then the schema's own, as velox
			// composes them.
			var args []string
			for _, i := range t.MixedInPolicies() {
				args = append(args, fmt.Sprintf("mixin[%d]", i))
			}
			args = append(args, fmt.Sprintf("%s.%s{}", schemaPkg, t.Name))
			w("\tm.Policy = %s.NewPolicies(%s)\n", im.use("github.com/syssam/velox/privacy", "privacy"), strings.Join(args, ", "))
		}
		if len(hooks) > 0 {
			for _, i := range t.MixedInHooks() {
				w("\tmixinHooks%d := mixin[%d].Hooks()\n", i, i)
			}
			var list []string
			ownHooks := false
			for _, p := range hooks {
				if p.MixedIn {
					list = append(list, fmt.Sprintf("mixinHooks%d[%d]", p.MixinIndex, p.Index))
				} else {
					ownHooks = true
					list = append(list, fmt.Sprintf("hooks[%d]", p.Index))
				}
			}
			if ownHooks {
				w("\thooks := %s.%s{}.Hooks()\n", schemaPkg, t.Name)
			}
			w("\tm.Hooks = []%s.Hook{%s}\n", im.use("github.com/syssam/velox", "velox"), strings.Join(list, ", "))
		}
		if own {
			w("\tfields := %s.%s{}.Fields()\n", schemaPkg, t.Name)
		}
		for _, f := range fields {
			full := f.Base
			if f.Enum != "" {
				_, types, _, _ := g.enumNames(e)
				full = im.use(g.cfg.Package+"/"+e.Pkg, e.Pkg+"pkg") + "." + types[f]
			} else {
				ref, err := goTypeOf(f.F)
				if err != nil {
					return nil, err
				}
				if ref.PkgPath != "" {
					im.use(ref.PkgPath, ref.PkgName)
				}
			}
			basic := basicType(f.F, full)
			switch basic {
			case "time.Time":
				im.use("time", "time")
			case "uuid.UUID":
				im.use("github.com/google/uuid", "uuid")
			}
			src := "fields"
			idx := 0
			if p := f.F.Position; p != nil {
				idx = p.Index
				if p.MixedIn {
					src = fmt.Sprintf("mixinFields%d", p.MixinIndex)
				}
			}
			// What the descriptor holds becomes what the runtime calls in package
			// orm (DefaultValue, DefaultFunc, Validators), not in a closure
			// written here: see orm/stitch.go for what the closures cost.
			w("\t{ // %s\n\t\td, f := %s[%d].Descriptor(), m.Field(%q)\n", f.F.Name, src, idx, f.Col)
			if f.F.Default {
				switch {
				case f.F.DefaultFunc():
					w("\t\tf.Default = orm.DefaultFunc[%s](d.Default)\n", full)
				case f.Enum != "":
					w("\t\tf.Default = orm.DefaultValue(%s(d.Default.(string)))\n", full)
				case f.F.HasGoType() && !f.F.IsJSON() && !f.F.IsOther() && scansDefault(f.F):
					// The Go type is not a named form of the basic type (a
					// decimal stored as a string): it parses the default the
					// way it parses a column value.
					w("\t\tvar def %s\n\t\tif err := (&def).Scan(d.Default); err != nil {\n\t\t\tpanic(\"velox: default of %s.%s: \" + err.Error())\n\t\t}\n\t\tf.Default = orm.DefaultValue(def)\n", full, e.Name, f.F.Name)
				case f.F.HasGoType() && !f.F.IsJSON() && !f.F.IsOther():
					w("\t\tf.Default = orm.DefaultValue(%s(d.Default.(%s)))\n", full, basic)
				default:
					w("\t\tf.Default = orm.DefaultValue(d.Default.(%s))\n", full)
				}
			}
			if f.F.UpdateDefault {
				w("\t\tf.UpdateDefault = orm.DefaultFunc[%s](d.UpdateDefault)\n", full)
			}
			if f.F.Validators > 0 {
				if basic == full {
					w("\t\tf.Validate = orm.Validators[%s](d.Validators)\n", full)
				} else {
					w("\t\tf.Validate = orm.ValidatorsOf(d.Validators, func(v %s) %s { return %s(v) })\n", full, basic, basic)
				}
			}
			w("\t}\n")
		}
		w("\t})\n}\n\n")
	}

	var out bytes.Buffer
	out.WriteString(header)
	out.WriteString("// Package schemainit stitches the defaults, validators, hooks and policies of the schema\n// package into the runtime tables of the entities. The root package of the generated tree\n// imports it, so a program that starts its queries there has them; one that uses the\n// entity package alone imports it itself:\n//\n//\timport _ \"" + g.cfg.Package + "/schemainit\"\npackage schemainit\n\n")
	// An import block is what split reads the file by, even an empty one.
	if b.Len() == 0 {
		im = g.Imports()
	}
	out.WriteString(im.block())
	out.WriteString("\n")
	out.Write(b.Bytes())
	return split(out.Bytes(), "schemainit")
}

// slots generates the package that holds a slot per entity with schema
// runtime: the one thing package entity and package schemainit share. It
// changes when such an entity is added or removed, not when a field is.
func (g *Generator) slots() ([]byte, error) {
	var b bytes.Buffer
	for _, e := range g.ents {
		if hasRuntime(e) {
			fmt.Fprintf(&b, "\t%s orm.Slot\n", e.Name)
		}
	}
	if b.Len() == 0 {
		return nil, nil
	}
	var out bytes.Buffer
	out.WriteString(header + "// Package slots joins each entity with the code of its schema: see orm.Slot.\npackage slots\n\nimport orm \"" + ormPath + "\"\n\n// A slot per entity whose schema has defaults, validators, hooks or a policy.\nvar (\n" + b.String() + ")\n")
	return formatted(&out, "internal/slots")
}
