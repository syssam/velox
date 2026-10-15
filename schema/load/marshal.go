package load

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/internal/desc"
)

// Marshal describes the schemas of the package pkgPath as a Model, in JSON.
// The program velox-gen writes calls it.
//
// The schemas come out sorted by name, and the output does not depend on the
// order of the arguments. A schema that has a Hooks, Policy or Interceptors
// method is refused: those are methods on the generated entity, not part of
// the schema.
func Marshal(pkgPath string, schemas ...schema.Interface) ([]byte, error) {
	m := &Model{Version: Version, Package: pkgPath, Schemas: make([]*Schema, 0, len(schemas))}
	seen := make(map[string]bool, len(schemas))
	for _, s := range schemas {
		if s == nil {
			return nil, fmt.Errorf("a schema is nil")
		}
		ls, err := loadSchema(s)
		if err != nil {
			return nil, err
		}
		if seen[ls.Name] {
			return nil, fmt.Errorf("schema %s: declared twice", ls.Name)
		}
		seen[ls.Name] = true
		m.Schemas = append(m.Schemas, ls)
	}
	slices.SortFunc(m.Schemas, func(a, b *Schema) int { return strings.Compare(a.Name, b.Name) })
	return json.MarshalIndent(m, "", "  ")
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
func position(j int, mixin int, mixedIn bool) Position {
	if mixedIn {
		return Position{Index: j, MixedIn: true, MixinIndex: mixin}
	}
	return Position{Index: j}
}

func loadSchema(s schema.Interface) (*Schema, error) {
	t := reflect.TypeOf(s)
	name := t.Name()
	if t.Kind() == reflect.Pointer {
		name = t.Elem().Name()
	}
	if name == "" {
		return nil, fmt.Errorf("schema %v: the type has no name", t)
	}
	out, err := build(name, t, s)
	if err != nil {
		return nil, fmt.Errorf("schema %s: %w", name, err)
	}
	return out, nil
}

// elements is the source of one list of fields, edges or indexes: a schema or
// a mixin.
type elements struct {
	fields      []schema.Field
	edges       []schema.Edge
	indexes     []schema.Index
	annotations []schema.Annotation
}

func read(fields func() []schema.Field, edges func() []schema.Edge, indexes func() []schema.Index, annotations func() []schema.Annotation) (e elements, err error) {
	if e.fields, err = call("Fields", fields); err != nil {
		return e, err
	}
	if e.edges, err = call("Edges", edges); err != nil {
		return e, err
	}
	if e.indexes, err = call("Indexes", indexes); err != nil {
		return e, err
	}
	e.annotations, err = call("Annotations", annotations)
	return e, err
}

func build(name string, t reflect.Type, s schema.Interface) (*Schema, error) {
	for _, h := range []string{"Hooks", "Policy", "Interceptors"} {
		if _, ok := t.MethodByName(h); ok {
			return nil, fmt.Errorf("has a %s method; hooks and policies are methods on the generated entity (schema design §3), not in the schema", h)
		}
	}
	mixins, err := call("Mixin", s.Mixin)
	if err != nil {
		return nil, err
	}
	out := &Schema{Name: name}
	annos := map[string]schema.Annotation{}
	add := func(e elements, mixin int, mixedIn bool) error {
		for j, f := range e.fields {
			lf, err := buildField(f, j, position(j, mixin, mixedIn))
			if err != nil {
				return err
			}
			out.Fields = append(out.Fields, lf)
		}
		for j, ed := range e.edges {
			es, err := buildEdge(ed, j, position(j, mixin, mixedIn))
			if err != nil {
				return err
			}
			out.Edges = append(out.Edges, es...)
		}
		for j, ix := range e.indexes {
			li, err := buildIndex(ix, j, position(j, mixin, mixedIn))
			if err != nil {
				return err
			}
			out.Indexes = append(out.Indexes, li)
		}
		return mergeAnnotations(annos, e.annotations)
	}
	for i, m := range mixins {
		if isNil(m) {
			return nil, fmt.Errorf("Mixin()[%d] is nil", i)
		}
		e, err := read(m.Fields, m.Edges, m.Indexes, m.Annotations)
		if err != nil {
			return nil, fmt.Errorf("Mixin()[%d]: %w", i, err)
		}
		if err := add(e, i, true); err != nil {
			return nil, fmt.Errorf("Mixin()[%d]: %w", i, err)
		}
	}
	e, err := read(s.Fields, s.Edges, s.Indexes, s.Annotations)
	if err != nil {
		return nil, err
	}
	if err := add(e, 0, false); err != nil {
		return nil, err
	}
	if out.Annotations, err = marshalAnnotations(annos); err != nil {
		return nil, err
	}
	return out, nil
}

// mergeAnnotations adds list to into: an annotation whose name is already
// there is merged when it implements schema.Merger, and replaces the old one
// otherwise.
func mergeAnnotations(into map[string]schema.Annotation, list []schema.Annotation) error {
	for i, a := range list {
		if a == nil {
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
	for n, a := range annos {
		b, err := json.Marshal(a)
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

func buildField(f schema.Field, j int, pos Position) (*Field, error) {
	if isNil(f) {
		return nil, fmt.Errorf("Fields()[%d] is nil", j)
	}
	d, err := call("Descriptor", f.Descriptor)
	if err != nil {
		return nil, fmt.Errorf("Fields()[%d]: %w", j, err)
	}
	if d == nil {
		return nil, fmt.Errorf("Fields()[%d] has no descriptor", j)
	}
	if d.Err != nil {
		return nil, d.Err
	}
	out := &Field{
		Name: d.Name, Type: d.Type, Size: d.Size,
		Optional: d.Optional, Nillable: d.Nillable, Immutable: d.Immutable, Unique: d.Unique,
		Sensitive: d.Sensitive, Tenant: d.Tenant,
		DefaultFunc:   d.DefaultFunc != nil,
		UpdateDefault: d.UpdateDefault != nil,
		Validators:    len(d.Validators),
		StorageKey:    d.StorageKey, SchemaType: d.SchemaType, Comment: d.Comment, StructTag: d.StructTag,
		Position: pos,
	}
	if d.GoType != nil {
		g := GoType(*d.GoType)
		out.GoType = &g
	}
	switch {
	case d.Default == nil:
	case isFunc(d.Default):
		out.DefaultFunc = true
	default:
		b, err := json.Marshal(d.Default)
		if err != nil {
			return nil, fmt.Errorf("field %q: Default does not marshal: %w", d.Name, err)
		}
		out.Default = b
	}
	for _, e := range d.Enums {
		out.Enums = append(out.Enums, Enum(e))
	}
	for _, c := range d.Checks {
		lc := Check{Kind: c.Kind}
		for _, a := range c.Args {
			b, err := json.Marshal(a)
			if err != nil {
				return nil, fmt.Errorf("field %q: %s argument does not marshal: %w", d.Name, c.Kind, err)
			}
			lc.Args = append(lc.Args, b)
		}
		out.Checks = append(out.Checks, lc)
	}
	if out.Annotations, err = listAnnotations(d.Annotations); err != nil {
		return nil, fmt.Errorf("field %q: %w", d.Name, err)
	}
	return out, nil
}

func buildEdge(e schema.Edge, j int, pos Position) ([]*Edge, error) {
	if isNil(e) {
		return nil, fmt.Errorf("Edges()[%d] is nil", j)
	}
	d, err := call("Descriptor", e.Descriptor)
	if err != nil {
		return nil, fmt.Errorf("Edges()[%d]: %w", j, err)
	}
	if d == nil {
		return nil, fmt.Errorf("Edges()[%d] has no descriptor", j)
	}
	if d.Err != nil {
		return nil, d.Err
	}
	if d.Ref != nil && d.Ref.Err != nil {
		return nil, d.Ref.Err
	}
	first, err := edgeOf(d, pos)
	if err != nil {
		return nil, err
	}
	out := []*Edge{first}
	if d.Ref != nil {
		inv, err := edgeOf(d.Ref, pos)
		if err != nil {
			return nil, err
		}
		inv.Inline, inv.Inverse, inv.Ref = true, true, d.Name
		out = append(out, inv)
	}
	return out, nil
}

func edgeOf(d *desc.Edge, pos Position) (*Edge, error) {
	out := &Edge{
		Name: d.Name, Type: d.Type, TypePkg: d.TypePkg, Inverse: d.Inverse, Ref: d.RefName,
		Unique: d.Unique, Required: d.Required, Immutable: d.Immutable,
		Field: d.Field, Table: d.Table, Columns: d.Columns, Symbols: d.Symbols, Comment: d.Comment,
		Position: pos,
	}
	var err error
	if out.Annotations, err = listAnnotations(d.Annotations); err != nil {
		return nil, fmt.Errorf("edge %q: %w", d.Name, err)
	}
	return out, nil
}

func buildIndex(x schema.Index, j int, pos Position) (*Index, error) {
	if isNil(x) {
		return nil, fmt.Errorf("Indexes()[%d] is nil", j)
	}
	d, err := call("Descriptor", x.Descriptor)
	if err != nil {
		return nil, fmt.Errorf("Indexes()[%d]: %w", j, err)
	}
	if d == nil {
		return nil, fmt.Errorf("Indexes()[%d] has no descriptor", j)
	}
	out := &Index{
		Fields: d.Fields, Edges: d.Edges, Unique: d.Unique, StorageKey: d.StorageKey, Where: d.Where,
		Position: pos,
	}
	if out.Annotations, err = listAnnotations(d.Annotations); err != nil {
		return nil, fmt.Errorf("Indexes()[%d]: %w", j, err)
	}
	return out, nil
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
