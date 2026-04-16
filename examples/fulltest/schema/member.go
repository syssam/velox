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

type Member struct{ velox.Schema }

func (Member) Mixin() []velox.Mixin {
	return []velox.Mixin{mixin.Time{}}
}

func (Member) Fields() []velox.Field {
	return []velox.Field{
		field.Enum("role").
			Values("owner", "admin", "editor", "viewer").
			Default("viewer"),
		field.String("invite_token").
			Optional().
			Nillable().
			Sensitive().
			MaxLen(100),
		field.Bool("accepted").
			Default(false),
	}
}

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

func (Member) Indexes() []velox.Index {
	return []velox.Index{
		index.Fields("role"),
	}
}

func (Member) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.RelayConnection(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
