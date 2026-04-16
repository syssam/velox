package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Comment struct{ velox.Schema }

func (Comment) Mixin() []velox.Mixin {
	return []velox.Mixin{mixin.Time{}}
}

func (Comment) Fields() []velox.Field {
	return []velox.Field{
		field.Text("content").
			NotEmpty(),
	}
}

func (Comment) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("todo", Todo.Type).
			Ref("comments").
			Unique().
			Required(),
		edge.From("author", User.Type).
			Ref("comments").
			Unique().
			Required(),
	}
}

func (Comment) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.RelayConnection(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
