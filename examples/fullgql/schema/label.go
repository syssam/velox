package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Label holds the schema definition for the Label entity.
// It is a lightweight entity for M2M relationships with no timestamps.
type Label struct {
	velox.Schema
}

// Fields of the Label.
func (Label) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").
			Unique().
			NotEmpty().
			MaxLen(50).
			Annotations(
				graphql.WhereInput(),
				graphql.OrderField("NAME"),
			),
		field.String("color").
			Optional().
			Default("#000000").
			MaxLen(7),
	}
}

// Edges of the Label.
func (Label) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("todos", Todo.Type).
			Ref("labels").
			Comment("Todos with this label"),
	}
}

// Annotations of the Label.
func (Label) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.RelayConnection(),
		graphql.Mutations(
			graphql.MutationCreate(),
			graphql.MutationUpdate(),
		),
	}
}
