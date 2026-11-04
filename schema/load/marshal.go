// Package load is for the program velox-gen writes, and for nothing else. That
// program imports the schema package, passes its schemas to Marshal and prints
// the result, which velox-gen reads back. The JSON is internal to velox: its
// format changes with velox-gen, which is the only reader.
package load

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/syssam/velox/internal/codegen/loadjson"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/internal/desc"
)

// Marshal describes the schemas of the package pkgPath, in JSON. The program
// velox-gen writes calls it.
//
// The schemas come out sorted by name, and the output does not depend on the
// order of the arguments. A schema that has a Hooks, Policy or Interceptors
// method is refused: those are methods on the generated entity, not part of
// the schema. Every misuse Marshal finds, that one, a schema declared twice,
// and each misuse of a field or edge builder, is in the JSON, which velox-gen
// reports; the error is only that of encoding the JSON.
func Marshal(pkgPath string, schemas ...schema.Interface) ([]byte, error) {
	m := &loadjson.Model{Version: loadjson.Version, Package: pkgPath, Schemas: make([]*loadjson.Schema, 0, len(schemas))}
	seen := make(map[string]bool, len(schemas))
	for _, s := range schemas {
		if s == nil {
			m.Errors = append(m.Errors, loadjson.Error{Reason: "a schema is nil"})
			continue
		}
		name := typeName(reflect.TypeOf(s))
		if name != "" && seen[name] {
			m.Errors = append(m.Errors, loadjson.Error{Schema: name, Reason: "declared twice"})
			continue
		}
		seen[name] = true
		l := &loader{name: name}
		if ls := l.load(s); ls != nil {
			m.Schemas = append(m.Schemas, ls)
		}
		m.Errors = append(m.Errors, l.errs...)
	}
	slices.SortFunc(m.Schemas, func(a, b *loadjson.Schema) int { return strings.Compare(a.Name, b.Name) })
	slices.SortStableFunc(m.Errors, func(a, b loadjson.Error) int { return strings.Compare(a.Schema, b.Schema) })
	return json.MarshalIndent(m, "", "  ")
}

// typeName is the name of a schema's type, or of the type a pointer points to.
func typeName(t reflect.Type) string {
	if t.Kind() == reflect.Pointer {
		return t.Elem().Name()
	}
	return t.Name()
}

// loader loads one schema and collects its errors.
type loader struct {
	name     string
	mixinPos string // the line of the schema's Mixin method, or ""
	errs     []loadjson.Error
}

// mixinPkg prefixes the functions of package schema/mixin, whose elements
// are declared on the line of the schema's Mixin method instead.
const mixinPkg = "github.com/syssam/velox/schema/mixin."

// pos is the file:line of a counter that desc.Caller returned: the line of
// the schema's Mixin method for an element that a mixin of velox declares,
// since the user wrote that line and not the mixin's, else the
// constructor's call.
func (l *loader) pos(pc uintptr) string {
	if l.mixinPos != "" && strings.HasPrefix(desc.FuncOf(pc), mixinPkg) {
		return l.mixinPos
	}
	return desc.PosOf(pc)
}

// fail records a misuse at pos, of the element where names ("" for the schema).
func (l *loader) fail(pos, where, format string, args ...any) {
	l.errs = append(l.errs, loadjson.Error{Pos: pos, Schema: l.name, Where: where, Reason: fmt.Sprintf(format, args...)})
}

// call runs f and returns its result, turning a panic into an error that names
// the method.
func call[T any](method string, f func() T) (v T, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s panicked: %v", method, r)
		}
	}()
	return f(), nil
}

// position is where an element was declared.
func position(j int, mixin int, mixedIn bool) loadjson.Position {
	if mixedIn {
		return loadjson.Position{Index: j, MixedIn: true, MixinIndex: mixin}
	}
	return loadjson.Position{Index: j}
}

func (l *loader) load(s schema.Interface) (out *loadjson.Schema) {
	t := reflect.TypeOf(s)
	if l.name == "" {
		l.fail("", "", "the type %v has no name", t)
		return nil
	}
	// Annotation methods and the JSON encoding of annotations and defaults run
	// user code outside call; a panic there is still the schema's error.
	defer func() {
		if r := recover(); r != nil {
			out = nil
			l.fail("", "", "panicked: %v", r)
		}
	}()
	return l.build(t, s)
}

// elements is the source of one list of fields, edges or indexes: a schema or
// a mixin.
type elements struct {
	fields      []schema.Field
	edges       []schema.Edge
	indexes     []schema.Index
	annotations []schema.Annotation
}

