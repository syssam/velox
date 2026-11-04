package codegen

import (
	"maps"
	"slices"
	"strconv"
	"strings"
)

// veloxPkg is velox's import path.
const veloxPkg = "github.com/syssam/velox"

// runtimeValue reports whether entity e reads a value package runtime sets:
// its Policy, Hooks or Interceptors method, or a default or validator it
// cannot call by name.
func runtimeValue(e *OutEntity) bool {
	return len(e.Binds) > 0
}

// RuntimeImport is the import declaration a program adds to link package
// runtime, which binds the schema to the generated package at genPath.
func RuntimeImport(genPath string) string {
	return "import _ " + strconv.Quote(genPath+"/"+RuntimeDir)
}

// renderRuntimeHolder writes runtime_velox.go when the generated package
// reads values of the schema: the type VeloxRuntime, a field per value
// package runtime gives an entity (OutBind), its Policy, Hooks and
// Interceptors already bound, in the form of the Descriptor's fields;
// SetVeloxRuntime, which package runtime calls with a VeloxRuntime; and
// veloxRuntime, which every Descriptor function calls first.
func renderRuntimeHolder(o *Out) string {
	imports := map[string]string{"velox": veloxPkg}
	for _, e := range o.Entities {
		for _, b := range e.Binds {
			maps.Copy(imports, b.Imports)
		}
	}
	var w writer
	w.WriteString(header)
	w.p("package %s", o.Package)
	w.p("")
	writeImports(&w, imports)
	w.p("// VeloxRuntime is what package %s gives package %s when the program", RuntimeDir, o.Package)
	w.p("// starts: the Policy, Hooks and Interceptors of the schema and its")
	w.p("// mixins, bound, and the defaults and validators that %s cannot call", o.Package)
	w.p("// by name. The schema package imports %s, so %[1]s cannot import it.", o.Package)
	w.p("// Applications do not use this type: they import package %s for its", RuntimeDir)
	w.p("// effect.")
	w.p("type VeloxRuntime struct {")
	for _, e := range o.Entities {
		for _, b := range e.Binds {
			w.p("%s %s", b.Field, b.Type)
		}
	}
	w.p("}")
	w.p("")
	w.p("var veloxRT *VeloxRuntime")
	w.p("")
	w.p("// SetVeloxRuntime is called by package %s, once, when the program", RuntimeDir)
	w.p("// starts; applications do not call it. A second call panics. The")
	w.p("// functions it is given are not called yet: velox calls each once,")
	w.p("// when it builds the entity's metadata.")
	w.p("func SetVeloxRuntime(r VeloxRuntime) {")
	w.p("if veloxRT != nil {")
	w.p("panic(%s)", strconv.Quote(o.Package+": SetVeloxRuntime called twice"))
	w.p("}")
	w.p("veloxRT = &r")
	w.p("}")
	w.p("")
	w.p("// veloxRuntime returns what package %s set. It panics when the program", RuntimeDir)
	w.p("// does not link package %s: the entities would run without the", RuntimeDir)
	w.p("// schema's hooks, policies and defaults. velox does not keep the")
	w.p("// failure: an entity used before %s's init works once it has run.", RuntimeDir)
	w.p("func veloxRuntime() *VeloxRuntime {")
	w.p("if veloxRT == nil {")
	w.p("panic(velox.NotLinkedError(%s))", strconv.Quote(NotLinkedMessage(o.Package, o.GenPath)))
	w.p("}")
	w.p("return veloxRT")
	w.p("}")
	return w.String()
}

// NotLinkedMessage is the message of the panic of veloxRuntime, in the
// generated package pkg at genPath, when the program does not link package
// runtime: it names the line the program or the test lacks.
func NotLinkedMessage(pkg, genPath string) string {
	return pkg + ": the schema's hooks, policies and defaults are not linked: add " + RuntimeImport(genPath) +
		" to the program's main package, or to the test"
}

