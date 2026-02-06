package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/mixin"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
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
		field.String("title").
			NotEmpty().
			MaxLen(200),
		field.Text("content").
			Optional(),
		field.Enum("status").
			Values("draft", "published", "archived").
			Default("draft"),
		field.Int("view_count").
			Default(0).
			NonNegative(),
	}
}

// Edges of the Post.
func (Post) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("author", User{}).
			Ref("posts").
			Unique().
			Required(),
		edge.To("comments", Comment{}),
		edge.To("tags", Tag{}),
	}
}
