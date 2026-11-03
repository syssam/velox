package codegen

import (
	"strconv"
)

// runtimeFile is the file of package runtime, below the generated package.
const runtimeFile = "runtime/runtime_velox.go"

// runtimeNeeds reports what e takes from the runtime: its schema, read for
// defaults and validators, and the Hooks and Policy the schema package
// declares for it.
func runtimeNeeds(e *OutEntity) (schema, hooks, policy bool) {
	return e.NeedsSch, e.Bound.Hooks, e.Bound.Policy
}

// renderRuntimeHolder writes runtime_velox.go: the type VeloxRuntime, which
// package runtime fills, the function it calls with it, and veloxRuntime,
// which every Descriptor function calls first.
func renderRuntimeHolder(o *Out) string {
	var w writer
	w.WriteString(header)
	w.p("package %s", o.Package)
	w.p("")
	schemas := false
	for _, e := range o.Entities {
		schemas = schemas || e.NeedsSch
	}
	w.p("import (")
	w.p("%s", strconv.Quote("github.com/syssam/velox"))
	if schemas {
		w.p("%s", strconv.Quote("github.com/syssam/velox/schema"))
	}
	w.p(")")
	w.p("")
	w.p("// VeloxRuntime is what %s takes from the schema package when the program", o.Package)
	w.p("// starts: the schemas, whose defaults and validators the entities read, and")
	w.p("// the Hooks and Policy methods the schema package declares. The schema")
	w.p("// package imports %s, so %s cannot import it: package runtime fills", o.Package, o.Package)
	w.p("// VeloxRuntime. Applications import that package, and do not use this type:")
	w.p("//")
	w.p("//	import _ %s", strconv.Quote(o.GenPath+"/runtime"))
	w.p("type VeloxRuntime struct {")
	for _, e := range o.Entities {
		schema, hooks, policy := runtimeNeeds(e)
		if schema {
			w.p("%s schema.Interface", e.Name)
		}
		if hooks {
			w.p("%sHooks func() []velox.Hook[%s]", e.Name, e.Name)
		}
		if policy {
			w.p("%sPolicy func() velox.Policy[%s]", e.Name, e.Name)
		}
	}
	w.p("}")
	w.p("")
	w.p("var veloxRT *VeloxRuntime")
	w.p("")
	w.p("// SetVeloxRuntime is called by package runtime, once, when the program")
	w.p("// starts; applications do not call it. A second call panics.")
	w.p("func SetVeloxRuntime(r VeloxRuntime) {")
	w.p("if veloxRT != nil {")
	w.p("panic(%s)", strconv.Quote(o.Package+": SetVeloxRuntime called twice"))
	w.p("}")
	w.p("veloxRT = &r")
	w.p("}")
	w.p("")
	w.p("// veloxRuntime returns what package runtime set. It panics when the program")
	w.p("// does not link package runtime: the entities would run without the")
	w.p("// schema's hooks, policies and defaults.")
	w.p("func veloxRuntime() *VeloxRuntime {")
	w.p("if veloxRT == nil {")
	w.p("panic(%s)", strconv.Quote(o.Package+": the schema's hooks, policies and defaults are not linked: import _ "+strconv.Quote(o.GenPath+"/runtime")+" in package main, and in the tests that use "+o.Package))
	w.p("}")
	w.p("return veloxRT")
	w.p("}")
	return w.String()
}

// renderRuntime writes runtime/runtime_velox.go, package runtime, whose init
// gives the generated package the schemas and their methods.
func renderRuntime(o *Out) string {
	gen, sch := o.Package, "sch"
	if gen == sch {
		sch = "schemapkg"
	}
	var w writer
	w.WriteString(header)
	w.p("// Package runtime gives %s what it takes from the schema package: the", o.Package)
	w.p("// schemas and the Hooks and Policy methods they declare. Import it, for its")
	w.p("// effect, in package main and in the tests that use %s:", o.Package)
	w.p("//")
	w.p("//\timport _ %s", strconv.Quote(o.GenPath+"/runtime"))
	w.p("//")
	w.p("// %s panics at the first use of an entity when it is not linked.", o.Package)
	w.p("package runtime")
	w.p("")
	w.p("import (")
	w.p("%s %s", gen, strconv.Quote(o.GenPath))
	w.p("%s %s", sch, strconv.Quote(o.SchemaPath))
	w.p(")")
	w.p("")
	w.p("func init() {")
	w.p("%s.SetVeloxRuntime(%s.VeloxRuntime{", gen, gen)
	for _, e := range o.Entities {
		schema, hooks, policy := runtimeNeeds(e)
		if schema {
			w.p("%s: &%s.%s{},", e.Name, sch, e.Name)
		}
		if hooks {
			w.p("%sHooks: (&%s.%s{}).Hooks,", e.Name, sch, e.Name)
		}
		if policy {
			w.p("%sPolicy: (&%s.%s{}).Policy,", e.Name, sch, e.Name)
		}
	}
	w.p("})")
	w.p("}")
	return w.String()
}
