package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type User struct{ velox.Schema }

func (User) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (User) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").NotEmpty().MaxLen(100),
		field.String("email").Unique().NotEmpty(),
		field.Int("age").Optional().Nillable().Positive(),
		field.Bool("active").Default(true),
	}
}

func (User) Edges() []velox.Edge {
	return []velox.Edge{
		// O2M: User → Posts
		edge.To("posts", Post.Type),
		// O2M: User → Comments
		edge.To("comments", Comment.Type),
		// O2O: User → Profile (unique)
		edge.To("profile", Profile.Type).Unique(),
	}
}
