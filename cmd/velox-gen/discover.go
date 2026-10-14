package main

import (
	"fmt"
	"go/types"
	"reflect"

	"golang.org/x/tools/go/packages"
)

// discover finds the entities of pkg: struct types that declare
// func (x *T) Schema(s *velox.Schema) themselves. They come in name order, so
// output is deterministic.
func discover(pkg *packages.Package, cfg config) ([]*entity, []problem) {
	var ents []*entity
	var probs []problem
	scope := pkg.Types.Scope()
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || tn.IsAlias() {
			continue
		}
		named, ok := tn.Type().(*types.Named)
		if !ok {
			continue
		}
		found, why := declares(named, "Schema")
		if !found {
			continue
		}
		pos := pkg.Fset.Position(tn.Pos())
		if why != "" {
			probs = append(probs, problem{pos, name + ": " + why})
			continue
		}
		if named.TypeParams().Len() > 0 {
			probs = append(probs, problem{pos, name + " has type parameters; a velox entity cannot be generic"})
			continue
		}
		st, ok := named.Underlying().(*types.Struct)
		if !ok {
			probs = append(probs, problem{pos, name + " has a Schema method but is not a struct"})
			continue
		}
		e := &entity{pkg: pkg, name: name, file: pos.Filename, pos: pos}
		w := &walker{cfg: cfg, pkg: pkg, e: e, seen: map[string]bool{}}
		w.fields(st, 1)
		probs = append(probs, w.probs...)
		ents = append(ents, e)
	}
	return ents, probs
}

// declares reports whether t itself declares velox's method name: one
// *velox.Schema parameter. A method of that name with other parameters is not
// velox's and is not found. When velox's method has a value receiver or
// results, why says so.
func declares(t *types.Named, name string) (found bool, why string) {
	for i := range t.NumMethods() {
		m := t.Method(i)
		if m.Name() != name {
			continue
		}
		sig := m.Type().(*types.Signature)
		if sig.Params().Len() != 1 || !isSchemaPointer(sig.Params().At(0).Type()) {
			return false, ""
		}
		want := fmt.Sprintf("velox needs func (x *%s) %s(s *velox.Schema)", t.Obj().Name(), name)
		if _, ptr := sig.Recv().Type().(*types.Pointer); !ptr {
			return true, name + " has a value receiver; " + want
		}
		if sig.Results().Len() != 0 {
			return true, name + " returns values; " + want
		}
		return true, ""
	}
	return false, ""
}

func isSchemaPointer(t types.Type) bool {
	p, ok := types.Unalias(t).(*types.Pointer)
	if !ok {
		return false
	}
	n, ok := types.Unalias(p.Elem()).(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == veloxPath && n.Obj().Name() == "Schema"
}

// walker collects an entity's columns and relations exactly as the runtime's
// flatten does (meta.go): a field tagged velox:"-" is skipped, an embedded
// struct flattens in, an unexported field is skipped, a velox.One or
// velox.Many field is a relation, and every other field is a column.
type walker struct {
	cfg   config
	pkg   *packages.Package
	e     *entity
	seen  map[string]bool
	probs []problem
}

func (w *walker) fail(v *types.Var, format string, args ...any) {
	w.probs = append(w.probs, problem{w.pkg.Fset.Position(v.Pos()), w.e.name + ": " + fmt.Sprintf(format, args...)})
}

func (w *walker) fields(st *types.Struct, depth int) {
	for i := range st.NumFields() {
		f := st.Field(i)
		switch {
		case reflect.StructTag(st.Tag(i)).Get("velox") == "-":
		case f.Embedded() && flattens(f.Type()):
			w.embedded(f, depth)
		case !f.Exported():
		case relation(f.Type()):
			w.add(f)
			n := types.Unalias(f.Type()).(*types.Named)
			w.e.relations = append(w.e.relations, rel{field: f.Name(), target: n.TypeArgs().At(0)})
		default:
			w.add(f)
			w.column(f)
		}
	}
}

// flattens reports an embedded field the runtime does not treat as a plain
// field: a pointer (refused) or a struct (flattened in).
func flattens(t types.Type) bool {
	if _, ok := types.Unalias(t).(*types.Pointer); ok {
		return true
	}
	_, ok := t.Underlying().(*types.Struct)
	return ok
}

func (w *walker) embedded(f *types.Var, depth int) {
	t := types.Unalias(f.Type())
	if _, ok := t.(*types.Pointer); ok {
		w.fail(f, "embedded pointer %s is not supported; embed the struct itself", f.Name())
		return
	}
	mixin := false
	if n, ok := t.(*types.Named); ok {
		var why string
		if mixin, why = declares(n, "Mixin"); why != "" {
			w.fail(f, "%s's %s", f.Name(), why)
		}
	}
	if mixin && depth > 1 {
		w.fail(f, "mixin %s is inside another embedded struct; embed mixins directly in the entity, where velox applies them", f.Name())
	}
	before := len(w.seen)
	w.fields(t.Underlying().(*types.Struct), depth+1)
	if !mixin && len(w.seen) == before {
		w.fail(f, "embedded %s adds no columns; embed a struct only to share columns, or give the field a name", f.Name())
	}
}

// add records a field's name: two fields of one name (the entity's own and an
// embedded struct's) would be two columns of one name.
func (w *walker) add(f *types.Var) {
	if w.seen[f.Name()] {
		w.fail(f, "two fields are named %s (one in an embedded struct); velox needs one field per name", f.Name())
	}
	w.seen[f.Name()] = true
}

func (w *walker) column(f *types.Var) {
	if !resolved(f.Type()) {
		w.fail(f, "the type of %s cannot be resolved; fix the errors of the package that declares it", f.Name())
		return
	}
	h, v := classify(f.Type(), w.cfg)
	w.e.columns = append(w.e.columns, column{field: f.Name(), handle: h, value: v, typ: f.Type()})
}

// relation reports a velox.One or velox.Many field.
func relation(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return false
	}
	o := n.Origin().Obj()
	return o.Pkg() != nil && o.Pkg().Path() == veloxPath && (o.Name() == "One" || o.Name() == "Many")
}

// resolved reports whether a type has no invalid part; go/types puts one
// where a package does not type-check.
func resolved(t types.Type) bool {
	switch t := types.Unalias(t).(type) {
	case *types.Basic:
		return t.Kind() != types.Invalid
	case *types.Pointer:
		return resolved(t.Elem())
	case *types.Slice:
		return resolved(t.Elem())
	case *types.Array:
		return resolved(t.Elem())
	case *types.Map:
		return resolved(t.Key()) && resolved(t.Elem())
	}
	return true
}
