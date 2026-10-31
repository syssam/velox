//go:build ignore

package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
)

// Node references itself: a tree and a friends list.
type Node struct{ schema.Schema }

func (Node) Edges() []schema.Edge {
	return []schema.Edge{edge.To("children", Node.Type).From("parent").Unique(), edge.To("friends", Node.Type)}
}
