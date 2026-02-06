package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Tag holds the schema definition for the Tag entity.
type Tag struct {
	velox.Schema
}

// Fields of the Tag.
func (Tag) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").
			Unique().
			NotEmpty().
			MaxLen(50),
	}
}

// Edges of the Tag.
func (Tag) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("posts", Post{}).
			Ref("tags"),
	}
}
