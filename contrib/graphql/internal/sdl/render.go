// Package sdl renders the GraphQL schema, velox.graphqls, of a velox model.
package sdl

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/syssam/velox2/contrib/graphql"
	"github.com/syssam/velox2/plugin"
)

// Render returns the SDL of m: the content of velox.graphqls, without the
// generated-file header, which velox-gen adds.
//
// Each entity that is neither skipped nor a join entity becomes a type with
// its connection and edge types and its enums; the root Query has node, nodes
// and each entity's QueryField. The output depends only on m: entities come
// in name order, fields and edges in the model's order.
//
// It fails, naming the schema and the element, on a model it cannot render:
// a field type with no GraphQL type, a name that is not a GraphQL name, two
// elements that would declare one name.
func Render(m *plugin.Model) (string, error) {
	g, err := build(m)
	if err != nil {
		return "", err
	}
	var w writer
	g.write(&w)
	out := w.String()
	if err := parse(out); err != nil {
		return "", fmt.Errorf("velox-graphql wrote an SDL that does not parse, which is a bug: %w", err)
	}
	return out, nil
}

// graph is the GraphQL surface of a model.
type graph struct {
	objects []*object          // the entities with a type, in name order
	byName  map[string]*object // by entity name
	types   typeNames
	query   []gqlField // the QueryFields, in entity name order
}

// object is an entity's type.
type object struct {
	ent    *plugin.Entity
	ann    graphql.Annotation
	fanns  []graphql.Annotation // the annotations of ent.Fields, by index
	eanns  []graphql.Annotation // the annotations of ent.Edges, by index
	name   string               // the GraphQL type name
	node   bool                 // a Node: its key is one column
	dirs   []string             // rendered directives
	fields []gqlField           // fields, then edges
	enums  []gqlEnum
}

type gqlField struct {
	name, args, typ, desc string
	dirs                  []string
}

type gqlEnum struct {
	name   string
	values []string
}

// connArgs are the paging arguments of a connection field.
const connArgs = "(after: Cursor, first: Int, before: Cursor, last: Int)"

func build(m *plugin.Model) (*graph, error) {
	if m == nil {
		return nil, errors.New("no model")
	}
	g := &graph{byName: map[string]*object{}, types: newTypeNames()}
	ents := slices.SortedStableFunc(slices.Values(m.Entities), func(a, b *plugin.Entity) int { return cmp.Compare(a.Name, b.Name) })

	// The types first, so that edges know their targets' names.
	for _, e := range ents {
		if e.Join {
			continue
		}
		where := "schema " + e.Name
		a, err := decode(e.Annotations, where)
		if err != nil {
			return nil, err
		}
		if a.Skip&graphql.SkipType != 0 {
			continue
		}
		o := &object{ent: e, ann: a, name: e.Name, node: len(e.PrimaryKey) == 1}
		if a.Type != "" {
			if !validName(a.Type) {
				return nil, fmt.Errorf("%s: graphql.Type(%q) is not a GraphQL name", where, a.Type)
			}
			o.name = a.Type
		}
		for _, n := range []string{o.name, o.name + "Connection", o.name + "Edge"} {
			if err := g.types.add(n, where); err != nil {
				return nil, err
			}
		}
		if o.dirs, err = directives(a.Directives, where); err != nil {
			return nil, err
		}
		for _, f := range e.Fields {
			fa, err := decode(f.Annotations, where+", field "+f.Name)
			if err != nil {
				return nil, err
			}
			o.fanns = append(o.fanns, fa)
		}
		for _, ed := range e.Edges {
			ea, err := decode(ed.Annotations, where+", edge "+ed.Name)
			if err != nil {
				return nil, err
			}
			o.eanns = append(o.eanns, ea)
		}
		g.objects = append(g.objects, o)
		g.byName[e.Name] = o
	}

	// A foreign-key field bound to an edge is in no type: the edge stands for
	// it. An edge names its field on whichever entity holds it.
	fks := map[string]map[string]bool{}
	for _, e := range m.Entities {
		for _, ed := range e.Edges {
			if ed.Field != "" && ed.FieldOn != "" {
				if fks[ed.FieldOn] == nil {
					fks[ed.FieldOn] = map[string]bool{}
				}
				fks[ed.FieldOn][ed.Field] = true
			}
		}
	}

	queryBy := map[string]string{"node": builtin, "nodes": builtin}
	for _, o := range g.objects {
		if err := g.fill(o, fks[o.ent.Name]); err != nil {
			return nil, err
		}
		if q := o.ann.QueryField; q != "" {
			where := "schema " + o.ent.Name
			if !validName(q) {
				return nil, fmt.Errorf("%s: graphql.QueryField(%q) is not a GraphQL name", where, q)
			}
			switch prev := queryBy[q]; prev {
			case "":
			case builtin:
				return nil, fmt.Errorf("%s: the Query field %s is one velox-graphql declares itself", where, q)
			default:
				return nil, fmt.Errorf("%s and %s both declare the Query field %s", prev, where, q)
			}
			queryBy[q] = where
			g.query = append(g.query, gqlField{name: q, args: connArgs, typ: o.name + "Connection!"})
		}
	}
	return g, nil
}

