package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type Tag struct{ velox.Schema }

func (Tag) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").
			Unique().
			NotEmpty().
			MaxLen(50),
	}
}

func (Tag) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("todos", Todo.Type).
			Ref("tags"),
		edge.From("products", Product.Type).
			Ref("tags"),
	}
}

func (Tag) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.RelayConnection(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
