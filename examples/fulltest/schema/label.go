package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type Label struct{ velox.Schema }

func (Label) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").
			Unique().
			NotEmpty().
			MaxLen(50),
		field.String("color").
			Optional().
			Default("#000000").
			MaxLen(7),
	}
}

func (Label) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("todos", Todo.Type).
			Ref("labels"),
	}
}

func (Label) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.RelayConnection(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