// decode is the graphql annotation among an element's annotations.
func decode(anns map[string]json.RawMessage, where string) (graphql.Annotation, error) {
	var a graphql.Annotation
	raw, ok := anns[graphql.Annotation{}.Name()]
	if !ok {
		return a, nil
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return a, fmt.Errorf("%s: the graphql annotation: %w", where, err)
	}
	return a, nil
}

// fill computes o's fields, edges and enums. fks are the names of o's
// foreign-key fields.
func (g *graph) fill(o *object, fks map[string]bool) error {
	e := o.ent
	seen := map[string]string{} // GraphQL field name → the element
	add := func(f gqlField, where string) error {
		if prev, ok := seen[f.name]; ok {
			return fmt.Errorf("%s and %s are both the GraphQL field %s.%s", prev, where, o.name, f.name)
		}
		seen[f.name] = where
		o.fields = append(o.fields, f)
		return nil
	}
	if o.node {
		pk := e.Fields[e.PrimaryKey[0]]
		if err := add(gqlField{name: "id", typ: "ID!", desc: pk.Comment}, "schema "+e.Name+", field "+pk.Name); err != nil {
			return err
		}
	}
	for i, f := range e.Fields {
		a := o.fanns[i]
		switch {
		case o.node && i == e.PrimaryKey[0], i == e.Tenant, f.Sensitive, f.Implicit != "", fks[f.Name], a.Skip&graphql.SkipType != 0:
			continue
		}
		where := "schema " + e.Name + ", field " + f.Name
		name := camel(f.GoName)
		if a.FieldName != "" {
			if !validName(a.FieldName) {
				return fmt.Errorf("%s: graphql.FieldName(%q) is not a GraphQL name", where, a.FieldName)
			}
			name = a.FieldName
		}
		typ, err := g.fieldType(o, f, a, where)
		if err != nil {
			return err
		}
		if !f.Nullable {
			typ += "!"
		}
		var dirs []string
		if !strings.EqualFold(name, f.GoName) {
			// gqlgen binds a field to the Go field of its name, ignoring case.
			dirs = append(dirs, bindTo(f.GoName))
		}
		ds, err := directives(a.Directives, where)
		if err != nil {
			return err
		}
		if err := add(gqlField{name: name, typ: typ, desc: f.Comment, dirs: append(dirs, ds...)}, where); err != nil {
			return err
		}
	}
	for i, ed := range e.Edges {
		a := o.eanns[i]
		t := g.byName[ed.Target]
		if a.Skip&graphql.SkipType != 0 || t == nil { // skipped, or its target is
			continue
		}
		where := "schema " + e.Name + ", edge " + ed.Name
		f := gqlField{name: camel(ed.GoName), desc: ed.Comment}
		if a.FieldName != "" {
			if !validName(a.FieldName) {
				return fmt.Errorf("%s: graphql.FieldName(%q) is not a GraphQL name", where, a.FieldName)
			}
			f.name = a.FieldName
		}
		switch {
		case ed.Unique:
			f.typ = t.name
			if ed.Required {
				f.typ += "!"
			}
		case a.Connection != nil && !*a.Connection:
			f.typ = "[" + t.name + "!]!"
		default:
			f.args, f.typ = connArgs, t.name+"Connection!"
		}
		// The entity's struct has a field of the edge's name, so the edge's
		// method is <Edge>GQL.
		ds, err := directives(a.Directives, where)
		if err != nil {
			return err
		}
		f.dirs = append([]string{bindTo(ed.GoName + "GQL")}, ds...)
		if err := add(f, where); err != nil {
			return err
		}
	}
	return nil
}

