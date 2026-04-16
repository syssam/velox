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

// Workspace represents a multi-tenant workspace.
type Workspace struct{ velox.Schema }

// Mixin of the Workspace.
func (Workspace) Mixin() []velox.Mixin {
	return []velox.Mixin{mixin.Time{}}
}

// Fields of the Workspace.
func (Workspace) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").NotEmpty(),
	}
}

// Edges of the Workspace.
func (Workspace) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("tasks", Task.Type),
	}
}

// Annotations of the Workspace.
func (Workspace) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.Mutations(
			graphql.MutationCreate(),
			graphql.MutationUpdate(),
		),
	}
}

// Policy of the Workspace.
func (Workspace) Policy() velox.Policy {
	return privacy.Policy{
		Query: privacy.QueryPolicy{
			privacy.DenyIfNoViewer(),
			privacy.AlwaysAllowRule(),
		},
		Mutation: privacy.MutationPolicy{
			privacy.DenyIfNoViewer(),
			privacy.HasRole("admin"),
		},
	}
}
