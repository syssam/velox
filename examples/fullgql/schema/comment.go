package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

// Comment holds the schema definition for the Comment entity.
type Comment struct {
	velox.Schema
}

// Mixin of the Comment.
func (Comment) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{},
	}
}

// Fields of the Comment.
func (Comment) Fields() []velox.Field {
	return []velox.Field{
		field.Text("content").
			NotEmpty().
			Annotations(
				graphql.CreateInputValidate("required,min=1"),
				graphql.UpdateInputValidate("omitempty,min=1"),
			),
	}
}

// Edges of the Comment.
func (Comment) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("todo", Todo.Type).
			Ref("comments").
			Unique().
			Required().
			Comment("The todo this comment belongs to"),
		edge.From("author", User.Type).
			Ref("comments").
			Unique().
			Required().
			Comment("The user who wrote this comment"),
	}
}

// Annotations of the Comment.
func (Comment) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.Mutations(
			graphql.MutationCreate(),
			graphql.MutationUpdate(),
		),
	}
}
