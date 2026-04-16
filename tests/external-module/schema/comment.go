package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Comment struct{ velox.Schema }

func (Comment) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Comment) Fields() []velox.Field {
	return []velox.Field{
		field.Text("body").NotEmpty(),
	}
}

func (Comment) Edges() []velox.Edge {
	return []velox.Edge{
		// M2O: Comment → User (author)
		edge.From("author", User.Type).Ref("comments").Unique().Required(),
		// M2O: Comment → Post
		edge.From("post", Post.Type).Ref("comments").Unique().Required(),
	}
}
