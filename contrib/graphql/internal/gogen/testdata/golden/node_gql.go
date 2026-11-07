package gen

import (
	"context"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
)

const _ = veloxgql.GeneratedCodeIsVersion19

// Node returns the node with the global ID id, or nil when no row has its key.
// It reads only the columns the selection needs.
func Node(ctx context.Context, id string) (veloxgql.Noder, error) {
	return gqlgraph.NodeByID(ctx, id, nodeKind)
}

// Nodes returns the nodes with the global IDs ids, in their order, with nil for
// an ID no row has. It reads one query per type.
func Nodes(ctx context.Context, ids []string) ([]veloxgql.Noder, error) {
	return gqlgraph.NodesByID(ctx, ids, nodeKind)
}

// nodeKind is the Node type the ID type typ names, and false when it names
// none.
func nodeKind(typ string) (gqlgraph.NodeKind, bool) {
	switch typ {
	case "Category":
		return gqlgraph.NewNode[int64](&categoryType), true
	case "Group":
		return gqlgraph.NewNode[int64](&groupType), true
	case "Post":
		return gqlgraph.NewNode[int64](&postType), true
	case "Account":
		return gqlgraph.NewNode[int64](&userType), true
	}
	return gqlgraph.NodeKind{}, false
}