// fieldType is the GraphQL type of f, without its non-null mark. An enum
// field adds its enum to o.
func (g *graph) fieldType(o *object, f *plugin.Field, a graphql.Annotation, where string) (string, error) {
	switch f.Type {
	case "string":
		return "String", nil
	case "int", "int8", "int16", "int32", "int64", "uint8", "uint16", "uint32":
		return "Int", nil
	case "uint", "uint64": // a uint is 64 bits wide where velox runs
		return "Uint64", nil
	case "float32", "float64":
		return "Float", nil
	case "bool":
		return "Boolean", nil
	case "time":
		return "Time", nil
	case "uuid":
		return "UUID", nil
	case "bytes":
		return "Bytes", nil
	case "json":
		return "JSON", nil
	case "enum":
		if a.Skip&graphql.SkipEnumField != 0 {
			return "String", nil // no enum: the stored values, as they are
		}
		values, err := enumValues(f, a, where)
		if err != nil {
			return "", err
		}
		name := o.name + f.GoName
		if err := g.types.add(name, where); err != nil {
			return "", err
		}
		o.enums = append(o.enums, gqlEnum{name: name, values: values})
		return name, nil
	default:
		return "", fmt.Errorf("%s: a field of type %s has no GraphQL type; leave it out with graphql.Skip()", where, f.Type)
	}
}

// enumValues are the GraphQL values of an enum field: its stored values, upper
// cased, or as graphql.EnumValues names them.
func enumValues(f *plugin.Field, a graphql.Annotation, where string) ([]string, error) {
	for _, k := range slices.Sorted(maps.Keys(a.EnumValues)) {
		if !slices.Contains(f.EnumValues, k) {
			return nil, fmt.Errorf("%s: graphql.EnumValues names %q, which is not a value of the field", where, k)
		}
	}
	if len(f.EnumValues) == 0 {
		return nil, fmt.Errorf("%s: an enum field with no values", where)
	}
	values := make([]string, 0, len(f.EnumValues))
	by := map[string]string{}
	for _, v := range f.EnumValues {
		gv, renamed := a.EnumValues[v]
		if !renamed {
			gv = strings.ToUpper(v)
		}
		switch {
		case !validEnumValue(gv) && renamed:
			return nil, fmt.Errorf("%s: graphql.EnumValues names the value %q %q, which is not a GraphQL enum value", where, v, gv)
		case !validEnumValue(gv):
			return nil, fmt.Errorf("%s: the value %q makes %q, which is not a GraphQL enum value; name it with graphql.EnumValues", where, v, gv)
		case by[gv] != "":
			return nil, fmt.Errorf("%s: the values %q and %q are both the GraphQL enum value %s; name one with graphql.EnumValues", where, by[gv], v, gv)
		}
		by[gv] = v
		values = append(values, gv)
	}
	return values, nil
}

// bindTo is the directive that binds a field to the Go field or method name.
func bindTo(name string) string {
	return "@goField(name: " + quote(name) + ")"
}

// directives renders ds, the directives of the element where.
func directives(ds []graphql.Directive, where string) ([]string, error) {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		if !validName(d.Name) {
			return nil, fmt.Errorf("%s: the directive name %q is not a GraphQL name", where, d.Name)
		}
		s := "@" + d.Name
		if len(d.Args) > 0 {
			args := make([]string, 0, len(d.Args))
			for _, k := range slices.Sorted(maps.Keys(d.Args)) {
				if !validName(k) {
					return nil, fmt.Errorf("%s: directive @%s: the argument name %q is not a GraphQL name", where, d.Name, k)
				}
				v, err := value(d.Args[k])
				if err != nil {
					return nil, fmt.Errorf("%s: directive @%s, argument %s: %w", where, d.Name, k, err)
				}
				args = append(args, k+": "+v)
			}
			s += "(" + strings.Join(args, ", ") + ")"
		}
		out = append(out, s)
	}
	return out, nil
}

// value is v, a JSON value, as a GraphQL value.
func value(v any) (string, error) {
	switch v := v.(type) {
	case nil:
		return "null", nil
	case bool:
		return strconv.FormatBool(v), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case string:
		return quote(v), nil
	case []any:
		items := make([]string, 0, len(v))
		for _, x := range v {
			s, err := value(x)
			if err != nil {
				return "", err
			}
			items = append(items, s)
		}
		return "[" + strings.Join(items, ", ") + "]", nil
	case map[string]any:
		items := make([]string, 0, len(v))
		for _, k := range slices.Sorted(maps.Keys(v)) {
			if !validName(k) {
				return "", fmt.Errorf("the object field name %q is not a GraphQL name", k)
			}
			s, err := value(v[k])
			if err != nil {
				return "", err
			}
			items = append(items, k+": "+s)
		}
		return "{" + strings.Join(items, ", ") + "}", nil
	default:
		return "", fmt.Errorf("a value of type %T", v)
	}
}

// quote is s as a GraphQL string.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// ---- writing ----

