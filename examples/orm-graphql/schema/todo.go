package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

// Todo holds the schema definition for the Todo entity.
type Todo struct {
	velox.Schema
}

// Mixin of the Todo.
func (Todo) Mixin() []velox.Mixin {
	return []velox.Mixin{mixin.Time{}}
}

// Fields of the Todo.
func (Todo) Fields() []velox.Field {
	return []velox.Field{
		field.String("title").
			NotEmpty().
			MaxLen(200).
			Annotations(graphql.WhereInput(), graphql.OrderField("TITLE")),
		field.Enum("status").
			Values("todo", "in_progress", "done").
			Default("todo").
			Annotations(graphql.WhereInput(), graphql.OrderField("STATUS")),
		field.Int("priority").
			Default(0).
			Annotations(graphql.WhereOps(graphql.OpsComparison), graphql.OrderField("PRIORITY")),
		field.Time("due_date").
			Optional().
			Nillable().
			Annotations(graphql.WhereOps(graphql.OpsComparison)),
	}
}

// Edges of the Todo.
func (Todo) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("owner", User.Type).
			Ref("todos").
			Unique().
			Required().
			Annotations(graphql.WhereInput()),
	}
}

// Annotations of the Todo.
func (Todo) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
	}
}