// read calls the methods of a schema or a mixin. A method that panics is
// recorded, under prefix, and the others are still read.
func (l *loader) read(prefix string, fields func() []schema.Field, edges func() []schema.Edge, indexes func() []schema.Index, annotations func() []schema.Annotation) (e elements) {
	var err error
	if e.fields, err = call("Fields", fields); err != nil {
		l.fail("", "", "%s%v", prefix, err)
	}
	if e.edges, err = call("Edges", edges); err != nil {
		l.fail("", "", "%s%v", prefix, err)
	}
	if e.indexes, err = call("Indexes", indexes); err != nil {
		l.fail("", "", "%s%v", prefix, err)
	}
	if e.annotations, err = call("Annotations", annotations); err != nil {
		l.fail("", "", "%s%v", prefix, err)
	}
	return e
}

func (l *loader) build(t reflect.Type, s schema.Interface) *loadjson.Schema {
	vt := t
	if vt.Kind() == reflect.Pointer {
		vt = vt.Elem()
	}
	// The method set of a value type leaves out pointer-receiver methods.
	// velox-gen binds a Hooks, Policy or Interceptors method the schema
	// declares itself,
	// from the package's source, and the loader leaves such a method out of
	// what it compiles: one seen here is promoted from an embedded type, or
	// the schema was loaded without velox-gen.
	for _, h := range []string{"Hooks", "Policy", "Interceptors"} {
		if _, ok := reflect.PointerTo(vt).MethodByName(h); !ok {
			continue
		}
		result := "velox.Policy[gen." + vt.Name() + "]"
		switch h {
		case "Hooks":
			result = "[]velox.Hook[gen." + vt.Name() + "]"
		case "Interceptors":
			result = "[]velox.Interceptor[gen." + vt.Name() + "]"
		}
		want := fmt.Sprintf("func (%s) %s() %s", vt.Name(), h, result)
		article := "a"
		if h == "Interceptors" {
			article = "an"
		}
		if from, pos := promotedFrom(vt, h); from != nil {
			l.fail(pos, "", "has %s %s method promoted from %s, which velox-gen does not bind: declare %[2]s on the schema itself, in the schema package: %[4]s", article, h, from, want)
			continue
		}
		l.fail(methodPos(vt, h), "", "has %s %s method, which velox-gen binds from the schema package's source, as %s; the schema was loaded without velox-gen", article, h, want)
	}
	l.mixinPos = methodPos(vt, "Mixin")
	out := &loadjson.Schema{Name: l.name}
	annos := map[string]schema.Annotation{}
	add := func(e elements, prefix string, mixin int, mixedIn bool) {
		for j, f := range e.fields {
			if lf := l.buildField(f, prefix, j, position(j, mixin, mixedIn)); lf != nil {
				out.Fields = append(out.Fields, lf)
			}
		}
		for j, ed := range e.edges {
			out.Edges = append(out.Edges, l.buildEdge(ed, prefix, j, position(j, mixin, mixedIn))...)
		}
		for j, ix := range e.indexes {
			if li := l.buildIndex(ix, prefix, j, position(j, mixin, mixedIn)); li != nil {
				out.Indexes = append(out.Indexes, li)
			}
		}
		if err := mergeAnnotations(annos, e.annotations); err != nil {
			l.fail("", "", "%s%v", prefix, err)
		}
	}
	mixins, err := call("Mixin", s.Mixin)
	if err != nil {
		l.fail("", "", "%v", err)
	}
	out.Pointer = !vt.Implements(reflect.TypeFor[schema.Interface]())
	for i, m := range mixins {
		prefix := fmt.Sprintf("Mixin()[%d]: ", i)
		if isNil(m) {
			l.fail("", "", "Mixin()[%d] is nil", i)
			out.Mixins = append(out.Mixins, loadjson.Mixin{})
			continue
		}
		out.Mixins = append(out.Mixins, mixinOf(m))
		add(l.read(prefix, m.Fields, m.Edges, m.Indexes, m.Annotations), prefix, i, true)
	}
	add(l.read("", s.Fields, s.Edges, s.Indexes, s.Annotations), "", 0, false)
	if out.Annotations, err = marshalAnnotations(annos); err != nil {
		l.fail("", "", "%v", err)
	}
	return out
}

// mixinOf describes the mixin m, which is not nil: the type velox-gen reads
// its behaviour methods from, and whether m is its zero value.
func mixinOf(m schema.Mixin) loadjson.Mixin {
	v := reflect.ValueOf(m)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	t := v.Type()
	name, _, generic := strings.Cut(t.Name(), "[")
	return loadjson.Mixin{PkgPath: t.PkgPath(), Name: name, Generic: generic, NonZero: !v.IsZero()}
}

