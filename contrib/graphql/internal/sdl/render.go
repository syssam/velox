package sdl

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/syssam/velox/plugin"
)

// Render returns the SDL of m: the content of velox.graphql, without the
// generated-file header, which velox-gen adds.
//
// Each entity that is neither skipped nor a join entity becomes a type with
// its connection and edge types, its enums, and as its annotations ask, its
// order, its where input and its mutation inputs; the root Query has each
// entity's QueryField. It has no root Mutation: the
// application declares its mutations, which take the inputs, in its own
// schema. The output depends only on m: entities come in
// name order, fields and edges in the model's order.
//
// It fails, naming the schema and the element, on a model it cannot render:
// a field type with no GraphQL type, a name that is not a GraphQL name, two
// elements that would declare one name, a type with no field to show, a
// directive from graphql.Directives that GraphQL does not allow where it is,
// or an annotation on an element it does not apply to.
//
// The application declares its own directives in its own schema files, so
// the SDL is validated without them: it is rendered again with only the
// directives GraphQL defines and @goField, and that SDL must be valid on its
// own.
func Render(m *plugin.Model) (string, error) {
	g, err := build(m)
	if err != nil {
		return "", err
	}
	w := writer{app: true}
	g.write(&w)
	out := w.String()
	if err := parse(out); err != nil {
		return "", fmt.Errorf("velox-graphql wrote an SDL that does not parse, which is a bug: %w", err)
	}
	var own writer
	g.write(&own)
	if _, err := Check(own.String()); err != nil {
		return "", own.explain(err)
	}
	return out, nil
}

// jsonList is the scalar of the elements of a JSON field of these Go types,
// which is a list of them, as entgql gives field.Strings: gqlgen binds the Go
// slice. A JSON field of any other Go type is JSON.
var jsonList = map[string]string{"[]string": "String", "[]int": "Int", "[]int64": "Int", "[]float64": "Float", "[]bool": "Boolean"}

// ---- writing ----

// writer writes the SDL, and remembers which element each line belongs to,
// so that an error gqlparser finds names the element.
type writer struct {
	strings.Builder
	app   bool     // write the application's directives too
	at    string   // the element being written, "" for velox-graphql's own
	where []string // the element of each line written so far
}

func (w *writer) line(parts ...string) {
	for _, p := range parts {
		w.WriteString(p)
	}
	w.WriteByte('\n')
	w.where = append(w.where, w.at)
}

// prelude are the directives GraphQL defines, which gqlparser declares; any
// other directive of graphql.Directives the application declares.
var prelude = map[string]bool{"deprecated": true, "include": true, "skip": true, "specifiedBy": true, "defer": true, "oneOf": true}

// dirs renders ds, each after a space, leaving out the application's own
// directives unless w.app.
func (w *writer) dirs(ds []directive) string {
	var b strings.Builder
	for _, d := range ds {
		if w.app || d.name == "goField" || prelude[d.name] {
			b.WriteString(" " + d.text)
		}
	}
	return b.String()
}

// explain is err, an error gqlparser found in what w wrote, with the element
// of the line it is on.
func (w *writer) explain(err error) error {
	var ge *gqlerror.Error
	if errors.As(err, &ge) && len(ge.Locations) > 0 {
		if l := ge.Locations[0].Line; l >= 1 && l <= len(w.where) && w.where[l-1] != "" {
			return fmt.Errorf("%s: not valid GraphQL: %s", w.where[l-1], ge.Message)
		}
	}
	return fmt.Errorf("velox-graphql wrote an SDL that is not valid GraphQL, which is a bug: %w", err)
}

// desc writes text as a description, at indent.
func (w *writer) desc(indent, text string) {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	text = strings.TrimSpace(text)
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
	at := w.at
	w.at = f.where
	w.desc("  ", f.desc)
	w.line("  ", f.name, f.args, ": ", f.typ, f.def, w.dirs(f.dirs))
	w.at = at
}

const vocabulary = `# The vocabulary of velox's GraphQL types: the scalars, PageInfo and
# OrderDirection, bound to Go by contrib/graphql/veloxgql, and gqlgen's @goField
# and @goModel, as gqlgen documents them.

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

directive @goField(forceResolver: Boolean, name: String, omittable: Boolean, type: String, maxList: Int) on INPUT_FIELD_DEFINITION | FIELD_DEFINITION

directive @goModel(model: String, models: [String!], forceGenerate: Boolean) on OBJECT | INPUT_OBJECT | SCALAR | ENUM | INTERFACE | UNION

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
	for l := range strings.SplitSeq(strings.TrimSuffix(vocabulary, "\n"), "\n") {
		w.line(l)
	}

	// The root query holds the QueryFields; with none, the application
	// declares Query itself, as GraphQL has no empty type.
	if len(g.query) > 0 {
		w.line()
		w.line("# The root query.")
		w.line()
		w.line("type Query {")
		for _, f := range g.query {
			w.field(f)
		}
		w.line("}")
	}

	for _, o := range g.objects {
		g.writeObject(w, o)
	}

	if binds := g.types.introspectionBinds(); binds != "" {
		w.line()
		w.line("# GraphQL's own types keep gqlgen's models: the generated package has a Go")
		w.line("# type of each of these names, which gqlgen's autobind would bind instead.")
		w.line()
		for l := range strings.SplitSeq(strings.TrimSuffix(binds, "\n"), "\n") {
			w.line(l)
		}
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
	w.at = "schema " + o.ent.Name
	w.desc("", o.ent.Comment)
	w.line("type "+o.name, w.dirs(o.dirs), " {")
	for _, f := range o.fields {
		w.field(f)
	}
	w.line("}")
	w.at = ""

	for _, en := range o.enums {
		if en.name == "" {
			continue // a String
		}
		w.line()
		// @goModel is not one of w.dirs's: it is velox-graphql's own, and
		// binds the type whether or not the application's directives are
		// written.
		var bind string
		if en.model != "" {
			bind = " @goModel(model: " + quote(en.model) + ")"
		}
		w.line("enum ", en.name, bind, " {")
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

	if o.order != nil {
		w.order(o)
	}
	at := "schema " + o.ent.Name
	if o.hasWhere {
		w.at = at
		w.input("Filters "+o.name+" nodes: a node matches when every predicate given holds.", o.name+"WhereInput", o.where)
	}
	if o.create != nil {
		w.at = at
		w.input(inputDesc(o, true), "Create"+o.name+"Input", o.create)
	}
	if o.update != nil {
		w.at = at
		w.input(inputDesc(o, false), "Update"+o.name+"Input", o.update)
	}
	w.at = ""
}
