package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
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
			MaxLen(50).
			Annotations(
				graphql.WhereInput(),
				graphql.OrderField("NAME"),
				graphql.CreateInputValidate("required,min=1,max=50"),
			),
	}
}

// Edges of the Tag.
func (Tag) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("todos", Todo.Type).
			Ref("tags").
			Comment("Todos associated with this tag"),
		edge.From("products", Product.Type).
			Ref("tags").
			Comment("Products associated with this tag"),
	}
}

// Annotations of the Tag.
func (Tag) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.RelayConnection(),
		graphql.Mutations(
			graphql.MutationCreate(),
			graphql.MutationUpdate(),
		),
		graphql.WhereInputEdges("todos"),
	}
}