// methodPos is the file:line of the method name of the schema type t, or ""
// when the binary does not say, as for a method a struct embeds.
// promotedFrom returns the embedded type of struct t that declares the method
// name t has through embedding, at any depth, and the method's position; nil
// when t declares it itself, or no embedded type does.
func promotedFrom(t reflect.Type, name string) (reflect.Type, string) {
	if methodPos(t, name) != "" || t.Kind() != reflect.Struct {
		return nil, ""
	}
	for f := range t.Fields() {
		if !f.Anonymous {
			continue
		}
		ft := f.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if _, ok := reflect.PointerTo(ft).MethodByName(name); !ok {
			continue
		}
		if from, pos := promotedFrom(ft, name); from != nil {
			return from, pos
		}
		return ft, methodPos(ft, name)
	}
	return nil, ""
}

func methodPos(t reflect.Type, name string) string {
	for _, typ := range []reflect.Type{t, reflect.PointerTo(t)} {
		m, ok := typ.MethodByName(name)
		if !ok {
			continue
		}
		pc := m.Func.Pointer()
		fn := runtime.FuncForPC(pc)
		if fn == nil {
			continue
		}
		if file, line := fn.FileLine(pc); file != "" && !strings.HasPrefix(file, "<") {
			return file + ":" + strconv.Itoa(line)
		}
	}
	return ""
}

// mergeAnnotations adds list to into: an annotation whose name is already
// there is merged when it implements schema.Merger, and replaces the old one
// otherwise.
func mergeAnnotations(into map[string]schema.Annotation, list []schema.Annotation) error {
	for i, a := range list {
		if isNil(a) {
			return fmt.Errorf("Annotations()[%d] is nil", i)
		}
		n := a.Name()
		if old, ok := into[n]; ok {
			if m, ok := a.(schema.Merger); ok {
				a = m.Merge(old)
			}
		}
		into[n] = a
	}
	return nil
}

func marshalAnnotations(annos map[string]schema.Annotation) (map[string]json.RawMessage, error) {
	if len(annos) == 0 {
		return nil, nil
	}
	out := make(map[string]json.RawMessage, len(annos))
	for _, n := range slices.Sorted(maps.Keys(annos)) {
		b, err := json.Marshal(annos[n])
		if err != nil {
			return nil, fmt.Errorf("annotation %q does not marshal: %w", n, err)
		}
		out[n] = b
	}
	return out, nil
}

func listAnnotations(list []desc.Annotation) (map[string]json.RawMessage, error) {
	annos := map[string]schema.Annotation{}
	if err := mergeAnnotations(annos, list); err != nil {
		return nil, err
	}
	return marshalAnnotations(annos)
}

func isFunc(v any) bool { return v != nil && reflect.TypeOf(v).Kind() == reflect.Func }

// buildField loads one field, or records why it cannot and returns nil.
func (l *loader) buildField(f schema.Field, prefix string, j int, pos loadjson.Position) *loadjson.Field {
	if isNil(f) {
		l.fail("", "", "%sFields()[%d] is nil", prefix, j)
		return nil
	}
	d, err := call("Descriptor", f.Descriptor)
	if err != nil {
		l.fail("", "", "%sFields()[%d]: %v", prefix, j, err)
		return nil
	}
	if d == nil {
		l.fail("", "", "%sFields()[%d] has no descriptor", prefix, j)
		return nil
	}
	where := fmt.Sprintf("field %q", d.Name)
	for _, err := range d.Errs {
		l.fail(l.pos(d.PC), where, "%v", err)
	}
	if len(d.Errs) > 0 {
		return nil
	}
	out := &loadjson.Field{
		Name: d.Name, Type: d.Type, Size: d.Size,
		Optional: d.Optional, Nillable: d.Nillable, Immutable: d.Immutable, Unique: d.Unique,
		Sensitive:     d.Sensitive,
		DefaultFunc:   d.DefaultFunc != nil,
		UpdateDefault: d.UpdateDefault != nil,
		Validators:    len(d.Validators),
		StorageKey:    d.StorageKey, SchemaType: d.SchemaType, SchemaDefault: d.SchemaDefault, Comment: d.Comment, StructTag: d.StructTag,
		Position: pos, Pos: l.pos(d.PC),
	}
	if d.GoType != nil {
		g := loadjson.GoType(*d.GoType)
		out.GoType = &g
	}
	if isFunc(d.DefaultFunc) {
		r := funcRef(d.DefaultFunc)
		out.DefaultRef = &r
	}
	if isFunc(d.UpdateDefault) {
		r := funcRef(d.UpdateDefault)
		out.UpdateDefaultRef = &r
	}
	for _, v := range d.Validators {
		if isFunc(v) {
			out.ValidatorRefs = append(out.ValidatorRefs, funcRef(v))
		} else {
			out.ValidatorRefs = append(out.ValidatorRefs, loadjson.FuncRef{Why: "not a function"})
		}
	}
	ok := true
	switch {
	case d.Default == nil:
	case isFunc(d.Default):
		out.DefaultFunc = true
		r := funcRef(d.Default)
		out.DefaultRef = &r
	default:
		b, err := json.Marshal(d.Default)
		if err != nil {
			l.fail(l.pos(d.PC), where, "Default does not marshal: %v", err)
			ok = false
		}
		out.Default = b
	}
	for _, e := range d.Enums {
		out.Enums = append(out.Enums, loadjson.Enum(e))
	}
	for _, c := range d.Checks {
		lc := loadjson.Check{Kind: c.Kind}
		for _, a := range c.Args {
			b, err := json.Marshal(a)
			if err != nil {
				l.fail(l.pos(d.PC), where, "%s argument does not marshal: %v", c.Kind, err)
				ok = false
			}
			lc.Args = append(lc.Args, b)
		}
		out.Checks = append(out.Checks, lc)
	}
	if out.Annotations, err = listAnnotations(d.Annotations); err != nil {
		l.fail(l.pos(d.PC), where, "%v", err)
		ok = false
	}
	if !ok {
		return nil
	}
	return out
}

