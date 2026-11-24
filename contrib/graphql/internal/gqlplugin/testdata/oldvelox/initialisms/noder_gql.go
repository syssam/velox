package gen

import (
	"context"
	"slices"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
)

const _ = veloxgql.GeneratedCodeIsVersion31

// Noder is the Go type of the GraphQL interface Node, which every type with an
// ID implements.
type Noder interface {
	// IsNode marks a type of Node.
	IsNode()
}

// IsNode makes APIKey a Noder: the GraphQL type APIKey implements Node.
func (*APIKey) IsNode() {}

// IsNode makes Device a Noder: the GraphQL type Device implements Node.
func (*Device) IsNode() {}

// IsNode makes SLADefinition a Noder: the GraphQL type SLADefinition implements
// Node.
func (*SLADefinition) IsNode() {}

// nodeTypes are the types of Node, in name order.
var nodeTypes = []*gqlgraph.Type{
	&aPIKeyType,
	&deviceType,
	&sLADefinitionType,
}

// NodeTypes returns the types of Node, in name order, for an application that
// resolves node(id:) its own way with gqlgraph.LoadNode.
func NodeTypes() []*gqlgraph.Type { return slices.Clone(nodeTypes) }

// NodeOption configures ResolveNode and ResolveNodes.
type NodeOption = gqlgraph.NodeOption

// WithNodeType gives the type of an ID: fn returns the GraphQL name of the type
// of Node that holds id, or "" when it does not know it. ResolveNode and
// ResolveNodes then read only that type and look up only the IDs of no known
// type. A name that is no type of Node is an error.
func WithNodeType(fn func(ctx context.Context, id int64) (string, error)) NodeOption {
	return gqlgraph.WithNodeType(fn)
}

// WithFixedNodeType gives every ID the type name, the GraphQL name of a type of
// Node, so that no statement looks it up.
func WithFixedNodeType(name string) NodeOption { return gqlgraph.WithFixedNodeType(name) }

// ResolveNode answers node(id:) as entgql's generated Client.Noder does: the
// one node of any type whose key is id, read with db, so each type's Read
// policy applies, a hidden or denied row counting as none. One statement finds
// which types hold the ID, unless an option gives its type; only those are
// read. Its error wraps gqlgraph.ErrNodeNotFound (NOT_FOUND) when no type has
// the ID, and gqlgraph.ErrAmbiguousNodeID when more than one does.
func ResolveNode(ctx context.Context, db *velox.DB, id int64, opts ...NodeOption) (Noder, error) {
	n, err := gqlgraph.ResolveNode(ctx, db, nodeTypes, id, opts...)
	if err != nil {
		return nil, err
	}
	return n.(Noder), nil
}

// ResolveNodes answers nodes(ids:) as entgql's generated Client.Noders does:
// the node of each ID, in order, nil for one no type has. One statement finds
// which types hold the IDs whose type no option gives, then it reads each type
// with one IN query of its IDs, and each type's nodes batch their edges. More
// IDs than Limits.MaxPage is veloxgql.ErrPageTooLarge (BAD_USER_INPUT); an ID
// of two types is gqlgraph.ErrAmbiguousNodeID. Any other error stops it.
func ResolveNodes(ctx context.Context, db *velox.DB, ids []int64, opts ...NodeOption) ([]Noder, error) {
	ns, err := gqlgraph.ResolveNodes(ctx, db, nodeTypes, ids, opts...)
	if err != nil {
		return nil, err
	}
	out := make([]Noder, len(ns))
	for i, n := range ns {
		if n != nil {
			out[i] = n.(Noder)
		}
	}
	return out, nil
}
