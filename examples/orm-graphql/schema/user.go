// Package schema defines the entities of the example: a user and the todos
// they own, annotated for GraphQL.
package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

// User holds the schema definition for the User entity.
type User struct {
	velox.Schema
}

// Mixin of the User.
func (User) Mixin() []velox.Mixin {
	return []velox.Mixin{mixin.Time{}}
}

// Fields of the User.
func (User) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").
			NotEmpty().
			Annotations(graphql.WhereInput(), graphql.OrderField("NAME")),
		field.String("email").
			Unique().
			Annotations(graphql.WhereInput()),
	}
}

// Edges of the User.
func (User) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("todos", Todo.Type).
			Annotations(graphql.RelayConnection(), graphql.WhereInput()),
	}
}

// Annotations of the User.
func (User) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
	}
}
