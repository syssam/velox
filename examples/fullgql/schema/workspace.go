package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/privacy"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/mixin"
)

// Workspace holds the schema definition for the Workspace entity.
type Workspace struct {
	velox.Schema
}

// Mixin of the Workspace.
func (Workspace) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{},
	}
}

// Fields of the Workspace.
func (Workspace) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").
			NotEmpty().
			MaxLen(100).
			Annotations(
				graphql.WhereInput(),
				graphql.OrderField("NAME"),
			),
		field.String("description").
			Optional().
			Nillable().
			MaxLen(500),
		field.Time("deleted_at").
			Optional().
			Nillable(),
		field.Bool("active").
			Default(true).
			Annotations(
				graphql.WhereInput(),
			),
	}
}

// Edges of the Workspace.
func (Workspace) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("members", Member.Type).
			Comment("Members of this workspace"),
		edge.To("todos", Todo.Type).
			Comment("Todos in this workspace"),
	}
}

// Indexes of the Workspace.
func (Workspace) Indexes() []velox.Index {
	return []velox.Index{
		index.Fields("name").Unique(),
	}
}

// Policy defines the privacy policy of the Workspace.
func (Workspace) Policy() velox.Policy {
	return privacy.Policy{
		Query: privacy.QueryPolicy{
			privacy.AlwaysAllowRule(),
		},
		Mutation: privacy.MutationPolicy{
			privacy.AlwaysAllowRule(),
		},
	}
}

// Annotations of the Workspace.
func (Workspace) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.RelayConnection(),
		graphql.Mutations(
			graphql.MutationCreate(),
			graphql.MutationUpdate(),
		),
	}
}