// buildEdge loads one edge, and the inverse declared inline with it, or
// records why it cannot and returns nil.
func (l *loader) buildEdge(e schema.Edge, prefix string, j int, pos loadjson.Position) []*loadjson.Edge {
	if isNil(e) {
		l.fail("", "", "%sEdges()[%d] is nil", prefix, j)
		return nil
	}
	d, err := call("Descriptor", e.Descriptor)
	if err != nil {
		l.fail("", "", "%sEdges()[%d]: %v", prefix, j, err)
		return nil
	}
	if d == nil {
		l.fail("", "", "%sEdges()[%d] has no descriptor", prefix, j)
		return nil
	}
	n := len(l.errs)
	for _, err := range d.Errs {
		l.fail(l.pos(d.PC), fmt.Sprintf("edge %q", d.Name), "%v", err)
	}
	if d.Ref != nil {
		for _, err := range d.Ref.Errs {
			l.fail(l.pos(cmp.Or(d.Ref.PC, d.PC)), fmt.Sprintf("edge %q", d.Ref.Name), "%v", err)
		}
	}
	first := l.edgeOf(d, pos)
	out := []*loadjson.Edge{first}
	if d.Ref != nil {
		inv := l.edgeOf(d.Ref, pos)
		inv.Inline, inv.Inverse, inv.Ref = true, true, d.Name
		out = append(out, inv)
	}
	if len(l.errs) > n {
		return nil
	}
	return out
}

func (l *loader) edgeOf(d *desc.Edge, pos loadjson.Position) *loadjson.Edge {
	out := &loadjson.Edge{
		Name: d.Name, Type: d.Type, TypePkg: d.TypePkg, Inverse: d.Inverse, Ref: d.RefName,
		Unique: d.Unique, Required: d.Required, Immutable: d.Immutable,
		Field: d.Field, OnDelete: d.OnDelete, Table: d.Table, Columns: d.Columns, Symbols: d.Symbols, Comment: d.Comment,
		Position: pos, Pos: l.pos(d.PC),
	}
	var err error
	if out.Annotations, err = listAnnotations(d.Annotations); err != nil {
		l.fail(l.pos(d.PC), fmt.Sprintf("edge %q", d.Name), "%v", err)
	}
	return out
}

// buildIndex loads one index, or records why it cannot and returns nil.
func (l *loader) buildIndex(x schema.Index, prefix string, j int, pos loadjson.Position) *loadjson.Index {
	if isNil(x) {
		l.fail("", "", "%sIndexes()[%d] is nil", prefix, j)
		return nil
	}
	d, err := call("Descriptor", x.Descriptor)
	if err != nil {
		l.fail("", "", "%sIndexes()[%d]: %v", prefix, j, err)
		return nil
	}
	if d == nil {
		l.fail("", "", "%sIndexes()[%d] has no descriptor", prefix, j)
		return nil
	}
	out := &loadjson.Index{
		Fields: d.Fields, Edges: d.Edges, Unique: d.Unique, StorageKey: d.StorageKey, Where: d.Where,
		Position: pos, Pos: l.pos(d.PC),
	}
	if out.Annotations, err = listAnnotations(d.Annotations); err != nil {
		l.fail(l.pos(d.PC), "", "%sIndexes()[%d]: %v", prefix, j, err)
		return nil
	}
	return out
}

// isNil reports whether v is nil or a nil pointer, map, slice or func.
func isNil(v any) bool {
	if v == nil {
		return true
	}
	switch rv := reflect.ValueOf(v); rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Interface:
		return rv.IsNil()
	}
	return false
}
