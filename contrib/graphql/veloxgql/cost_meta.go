package veloxgql

import (
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

// metaKind is what a value of the introspection types is.
type metaKind uint8

const (
	metaSchema metaKind = iota
	metaType
	metaField
	metaInput
	metaDirective
	metaLeaf // an enum value: its fields are all scalars
)

// metaValue is a value __schema or __type returns, comparable so that a walk
// costs it once for each selection.
type metaValue struct {
	kind metaKind
	def  *ast.Definition          // metaType: a named type
	typ  *ast.Type                // metaType: a list or a non-null, interned
	fd   *ast.FieldDefinition     // metaField; metaInput: an input object's field
	arg  *ast.ArgumentDefinition  // metaInput: a field's or a directive's argument
	dir  *ast.DirectiveDefinition // metaDirective
}

// metaKey is one selection on one value.
type metaKey struct {
	f    *ast.Field
	frag string
	v    metaValue
}

// metaWalk costs selections on the values the schema's own introspection
// returns, as gqlgen's graphql/introspection walks them: exactly, so that a
// nested list costs the fields the schema has, and each (selection, value) once,
// so that its work is bounded by the document and the schema.
type metaWalk struct {
	c     *coster
	memo  map[metaKey]int64
	types map[string]*ast.Type // the interned lists and non-nulls
	steps int                  // the fields visited
	over  bool                 // the steps passed MaxIntrospectionCost
}

// introspection is the cost of f, a field __schema or __type, on the root.
func (c *coster) introspection(f *ast.Field) int64 {
	if c.meta == nil {
		c.meta = &metaWalk{c: c, memo: map[metaKey]int64{}, types: map[string]*ast.Type{}}
	}
	m := c.meta
	if f.Name == "__schema" {
		return add(1, m.set(f.SelectionSet, metaValue{kind: metaSchema}))
	}
	a := f.Arguments.ForName("name")
	if a == nil || a.Value == nil {
		return 1
	}
	if v, err := a.Value.Value(c.vars); err == nil {
		name, ok := v.(string)
		if !ok {
			return 1 // null
		}
		d := c.schema.Types[name]
		if d == nil {
			return 1
		}
		return add(1, m.set(f.SelectionSet, metaValue{kind: metaType, def: d}))
	}
	// A variable not given, which gqlgen's coercion refuses for a String!: the
	// sum over the types is an upper bound of any one of them.
	sum := int64(1)
	for _, d := range c.schema.Types {
		sum = add(sum, m.set(f.SelectionSet, metaValue{kind: metaType, def: d}))
	}
	return sum
}

// set is the cost of sels on v.
func (m *metaWalk) set(sels ast.SelectionSet, v metaValue) int64 {
	var sum int64
	for _, s := range sels {
		if m.over {
			return maxCost
		}
		switch s := s.(type) {
		case *ast.Field:
			if !m.c.skipped(s.Directives) {
				sum = add(sum, m.field(s, v))
			}
		case *ast.InlineFragment:
			if !m.c.skipped(s.Directives) {
				sum = add(sum, m.set(s.SelectionSet, v))
			}
		case *ast.FragmentSpread:
			if m.c.skipped(s.Directives) || s.Definition == nil {
				continue
			}
			key := metaKey{frag: s.Name, v: v}
			n, ok := m.memo[key]
			if !ok {
				m.memo[key] = 0 // a cycle, which validation refuses, ends
				n = m.set(s.Definition.SelectionSet, v)
				m.memo[key] = n
			}
			sum = add(sum, n)
		}
	}
	return sum
}

// field is the cost of f on v: 1, and what it selects on each value it returns.
func (m *metaWalk) field(f *ast.Field, v metaValue) int64 {
	key := metaKey{f: f, v: v}
	if n, ok := m.memo[key]; ok {
		return n
	}
	if m.step(1) {
		return maxCost
	}
	n := int64(1)
	if len(f.SelectionSet) > 0 {
		children := m.children(f.Name, v)
		// A value costs its work, its selections skipped or not.
		if m.step(len(children)) {
			return maxCost
		}
		for _, child := range children {
			n = add(n, max(m.set(f.SelectionSet, child), 1))
		}
	}
	m.memo[key] = n
	return n
}

// step counts n steps, and reports whether they passed the limit. A walk with no
// limit, one Limits{} gives Cost, goes on: the document and the schema bound it.
func (m *metaWalk) step(n int) bool {
	m.steps += n
	if limit := m.c.l.MaxIntrospectionCost; limit > 0 && m.steps > limit {
		m.over = true
	}
	return m.over
}

// children are what field name of v returns, as gqlgen's Schema, Type, Field and
// InputValue do: its deprecated members counted too, as the cost is an upper
// bound.
func (m *metaWalk) children(name string, v metaValue) []metaValue {
	switch v.kind {
	case metaSchema:
		return m.ofSchema(name)
	case metaType:
		return m.ofType(name, v)
	case metaField:
		switch name {
		case "args":
			return inputsOf(v.fd.Arguments)
		case "type":
			return m.value(v.fd.Type)
		}
	case metaDirective:
		if name == "args" {
			return inputsOf(v.dir.Arguments)
		}
	case metaInput:
		if name == "type" {
			if v.arg != nil {
				return m.value(v.arg.Type)
			}
			return m.value(v.fd.Type)
		}
	}
	return nil
}

func (m *metaWalk) ofSchema(name string) []metaValue {
	s := m.c.schema
	var d *ast.Definition
	switch name {
	case "types":
		out := make([]metaValue, 0, len(s.Types))
		for _, t := range s.Types {
			out = append(out, metaValue{kind: metaType, def: t})
		}
		return out
	case "directives":
		out := make([]metaValue, 0, len(s.Directives))
		for _, t := range s.Directives {
			out = append(out, metaValue{kind: metaDirective, dir: t})
		}
		return out
	case "queryType":
		d = s.Query
	case "mutationType":
		d = s.Mutation
	case "subscriptionType":
		d = s.Subscription
	}
	if d == nil {
		return nil
	}
	return []metaValue{{kind: metaType, def: d}}
}

func (m *metaWalk) ofType(name string, v metaValue) []metaValue {
	if name == "ofType" {
		switch {
		case v.typ == nil:
			return nil
		case v.typ.NonNull:
			inner := *v.typ
			inner.NonNull = false
			return m.value(&inner)
		}
		return m.value(v.typ.Elem)
	}
	d := v.def
	if d == nil {
		return nil // a list or a non-null has only ofType
	}
	var out []metaValue
	switch name {
	case "fields":
		if d.Kind == ast.Object || d.Kind == ast.Interface {
			for _, f := range d.Fields {
				if !strings.HasPrefix(f.Name, "__") {
					out = append(out, metaValue{kind: metaField, fd: f})
				}
			}
		}
	case "inputFields":
		if d.Kind == ast.InputObject {
			for _, f := range d.Fields {
				out = append(out, metaValue{kind: metaInput, fd: f})
			}
		}
	case "enumValues":
		if d.Kind == ast.Enum {
			for range d.EnumValues {
				out = append(out, metaValue{kind: metaLeaf})
			}
		}
	case "interfaces":
		if d.Kind == ast.Object {
			for _, i := range d.Interfaces {
				if t := m.c.schema.Types[i]; t != nil {
					out = append(out, metaValue{kind: metaType, def: t})
				}
			}
		}
	case "possibleTypes":
		if d.Kind == ast.Interface || d.Kind == ast.Union {
			for _, t := range m.c.schema.GetPossibleTypes(d) {
				out = append(out, metaValue{kind: metaType, def: t})
			}
		}
	}
	return out
}

// value is the value of the type t: its named type, or the interned list or
// non-null. A name the schema does not hold, which it cannot load with, is none.
func (m *metaWalk) value(t *ast.Type) []metaValue {
	if t == nil {
		return nil
	}
	if !t.NonNull && t.NamedType != "" {
		d := m.c.schema.Types[t.NamedType]
		if d == nil {
			return nil
		}
		return []metaValue{{kind: metaType, def: d}}
	}
	key := t.String()
	i, ok := m.types[key]
	if !ok {
		i = t
		m.types[key] = i
	}
	return []metaValue{{kind: metaType, typ: i}}
}

func inputsOf(args ast.ArgumentDefinitionList) []metaValue {
	out := make([]metaValue, 0, len(args))
	for _, a := range args {
		out = append(out, metaValue{kind: metaInput, arg: a})
	}
	return out
}
