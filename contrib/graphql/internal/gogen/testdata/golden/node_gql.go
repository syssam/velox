package gen

import (
	"context"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

const _ = veloxgql.GeneratedCodeIsVersion7

// Node returns the node with the global ID id, or nil when no row has its key.
// It reads the ID's type, decodes the key as that type's, and looks the row up
// with one query on that entity, through the DB veloxgql.DBFromContext reads
// from ctx, reading only the columns the field's selection needs; its edges
// batch (veloxgql.Batch). A column the selection does not need is zero in the
// node, except the key and the foreign keys: an application's resolver must not
// read one it did not select. An ID of no Node type fails with
// veloxgql.ErrWrongIDType and a malformed one with veloxgql.ErrBadID; neither
// runs a query.
func Node(ctx context.Context, id string) (veloxgql.Noder, error) {
	return gqlimpl.NodeByID(ctx, id, nodeKindGQL)
}

// Nodes returns the nodes with the global IDs ids, in their order, with nil for
// an ID no row has. It decodes every ID first, and then looks up each type's
// nodes with one query, through the DB veloxgql.DBFromContext reads from ctx;
// the query reads at most one row for each of its IDs, whatever the DB's
// WithMaxRows, and only the columns the field's selection needs. The nodes of a
// type are one batch for their edges (veloxgql.Batch). A column the selection
// does not need is zero in a node, except the key and the foreign keys: an
// application's resolver must not read one it did not select. It fails as Node
// does on an ID, before any query runs, and with veloxgql.ErrPageTooLarge on
// more IDs than the MaxPage of veloxgql.LimitsFromContext(ctx).
func Nodes(ctx context.Context, ids []string) ([]veloxgql.Noder, error) {
	return gqlimpl.NodesByID(ctx, ids, nodeKindGQL)
}

// nodeKindGQL is the Node type the ID type typ names, or nil when it names
// none.
func nodeKindGQL(typ string) gqlimpl.NodeKind {
	switch typ {
	case "Category":
		return categoryNodeGQL()
	case "Group":
		return groupNodeGQL()
	case "Post":
		return postNodeGQL()
	case "Account":
		return userNodeGQL()
	}
	return nil
}
