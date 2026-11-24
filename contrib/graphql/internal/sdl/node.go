package sdl

// Node, under node=on: Relay's interface Node, which every type with an ID
// implements, and node(id:) and nodes(ids:) first in the root query, as
// entgql declares them; clients built against such an API ask for them (nodes(ids:) with "... on Invoice"
// fragments). By default velox's API has no Node (ADRs 0061 and 0152). The
// Go side, Noder and IsNode, is gogen's noder_gql.go.

// hasNode reports whether the SDL declares Node: under Config.NodeOn, when
// a written type has an ID.
func (g *graph) hasNode() bool {
	if !g.cfg.NodeOn() {
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

// writeNode writes the interface Node, as entgql declares it, bound with
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
// nodes when the SDL declares Node.
func (g *graph) rootQuery() []gqlField {
	if !g.hasNode() {
		return g.query
	}
	return append([]gqlField{
		{name: "node", args: "(id: ID!)", typ: "Node", desc: "Fetches an object given its ID."},
		{name: "nodes", args: "(ids: [ID!]!)", typ: "[Node]!", desc: "Lookup nodes by a list of IDs."},
	}, g.query...)
}
