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

type User struct{ velox.Schema }

func (User) Mixin() []velox.Mixin {
	return []velox.Mixin{mixin.Time{}}
}

func (User) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").
			NotEmpty().
			MaxLen(100),
		field.String("email").
			Unique().
			NotEmpty().
			MaxLen(255).
			Immutable(),
		field.Int("age").
			Optional().
			Nillable().
			Positive(),
		field.String("bio").
			Optional().
			Nillable().
			MaxLen(500),
		field.Enum("role").
			Values("admin", "moderator", "user", "guest").
			Default("user"),
		field.Bool("active").
			Default(true),
	}
}

func (User) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("todos", Todo.Type),
		edge.To("comments", Comment.Type),
		edge.To("memberships", Member.Type),
		edge.To("audit_logs", AuditLog.Type),
	}
}

func (User) Indexes() []velox.Index {
	return []velox.Index{
		index.Fields("email").Unique(),
		index.Fields("role", "active"),
	}
}

func (User) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.RelayConnection(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
