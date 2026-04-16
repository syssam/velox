package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Category struct{ velox.Schema }

func (Category) Mixin() []velox.Mixin {
	return []velox.Mixin{mixin.Time{}}
}

func (Category) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").
			NotEmpty().
			MaxLen(100),
		field.Text("description").
			Optional().
			Nillable(),
	}
}

func (Category) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("todos", Todo.Type),
		edge.To("children", Category.Type).
			From("parent").
			Unique(),
	}
}

func (Category) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.RelayConnection(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
