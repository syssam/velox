// Package schema defines the entity schemas for the full GraphQL example.
package schema

import (
	"example.com/fullgql/hook"
	"example.com/fullgql/rule"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/privacy"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/mixin"
)

// User holds the schema definition for the User entity.
type User struct {
	velox.Schema
}

// Mixin of the User.
func (User) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{}, // Adds created_at, updated_at
	}
}

// Fields of the User.
func (User) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").
			NotEmpty().
			MaxLen(100).
			Annotations(
				graphql.WhereInput(),
				graphql.OrderField("NAME"),
				graphql.CreateInputValidate("required,min=2,max=100"),
				graphql.UpdateInputValidate("omitempty,min=2,max=100"),
			),
		field.String("email").
			Unique().
			NotEmpty().
			MaxLen(255).
			Immutable().
			Annotations(
				graphql.WhereInput(),
				graphql.OrderField("EMAIL"),
				graphql.CreateInputValidate("required,email"),
			),
		field.Int("age").
			Optional().
			Nillable().
			Positive().
			Annotations(
				graphql.WhereOps(graphql.OpsComparison),
			),
		field.String("bio").
			Optional().
			Nillable().
			MaxLen(500).
			Annotations(
				graphql.Skip(graphql.SkipWhereInput),
			),
		field.Enum("role").
			Values("admin", "moderator", "user", "guest").
			Default("user").
			Annotations(
				graphql.WhereInput(),
			),
		field.Bool("active").
			Default(true).
			Annotations(
				graphql.WhereInput(),
			),
	}
}

// Edges of the User.
func (User) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("todos", Todo.Type).
			Comment("Todos owned by this user").
			Annotations(
				sqlschema.OnDelete(sqlschema.Cascade),
			),
		edge.To("comments", Comment.Type).
			Comment("Comments written by this user"),
		edge.To("memberships", Member.Type).
			Comment("Workspace memberships"),
		edge.To("audit_logs", AuditLog.Type).
			Comment("Audit trail"),
	}
}

// Indexes of the User.
func (User) Indexes() []velox.Index {
	return []velox.Index{
		index.Fields("email").Unique(),
		index.Fields("role", "active"),
		index.Fields("created_at"),
	}
}

// Policy defines the privacy policy of the User.
func (User) Policy() velox.Policy {
	return privacy.Policy{
		Mutation: privacy.MutationPolicy{
			rule.DenyBlockedName("blocked"),
		},
		Query: privacy.QueryPolicy{
			privacy.AlwaysAllowRule(),
		},
	}
}

// Hooks of the User.
func (User) Hooks() []velox.Hook {
	return []velox.Hook{
		hook.NormalizeName(),
	}
}

// Annotations of the User.
func (User) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.RelayConnection(),
		graphql.Mutations(
			graphql.MutationCreate(),
			graphql.MutationUpdate(),
		),
		graphql.WhereInputEdges("todos", "memberships"),
	}
}
