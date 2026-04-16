package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/privacy"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

// Task represents a work item in a workspace.
type Task struct{ velox.Schema }

// Mixin of the Task.
func (Task) Mixin() []velox.Mixin {
	return []velox.Mixin{mixin.Time{}}
}

// Fields of the Task.
func (Task) Fields() []velox.Field {
	return []velox.Field{
		field.String("title").NotEmpty(),
		field.Text("description").Optional().Nillable(),
		field.Enum("status").
			Values("todo", "in_progress", "done").
			Default("todo"),
		field.Enum("priority").
			Values("low", "medium", "high").
			Default("medium"),
	}
}

// Edges of the Task.
func (Task) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("workspace", Workspace.Type).
			Ref("tasks").
			Unique().
			Required(),
	}
}

// Annotations of the Task.
func (Task) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.RelayConnection(),
		graphql.Mutations(
			graphql.MutationCreate(),
			graphql.MutationUpdate(),
		),
	}
}

// Policy of the Task.
func (Task) Policy() velox.Policy {
	return privacy.Policy{
		Query: privacy.QueryPolicy{
			privacy.DenyIfNoViewer(),
			privacy.AlwaysAllowRule(),
		},
		Mutation: privacy.MutationPolicy{
			privacy.DenyIfNoViewer(),
			privacy.AlwaysAllowRule(),
		},
	}
}
