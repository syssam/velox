package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/mixin"
)

type Todo struct{ velox.Schema }

func (Todo) Mixin() []velox.Mixin {
	return []velox.Mixin{mixin.Time{}}
}

func (Todo) Fields() []velox.Field {
	return []velox.Field{
		field.String("title").
			NotEmpty().
			MaxLen(200),
		field.Text("description").
			Optional().
			Nillable(),
		field.Enum("status").
			Values("todo", "in_progress", "done", "canceled").
			Default("todo"),
		field.Enum("priority").
			Values("low", "medium", "high", "urgent").
			Default("medium"),
		field.Time("due_date").
			Optional().
			Nillable(),
		field.Bool("completed").
			Default(false),
		field.Int("estimated_hours").
			Optional().
			Nillable().
			NonNegative(),
	}
}

func (Todo) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("owner", User.Type).
			Ref("todos").
			Unique().
			Required(),
		edge.To("comments", Comment.Type),
		edge.To("tags", Tag.Type),
		edge.From("category", Category.Type).
			Ref("todos").
			Unique(),
		edge.To("labels", Label.Type),
	}
}

func (Todo) Indexes() []velox.Index {
	return []velox.Index{
		index.Fields("status", "priority"),
		index.Fields("due_date"),
		index.Fields("completed"),
	}
}

func (Todo) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.RelayConnection(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