type writer struct{ strings.Builder }

func (w *writer) line(parts ...string) {
	for _, p := range parts {
		w.WriteString(p)
	}
	w.WriteByte('\n')
}

// desc writes text as a description, at indent.
func (w *writer) desc(indent, text string) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return
	}
	text = strings.ReplaceAll(text, `"""`, `\"""`)
	// One line, unless a closing """ would follow a quote or a backslash.
	if !strings.Contains(text, "\n") && !strings.HasSuffix(text, `"`) && !strings.HasSuffix(text, `\`) {
		w.line(indent, `"""`, text, `"""`)
		return
	}
	w.line(indent, `"""`)
	for l := range strings.SplitSeq(text, "\n") {
		if l = strings.TrimRight(l, " \t"); l == "" {
			w.line()
		} else {
			w.line(indent, l)
		}
	}
	w.line(indent, `"""`)
}

func (w *writer) field(f gqlField) {
	w.desc("  ", f.desc)
	dirs := ""
	if len(f.dirs) > 0 {
		dirs = " " + strings.Join(f.dirs, " ")
	}
	w.line("  ", f.name, f.args, ": ", f.typ, dirs)
}

const vocabulary = `# The vocabulary of velox's GraphQL types: the scalars, Node, PageInfo and
# OrderDirection, bound to Go by contrib/graphql/veloxgql, and gqlgen's @goField.

"""A time, as an RFC 3339 string."""
scalar Time

"""A UUID, as a string."""
scalar UUID

"""Bytes, as a base64 string."""
scalar Bytes

"""A JSON value."""
scalar JSON

"""An unsigned 64-bit integer, as a decimal string."""
scalar Uint64

"""An opaque cursor for paging through a connection."""
scalar Cursor

directive @goField(forceResolver: Boolean, name: String, omittable: Boolean) on INPUT_FIELD_DEFINITION | FIELD_DEFINITION

"""An object with a global ID."""
interface Node {
  """The global ID of the object."""
  id: ID!
}

"""Information about a page of a connection."""
type PageInfo {
  """Whether there are more nodes after this page."""
  hasNextPage: Boolean!
  """Whether there are more nodes before this page."""
  hasPreviousPage: Boolean!
  """The cursor of the first node of the page."""
  startCursor: Cursor
  """The cursor of the last node of the page."""
  endCursor: Cursor
}

"""The direction of an order."""
enum OrderDirection {
  ASC
  DESC
}
`

func (g *graph) write(w *writer) {
	w.WriteString(vocabulary)

	w.line()
	w.line("# The root query.")
	w.line()
	w.line("type Query {")
	w.line(`  """The node with this ID, or null."""`)
	w.line("  node(id: ID!): Node")
	w.line(`  """The nodes with these IDs, in their order; null for an ID with no node."""`)
	w.line("  nodes(ids: [ID!]!): [Node]!")
	for _, f := range g.query {
		w.field(f)
	}
	w.line("}")

	for _, o := range g.objects {
		g.writeObject(w, o)
	}
}

func (g *graph) writeObject(w *writer, o *object) {
	w.line()
	if o.name == o.ent.Name {
		w.line("# ", o.ent.Name)
	} else {
		w.line("# ", o.ent.Name, ", as the type ", o.name)
	}
	w.line()
	w.desc("", o.ent.Comment)
	head := "type " + o.name
	if o.node {
		head += " implements Node"
	}
	for _, d := range o.dirs {
		head += " " + d
	}
	w.line(head, " {")
	for _, f := range o.fields {
		w.field(f)
	}
	w.line("}")

	for _, en := range o.enums {
		w.line()
		w.line("enum ", en.name, " {")
		for _, v := range en.values {
			w.line("  ", v)
		}
		w.line("}")
	}

	w.line()
	w.line(`"""A connection to a list of `, o.name, `."""`)
	w.line("type ", o.name, "Connection {")
	w.line(`  """The edges of this page."""`)
	w.line("  edges: [", o.name, "Edge!]!")
	w.line(`  """Information about this page."""`)
	w.line("  pageInfo: PageInfo!")
	w.line(`  """The number of nodes in the whole connection, not only on this page."""`)
	w.line("  totalCount: Int!")
	w.line("}")
	w.line()
	w.line(`"""An edge in a connection to a list of `, o.name, `."""`)
	w.line("type ", o.name, "Edge {")
	w.line(`  """The `, o.name, ` at the end of the edge."""`)
	w.line("  node: ", o.name, "!")
	w.line(`  """The cursor of this edge, for paging."""`)
	w.line("  cursor: Cursor!")
	w.line("}")
}
