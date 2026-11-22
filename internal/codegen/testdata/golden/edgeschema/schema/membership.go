//go:build ignore

package schema

import (
	"time"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Membership is the edge schema of User.groups: Add fills every column.
type Membership struct{ schema.Schema }

func (Membership) Fields() []schema.Field {
	return []schema.Field{
		field.Enum("role").Values("owner", "member").Default("member"),
		field.Time("joined_at").Default(time.Now).Immutable(),
		field.Int64("user_id"),
		field.Int64("group_id"),
	}
}

func (Membership) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("user", User.Type).Unique().Required().Field("user_id").OnDelete(edge.Cascade),
		edge.To("group", Group.Type).Unique().Required().Field("group_id").OnDelete(edge.Cascade),
	}
}
