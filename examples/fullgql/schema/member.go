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

// Member holds the schema definition for the Member entity.
// It represents a user's membership in a workspace.
type Member struct {
	velox.Schema
}

// Mixin of the Member.
func (Member) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{},
	}
}

// Fields of the Member.
func (Member) Fields() []velox.Field {
	return []velox.Field{
		field.Enum("role").
			Values("owner", "admin", "editor", "viewer").
			Default("viewer").
			Annotations(
				graphql.WhereInput(),
				graphql.OrderField("ROLE"),
			),
		field.String("invite_token").
			Optional().
			Nillable().
			Sensitive().
			MaxLen(100),
		field.Bool("accepted").
			Default(false),
	}
}

// Edges of the Member.
func (Member) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("workspace", Workspace.Type).
			Ref("members").
			Unique().
			Required(),
		edge.From("user", User.Type).
			Ref("memberships").
			Unique().
			Required(),
	}
}

// Indexes of the Member.
func (Member) Indexes() []velox.Index {
	return []velox.Index{
		index.Fields("role"),
	}
}

// Annotations of the Member.
func (Member) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.Mutations(
			graphql.MutationCreate(),
			graphql.MutationUpdate(),
		),
	}
}
