package gen

import (
	"context"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
)

// IsNode marks Category as a GraphQL Node: gqlgen binds the interface Node to
// veloxgql.Noder.
func (*Category) IsNode() {}

// IDGQL is the global ID of the Category, the GraphQL field id: the type
// Category and the key id, encoded.
func (x *Category) IDGQL() string { return veloxgql.EncodeID("Category", x.ID) }

// CategoryConnection is the GraphQL type CategoryConnection, a connection to a
// list of Category. It holds the query of the whole list; gqlgen calls its
// methods for the fields a query selects, and each runs only then.
type CategoryConnection struct {
	query velox.Query[Category] // the whole list, before paging
}

// TotalCount is the number of Category nodes in the whole connection, not only
// on this page. It counts the rows when gqlgen asks for it, which is only when
// the field totalCount is selected.
func (c *CategoryConnection) TotalCount(ctx context.Context) (int, error) {
	n, err := c.query.Count(ctx)
	return int(n), err
}

// CategoryEdge is the GraphQL type CategoryEdge, an edge in a connection to a
// list of Category.
type CategoryEdge struct {
	// Node is the Category at the end of the edge.
	Node *Category
	// Cursor is the cursor of the edge, for paging.
	Cursor veloxgql.Cursor
}

// CategoryWhereInput is the GraphQL input CategoryWhereInput, a filter of
// Category nodes: a node matches when every predicate given holds.
type CategoryWhereInput struct {
	// Not, And and Or combine filters: a node matches when it does not match
	// Not, matches each of And, and matches one of Or.
	Not *CategoryWhereInput
	And []*CategoryWhereInput
	Or  []*CategoryWhereInput

	// The edge parent: whether it has a node, or one that matches.
	HasParent     *bool
	HasParentWith []*CategoryWhereInput
}
