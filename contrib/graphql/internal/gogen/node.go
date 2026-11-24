package gogen

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/syssam/velox/contrib/graphql/internal/sdl"
	"github.com/syssam/velox/plugin"
)

// nodeFile is noder_gql.go, under node=on: the
// Go side of Relay's interface Node, which every type with an ID
// implements. gqlgen binds Node to Noder (the SDL's @goModel) and requires
// each implementing type's Go type to satisfy it, so each gets IsNode, as
// entgql's generated entities have. ResolveNode and ResolveNodes answer
// node(id:) and nodes(ids:) as entgql's Client.Noder and Client.Noders do,
// after one statement that finds the types holding the IDs, for the
// application's resolvers; WithNodeType and WithFixedNodeType, as entgql's,
// give an ID's type so that no statement looks it up. NodeTypes is for an
// application that resolves them its own way. ok is false when no type has
// an ID, as the SDL then has no Node.
//
// The file's name is no entity's: an entity named Noder would also take the
// Go name Noder, which declare refuses.
func (r *renderer) nodeFile(types []*sdl.Type) (file plugin.File, ok bool, err error) {
	var nodes []*sdl.Type
	for _, t := range types {
		if t.HasID {
			nodes = append(nodes, t)
		}
	}
	if len(nodes) == 0 {
		return plugin.File{}, false, nil
	}
	slices.SortFunc(nodes, func(a, b *sdl.Type) int { return cmp.Compare(a.Name, b.Name) })
	for _, n := range []string{"Noder", "NodeTypes", "NodeOption", "WithNodeType", "WithFixedNodeType", "ResolveNode", "ResolveNodes", "nodeTypes"} {
		if err := r.declare(n, "velox-graphql's Node, under node=on"); err != nil {
			return plugin.File{}, false, err
		}
	}
	f := newFile()
	ctx, velox, impl := f.use("context", "context"), f.velox(), f.gqlgraph()
	// One key Go type per schema (sdl refuses others), so the first's is all.
	key, err := r.idKeyOf(f, nodes[0].Entity, nodes[0])
	if err != nil {
		return plugin.File{}, false, err
	}

	f.doc("", "Noder is the Go type of the GraphQL interface Node, which every type with an ID implements.")
	f.p("type Noder interface {")
	f.doc("\t", "IsNode marks a type of Node.")
	f.p("\tIsNode()")
	f.p("}")
	f.p("")
	for _, t := range nodes {
		e := t.Entity
		if err := r.declare(e.Name+".IsNode", "schema "+e.Name+", the method IsNode of Node"); err != nil {
			return plugin.File{}, false, err
		}
		f.doc("", fmt.Sprintf("IsNode makes %s a Noder: the GraphQL type %s implements Node.", e.Name, t.Name))
		f.p("func (*%s) IsNode() {}", e.Name)
		f.p("")
	}
	f.doc("", "nodeTypes are the types of Node, in name order.")
	f.p("var nodeTypes = []*%s.Type{", impl)
	for _, t := range nodes {
		f.p("\t&%s,", typeVar(t))
	}
	f.p("}")
	f.p("")
	f.doc("", "NodeTypes returns the types of Node, in name order, for an application that resolves node(id:) its own way with gqlgraph.LoadNode.")
	f.p("func NodeTypes() []*%s.Type { return %s.Clone(nodeTypes) }", impl, f.use("slices", "slices"))
	f.p("")
	f.doc("", "NodeOption configures ResolveNode and ResolveNodes.")
	f.p("type NodeOption = %s.NodeOption", impl)
	f.p("")
	f.doc("", "WithNodeType gives the type of an ID: fn returns the GraphQL name of the type of Node that holds id, or \"\" when it does not know it. ResolveNode and ResolveNodes then read only that type and look up only the IDs of no known type. A name that is no type of Node is an error.")
	f.p("func WithNodeType(fn func(ctx %s.Context, id %s) (string, error)) NodeOption {", ctx, key.typ)
	f.p("\treturn %s.WithNodeType(fn)", impl)
	f.p("}")
	f.p("")
	f.doc("", "WithFixedNodeType gives every ID the type name, the GraphQL name of a type of Node, so that no statement looks it up.")
	f.p("func WithFixedNodeType(name string) NodeOption { return %s.WithFixedNodeType(name) }", impl)
	f.p("")
	f.doc("", "ResolveNode answers node(id:) as entgql's generated Client.Noder does: the one node of any type whose key is id, read with db, so each type's Read policy applies, a hidden or denied row counting as none. One statement finds which types hold the ID, unless an option gives its type; only those are read. Its error wraps gqlgraph.ErrNodeNotFound (NOT_FOUND) when no type has the ID, and gqlgraph.ErrAmbiguousNodeID when more than one does.")
	f.p("func ResolveNode(ctx %s.Context, db *%s.DB, id %s, opts ...NodeOption) (Noder, error) {", ctx, velox, key.typ)
	f.p("\tn, err := %s.ResolveNode(ctx, db, nodeTypes, id, opts...)", impl)
	f.p("\tif err != nil {")
	f.p("\t\treturn nil, err")
	f.p("\t}")
	f.p("\treturn n.(Noder), nil")
	f.p("}")
	f.p("")
	f.doc("", "ResolveNodes answers nodes(ids:) as entgql's generated Client.Noders does: the node of each ID, in order, nil for one no type has. One statement finds which types hold the IDs whose type no option gives, then it reads each type with one IN query of its IDs, and each type's nodes batch their edges. More IDs than Limits.MaxPage is veloxgql.ErrPageTooLarge (BAD_USER_INPUT); an ID of two types is gqlgraph.ErrAmbiguousNodeID. Any other error stops it.")
	f.p("func ResolveNodes(ctx %s.Context, db *%s.DB, ids []%s, opts ...NodeOption) ([]Noder, error) {", ctx, velox, key.typ)
	f.p("\tns, err := %s.ResolveNodes(ctx, db, nodeTypes, ids, opts...)", impl)
	f.p("\tif err != nil {")
	f.p("\t\treturn nil, err")
	f.p("\t}")
	f.p("\tout := make([]Noder, len(ns))")
	f.p("\tfor i, n := range ns {")
	f.p("\t\tif n != nil {")
	f.p("\t\t\tout[i] = n.(Noder)")
	f.p("\t\t}")
	f.p("\t}")
	f.p("\treturn out, nil")
	f.p("}")
	file, err = f.finish("noder_gql.go", r.outPkg)
	return file, err == nil, err
}
