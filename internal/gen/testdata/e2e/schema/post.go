//go:build ignore

package schema

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
)

type Post struct{ schema.Schema }

func (Post) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("author_id"),
		field.String("title").NotEmpty(),
		field.Enum("status").Values("draft", "published").Default("draft"),
		field.Int("views").Nillable().Min(0).Default(0),
	}
}

func (Post) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("author", User.Type).Ref("posts").Unique().Required().Field("author_id"),
	}
}
