package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Post struct{ velox.Schema }

func (Post) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Post) Fields() []velox.Field {
	return []velox.Field{
		field.String("title").NotEmpty().MaxLen(200),
		field.Text("content").Optional().Nillable(),
		field.Enum("status").Values("draft", "published", "archived").Default("draft"),
	}
}

func (Post) Edges() []velox.Edge {
	return []velox.Edge{
		// M2O: Post → User (author)
		edge.From("author", User.Type).Ref("posts").Unique().Required(),
		// O2M: Post → Comments
		edge.To("comments", Comment.Type),
		// M2M: Post ↔ Tags
		edge.To("tags", Tag.Type),
	}
}
