package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

// Category holds the schema definition for the Category entity.
type Category struct {
	velox.Schema
}

// Mixin of the Category.
func (Category) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{},
	}
}

// Fields of the Category.
func (Category) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").
			NotEmpty().
			MaxLen(100).
			Annotations(
				graphql.WhereInput(),
				graphql.OrderField("NAME"),
				graphql.CreateInputValidate("required,min=1,max=100"),
				graphql.UpdateInputValidate("omitempty,min=1,max=100"),
			),
		field.Text("description").
			Optional().
			Nillable().
			Annotations(
				graphql.Skip(graphql.SkipWhereInput),
			),
	}
}

// Edges of the Category.
func (Category) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("todos", Todo.Type).
			Comment("Todos in this category"),
		edge.To("children", Category.Type).
			Comment("Subcategories").
			From("parent").
			Unique(),
	}
}

// Annotations of the Category.
func (Category) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.RelayConnection(),
		graphql.Mutations(
			graphql.MutationCreate(),
			graphql.MutationUpdate(),
		),
		graphql.WhereInputEdges("todos", "parent"),
	}
}
