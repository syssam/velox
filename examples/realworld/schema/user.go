// Package schema defines the entity schemas for the real-world example.
package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/privacy"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

// User represents an authenticated user in the system.
type User struct{ velox.Schema }

// Mixin of the User.
func (User) Mixin() []velox.Mixin {
	return []velox.Mixin{mixin.Time{}}
}

// Fields of the User.
func (User) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").NotEmpty(),
		field.String("email").Unique(),
		field.Enum("role").
			Values("admin", "member", "viewer").
			Default("member"),
	}
}

// Annotations of the User.
func (User) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.Mutations(
			graphql.MutationCreate(),
			graphql.MutationUpdate(),
		),
	}
}

// Policy of the User.
func (User) Policy() velox.Policy {
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
