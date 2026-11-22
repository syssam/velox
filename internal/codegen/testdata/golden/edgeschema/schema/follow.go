//go:build ignore

package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Follow is the edge schema of User.following, to its own schema: its note
// has no default, so Add cannot fill it.
type Follow struct{ schema.Schema }

func (Follow) Fields() []schema.Field {
	return []schema.Field{field.String("note"), field.Int64("user_id"), field.Int64("follower_id")}
}

func (Follow) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("user", User.Type).Unique().Required().Field("user_id"),
		edge.To("follower", User.Type).Unique().Required().Field("follower_id"),
	}
}
