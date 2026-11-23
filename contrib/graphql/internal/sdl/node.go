package sdl

// Node, under compat=veloxe: veloxe's generator declared Relay's interface
// Node, made every type with an ID implement it and put node(id:) and
// nodes(ids:) first in the root query; clients built against it ask for
// them (nodes(ids:) with "... on Invoice" fragments). velox's own API has no
// Node (ADR 0061). The Go side, Noder and IsNode, is gogen's noder_gql.go.

// hasNode reports whether the SDL declares Node: under Config.Veloxe, when
// a type has an ID.
func (g *graph) hasNode() bool {
	if !g.cfg.Veloxe {
		return false
	}
	for _, o := range g.objects {
		if o.hasID && g.emit.writesType(o) {
			return true
		}
	}
	return false
}

// implementsNode is what follows o's name in its type definition: "
// implements Node" for a type of Node, else "".
func (g *graph) implementsNode(o *object) string {
	if o.hasID && g.hasNode() {
		return " implements Node"
	}
	return ""
}

// writeNode writes the interface Node, as veloxe declared it, bound with
// @goModel to the generated package's Noder when Config.Package names the
// package (velox-graphql always does; a test may not, and gqlgen's autobind
// then looks for a Go type Node).
func (g *graph) writeNode(w *writer) {
	if !g.hasNode() {
		return
	}
	var bind string
	if g.cfg.Package != "" {
		bind = " @goModel(model: " + quote(g.cfg.Package+".Noder") + ")"
	}
	w.line()
	w.line(`"""`)
	w.line("An object with an ID.")
	w.line("Follows the [Relay Global Object Identification Specification](https://relay.dev/graphql/objectidentification.htm)")
	w.line(`"""`)
	w.line("interface Node", bind, " {")
	w.line(`  """The id of the object."""`)
	w.line("  id: ID!")
	w.line("}")
}

// rootQuery is the root query's fields: the QueryFields, after node and
// nodes when the SDL declares Node, as veloxe's were.
func (g *graph) rootQuery() []gqlField {
	if !g.hasNode() {
		return g.query
	}
	return append([]gqlField{
		{name: "node", args: "(id: ID!)", typ: "Node", desc: "Fetches an object given its ID."},
		{name: "nodes", args: "(ids: [ID!]!)", typ: "[Node]!", desc: "Lookup nodes by a list of IDs."},
	}, g.query...)
}
