package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/mixin"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Comment holds the schema definition for the Comment entity.
type Comment struct {
	velox.Schema
}

// Mixin of the Comment.
func (Comment) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{},
	}
}

// Fields of the Comment.
func (Comment) Fields() []velox.Field {
	return []velox.Field{
		field.Text("content").
			NotEmpty(),
	}
}

// Edges of the Comment.
func (Comment) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("post", Post{}).
			Ref("comments").
			Unique().
			Required(),
		edge.From("author", User{}).
			Unique().
			Required(),
	}
}
