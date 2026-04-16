package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type Tag struct{ velox.Schema }

func (Tag) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").Unique().NotEmpty().MaxLen(50),
	}
}

func (Tag) Edges() []velox.Edge {
	return []velox.Edge{
		// M2M back-reference: Tag ↔ Posts
		edge.From("posts", Post.Type).Ref("tags"),
	}
}
