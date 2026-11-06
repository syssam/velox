package gen

import (
	"context"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

const _ = veloxgql.GeneratedCodeIsVersion17

// Node returns the node with the global ID id, or nil when no row has its key.
// It reads only the columns the selection needs.
func Node(ctx context.Context, id string) (veloxgql.Noder, error) {
	return gqlimpl.NodeByID(ctx, id, nodeType)
}

// Nodes returns the nodes with the global IDs ids, in their order, with nil for
// an ID no row has. It reads one query per type.
func Nodes(ctx context.Context, ids []string) ([]veloxgql.Noder, error) {
	return gqlimpl.NodesByID(ctx, ids, nodeType)
}

// nodeType is the Node type the ID type typ names, and false when it names
// none.
func nodeType(typ string) (gqlimpl.NodeKind, bool) {
	switch typ {
	case "Category":
		return gqlimpl.NewNode[int64](&categoryGQL), true
	case "Group":
		return gqlimpl.NewNode[int64](&groupGQL), true
	case "Post":
		return gqlimpl.NewNode[int64](&postGQL), true
	case "Account":
		return gqlimpl.NewNode[int64](&userGQL), true
	}
	return gqlimpl.NodeKind{}, false
}
