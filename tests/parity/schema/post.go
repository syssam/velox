package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

// Post holds the schema definition for the Post entity.
type Post struct {
	velox.Schema
}

// Mixin of the Post.
func (Post) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{},
	}
}

// Fields of the Post.
func (Post) Fields() []velox.Field {
	return []velox.Field{
		field.String("title"),
		field.Enum("status").
			Values("draft", "published").
			Default("draft"),
		field.Int("view_count").
			Default(0).
			Annotations(graphql.OrderField("VIEW_COUNT")),
		field.Strings("labels").
			Optional(),
	}
}

// Edges of the Post.
func (Post) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("author", Author.Type).
			Ref("posts").
			Unique().
			Required(),
		edge.To("comments", Comment.Type),
		edge.To("tags", Tag.Type),
	}
}

// Annotations of the Post.
func (Post) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.RelayConnection(),
		graphql.MultiOrder(),
	}
}