// renderRuntime writes runtime/runtime.go, package runtime: the package
// comment, the imports, and the init that gives the generated package the
// schema's values.
//
// Each value is set by name: a method value of the schema, such as
// sch.Doc{}.Policy, a function of the schema package, or one read from the
// descriptor of the field that declares it, with schema.FieldDescriptor.
// The compiler checks the type of each at its line.
func renderRuntime(o *Out) string {
	descs := false
	for _, e := range o.Entities {
		for _, b := range e.Binds {
			descs = descs || b.Desc != ""
		}
	}
	var w writer
	w.WriteString(header)
	w.p("// Package %s binds the schema package to package %s, which the", RuntimeDir, o.Package)
	w.p("// schema imports, when the program starts: its Hooks, Policy and")
	w.p("// Interceptors methods, and the defaults and validators %s cannot call", o.Package)
	w.p("// by name. A program imports it once, for its effect, in its main")
	w.p("// package and in each test package that uses the entities:")
	w.p("//")
	w.p("//\t%s", RuntimeImport(o.GenPath))
	w.p("//")
	w.p("// Without it, a statement on an entity fails.")
	behaviourHeader(&w, o)
	w.p("package %s", RuntimeDir)
	w.p("")
	all := map[string]string{o.Package: o.GenPath, "sch": o.SchemaPath}
	if descs {
		all["schema"] = schemaPkg
	}
	for _, e := range o.Entities {
		for _, b := range e.Binds {
			if b.Desc != "" {
				maps.Copy(all, b.Imports)
			}
		}
		for _, k := range behaviourKinds {
			for _, s := range *e.sources(k) {
				all["velox"] = veloxPkg
				maps.Copy(all, s.Imports)
			}
		}
	}
	writeImports(&w, all)
	w.p("func init() {")
	declared := map[string]bool{}
	for _, e := range o.Entities {
		for _, b := range e.Binds {
			if b.Desc != "" && !declared[b.DescVar] {
				declared[b.DescVar] = true
				w.p("%s := schema.FieldDescriptor(&sch.%s{}, %s)", b.DescVar, e.Name, strconv.Quote(b.Desc))
			}
		}
	}
	w.p("%s.SetVeloxRuntime(%[1]s.VeloxRuntime{", o.Package)
	for _, e := range o.Entities {
		for _, b := range e.Binds {
			w.p("%s: %s,", b.Field, b.Value)
		}
	}
	w.p("})")
	w.p("}")
	return w.String()
}

// behaviourHeader writes, in package runtime's comment, where each entity's
// Policy, Hooks and Interceptors come from, in the order velox runs them,
// one entity to a line, in columns:
//
//	Comment  Policy: InOrg, Comment  Hooks: InOrg
//
// It writes nothing when no entity has any. A source missing from a line,
// or a line that changes, shows in review as a changed generated line.
func behaviourHeader(w *writer, o *Out) {
	var rows [][]string
	width := make([]int, 1+len(behaviourKinds))
	for _, e := range o.Entities {
		row := []string{e.Name}
		some := false
		for _, k := range behaviourKinds {
			cell := ""
			if srcs := *e.sources(k); len(srcs) > 0 {
				names := make([]string, len(srcs))
				for i, s := range srcs {
					names[i] = s.Name
				}
				cell, some = k+": "+strings.Join(names, ", "), true
			}
			row = append(row, cell)
		}
		if some {
			rows = append(rows, row)
			for i, c := range row {
				width[i] = max(width[i], len(c))
			}
		}
	}
	if len(rows) == 0 {
		return
	}
	w.p("//")
	w.p("// Behaviour, outermost first:")
	w.p("//")
	for _, row := range rows {
		var b strings.Builder
		for i, c := range row {
			if width[i] == 0 {
				continue
			}
			if b.Len() > 0 {
				b.WriteString("  ")
			}
			b.WriteString(c + strings.Repeat(" ", width[i]-len(c)))
		}
		w.p("//\t%s", strings.TrimRight(b.String(), " "))
	}
}

// writeImports writes the import declaration of imports, by name: the
// standard packages first, then the others. A name that is the last element
// of its path is left out.
func writeImports(w *writer, imports map[string]string) {
	var std, other []string
	for n, p := range imports {
		spec := strconv.Quote(p)
		if guessName(p) != n {
			spec = n + " " + spec
		}
		if first, _, _ := strings.Cut(p, "/"); strings.Contains(first, ".") {
			other = append(other, spec)
		} else {
			std = append(std, spec)
		}
	}
	slices.Sort(std)
	slices.Sort(other)
	if len(std)+len(other) == 1 {
		w.p("import %s", slices.Concat(std, other)[0])
		w.p("")
		return
	}
	w.p("import (")
	for _, s := range std {
		w.p("%s", s)
	}
	if len(std) > 0 && len(other) > 0 {
		w.p("")
	}
	for _, s := range other {
		w.p("%s", s)
	}
	w.p(")")
	w.p("")
}
