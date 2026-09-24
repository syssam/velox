package schema

import (
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Like is the testschema's edge schema: User.liked_posts and Post.likers are
// an M2M relation declared Through() it. liked_at has a Go-function default,
// which the join row can only get from the edge schema's registered defaults
// (runtime.RegisterEdgeSchemaDefaults) — the adding entity cannot import
// this one. It is also what keeps that registry reachable in the root module.
type Like struct {
	velox.Schema
}

// Fields of the Like.
func (Like) Fields() []velox.Field {
	return []velox.Field{
		field.Time("liked_at").
			Default(time.Now).
			Immutable(),
		field.Int("user_id").Immutable(),
		field.Int("post_id").Immutable(),
	}
}

// Edges of the Like.
func (Like) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("user", User.Type).Unique().Required().Immutable().Field("user_id"),
		edge.To("post", Post.Type).Unique().Required().Immutable().Field("post_id"),
	}
}
